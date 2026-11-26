package dbdump

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/AprendendoLinux/corujabackup/internal/dockerapi"
)

var verRE = regexp.MustCompile(`^(\d+)\.(\d+)`)

// DefaultImage escolhe a imagem do servidor descartável a partir do motor e da versão do servidor de origem.
func DefaultImage(engine, version string) string {
	m := verRE.FindStringSubmatch(version)
	major, minor := "", ""
	if m != nil {
		major, minor = m[1], m[2]
	}
	switch engine {
	case EngineMySQL:
		if major != "" {
			return "mysql:" + major + "." + minor
		}
		return "mysql:8.4"
	case EngineMariaDB:
		if major != "" {
			return "mariadb:" + major + "." + minor
		}
		return "mariadb:11.4"
	case EnginePostgres:
		if major != "" {
			return "postgres:" + major
		}
		return "postgres:17"
	default:
		if major != "" {
			return "mongo:" + major + "." + minor
		}
		return "mongo:7.0"
	}
}

// RestoreTest restaura todos os dumps em um servidor descartável (contêiner sem rede, apagado ao final) e confere que
// o número de tabelas/coleções restauradas é igual ao da origem. É a prova de que o backup realmente se restaura.
func RestoreTest(ctx context.Context, dk *dockerapi.Client, cfg Config, out *Output, h Hooks) error {
	if dk == nil || !dk.Available(ctx) {
		return errors.New("o Docker não está acessível no agente")
	}
	image := cfg.RestoreImage
	if image == "" {
		image = DefaultImage(cfg.Engine, out.Version)
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Minute)
	defer cancel()
	h.log("info", "teste de restauração: preparando servidor descartável (%s)", image)
	if err := dk.Pull(ctx, image); err != nil {
		return fmt.Errorf("não foi possível obter a imagem %s: %w", image, err)
	}
	pw := randHex(12)
	var env []string
	rc := Config{Engine: cfg.Engine, Mode: ModeDocker}
	switch cfg.Engine {
	case EngineMySQL:
		env, rc.User, rc.Password = []string{"MYSQL_ROOT_PASSWORD=" + pw}, "root", pw
	case EngineMariaDB:
		env, rc.User, rc.Password = []string{"MARIADB_ROOT_PASSWORD=" + pw, "MYSQL_ROOT_PASSWORD=" + pw}, "root", pw
	case EnginePostgres:
		env, rc.User, rc.Password = []string{"POSTGRES_PASSWORD=" + pw}, "postgres", pw
	}
	id, err := dk.Create(ctx, dockerapi.CreateOptions{Image: image, Env: env, Memory: 2 << 30, Labels: map[string]string{"coruja.restore-test": "1"}})
	if err != nil {
		return fmt.Errorf("não foi possível criar o servidor descartável: %w", err)
	}
	defer func() { // limpeza mesmo se o contexto já expirou
		cctx, c := context.WithTimeout(context.Background(), 30*time.Second)
		defer c()
		_ = dk.Remove(cctx, id)
	}()
	if err := dk.Start(ctx, id); err != nil {
		return fmt.Errorf("não foi possível iniciar o servidor descartável: %w", err)
	}
	rc.Container = id
	r := DockerRunner{C: dk, Container: id}
	e := newEngine(cfg.Engine)
	if err := waitReady(ctx, r, rc); err != nil {
		return err
	}
	h.log("info", "teste de restauração: servidor pronto; restaurando %d arquivo(s)", countDumps(out))
	for _, f := range out.Files {
		if f.DB == "" {
			continue // globais (papéis) não entram no teste
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := restoreOne(ctx, r, rc, f); err != nil {
			return fmt.Errorf("falha ao restaurar %s: %w", f.DB, err)
		}
		n, err := e.count(ctx, r, rc, f.DB)
		if err != nil {
			return fmt.Errorf("restaurado %s, mas não foi possível conferir: %w", f.DB, err)
		}
		if f.Tables > 0 && n != f.Tables {
			return fmt.Errorf("%s: restaurado com %d tabela(s)/coleção(ões), a origem tinha %d", f.DB, n, f.Tables)
		}
		h.log("info", "teste de restauração: %s restaurado com %d tabela(s)/coleção(ões) (igual à origem)", f.DB, n)
	}
	return nil
}

func countDumps(o *Output) int {
	n := 0
	for _, f := range o.Files {
		if f.DB != "" {
			n++
		}
	}
	return n
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func waitReady(ctx context.Context, r Runner, c Config) error {
	deadline := time.Now().Add(4 * time.Minute)
	var last string
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var cmd Cmd
		switch c.Engine {
		case EngineMySQL, EngineMariaDB:
			// -h 127.0.0.1 força TCP: o servidor temporário da inicialização só escuta no socket
			cmd = Cmd{Args: wrap("mariadb-admin|mysqladmin", "-h", "127.0.0.1", "-u", "@@USER@@", "ping"), Env: mysqlEngine{}.env(c)}
		case EnginePostgres:
			cmd = Cmd{Args: wrap("pg_isready", "-h", "127.0.0.1", "-U", "@@USER@@"), Env: envFor(c)}
		default:
			cmd = Cmd{Args: wrap("mongosh|mongo", "--quiet", "--eval", "db.runCommand({ping:1}).ok"), Env: envFor(c)}
		}
		code, se, err := r.Run(ctx, Cmd{Args: cmd.Args, Env: cmd.Env, Stdout: io.Discard})
		if err == nil && code == 0 {
			if c.Engine == EngineMongo {
				out, _, _, _ := capture(ctx, r, cmd)
				if strings.TrimSpace(out) != "1" {
					time.Sleep(2 * time.Second)
					continue
				}
			}
			return nil
		}
		last = se
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("o servidor descartável não ficou pronto a tempo: %s", clip(last))
}

func restoreOne(ctx context.Context, r Runner, c Config, f DumpFile) error {
	in, err := os.Open(f.Path)
	if err != nil {
		return err
	}
	defer in.Close()
	var cmd Cmd
	switch c.Engine {
	case EngineMySQL, EngineMariaDB:
		cmd = Cmd{Args: wrap("mariadb|mysql", "-u", "@@USER@@"), Env: mysqlEngine{}.env(c), Stdin: in}
	case EnginePostgres:
		pe := pgEngine{}
		code, se, err := r.Run(ctx, Cmd{Args: wrap("psql", "-U", "@@USER@@", "-d", "postgres", "-c", `CREATE DATABASE "`+f.DB+`"`), Env: pe.env(c), Stdout: io.Discard})
		exists := strings.Contains(se, "already exists") // ex.: o banco "postgres" já vem com o servidor
		if err != nil || (code != 0 && !exists) {
			return fmt.Errorf("não foi possível criar o banco: %v %s", err, clip(se))
		}
		args := []string{"-U", "@@USER@@", "-d", f.DB, "--no-owner", "--no-privileges", "--exit-on-error"}
		if exists {
			args = append(args, "--clean", "--if-exists")
		}
		cmd = Cmd{Args: wrap("pg_restore", args...), Env: pe.env(c), Stdin: in}
	default:
		cmd = Cmd{Args: wrap("mongorestore", "--archive", "--gzip", "--drop"), Env: envFor(c), Stdin: in}
	}
	cmd.Stdout = io.Discard
	code, se, err := r.Run(ctx, cmd)
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("código %d: %s", code, clip(se))
	}
	return nil
}
