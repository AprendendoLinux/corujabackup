package dbdump

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// RestoreInto restaura o dump f de um banco no servidor de cfg, com o nome target. Por padrão o banco não pode existir
// (restaurar num banco novo, ao lado do original). Com overwrite, um banco existente com esse nome é APAGADO e recriado,
// para ficar exatamente como no backup. Devolve a quantidade de tabelas/coleções do banco restaurado.
func RestoreInto(ctx context.Context, r Runner, cfg Config, f DumpFile, target string, overwrite bool, h Hooks) (int64, error) {
	if f.DB == "" || f.Path == "" {
		return 0, errors.New("arquivo de dump sem banco")
	}
	if !ValidName(target) {
		return 0, fmt.Errorf("nome de banco inválido: %q", target)
	}
	e := newEngine(cfg.Engine)
	all, err := e.list(ctx, r, cfg)
	if err != nil {
		return 0, fmt.Errorf("não foi possível listar os bancos do servidor: %w", err)
	}
	exists := false
	for _, d := range all {
		if d == target {
			exists = true
		}
	}
	if exists && !overwrite {
		return 0, fmt.Errorf("o banco %q já existe no servidor: escolha outro nome (ou, como administrador, sobrescreva)", target)
	}
	in, err := os.Open(f.Path)
	if err != nil {
		return 0, err
	}
	defer in.Close()
	var cmd Cmd
	switch cfg.Engine {
	case EngineMySQL, EngineMariaDB:
		me := mysqlEngine{}
		if exists {
			h.log("warn", "apagando o banco %s para recriá-lo a partir do backup", target)
			if _, err := me.query(ctx, r, cfg, "DROP DATABASE `"+target+"`"); err != nil {
				return 0, fmt.Errorf("não foi possível apagar o banco %s: %w", target, err)
			}
		}
		// o dump cria o banco e entra nele (CREATE DATABASE / USE): só essas linhas trocam o nome, mantendo o charset
		pr, pw := io.Pipe()
		go func() { pw.CloseWithError(renameMySQLDump(in, pw, f.DB, target)) }()
		cmd = Cmd{Args: wrap("mariadb|mysql", append(me.conn(cfg), "--default-character-set=utf8mb4")...), Env: me.env(cfg), Stdin: pr}
	case EnginePostgres:
		pe := pgEngine{}
		if exists {
			h.log("warn", "apagando o banco %s para recriá-lo a partir do backup", target)
			if _, err := pe.query(ctx, r, cfg, "postgres", `DROP DATABASE "`+target+`" WITH (FORCE)`); err != nil {
				if _, err2 := pe.query(ctx, r, cfg, "postgres", `DROP DATABASE "`+target+`"`); err2 != nil {
					return 0, fmt.Errorf("não foi possível apagar o banco %s (há conexões abertas?): %w", target, err)
				}
			}
		}
		if _, err := pe.query(ctx, r, cfg, "postgres", `CREATE DATABASE "`+target+`"`); err != nil {
			return 0, fmt.Errorf("não foi possível criar o banco %s: %w", target, err)
		}
		cmd = Cmd{Args: wrap("pg_restore", append(pe.conn(cfg), "-d", target, "--no-owner", "--no-privileges", "--exit-on-error")...), Env: pe.env(cfg), Stdin: in}
	case EngineMongo:
		if exists {
			h.log("warn", "apagando o banco %s para recriá-lo a partir do backup", target)
			if _, err := (mongoEngine{}).eval(ctx, r, cfg, `db.getSiblingDB("`+target+`").dropDatabase()`); err != nil {
				return 0, fmt.Errorf("não foi possível apagar o banco %s: %w", target, err)
			}
		}
		args := append(mongoEngine{}.conn(cfg), "--archive", "--gzip", "--nsFrom", f.DB+".*", "--nsTo", target+".*", "--nsInclude", f.DB+".*")
		if overwrite {
			args = append(args, "--drop")
		}
		cmd = Cmd{Args: wrap("mongorestore", args...), Env: envFor(cfg), Stdin: in}
	default:
		return 0, fmt.Errorf("motor desconhecido: %q", cfg.Engine)
	}
	cmd.Stdout = io.Discard
	h.log("info", "restaurando %s em %s (%s)", f.DB, target, r.Where())
	code, se, err := r.Run(ctx, cmd)
	if err != nil {
		return 0, err
	}
	if code != 0 {
		return 0, fmt.Errorf("a restauração falhou (código %d): %s", code, clip(se))
	}
	n, err := e.count(ctx, r, cfg, target)
	if err != nil {
		h.log("warn", "restaurado, mas não foi possível contar as tabelas de %s: %v", target, err)
		return -1, nil
	}
	return n, nil
}

// renameMySQLDump copia o dump trocando o nome do banco só nas linhas CREATE DATABASE e USE (as do --databases). As
// linhas de dados podem ser enormes: a leitura não tem limite de tamanho de linha.
func renameMySQLDump(in io.Reader, out io.Writer, from, to string) error {
	br := bufio.NewReaderSize(in, 1<<20)
	bw := bufio.NewWriterSize(out, 1<<20)
	oldQ, newQ := "`"+from+"`", "`"+to+"`"
	for {
		line, err := br.ReadString('\n')
		if from != to && (strings.HasPrefix(line, "CREATE DATABASE ") || strings.HasPrefix(line, "USE `")) {
			line = strings.Replace(line, oldQ, newQ, 1)
		}
		if _, werr := bw.WriteString(line); werr != nil {
			return werr
		}
		if errors.Is(err, io.EOF) {
			return bw.Flush()
		}
		if err != nil {
			return err
		}
	}
}
