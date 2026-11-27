package agentcore

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTamanhoDaPasta(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "srv/a/um.txt"), strings.Repeat("x", 1000))
	write(t, filepath.Join(root, "srv/a/b/dois.txt"), strings.Repeat("y", 3000))
	write(t, filepath.Join(root, "srv/outra/tres.txt"), strings.Repeat("z", 500))
	if err := os.Link(filepath.Join(root, "srv/a/um.txt"), filepath.Join(root, "srv/a/b/link-duro.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc", filepath.Join(root, "srv/a/atalho")); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	r, err := DirSize(ctx, root, SizeRequest{Path: "/srv/a"}, nil)
	if err != nil || !r.Complete || r.Bytes != 4000 || r.Files != 2 {
		t.Fatalf("%+v %v (hard link conta uma vez; atalho não é seguido)", r, err)
	}
	r, _ = DirSize(ctx, root, SizeRequest{Path: "/"}, nil)
	if r.Bytes != 4500 || r.Path != "/" {
		t.Fatalf("raiz: %+v", r)
	}
	// CORUJA_ALLOWED_PATHS: o que não é permitido não entra na soma
	if r, err := DirSize(ctx, root, SizeRequest{Path: "/srv"}, []string{"/srv/outra"}); err != nil || r.Bytes != 500 {
		t.Fatalf("permitidos: %+v %v", r, err)
	}
	if _, err := DirSize(ctx, root, SizeRequest{Path: "/srv/a"}, []string{"/srv/outra"}); err == nil {
		t.Fatal("pasta não permitida deveria ser recusada")
	}
	for _, bad := range []string{"/nao-existe", "/srv/a/um.txt", "/../../etc"} {
		if r, err := DirSize(ctx, root, SizeRequest{Path: bad}, nil); err == nil && bad != "/../../etc" {
			t.Fatalf("%s deveria falhar: %+v", bad, r)
		} else if bad == "/../../etc" && (err == nil) {
			t.Fatalf("não pode sair da raiz: %+v", r)
		}
	}
	// guarda o resultado por alguns minutos
	write(t, filepath.Join(root, "srv/a/novo.txt"), "abc")
	if r, _ := DirSize(ctx, root, SizeRequest{Path: "/srv/a"}, nil); r.Bytes != 4000 {
		t.Fatalf("deveria vir do cache: %+v", r)
	}
	// /proc, /sys e /dev não são arquivos de verdade
	write(t, filepath.Join(root, "proc/x"), "123")
	if r, _ := DirSize(ctx, root, SizeRequest{Path: "/proc"}, nil); r.Bytes != 0 || !r.Complete {
		t.Fatalf("/proc: %+v", r)
	}
}
