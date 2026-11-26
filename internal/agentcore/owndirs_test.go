package agentcore

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func tarNames(t *testing.T, file string) []string {
	t.Helper()
	f, err := os.Open(file)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	var out []string
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, h.Name)
	}
}

func TestBackupNaoIncluiAsPastasDoProprioAgente(t *testing.T) {
	root := t.TempDir()
	for _, d := range []string{"srv/app", "srv/coruja-backups/antigo", "srv/destinos/nas"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []string{"srv/app/dados.txt", "srv/coruja-backups/antigo/velho.tar.gz", "srv/destinos/nas/copia.tar.gz"} {
		if err := os.WriteFile(filepath.Join(root, f), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// o agente grava por outro caminho (como o /backups do contêiner); aqui, um atalho para a mesma pasta
	alias := filepath.Join(t.TempDir(), "backups")
	if err := os.Symlink(filepath.Join(root, "srv/coruja-backups"), alias); err != nil {
		t.Fatal(err)
	}
	var logs []string
	h := Hooks{Logf: func(level, f string, a ...any) { logs = append(logs, level+" "+fmt.Sprintf(f, a...)) }}
	job := FilesJob{Name: "srv", Paths: []string{"/srv"}, Subdir: "srv", Keep: 3, OwnDirs: []string{filepath.Join(root, "srv/destinos")}}
	for i := range 2 { // na segunda vez já existe um backup gerado pelo próprio job
		res, err := RunFiles(context.Background(), job, root, alias, time.UTC, time.Date(2026, 1, 1, 0, i, 0, 0, time.UTC), h)
		if err != nil {
			t.Fatal(err)
		}
		got := tarNames(t, res.OutputPath)
		if !slices.Equal(got, []string{"srv/", "srv/app/", "srv/app/dados.txt"}) || res.Warnings != 0 {
			t.Fatalf("execução %d: %v (avisos: %d)", i, got, res.Warnings)
		}
	}
	all := strings.Join(logs, "\n")
	for _, want := range []string{"info pasta do próprio agente deixada de fora: /srv/coruja-backups", "info pasta do próprio agente deixada de fora: /srv/destinos"} {
		if !strings.Contains(all, want) {
			t.Errorf("faltou no log: %s", want)
		}
	}

	// origem escolhida DENTRO da pasta de backups: ignorada com aviso; se não sobrar nada, o job falha
	logs = nil
	job.Paths = []string{"/srv/coruja-backups/antigo", "/srv/app"}
	res, err := RunFiles(context.Background(), job, root, alias, time.UTC, time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC), h)
	if err != nil || res.Warnings != 1 || !slices.Equal(tarNames(t, res.OutputPath), []string{"srv/app/", "srv/app/dados.txt"}) {
		t.Fatalf("%v %+v", err, res)
	}
	if !strings.Contains(strings.Join(logs, "\n"), "warn origem ignorada: /srv/coruja-backups/antigo") {
		t.Errorf("faltou o aviso: %v", logs)
	}
	job.Paths = []string{"/srv/coruja-backups"}
	if _, err := RunFiles(context.Background(), job, root, alias, time.UTC, time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC), h); err == nil {
		t.Fatal("sem nada a salvar, o job deveria falhar")
	}
}

// Salvar o "/" não pode levar a credencial do agente nem as senhas dos jobs para o backup.
func TestBackupNaoLevaOsDadosDoAgente(t *testing.T) {
	root := t.TempDir()
	mk(t, root, "etc/hostname", "var/lib/dados-do-agente/", "var/lib/outro/arquivo")
	st, err := NewState(filepath.Join(root, "var/lib/dados-do-agente"))
	if err != nil {
		t.Fatal(err)
	}
	mk(t, root, "var/lib/dados-do-agente/agent.json", "var/lib/dados-do-agente/jobs.json")
	c := New(Config{HostRoot: root, BackupDir: t.TempDir()}, st, slog.New(slog.NewTextHandler(io.Discard, nil)))
	run := func(paths ...string) (Result, error) {
		return c.exec(context.Background(), FilesJob{Name: "tudo", Paths: paths, Subdir: "tudo", Keep: 1, SkipRetention: true}, time.UTC, time.Now(), discard())
	}
	res, err := run("/")
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range tarNames(t, res.OutputPath) {
		if strings.Contains(n, "dados-do-agente") {
			t.Errorf("foi para o backup: %s", n)
		}
	}
	if !slices.Contains(tarNames(t, res.OutputPath), "var/lib/outro/arquivo") {
		t.Error("o restante de /var/lib deveria ser salvo")
	}
	// nem escolhendo o arquivo da credencial diretamente
	for _, p := range []string{"/var/lib/dados-do-agente", "/var/lib/dados-do-agente/agent.json"} {
		if _, err := run(p); err == nil {
			t.Errorf("%s não poderia gerar backup", p)
		}
	}
}

func TestPontosDeMontagemDentroDasPastasDoAgente(t *testing.T) {
	info := "36 35 98:0 / /destinos/meu\\040nas rw - nfs host:/x rw\n37 35 98:1 / /destinos-outro rw - ext4 /dev/sdb rw\nlinha curta\n"
	m := mountPoints(strings.NewReader(info))
	if !slices.Equal(m, []string{"/destinos/meu nas", "/destinos-outro"}) {
		t.Fatalf("%q", m)
	}
	base := t.TempDir()
	for _, d := range []string{"destinos/nas", "destinos-outro"} {
		if err := os.MkdirAll(filepath.Join(base, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	own := resolveOwnDirs([]string{filepath.Join(base, "destinos/nas"), filepath.Join(base, "destinos-outro")}, filepath.Join(base, "destinos"), "", filepath.Join(base, "nao-existe"))
	if len(own) != 2 { // a própria raiz e o que está montado dentro; "destinos-outro" não está dentro
		t.Fatalf("esperava 2 pastas, veio %d", len(own))
	}
}
