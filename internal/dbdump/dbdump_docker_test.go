package dbdump

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AprendendoLinux/corujabackup/internal/dockerapi"
)

// Testes com contêineres REAIS (MySQL, MariaDB, PostgreSQL, MongoDB). Precisam do Docker e baixam imagens;
// rodam só com CORUJA_TEST_DOCKER=1 (e o socket montado).
func dockerOrSkip(t *testing.T) *dockerapi.Client {
	t.Helper()
	if os.Getenv("CORUJA_TEST_DOCKER") != "1" {
		t.Skip("CORUJA_TEST_DOCKER=1 não definido")
	}
	dk := dockerapi.New("")
	if !dk.Available(context.Background()) {
		t.Skip("Docker indisponível")
	}
	return dk
}

func startSource(t *testing.T, dk *dockerapi.Client, image string, env []string, c Config) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	if err := dk.Pull(ctx, image); err != nil {
		t.Fatalf("pull %s: %v", image, err)
	}
	id, err := dk.Create(ctx, dockerapi.CreateOptions{Image: image, Env: env, Memory: 2 << 30, Labels: map[string]string{"coruja.test": "1"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { dk.Remove(context.Background(), id) })
	if err := dk.Start(ctx, id); err != nil {
		t.Fatal(err)
	}
	c.Container, c.Mode = id, ModeDocker
	if err := waitReady(ctx, DockerRunner{C: dk, Container: id}, c); err != nil {
		t.Fatal(err)
	}
	return id
}

func run(t *testing.T, r Runner, args []string, env []string, stdin string) {
	t.Helper()
	var in io.Reader
	if stdin != "" {
		in = strings.NewReader(stdin)
	}
	code, se, err := r.Run(context.Background(), Cmd{Args: args, Env: env, Stdin: in, Stdout: io.Discard})
	if err != nil || code != 0 {
		t.Fatalf("exec %v: código %d %v %s", args[4:], code, err, se)
	}
}

type logs struct{ lines []string }

func (l *logs) hooks() Hooks {
	return Hooks{Logf: func(level, f string, a ...any) {
		l.lines = append(l.lines, level+": "+strings.TrimSpace(sprintf(f, a...)))
	}}
}

func sprintf(f string, a ...any) string { return fmt.Sprintf(f, a...) }

func runAndRestore(t *testing.T, dk *dockerapi.Client, cfg Config, wantDBs map[string]int64, ext string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	r, err := ResolveRunner(ctx, &cfg, dk)
	if err != nil {
		t.Fatal(err)
	}
	var lg logs
	dir := t.TempDir()
	out, err := Run(ctx, cfg, r, dk, dir, lg.hooks())
	if err != nil {
		t.Fatalf("%v\n%s", err, strings.Join(lg.lines, "\n"))
	}
	got := map[string]int64{}
	for _, f := range out.Files {
		if f.DB != "" {
			got[f.DB] = f.Tables
			if !strings.HasSuffix(f.Name, ext) || f.Bytes == 0 || len(f.SHA256) != 64 {
				t.Errorf("arquivo inesperado: %+v", f)
			}
		}
	}
	if len(got) != len(wantDBs) {
		t.Fatalf("bancos salvos %v, esperado %v\n%s", got, wantDBs, strings.Join(lg.lines, "\n"))
	}
	for db, n := range wantDBs {
		if got[db] != n {
			t.Errorf("banco %s: %d tabela(s), esperado %d", db, got[db], n)
		}
	}
	var m Manifest
	b, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil || json.Unmarshal(b, &m) != nil || m.Engine != cfg.Engine || len(m.Databases) == 0 || m.Version == "" {
		t.Fatalf("manifesto inválido: %v %s", err, b)
	}
	if err := RestoreTest(ctx, dk, cfg, out, lg.hooks()); err != nil {
		t.Fatalf("teste de restauração: %v\n%s", err, strings.Join(lg.lines, "\n"))
	}
	if !strings.Contains(strings.Join(lg.lines, "\n"), "igual à origem") {
		t.Fatalf("o teste de restauração deveria confirmar a igualdade:\n%s", strings.Join(lg.lines, "\n"))
	}
}

func TestMySQLContainerDumpEDeRestauracao(t *testing.T) {
	dk := dockerOrSkip(t)
	cfg := Config{Engine: EngineMySQL, Mode: ModeDocker, Exclude: []string{"mysql"}}
	id := startSource(t, dk, "mysql:8.0", []string{"MYSQL_ROOT_PASSWORD=segredo-mysql"}, Config{Engine: EngineMySQL, User: "root", Password: "segredo-mysql"})
	r := DockerRunner{C: dk, Container: id}
	sql := "CREATE DATABASE app; CREATE DATABASE outro; USE app; CREATE TABLE t1 (id INT PRIMARY KEY, v VARCHAR(20)); CREATE TABLE t2 (id INT PRIMARY KEY); INSERT INTO t1 VALUES (1,'um'),(2,'dois'); USE outro; CREATE TABLE x (id INT);"
	run(t, r, wrap("mysql", "-u", "@@USER@@"), []string{"CB_USER=root", "MYSQL_PWD=segredo-mysql"}, sql)
	cfg.Container = id
	runAndRestore(t, dk, cfg, map[string]int64{"app": 2, "outro": 1}, ".sql")
}

func TestMariaDBContainerDumpEDeRestauracao(t *testing.T) {
	dk := dockerOrSkip(t)
	id := startSource(t, dk, "mariadb:11.4", []string{"MARIADB_ROOT_PASSWORD=segredo-maria"}, Config{Engine: EngineMariaDB, User: "root", Password: "segredo-maria"})
	r := DockerRunner{C: dk, Container: id}
	run(t, r, wrap("mariadb|mysql", "-u", "@@USER@@"), []string{"CB_USER=root", "MYSQL_PWD=segredo-maria"},
		"CREATE DATABASE loja; USE loja; CREATE TABLE produtos (id INT PRIMARY KEY, nome TEXT); CREATE TABLE pedidos (id INT PRIMARY KEY); INSERT INTO produtos VALUES (1,'caneta');")
	cfg := Config{Engine: EngineMariaDB, Mode: ModeDocker, Container: id, Databases: []string{"loja"}}
	runAndRestore(t, dk, cfg, map[string]int64{"loja": 2}, ".sql")
}

func TestPostgresContainerDumpEDeRestauracao(t *testing.T) {
	dk := dockerOrSkip(t)
	id := startSource(t, dk, "postgres:17", []string{"POSTGRES_PASSWORD=segredo-pg"}, Config{Engine: EnginePostgres, User: "postgres", Password: "segredo-pg"})
	r := DockerRunner{C: dk, Container: id}
	env := []string{"CB_USER=postgres", "PGPASSWORD=segredo-pg"}
	run(t, r, wrap("psql", "-U", "@@USER@@", "-d", "postgres", "-c", "CREATE DATABASE clientes"), env, "")
	run(t, r, wrap("psql", "-U", "@@USER@@", "-d", "clientes"), env, "CREATE TABLE c (id serial PRIMARY KEY, nome text); CREATE TABLE d (id int); INSERT INTO c (nome) VALUES ('ana'),('bia'); CREATE ROLE leitura LOGIN;")
	cfg := Config{Engine: EnginePostgres, Mode: ModeDocker, Container: id}
	runAndRestore(t, dk, cfg, map[string]int64{"clientes": 2, "postgres": 0}, ".dump")
}

func TestMongoContainerDumpEDeRestauracao(t *testing.T) {
	dk := dockerOrSkip(t)
	id := startSource(t, dk, "mongo:7.0", nil, Config{Engine: EngineMongo})
	r := DockerRunner{C: dk, Container: id}
	run(t, r, wrap("mongosh|mongo", "--quiet", "--eval", `db.getSiblingDB("app").c1.insertOne({a:1}); db.getSiblingDB("app").c2.insertOne({b:2}); db.getSiblingDB("app").c2.insertOne({b:3});`), nil, "")
	cfg := Config{Engine: EngineMongo, Mode: ModeDocker, Container: id}
	runAndRestore(t, dk, cfg, map[string]int64{"app": 2, "admin": 1}, ".archive.gz")
}

func TestContainerInexistenteEBancoInvalido(t *testing.T) {
	dk := dockerOrSkip(t)
	if _, err := ResolveRunner(context.Background(), &Config{Engine: EngineMySQL, Mode: ModeDocker, Container: "nao-existe-xyz"}, dk); err == nil {
		t.Fatal("contêiner inexistente deveria falhar")
	}
	for _, bad := range []string{"", "-rf", "a b", "a;b", "x'y", strings.Repeat("a", 65)} {
		if ValidName(bad) {
			t.Errorf("aceitou nome de banco %q", bad)
		}
	}
	if err := (Config{Engine: "oracle", Mode: ModeDocker, Container: "x"}).validate(); err == nil {
		t.Fatal("motor desconhecido deveria falhar")
	}
}
