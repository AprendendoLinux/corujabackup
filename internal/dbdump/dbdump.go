package dbdump

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/AprendendoLinux/corujabackup/internal/dockerapi"
)

const (
	EngineMySQL    = "mysql"
	EngineMariaDB  = "mariadb"
	EnginePostgres = "postgres"
	EngineMongo    = "mongodb"

	ModeDocker  = "docker"
	ModeNetwork = "network"

	RestoreNone       = "none"
	RestoreStructural = "structural"
	RestoreFull       = "full"
)

// Config é a configuração de um job de banco de dados (vem do Server).
type Config struct {
	Engine        string
	Mode          string
	Container     string
	Host          string
	Port          int
	User          string
	Password      string
	AuthDB        string
	Databases     []string // vazio = todos (menos Exclude)
	Exclude       []string
	SSLSkipVerify bool
	RestoreTest   string
	RestoreImage  string
}

type Hooks struct {
	Logf func(level, format string, args ...any)
}

func (h Hooks) log(level, f string, a ...any) {
	if h.Logf != nil {
		h.Logf(level, f, a...)
	}
}

var dbNameRE = regexp.MustCompile(`^[A-Za-z0-9_.$@-]{1,64}$`)

// ValidName impede nomes de banco que possam virar argumentos maliciosos.
func ValidName(n string) bool { return dbNameRE.MatchString(n) && !strings.HasPrefix(n, "-") }

func (c Config) validate() error {
	switch c.Engine {
	case EngineMySQL, EngineMariaDB, EnginePostgres, EngineMongo:
	default:
		return fmt.Errorf("motor de banco desconhecido: %q", c.Engine)
	}
	switch c.Mode {
	case ModeDocker:
		if c.Container == "" {
			return errors.New("informe o contêiner do banco")
		}
	case ModeNetwork:
		if c.Host == "" {
			return errors.New("informe o endereço do banco")
		}
	default:
		return fmt.Errorf("modo inválido: %q", c.Mode)
	}
	for _, d := range c.Databases {
		if !ValidName(d) {
			return fmt.Errorf("nome de banco inválido: %q", d)
		}
	}
	return nil
}

// engine isola o que muda entre os SGBDs.
type engine interface {
	ext() string
	// list devolve todos os bancos do servidor.
	list(ctx context.Context, r Runner, c Config) ([]string, error)
	dump(ctx context.Context, r Runner, c Config, db string, w io.Writer) (warn string, err error)
	// check confere a estrutura do dump (sem restaurar): pg_restore --list, mongorestore --dryRun...
	check(ctx context.Context, r Runner, c Config, file string, db string) error
	// count conta tabelas/coleções do banco de origem (para comparar com o restaurado).
	count(ctx context.Context, r Runner, c Config, db string) (int64, error)
	version(ctx context.Context, r Runner, c Config) string
	defaultExclude() []string
}

func newEngine(name string) engine {
	switch name {
	case EngineMySQL, EngineMariaDB:
		return mysqlEngine{}
	case EnginePostgres:
		return pgEngine{}
	default:
		return mongoEngine{}
	}
}

// DumpFile é um arquivo gerado.
type DumpFile struct {
	Name   string `json:"name"`
	DB     string `json:"database,omitempty"`
	Path   string `json:"-"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
	Tables int64  `json:"tables"`
}

type Output struct {
	Dir      string
	Files    []DumpFile
	Version  string
	Warnings int
	Engine   string
	Notes    []string // linhas de resumo (ex.: resultado do teste de restauração)
}

type Manifest struct {
	Tool      string     `json:"tool"`
	CreatedAt time.Time  `json:"created_at"`
	Engine    string     `json:"engine"`
	Version   string     `json:"server_version"`
	Source    string     `json:"source"`
	Databases []DumpFile `json:"files"`
}

// ResolveRunner escolhe onde os comandos rodam e, no modo contêiner, completa as credenciais que faltarem usando o
// ambiente do próprio contêiner (MYSQL_ROOT_PASSWORD, POSTGRES_PASSWORD...).
func ResolveRunner(ctx context.Context, cfg *Config, dk *dockerapi.Client) (Runner, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	if cfg.Mode == ModeNetwork {
		return LocalRunner{}, nil
	}
	if dk == nil || !dk.Available(ctx) {
		return nil, errors.New("o Docker não está acessível no agente (monte /var/run/docker.sock no contêiner do agente)")
	}
	ins, err := dk.Inspect(ctx, cfg.Container)
	if err != nil {
		return nil, fmt.Errorf("contêiner %q não encontrado: %w", cfg.Container, err)
	}
	if !ins.State.Running {
		return nil, fmt.Errorf("o contêiner %q não está em execução", cfg.Container)
	}
	env := ins.Env()
	if cfg.Password == "" {
		switch cfg.Engine {
		case EngineMySQL:
			cfg.User, cfg.Password = firstNonEmpty(cfg.User, "root"), env["MYSQL_ROOT_PASSWORD"]
		case EngineMariaDB:
			cfg.User, cfg.Password = firstNonEmpty(cfg.User, "root"), firstNonEmpty(env["MARIADB_ROOT_PASSWORD"], env["MYSQL_ROOT_PASSWORD"])
		case EnginePostgres:
			cfg.User, cfg.Password = firstNonEmpty(cfg.User, env["POSTGRES_USER"], "postgres"), env["POSTGRES_PASSWORD"]
		case EngineMongo:
			cfg.User, cfg.Password = firstNonEmpty(cfg.User, env["MONGO_INITDB_ROOT_USERNAME"]), env["MONGO_INITDB_ROOT_PASSWORD"]
		}
	}
	if cfg.User == "" {
		switch cfg.Engine {
		case EngineMySQL, EngineMariaDB:
			cfg.User = "root"
		case EnginePostgres:
			cfg.User = firstNonEmpty(env["POSTGRES_USER"], "postgres")
		}
	}
	if cfg.RestoreImage == "" {
		cfg.RestoreImage = ins.Config.Image
	}
	return DockerRunner{C: dk, Container: cfg.Container}, nil
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

// ListDatabases devolve os bancos que seriam salvos (usado pelo "testar conexão" da interface).
func ListDatabases(ctx context.Context, cfg Config, r Runner) ([]string, string, error) {
	e := newEngine(cfg.Engine)
	all, err := e.list(ctx, r, cfg)
	if err != nil {
		return nil, "", err
	}
	return filterDBs(all, cfg, e), e.version(ctx, r, cfg), nil
}

func filterDBs(all []string, cfg Config, e engine) []string {
	skip := map[string]bool{}
	for _, d := range append(e.defaultExclude(), cfg.Exclude...) {
		skip[d] = true
	}
	var out []string
	if len(cfg.Databases) > 0 {
		out = append(out, cfg.Databases...)
	} else {
		for _, d := range all {
			if ValidName(d) && !skip[d] {
				out = append(out, d)
			}
		}
	}
	sort.Strings(out)
	return out
}

// Run faz o dump de todos os bancos selecionados para workdir e devolve o que foi gerado. Falha se qualquer banco
// não puder ser salvo e conferido (um backup parcial não é entregue como se estivesse completo).
func Run(ctx context.Context, cfg Config, r Runner, dk *dockerapi.Client, workdir string, h Hooks) (*Output, error) {
	e := newEngine(cfg.Engine)
	if err := os.MkdirAll(workdir, 0o750); err != nil {
		return nil, err
	}
	h.log("info", "motor: %s · origem: %s", cfg.Engine, r.Where())
	all, err := e.list(ctx, r, cfg)
	listed := true
	if err != nil {
		if len(cfg.Databases) == 0 {
			return nil, fmt.Errorf("não foi possível listar os bancos (informe os bancos manualmente se o cliente de listagem não existir): %w", err)
		}
		// com a lista informada seguimos mesmo sem conseguir listar (ex.: MongoDB em modo rede, sem mongosh no agente)
		h.log("warn", "não foi possível listar os bancos do servidor (%v); usando a lista informada", err)
		listed = false
		all = cfg.Databases
	}
	dbs := filterDBs(all, cfg, e)
	if len(cfg.Databases) > 0 && listed {
		have := map[string]bool{}
		for _, d := range all {
			have[d] = true
		}
		for _, d := range cfg.Databases {
			if !have[d] {
				return nil, fmt.Errorf("o banco %q não existe no servidor", d)
			}
		}
	}
	if len(dbs) == 0 {
		return nil, errors.New("nenhum banco para salvar (todos foram excluídos ou o servidor está vazio)")
	}
	out := &Output{Dir: workdir, Engine: cfg.Engine, Version: e.version(ctx, r, cfg)}
	h.log("info", "versão do servidor: %s · %d banco(s): %s", out.Version, len(dbs), strings.Join(dbs, ", "))

	for _, db := range dbs {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		f, warn, err := dumpOne(ctx, e, cfg, r, db, workdir)
		if err != nil {
			return nil, fmt.Errorf("banco %s: %w", db, err)
		}
		if warn != "" {
			out.Warnings++
			h.log("warn", "banco %s: %s", db, warn)
		}
		if err := e.check(ctx, r, cfg, f.Path, db); err != nil {
			return nil, fmt.Errorf("banco %s: o dump não passou na conferência estrutural: %w", db, err)
		}
		if n, err := e.count(ctx, r, cfg, db); err == nil {
			f.Tables = n
		}
		h.log("info", "banco %s: %s, %d tabela(s)/coleção(ões), conferido", db, humanBytes(f.Bytes), f.Tables)
		out.Files = append(out.Files, f)
	}
	if g, ok := e.(interface {
		globals(ctx context.Context, r Runner, c Config, w io.Writer) error
	}); ok && len(cfg.Databases) == 0 {
		path := filepath.Join(workdir, "globals.sql")
		f, err := os.Create(path)
		if err != nil {
			return nil, err
		}
		gerr := g.globals(ctx, r, cfg, f)
		f.Close()
		if gerr != nil {
			out.Warnings++
			h.log("warn", "não foi possível salvar papéis/globais: %v", gerr)
			os.Remove(path)
		} else if df, err := fileInfo(path, "globals.sql", ""); err == nil {
			out.Files = append(out.Files, df)
			h.log("info", "papéis e permissões globais salvos (%s)", humanBytes(df.Bytes))
		}
	}
	m := Manifest{Tool: "Coruja Backup", CreatedAt: time.Now().UTC(), Engine: cfg.Engine, Version: out.Version, Source: r.Where(), Databases: out.Files}
	mb, _ := json.MarshalIndent(m, "", "  ")
	if err := os.WriteFile(filepath.Join(workdir, "manifest.json"), mb, 0o640); err != nil {
		return nil, err
	}
	return out, nil
}

func dumpOne(ctx context.Context, e engine, cfg Config, r Runner, db, dir string) (DumpFile, string, error) {
	name := db + e.ext()
	path := filepath.Join(dir, name)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o640)
	if err != nil {
		return DumpFile{}, "", err
	}
	warn, derr := e.dump(ctx, r, cfg, db, f)
	cerr := f.Close()
	if derr != nil {
		os.Remove(path)
		return DumpFile{}, "", derr
	}
	if cerr != nil {
		return DumpFile{}, "", cerr
	}
	df, err := fileInfo(path, name, db)
	if err != nil {
		return DumpFile{}, "", err
	}
	if df.Bytes == 0 {
		return DumpFile{}, "", errors.New("o dump saiu vazio")
	}
	return df, warn, nil
}

func fileInfo(path, name, db string) (DumpFile, error) {
	f, err := os.Open(path)
	if err != nil {
		return DumpFile{}, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return DumpFile{}, err
	}
	return DumpFile{Name: name, DB: db, Path: path, Bytes: n, SHA256: hex.EncodeToString(h.Sum(nil))}, nil
}

func humanBytes(n int64) string {
	const u = 1024
	if n < u {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(u), 0
	for m := n / u; m >= u; m /= u {
		div *= u
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
