// Package db abre a conexão MySQL e aplica as migrações embutidas.
package db

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Open abre o banco e espera (até wait) ele aceitar conexões: no compose o MySQL demora a subir.
func Open(ctx context.Context, dsn string, wait time.Duration) (*sql.DB, error) {
	d, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, err
	}
	d.SetMaxOpenConns(20)
	d.SetMaxIdleConns(5)
	d.SetConnMaxLifetime(5 * time.Minute)
	deadline := time.Now().Add(wait)
	for {
		pctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		err = d.PingContext(pctx)
		cancel()
		if err == nil {
			return d, nil
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			d.Close()
			return nil, fmt.Errorf("banco indisponível: %w", err)
		}
		slog.Info("aguardando o banco de dados...", "erro", err.Error())
		select {
		case <-time.After(2 * time.Second):
		case <-ctx.Done():
			d.Close()
			return nil, ctx.Err()
		}
	}
}

// Migrate aplica, em ordem, os arquivos migrations/NNN_nome.sql ainda não aplicados. Cada arquivo roda numa
// transação lógica por comando (MySQL faz commit implícito em DDL); a versão só é gravada depois do sucesso.
func Migrate(ctx context.Context, d *sql.DB) (applied []string, err error) {
	if _, err = d.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version VARCHAR(64) NOT NULL PRIMARY KEY, applied_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
	) ENGINE=InnoDB`); err != nil {
		return nil, err
	}
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	// Proteção contra "downgrade": se o banco já foi migrado por uma versão mais nova, este binário não sabe o que há nele.
	known := map[string]bool{}
	for _, n := range names {
		known[strings.TrimSuffix(n, ".sql")] = true
	}
	rows, err := d.QueryContext(ctx, "SELECT version FROM schema_migrations")
	if err != nil {
		return nil, err
	}
	var unknown []string
	for rows.Next() {
		var v string
		if rows.Scan(&v) == nil && !known[v] {
			unknown = append(unknown, v)
		}
	}
	rows.Close()
	if len(unknown) > 0 {
		return nil, fmt.Errorf("o banco foi atualizado por uma versão MAIS NOVA do Coruja Backup (migrações %s). Use a mesma versão ou uma mais nova; volte a versão só restaurando um backup do banco", strings.Join(unknown, ", "))
	}
	for _, n := range names {
		ver := strings.TrimSuffix(n, ".sql")
		var one int
		switch err := d.QueryRowContext(ctx, "SELECT 1 FROM schema_migrations WHERE version=?", ver).Scan(&one); err {
		case nil:
			continue
		case sql.ErrNoRows:
		default:
			return applied, err
		}
		body, err := migrationsFS.ReadFile("migrations/" + n)
		if err != nil {
			return applied, err
		}
		for _, stmt := range splitSQL(string(body)) {
			if _, err := d.ExecContext(ctx, stmt); err != nil {
				return applied, fmt.Errorf("migração %s: %w", n, err)
			}
		}
		if _, err := d.ExecContext(ctx, "INSERT INTO schema_migrations (version) VALUES (?)", ver); err != nil {
			return applied, err
		}
		applied = append(applied, ver)
	}
	return applied, nil
}

// splitSQL separa comandos por ';' ao fim de linha, ignorando linhas de comentário "--".
func splitSQL(s string) []string {
	var out []string
	var cur strings.Builder
	for _, line := range strings.Split(s, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "--") {
			continue
		}
		cur.WriteString(line + "\n")
		if strings.HasSuffix(strings.TrimSpace(line), ";") {
			if q := strings.TrimSpace(cur.String()); q != "" {
				out = append(out, q)
			}
			cur.Reset()
		}
	}
	if q := strings.TrimSpace(cur.String()); q != "" {
		out = append(out, q)
	}
	return out
}
