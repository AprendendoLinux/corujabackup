package agentcore

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/AprendendoLinux/corujabackup/internal/bkname"
)

// tree lê uma árvore como caminho → conteúdo ("<dir>" para pastas, "-> alvo" para links).
func tree(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || p == root {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		switch {
		case d.Type()&fs.ModeSymlink != 0:
			l, _ := os.Readlink(p)
			out[rel] = "-> " + l
		case d.IsDir():
			out[rel] = "<dir>"
		default:
			b, _ := os.ReadFile(p)
			out[rel] = string(b)
		}
		return nil
	})
	return out
}

func diff(a, b map[string]string) string {
	var d []string
	for k, v := range a {
		if w, ok := b[k]; !ok {
			d = append(d, "falta "+k)
		} else if w != v {
			d = append(d, "difere "+k)
		}
	}
	for k := range b {
		if _, ok := a[k]; !ok {
			d = append(d, "sobra "+k)
		}
	}
	sort.Strings(d)
	return strings.Join(d, "; ")
}

func TestIncrementalCadeiaCompletaRestauraIgual(t *testing.T) {
	root, bk := t.TempDir(), t.TempDir()
	src := filepath.Join(root, "srv")
	write(t, filepath.Join(src, "a.txt"), "versão 1")
	write(t, filepath.Join(src, "b.txt"), "fica igual")
	write(t, filepath.Join(src, "dir/c.txt"), "vai sumir")
	write(t, filepath.Join(src, "dir2/d.txt"), "pasta inteira vai sumir")
	os.Symlink("a.txt", filepath.Join(src, "atalho"))
	ctx := context.Background()
	base := time.Date(2026, 11, 22, 3, 0, 0, 0, time.UTC) // domingo
	job := FilesJob{Name: "Inc", Paths: []string{"/srv"}, Subdir: "inc", Keep: 10}

	run := func(at time.Time, prev *Snapshot) (Result, *Snapshot) {
		t.Helper()
		j := job
		j.inc = &incPlan{base: prev, day: at.Format("2006-01-02")}
		res, err := RunFiles(ctx, j, root, bk, time.UTC, at, discard())
		if err != nil {
			t.Fatal(err)
		}
		snap := res.Snapshot
		snap.Sig, snap.FullDay = "x", "2026-11-22"
		return res, snap
	}

	r1, s1 := run(base, nil)
	if r1.Kind != ModeFull || !strings.HasSuffix(r1.OutputName, "-030000.tar.gz") || strings.Contains(r1.OutputName, "-inc") {
		t.Fatalf("o primeiro deve ser full: %+v", r1)
	}
	want1 := tree(t, root)

	// mudanças: altera a.txt, apaga dir/c.txt e a pasta dir2, cria e.txt, troca o alvo do atalho
	time.Sleep(10 * time.Millisecond)
	write(t, filepath.Join(src, "a.txt"), "versão 2 (maior)")
	os.Remove(filepath.Join(src, "dir/c.txt"))
	os.RemoveAll(filepath.Join(src, "dir2"))
	write(t, filepath.Join(src, "novo/e.txt"), "novo")
	os.Remove(filepath.Join(src, "atalho"))
	os.Symlink("b.txt", filepath.Join(src, "atalho"))
	r2, s2 := run(base.Add(24*time.Hour), s1)
	if r2.Kind != ModeIncremental || !strings.HasSuffix(r2.OutputName, "-inc.tar.gz") {
		t.Fatalf("o segundo deve ser incremental: %+v", r2)
	}
	if r2.BytesSource >= r1.BytesSource+20 || r2.Files > r1.Files {
		t.Fatalf("o incremental devia levar menos: full %d B/%d itens, inc %d B/%d itens", r1.BytesSource, r1.Files, r2.BytesSource, r2.Files)
	}
	want2 := tree(t, root)

	// segundo incremental: só um arquivo novo
	time.Sleep(10 * time.Millisecond)
	write(t, filepath.Join(src, "f.txt"), "f")
	r3, _ := run(base.Add(48*time.Hour), s2)
	want3 := tree(t, root)

	dir := filepath.Join(bk, "inc")
	restore := func(names ...string) map[string]string {
		t.Helper()
		dest := t.TempDir()
		for _, n := range names {
			_, _, man, err := extractOne(ctx, filepath.Join(dir, n), dest, func(string, string, ...any) {})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(dest, manifestName)); err == nil {
				t.Fatal("o manifesto não deve ir para a restauração")
			}
			if man != nil {
				applyDeletions(dest, man.Deleted)
			}
		}
		return tree(t, dest)
	}
	if d := diff(restore(r1.OutputName), want1); d != "" {
		t.Fatalf("full: %s", d)
	}
	if d := diff(restore(r1.OutputName, r2.OutputName), want2); d != "" {
		t.Fatalf("full + inc1: %s", d)
	}
	if d := diff(restore(r1.OutputName, r2.OutputName, r3.OutputName), want3); d != "" {
		t.Fatalf("full + inc1 + inc2: %s", d)
	}

	// a cadeia vista pelo nome dos arquivos bate com a ordem de aplicação
	ents, _ := os.ReadDir(dir)
	var fsl []bkname.File
	for _, e := range ents {
		fsl = append(fsl, bkname.File{Name: e.Name()})
	}
	if got := bkname.ChainUpTo(fsl, "inc", r3.OutputName); strings.Join(got, ",") != strings.Join([]string{r1.OutputName, r2.OutputName, r3.OutputName}, ",") {
		t.Fatalf("cadeia: %v", got)
	}
	// a retenção local por cadeia: um novo full abre outra cadeia; com keep=1, a cadeia velha sai inteira
	r4, _ := run(base.Add(7*24*time.Hour), nil)
	if removed := ApplyRetention(dir, "inc", 1); len(removed) != 3 {
		t.Fatalf("a cadeia anterior (3 arquivos) devia sair inteira: %v", removed)
	}
	if _, err := os.Stat(filepath.Join(dir, r4.OutputName)); err != nil {
		t.Fatal("o full novo deve ficar")
	}
}

func TestArquivoIlegivelVoltaNoProximoIncremental(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("como root, a permissão não impede a leitura")
	}
	root, bk := t.TempDir(), t.TempDir()
	write(t, filepath.Join(root, "d/ok.txt"), "ok")
	write(t, filepath.Join(root, "d/fechado.txt"), "segredo")
	os.Chmod(filepath.Join(root, "d/fechado.txt"), 0)
	j := FilesJob{Name: "il", Paths: []string{"/d"}, Subdir: "il", Keep: 5, inc: &incPlan{}}
	res, err := RunFiles(context.Background(), j, root, bk, time.UTC, time.Now(), discard())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := res.Snapshot.Files["/d/fechado.txt"]; ok {
		t.Fatal("arquivo que não foi lido não pode entrar no retrato")
	}
	os.Chmod(filepath.Join(root, "d/fechado.txt"), 0o600)
	j.inc = &incPlan{base: res.Snapshot}
	res2, err := RunFiles(context.Background(), j, root, bk, time.UTC, time.Now().Add(time.Second), discard())
	if err != nil || res2.Files == 0 {
		t.Fatal(res2, err)
	}
	if _, ok := res2.Snapshot.Files["/d/fechado.txt"]; !ok {
		t.Fatal("agora lido, deve entrar")
	}
}

func TestNeedFull(t *testing.T) {
	sp, _ := time.LoadLocation("America/Sao_Paulo")
	dom := time.Date(2026, 11, 22, 1, 0, 0, 0, sp) // domingo
	seg := dom.AddDate(0, 0, 1)
	snap := &Snapshot{Sig: "s", FullDay: "2026-11-22"}
	cases := []struct {
		name string
		snap *Snapshot
		sig  string
		now  time.Time
		wd   int
		frc  bool
		want bool
	}{
		{"sem retrato", nil, "s", seg, 0, false, true},
		{"incremental na segunda", snap, "s", seg, 0, false, false},
		{"full já feito no domingo", snap, "s", dom.Add(5 * time.Hour), 0, false, false},
		{"origens mudaram", snap, "outra", seg, 0, false, true},
		{"manual", snap, "s", seg, 0, true, true},
		{"domingo seguinte", snap, "s", dom.AddDate(0, 0, 7), 0, false, true},
		{"full de domingo perdido: segunda da outra semana", snap, "s", dom.AddDate(0, 0, 8), 0, false, true},
		{"dia do full é segunda", snap, "s", seg, 1, false, true},
	}
	for _, c := range cases {
		if got, why := needFull(c.snap, c.sig, c.now, c.wd, c.frc); got != c.want {
			t.Errorf("%s: %v (%s)", c.name, got, why)
		}
	}
}

func TestRetratoGravaELe(t *testing.T) {
	st, err := NewState(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if st.LoadSnapshot(7) != nil {
		t.Fatal("sem retrato")
	}
	in := &Snapshot{Version: snapshotVersion, Sig: "s", Chain: "c", FullDay: "2026-11-22", Last: "l", Files: map[string]fileMeta{"/a": {Size: 1, MTime: 2, Ino: 3}}}
	if err := st.SaveSnapshot(7, in); err != nil {
		t.Fatal(err)
	}
	got := st.LoadSnapshot(7)
	if got == nil || got.Files["/a"] != in.Files["/a"] || got.Last != "l" {
		t.Fatalf("%+v", got)
	}
	if fi, _ := os.Stat(st.snapshotPath(7)); fi.Mode().Perm() != 0o600 {
		t.Fatalf("permissão %v", fi.Mode())
	}
	st.DropSnapshot(7)
	if st.LoadSnapshot(7) != nil {
		t.Fatal("apagado")
	}
}
