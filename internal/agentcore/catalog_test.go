package agentcore

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AprendendoLinux/corujabackup/internal/pb"
)

// Cadeia full + 2 incrementais com catálogo: navegar, versões de um arquivo e restaurar só o escolhido (numa pasta e
// no local original), cada item vindo do pacote certo.
func TestCatalogoNavegarVersoesERestaurar(t *testing.T) {
	root := t.TempDir()
	bk := t.TempDir()
	st, err := NewState(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	c := New(Config{HostRoot: root, HostWriteRoot: root, BackupDir: bk}, st, slog.New(slog.NewTextHandler(io.Discard, nil)))
	src := filepath.Join(root, "srv")
	write(t, filepath.Join(src, "a.txt"), "a v1")
	write(t, filepath.Join(src, "b.txt"), "b fica igual")
	write(t, filepath.Join(src, "dir/c.txt"), "c v1")
	ctx := context.Background()
	base := time.Date(2026, 11, 22, 3, 0, 0, 0, time.UTC)
	job := FilesJob{Name: "Cat", Paths: []string{"/srv"}, Subdir: "cat", Keep: 10}
	var prev *Snapshot
	var names []string
	step := func(i int, inc bool) {
		t.Helper()
		j := job
		if inc {
			j.inc = &incPlan{base: prev}
		}
		res, err := RunFiles(ctx, j, root, bk, time.UTC, base.Add(time.Duration(i)*24*time.Hour), discard())
		if err != nil {
			t.Fatal(err)
		}
		if err := st.SaveCatalog(1, res.OutputName, res.Snapshot); err != nil {
			t.Fatal(err)
		}
		prev, names = res.Snapshot, append(names, res.OutputName)
	}
	step(0, true) // full (sem base)
	time.Sleep(10 * time.Millisecond)
	write(t, filepath.Join(src, "a.txt"), "a v2 (maior)")
	step(1, true)
	time.Sleep(10 * time.Millisecond)
	write(t, filepath.Join(src, "dir/c.txt"), "c v2")
	os.Remove(filepath.Join(src, "b.txt"))
	step(2, true)

	// navegar no último: b.txt sumiu; a.txt está no inc1 (não "aqui"); c.txt mudou neste
	cat, err := st.LoadCatalog(1, names[2])
	if err != nil || len(cat.Archives) != 3 {
		t.Fatalf("catálogo: %v %+v", err, cat)
	}
	list := CatalogDir(cat, "/srv")
	got := map[string]CatalogEntry{}
	for _, e := range list {
		got[e.Name] = e
	}
	if _, ok := got["b.txt"]; ok || got["a.txt"].Here || !got["dir"].Dir || !got["dir"].Here || got["a.txt"].Size != int64(len("a v2 (maior)")) {
		t.Fatalf("pasta: %+v", list)
	}
	if r := CatalogDir(cat, "/"); len(r) != 1 || r[0].Name != "srv" || !r[0].Dir {
		t.Fatalf("raiz: %+v", r)
	}
	// versões de a.txt: v2 (inc1 e inc2) e v1 (full)
	vs, _ := st.Versions(1, "/srv/a.txt")
	if len(vs) != 2 || vs[0].Backups != 2 || vs[0].Latest != names[2] || vs[0].Archive != names[1] || vs[1].Latest != names[0] {
		t.Fatalf("versões: %+v", vs)
	}

	act := func(name string, paths []string, original bool, target string) *pb.AgentAction {
		return &pb.AgentAction{Kind: "restore_files", JobId: 1, FileName: name, Subdir: "cat", Paths: paths, Original: original, Target: target}
	}
	// restaurar a pasta /srv inteira do último backup numa pasta nova: cada arquivo do seu pacote
	al := &actionLog{c: c, act: &pb.AgentAction{}}
	if err := c.restoreFiles(ctx, act(names[2], []string{"/srv"}, false, "r1"), al); err != nil {
		t.Fatal(err)
	}
	out := tree(t, filepath.Join(bk, "restores", "r1"))
	if out["srv/a.txt"] != "a v2 (maior)" || out["srv/dir/c.txt"] != "c v2" {
		t.Fatalf("restauração do estado do último: %v", out)
	}
	if _, ok := out["srv/b.txt"]; ok {
		t.Fatal("b.txt não existia no último backup")
	}
	// a versão antiga de um arquivo só (do full), por cima do original
	if err := c.restoreFiles(ctx, act(names[0], []string{"/srv/a.txt", "/srv/b.txt"}, true, ""), al); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(src, "a.txt")); string(b) != "a v1" {
		t.Fatalf("local original: %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(src, "b.txt")); string(b) != "b fica igual" {
		t.Fatalf("arquivo apagado restaurado no original: %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(src, "dir/c.txt")); string(b) != "c v2" {
		t.Fatal("o que não foi escolhido não pode mudar")
	}
	ents, _ := os.ReadDir(src)
	for _, e := range ents {
		if strings.Contains(e.Name(), "coruja-restaurando") {
			t.Fatal("sobrou temporário da restauração")
		}
	}
	// recusas
	if err := c.restoreFiles(ctx, act(names[2], []string{"/proc/x"}, true, ""), al); err == nil {
		t.Fatal("/proc não pode receber restauração")
	}
	if err := c.restoreFiles(ctx, act(names[2], []string{"/nao/existe"}, false, "r2"), al); err == nil || !strings.Contains(err.Error(), "nenhum") {
		t.Fatalf("nada escolhido no backup: %v", err)
	}
	if err := c.restoreFiles(ctx, act(names[2], []string{"/srv"}, false, "r1"), al); err == nil {
		t.Fatal("pasta de restauração já existente")
	}

	// backup sem catálogo: o catálogo é montado a partir do arquivo
	os.RemoveAll(st.catalogDir(1))
	catCache = &catalogCache{items: map[string]*Snapshot{}}
	if err := c.restoreFiles(ctx, act(names[0], []string{"/srv/dir"}, false, "r3"), al); err != nil {
		t.Fatal(err)
	}
	if out := tree(t, filepath.Join(bk, "restores", "r3")); out["srv/dir/c.txt"] != "c v1" {
		t.Fatalf("restauração a partir do arquivo: %v", out)
	}
	if _, err := st.LoadCatalog(1, names[0]); err != nil {
		t.Fatal("o catálogo montado deveria ter sido guardado")
	}
}

func TestCatalogosLimitados(t *testing.T) {
	st, _ := NewState(t.TempDir())
	for i := 0; i < maxCatalogs+5; i++ {
		n := "j-" + time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(i)*time.Hour).Format("20060102-150405") + ".tar.gz"
		if err := st.SaveCatalog(9, n, &Snapshot{Version: snapshotVersion, Archives: []string{n}, Files: map[string]fileMeta{}}); err != nil {
			t.Fatal(err)
		}
	}
	ents, _ := os.ReadDir(st.catalogDir(9))
	if len(ents) != maxCatalogs || !strings.HasPrefix(ents[0].Name(), "j-20260101-050000") {
		t.Fatalf("%d catálogos, o mais velho %s", len(ents), ents[0].Name())
	}
	if _, err := st.LoadCatalog(9, "../../etc/passwd"); err == nil {
		t.Fatal("nome inválido")
	}
}
