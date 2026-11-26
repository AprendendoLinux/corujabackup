package dbdump

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// wrapper escolhe a ferramenta de cliente disponível ("a|b" = tenta a, depois b) e troca os marcadores @@USER@@ e
// @@PASS@@ pelo conteúdo das variáveis CB_USER/CB_PASS. Assim a senha nunca aparece na linha de comando de quem chama
// (nem na lista de processos do host).
const wrapper = `T=; for c in $(echo "$1" | tr '|' ' '); do if command -v "$c" >/dev/null 2>&1; then T=$c; break; fi; done
[ -n "$T" ] || { echo "cliente de banco não encontrado: $1" >&2; exit 127; }
shift; n=$#; i=0
while [ $i -lt $n ]; do a=$1; shift; case "$a" in @@USER@@) a=$CB_USER;; @@PASS@@) a=$CB_PASS;; esac; set -- "$@" "$a"; i=$((i+1)); done
exec "$T" "$@"`

func wrap(tool string, args ...string) []string {
	return append([]string{"sh", "-c", wrapper, "sh", tool}, args...)
}

func envFor(c Config, extra ...string) []string {
	env := []string{"CB_USER=" + c.User, "CB_PASS=" + c.Password}
	return append(env, extra...)
}

// markerWriter guarda o final do que passa, para procurar o marcador de término do dump.
type markerWriter struct {
	w    io.Writer
	tail []byte
}

func (m *markerWriter) Write(p []byte) (int, error) {
	n, err := m.w.Write(p)
	m.tail = append(m.tail, p[:n]...)
	if len(m.tail) > 512 {
		m.tail = m.tail[len(m.tail)-512:]
	}
	return n, err
}

func lines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

func clip(s string) string {
	if len(s) > 300 {
		return s[:300] + "…"
	}
	return s
}

// ---------------------------------------------------------------- MySQL / MariaDB

type mysqlEngine struct{}

func (mysqlEngine) ext() string { return ".sql" }
func (mysqlEngine) defaultExclude() []string {
	return []string{"information_schema", "performance_schema", "sys"}
}

func (mysqlEngine) conn(c Config) []string {
	var a []string
	if c.User != "" {
		a = append(a, "-u", "@@USER@@")
	}
	if c.Mode == ModeNetwork {
		a = append(a, "-h", c.Host)
		if c.Port > 0 {
			a = append(a, "-P", strconv.Itoa(c.Port))
		}
		if c.SSLSkipVerify {
			a = append(a, "--ssl", "--skip-ssl-verify-server-cert") // cliente MariaDB com certificado autoassinado
		}
	}
	return a
}

func (e mysqlEngine) env(c Config) []string {
	if c.Password == "" {
		return envFor(c)
	}
	return envFor(c, "MYSQL_PWD="+c.Password)
}

func (e mysqlEngine) query(ctx context.Context, r Runner, c Config, q string) (string, error) {
	out, code, se, err := capture(ctx, r, Cmd{Args: wrap("mariadb|mysql", append(e.conn(c), "-N", "-B", "-e", q)...), Env: e.env(c)})
	if err != nil {
		return "", err
	}
	if code != 0 {
		return "", fmt.Errorf("%s", clip(firstNonEmpty(se, fmt.Sprintf("o cliente terminou com código %d", code))))
	}
	return out, nil
}

func (e mysqlEngine) list(ctx context.Context, r Runner, c Config) ([]string, error) {
	out, err := e.query(ctx, r, c, "SHOW DATABASES")
	if err != nil {
		return nil, err
	}
	return lines(out), nil
}

func (e mysqlEngine) version(ctx context.Context, r Runner, c Config) string {
	v, err := e.query(ctx, r, c, "SELECT VERSION()")
	if err != nil {
		return "desconhecida"
	}
	return v
}

func (e mysqlEngine) count(ctx context.Context, r Runner, c Config, db string) (int64, error) {
	out, err := e.query(ctx, r, c, "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema='"+db+"'")
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(strings.TrimSpace(out), 10, 64)
}

const mysqlMarker = "-- Dump completed"

func (e mysqlEngine) dump(ctx context.Context, r Runner, c Config, db string, w io.Writer) (string, error) {
	mw := &markerWriter{w: w}
	args := append(e.conn(c), "--single-transaction", "--quick", "--routines", "--triggers", "--events", "--hex-blob",
		"--default-character-set=utf8mb4", "--databases", db)
	code, se, err := r.Run(ctx, Cmd{Args: wrap("mariadb-dump|mysqldump", args...), Env: e.env(c), Stdout: mw})
	if err != nil {
		return "", err
	}
	// O código de saída sozinho engana: clientes MariaDB contra servidores MySQL novos devolvem erro depois de o dump
	// estar completo. O marcador final é a prova de que o dump terminou.
	hasMarker := bytes.Contains(mw.tail, []byte(mysqlMarker))
	switch {
	case code == 0 && hasMarker:
		return "", nil
	case hasMarker:
		return fmt.Sprintf("o dump terminou (marcador presente), mas o cliente retornou código %d: %s", code, clip(se)), nil
	default:
		return "", fmt.Errorf("dump incompleto (código %d, sem marcador final): %s", code, clip(se))
	}
}

func (e mysqlEngine) check(_ context.Context, _ Runner, _ Config, file, _ string) error {
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer f.Close()
	head := make([]byte, 256)
	n, _ := f.Read(head)
	if !bytes.Contains(head[:n], []byte("-- MySQL dump")) && !bytes.Contains(head[:n], []byte("-- MariaDB dump")) && !bytes.Contains(head[:n], []byte("/*")) {
		return errors.New("cabeçalho do dump não reconhecido")
	}
	st, _ := f.Stat()
	off := st.Size() - 512
	if off < 0 {
		off = 0
	}
	tail := make([]byte, st.Size()-off)
	if _, err := f.ReadAt(tail, off); err != nil && err != io.EOF {
		return err
	}
	if !bytes.Contains(tail, []byte(mysqlMarker)) {
		return errors.New("o dump não termina com o marcador de conclusão")
	}
	return nil
}

// ---------------------------------------------------------------- PostgreSQL

type pgEngine struct{}

func (pgEngine) ext() string              { return ".dump" }
func (pgEngine) defaultExclude() []string { return nil }

func (pgEngine) conn(c Config) []string {
	var a []string
	if c.User != "" {
		a = append(a, "-U", "@@USER@@")
	}
	if c.Mode == ModeNetwork {
		a = append(a, "-h", c.Host)
		if c.Port > 0 {
			a = append(a, "-p", strconv.Itoa(c.Port))
		}
	}
	return a
}

func (pgEngine) env(c Config) []string {
	e := envFor(c)
	if c.Password != "" {
		e = append(e, "PGPASSWORD="+c.Password)
	}
	if c.Mode == ModeNetwork && c.SSLSkipVerify {
		e = append(e, "PGSSLMODE=require")
	}
	return e
}

func (e pgEngine) query(ctx context.Context, r Runner, c Config, db, q string) (string, error) {
	args := append(e.conn(c), "-X", "-A", "-t", "-d", db, "-c", q)
	out, code, se, err := capture(ctx, r, Cmd{Args: wrap("psql", args...), Env: e.env(c)})
	if err != nil {
		return "", err
	}
	if code != 0 {
		return "", fmt.Errorf("%s", clip(firstNonEmpty(se, fmt.Sprintf("psql terminou com código %d", code))))
	}
	return out, nil
}

func (e pgEngine) list(ctx context.Context, r Runner, c Config) ([]string, error) {
	out, err := e.query(ctx, r, c, "postgres", "SELECT datname FROM pg_database WHERE NOT datistemplate AND datallowconn ORDER BY 1")
	if err != nil {
		return nil, err
	}
	return lines(out), nil
}

func (e pgEngine) version(ctx context.Context, r Runner, c Config) string {
	v, err := e.query(ctx, r, c, "postgres", "SHOW server_version")
	if err != nil {
		return "desconhecida"
	}
	return v
}

func (e pgEngine) count(ctx context.Context, r Runner, c Config, db string) (int64, error) {
	out, err := e.query(ctx, r, c, db, "SELECT count(*) FROM information_schema.tables WHERE table_schema NOT IN ('pg_catalog','information_schema')")
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(strings.TrimSpace(out), 10, 64)
}

func (e pgEngine) dump(ctx context.Context, r Runner, c Config, db string, w io.Writer) (string, error) {
	args := append(e.conn(c), "-Fc", "-d", db)
	code, se, err := r.Run(ctx, Cmd{Args: wrap("pg_dump", args...), Env: e.env(c), Stdout: w})
	if err != nil {
		return "", err
	}
	if code != 0 {
		return "", fmt.Errorf("pg_dump terminou com código %d: %s", code, clip(se))
	}
	return "", nil
}

// globals salva papéis e permissões do cluster (não fazem parte de nenhum banco).
func (e pgEngine) globals(ctx context.Context, r Runner, c Config, w io.Writer) error {
	code, se, err := r.Run(ctx, Cmd{Args: wrap("pg_dumpall", append(e.conn(c), "--globals-only")...), Env: e.env(c), Stdout: w})
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("pg_dumpall terminou com código %d: %s", code, clip(se))
	}
	return nil
}

func (e pgEngine) check(ctx context.Context, r Runner, c Config, file, _ string) error {
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer f.Close()
	var magic [5]byte
	if _, err := io.ReadFull(f, magic[:]); err != nil || string(magic[:]) != "PGDMP" {
		return errors.New("não é um dump no formato custom do PostgreSQL")
	}
	f.Seek(0, io.SeekStart)
	code, se, err := r.Run(ctx, Cmd{Args: wrap("pg_restore", "--list"), Env: e.env(c), Stdin: f, Stdout: io.Discard})
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("pg_restore --list falhou (código %d): %s", code, clip(se))
	}
	return nil
}

// ---------------------------------------------------------------- MongoDB

type mongoEngine struct{}

func (mongoEngine) ext() string              { return ".archive.gz" }
func (mongoEngine) defaultExclude() []string { return []string{"local", "config"} }

func (mongoEngine) conn(c Config) []string {
	var a []string
	if c.Mode == ModeNetwork {
		a = append(a, "--host", c.Host)
		if c.Port > 0 {
			a = append(a, "--port", strconv.Itoa(c.Port))
		}
	}
	if c.User != "" {
		a = append(a, "--username", "@@USER@@", "--password", "@@PASS@@", "--authenticationDatabase", firstNonEmpty(c.AuthDB, "admin"))
	}
	return a
}

func (e mongoEngine) eval(ctx context.Context, r Runner, c Config, js string) (string, error) {
	args := append(e.conn(c), "--quiet", "--eval", js)
	out, code, se, err := capture(ctx, r, Cmd{Args: wrap("mongosh|mongo", args...), Env: envFor(c)})
	if err != nil {
		return "", err
	}
	if code != 0 {
		return "", fmt.Errorf("%s", clip(firstNonEmpty(se, fmt.Sprintf("mongosh terminou com código %d", code))))
	}
	return out, nil
}

func (e mongoEngine) list(ctx context.Context, r Runner, c Config) ([]string, error) {
	out, err := e.eval(ctx, r, c, `db.adminCommand({listDatabases:1}).databases.map(function(d){return d.name}).join("\n")`)
	if err != nil {
		return nil, err
	}
	return lines(out), nil
}

func (e mongoEngine) version(ctx context.Context, r Runner, c Config) string {
	v, err := e.eval(ctx, r, c, "db.version()")
	if err != nil {
		return "desconhecida"
	}
	return v
}

func (e mongoEngine) count(ctx context.Context, r Runner, c Config, db string) (int64, error) {
	out, err := e.eval(ctx, r, c, `db.getSiblingDB("`+db+`").getCollectionNames().length`)
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(strings.TrimSpace(out), 10, 64)
}

func (e mongoEngine) dump(ctx context.Context, r Runner, c Config, db string, w io.Writer) (string, error) {
	args := append(e.conn(c), "--archive", "--gzip", "--db", db)
	code, se, err := r.Run(ctx, Cmd{Args: wrap("mongodump", args...), Env: envFor(c), Stdout: w})
	if err != nil {
		return "", err
	}
	if code != 0 {
		return "", fmt.Errorf("mongodump terminou com código %d: %s", code, clip(se))
	}
	return "", nil
}

// check: o arquivo precisa ser um gzip íntegro cujo conteúdo começa com a assinatura de arquivo do mongodump.
func (mongoEngine) check(_ context.Context, _ Runner, _ Config, file, _ string) error {
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("gzip inválido: %w", err)
	}
	var magic [4]byte
	if _, err := io.ReadFull(gz, magic[:]); err != nil {
		return fmt.Errorf("arquivo vazio ou truncado: %w", err)
	}
	if binary.LittleEndian.Uint32(magic[:]) != 0x8199e26d {
		return errors.New("não é um arquivo (archive) do mongodump")
	}
	if _, err := io.Copy(io.Discard, gz); err != nil {
		return fmt.Errorf("o gzip está corrompido: %w", err)
	}
	return nil
}
