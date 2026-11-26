package agentcore

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func names(r BrowseResult) []string {
	var out []string
	for _, e := range r.Entries {
		n := e.Name
		if e.Dir {
			n += "/"
		}
		if e.Link {
			n += "@"
		}
		out = append(out, n)
	}
	return out
}

func TestBrowseNavegacaoPermissoesELinks(t *testing.T) {
	base, outside := t.TempDir(), t.TempDir()
	for _, d := range []string{"etc/nginx", "home/ana", "root/segredo", "srv"} {
		if err := os.MkdirAll(filepath.Join(base, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for f, c := range map[string]string{"etc/hosts": "abc", "etc/Zeta": "", "root/segredo/chave": "x", "home/ana/nota.txt": "oi"} {
		if err := os.WriteFile(filepath.Join(base, f), []byte(c), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// atalhos: um para fora da raiz e dois (relativo e absoluto) para uma pasta não permitida
	for name, target := range map[string]string{"home/fora": outside, "home/rel": "../root", "home/abs": "/root"} {
		if err := os.Symlink(target, filepath.Join(base, name)); err != nil {
			t.Fatal(err)
		}
	}

	r, err := Browse(base, BrowseRequest{Area: BrowseHost, Path: "/etc"}, nil)
	if err != nil || !slices.Equal(names(r), []string{"nginx/", "hosts", "Zeta"}) || r.Entries[1].Size != 3 {
		t.Fatalf("pastas primeiro, ordem sem diferenciar maiúsculas: %v %+v", err, r)
	}
	r, err = Browse(base, BrowseRequest{Area: BrowseHost, Path: "/home"}, nil)
	if err != nil || !slices.Equal(names(r), []string{"ana/", "abs@", "fora@", "rel@"}) {
		t.Fatalf("links aparecem como atalhos, não como pastas: %v %v", err, names(r))
	}
	// ".." não sai da raiz: o caminho é normalizado antes
	r, err = Browse(base, BrowseRequest{Area: BrowseHost, Path: "../../.."}, nil)
	if err != nil || r.Path != "/" || !slices.Contains(names(r), "etc/") {
		t.Fatalf("%v %+v", err, r)
	}
	for _, p := range []string{"/home/fora", "/home/rel", "/home/abs", "/home/rel/segredo", "/etc/hosts", "/nao-existe"} {
		if r, err := Browse(base, BrowseRequest{Area: BrowseHost, Path: p}, nil); err == nil {
			t.Fatalf("%s não deveria ser listado: %v", p, names(r))
		}
	}

	// com lista de permissões: só aparecem os itens permitidos e o caminho até eles
	allowed := []string{"/etc/nginx", "/home"}
	r, err = Browse(base, BrowseRequest{Area: BrowseHost, Path: "/"}, allowed)
	if err != nil || !slices.Equal(names(r), []string{"etc/", "home/"}) {
		t.Fatalf("%v %v", err, names(r))
	}
	r, err = Browse(base, BrowseRequest{Area: BrowseHost, Path: "/etc"}, allowed)
	if err != nil || !slices.Equal(names(r), []string{"nginx/"}) {
		t.Fatalf("%v %v", err, names(r))
	}
	for _, p := range []string{"/root", "/root/segredo", "/srv", "/home/rel/segredo"} {
		if r, err := Browse(base, BrowseRequest{Area: BrowseHost, Path: p}, allowed); err == nil {
			t.Fatalf("%s está fora do permitido: %v", p, names(r))
		}
	}
	if _, err := Browse(base, BrowseRequest{Area: BrowseHost, Path: "/root"}, allowed); !strings.Contains(err.Error(), "CORUJA_ALLOWED_PATHS") {
		t.Fatalf("mensagem: %v", err)
	}
}

func TestBrowsePastaDeBackupsSoMostraDestinosValidos(t *testing.T) {
	base := t.TempDir()
	for _, d := range []string{"web/diario", "restores", ".tmp-abc", "com espaço", "web/restores"} {
		if err := os.MkdirAll(filepath.Join(base, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(base, "solto.tar.gz"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	// a lista de permissões da origem não se aplica à pasta de backups
	r, err := Browse(base, BrowseRequest{Area: BrowseBackups, Path: "/"}, []string{"/etc"})
	if err != nil || !slices.Equal(names(r), []string{"web/"}) {
		t.Fatalf("%v %v", err, names(r))
	}
	r, err = Browse(base, BrowseRequest{Area: BrowseBackups, Path: "web"}, nil)
	if err != nil || !slices.Equal(names(r), []string{"diario/", "restores/"}) {
		t.Fatalf("%v %v", err, names(r))
	}
}
