package agentcore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AprendendoLinux/corujabackup/internal/netdest"
	"github.com/AprendendoLinux/corujabackup/internal/pb"
)

// Backup em pasta numa pasta local: a pasta fica igual à origem (com permissões, link simbólico e pasta vazia), o
// incremental liga por link físico o que não mudou, a restauração confere cada arquivo pelo catálogo (e recusa um
// adulterado), o catálogo dentro da pasta serve a um agente sem o dele, a retenção apaga pastas antigas sem estragar as
// novas e um cancelamento não deixa a pasta provisória.
func TestBackupEmPasta(t *testing.T) {
	root, bk := t.TempDir(), t.TempDir()
	st, _ := NewState(t.TempDir())
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	c := New(Config{HostRoot: root, HostWriteRoot: root, BackupDir: bk}, st, quiet)
	logs := logSink(c)
	src := filepath.Join(root, "srv")
	write(t, filepath.Join(src, "a.txt"), "a v1")
	write(t, filepath.Join(src, "grande.bin"), noise(300<<10, 1))
	write(t, filepath.Join(src, "dir", "c.txt"), "c v1")
	write(t, filepath.Join(src, "secreto"), "só o dono")
	os.Chmod(filepath.Join(src, "secreto"), 0o600)
	os.MkdirAll(filepath.Join(src, "vazia"), 0o750)
	os.Symlink("a.txt", filepath.Join(src, "atalho"))
	ctx := context.Background()
	t0 := time.Date(2026, 11, 28, 3, 0, 0, 0, time.UTC)
	job := FilesJob{Name: "Pasta", Paths: []string{"/srv"}, Subdir: "pasta", Keep: 2}
	dest := filepath.Join(bk, "pasta")
	run := func(ctx context.Context, day int, base *Snapshot) (Result, error) {
		t.Helper()
		tg, err := netdest.LocalTarget(dest)
		if err != nil {
			t.Fatal(err)
		}
		defer tg.Close()
		j := job
		j.inc = &incPlan{base: base}
		res, err := RunDir(ctx, j, dirTarget{t: tg, dest: dest}, root, bk, time.UTC, t0.Add(time.Duration(day)*24*time.Hour), discard())
		if err == nil {
			if err := st.SaveCatalog(1, res.OutputName, res.Snapshot); err != nil {
				t.Fatal(err)
			}
		}
		return res, err
	}
	same := func(name string) {
		t.Helper()
		got := tree(t, filepath.Join(dest, name))
		if _, ok := got[dirManifest]; !ok {
			t.Fatal("a pasta deveria ter o catálogo")
		}
		delete(got, dirManifest)
		if d := diff(tree(t, root), got); d != "" {
			t.Fatalf("a pasta %s difere da origem:\n%s", name, d)
		}
	}

	r1, err := run(ctx, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if r1.OutputName != "pasta-20261128-030000" || r1.Kind != ModeFull || r1.Stored != nil || r1.OutputPath != filepath.Join(dest, r1.OutputName) {
		t.Fatalf("resultado: %+v", r1)
	}
	same(r1.OutputName)
	if fi, _ := os.Stat(filepath.Join(dest, r1.OutputName)); fi.Mode().Perm() != 0o700 {
		t.Fatalf("a pasta da execução deveria ser só do dono: %v", fi.Mode())
	}
	if fi, _ := os.Stat(filepath.Join(dest, r1.OutputName, "srv", "secreto")); fi.Mode().Perm() != 0o600 {
		t.Fatalf("permissões originais: %v", fi.Mode())
	}
	b, _ := os.ReadFile(filepath.Join(dest, r1.OutputName, dirManifest))
	if s := sha256.Sum256(b); hex.EncodeToString(s[:]) != r1.SHA256 {
		t.Fatal("o SHA-256 da execução é o do catálogo da pasta")
	}
	if m := r1.Snapshot.Files["/srv/grande.bin"]; len(m.H) != 64 || m.Size != 300<<10 {
		t.Fatalf("catálogo do arquivo: %+v", m)
	}
	if m := r1.Snapshot.Files["/srv/atalho"]; m.L != "a.txt" {
		t.Fatalf("link no catálogo: %+v", m)
	}

	// incremental: o que não mudou é o mesmo arquivo (link físico), o alterado é uma cópia nova
	time.Sleep(10 * time.Millisecond)
	write(t, filepath.Join(src, "a.txt"), "a v2, maior")
	r2, err := run(ctx, 1, r1.Snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if r2.Kind != ModeIncremental || r2.BytesSource != int64(len("a v2, maior")) {
		t.Fatalf("incremental: %+v", r2)
	}
	same(r2.OutputName)
	link := func(rel string) bool {
		a, _ := os.Stat(filepath.Join(dest, r1.OutputName, rel))
		b, _ := os.Stat(filepath.Join(dest, r2.OutputName, rel))
		return os.SameFile(a, b)
	}
	if !link("srv/grande.bin") || !link("srv/dir/c.txt") || link("srv/a.txt") {
		t.Fatal("só o que não mudou deveria ser link físico para a pasta anterior")
	}

	act := func(name, target string, paths ...string) *pb.AgentAction {
		return &pb.AgentAction{Kind: "restore_files", JobId: 1, FileName: name, Subdir: "pasta", Paths: paths, Target: target}
	}
	// um arquivo e uma pasta inteira (extrair), conferidos pelo catálogo
	if err := c.restoreFiles(ctx, act(r2.OutputName, "um", "/srv/grande.bin"), &actionLog{c: c, act: &pb.AgentAction{}}); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(bk, "restores", "um", "srv", "grande.bin")); string(b) != noise(300<<10, 1) {
		t.Fatal("arquivo restaurado diferente")
	}
	if l := logs(); !strings.Contains(l, "da pasta "+r2.OutputName+" (agente)") {
		t.Fatalf("log da restauração:\n%s", l)
	}
	ex := act(r2.OutputName, "tudo")
	ex.Kind = "extract"
	if err := c.extract(ctx, ex, &actionLog{c: c, act: ex}); err != nil {
		t.Fatal(err)
	}
	if d := diff(tree(t, root), tree(t, filepath.Join(bk, "restores", "tudo"))); d != "" {
		t.Fatalf("extração da pasta:\n%s", d)
	}

	// arquivo adulterado na pasta: recusado, e a pasta de restauração some
	os.WriteFile(filepath.Join(dest, r2.OutputName, "srv", "a.txt"), []byte("a v2, MAIOR"), 0o600)
	if err := c.restoreFiles(ctx, act(r2.OutputName, "adulterado", "/srv/a.txt"), &actionLog{c: c, act: &pb.AgentAction{}}); err == nil || !strings.Contains(err.Error(), "não confere") {
		t.Fatalf("arquivo adulterado deveria ser recusado: %v", err)
	}
	if _, err := os.Stat(filepath.Join(bk, "restores", "adulterado")); err == nil {
		t.Fatal("a pasta de restauração não deveria ficar")
	}
	os.WriteFile(filepath.Join(dest, r2.OutputName, "srv", "a.txt"), []byte("a v2, maior"), 0o600)

	// agente sem o catálogo (reinstalado): lê o da pasta, conferido pelo SHA-256 da execução
	st2, _ := NewState(t.TempDir())
	c2 := New(Config{HostRoot: root, HostWriteRoot: root, BackupDir: bk}, st2, quiet)
	catCache = &catalogCache{items: map[string]*Snapshot{}}
	a2 := act(r2.OutputName, "sem-catalogo", "/srv/dir")
	a2.Sha256 = r2.SHA256
	if err := c2.restoreFiles(ctx, a2, &actionLog{c: c2, act: a2}); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(bk, "restores", "sem-catalogo", "srv", "dir", "c.txt")); string(b) != "c v1" {
		t.Fatal("restauração com o catálogo da pasta")
	}
	st3, _ := NewState(t.TempDir())
	c3 := New(Config{HostRoot: root, HostWriteRoot: root, BackupDir: bk}, st3, quiet)
	a3 := act(r2.OutputName, "sha-errado", "/srv/dir")
	a3.Sha256 = strings.Repeat("0", 64)
	if err := c3.restoreFiles(ctx, a3, &actionLog{c: c3, act: a3}); err == nil || !strings.Contains(err.Error(), "não confere") {
		t.Fatalf("catálogo com SHA-256 diferente deveria ser recusado: %v", err)
	}

	// retenção (manter 2): a pasta mais antiga sai; a nova continua inteira (os links físicos mantêm o conteúdo)
	r3, err := run(ctx, 2, r2.Snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if removed := ApplyRetention(dest, r3.Prefix, 2); len(removed) != 1 || removed[0] != r1.OutputName {
		t.Fatalf("retenção: %v", removed)
	}
	same(r2.OutputName)
	same(r3.OutputName)

	// cancelado: nada fica para trás (nem a pasta provisória)
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := run(cctx, 3, r3.Snapshot); err == nil {
		t.Fatal("o backup cancelado deveria falhar")
	}
	ents, _ := os.ReadDir(dest)
	if len(ents) != 2 {
		t.Fatalf("depois do cancelamento: %v", ents)
	}
	// uma provisória deixada por um agente que parou no meio é apagada na execução seguinte
	os.MkdirAll(filepath.Join(dest, "pasta-20261201-030000"+netdest.PartSuffix, "srv"), 0o700)
	r4, err := run(ctx, 4, r3.Snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dest, "pasta-20261201-030000"+netdest.PartSuffix)); err == nil {
		t.Fatal("a pasta provisória antiga deveria ter sido apagada")
	}
	same(r4.OutputName)
}

// Um job em pasta não entra no próprio backup, mesmo com a pasta de destino dentro da origem.
func TestBackupEmPastaNaoSalvaASiMesmo(t *testing.T) {
	root, bk := t.TempDir(), t.TempDir()
	write(t, filepath.Join(root, "dados", "x.txt"), "x")
	dest := filepath.Join(root, "dados", "backups")
	tg, err := netdest.LocalTarget(dest)
	if err != nil {
		t.Fatal(err)
	}
	defer tg.Close()
	job := FilesJob{Name: "Eu", Paths: []string{"/dados"}, Keep: 3, DestPath: dest}
	for day := 0; day < 2; day++ {
		res, err := RunDir(context.Background(), job, dirTarget{t: tg, dest: dest}, root, bk, time.UTC, time.Date(2026, 11, 28+day, 3, 0, 0, 0, time.UTC), discard())
		if err != nil {
			t.Fatal(err)
		}
		for p := range res.Snapshot.Files {
			if strings.Contains(p, "eu-2026") {
				t.Fatalf("a pasta de backup entrou no backup: %s", p)
			}
		}
	}
}
