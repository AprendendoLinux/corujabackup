package secrets

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSealOpenERoundTrip(t *testing.T) {
	dir := t.TempDir()
	b, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	s, _ := b.Seal("refresh-token-secreto")
	if strings.Contains(s, "secreto") || !strings.HasPrefix(s, "v1:") {
		t.Fatalf("não está cifrado: %s", s)
	}
	if s2, _ := b.Seal("refresh-token-secreto"); s == s2 {
		t.Fatal("cada cifra deve usar nonce novo")
	}
	if got, err := b.Open(s); err != nil || got != "refresh-token-secreto" {
		t.Fatalf("%q %v", got, err)
	}
	// a mesma chave no disco abre depois
	b2, _ := Open(dir)
	if got, _ := b2.Open(s); got != "refresh-token-secreto" {
		t.Fatal("a chave deveria persistir")
	}
	if fi, _ := os.Stat(filepath.Join(dir, "secret.key")); fi.Mode().Perm() != 0o600 {
		t.Fatalf("permissão da chave: %v", fi.Mode().Perm())
	}
	// chave diferente / adulteração
	other, _ := Open(t.TempDir())
	if _, err := other.Open(s); err == nil {
		t.Fatal("chave diferente não pode abrir")
	}
	if _, err := b.Open(s[:len(s)-4] + "AAAA"); err == nil {
		t.Fatal("adulteração deve ser detectada")
	}
	if v, err := b.Open(""); err != nil || v != "" {
		t.Fatal("vazio é vazio")
	}
}
