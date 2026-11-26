package cloud

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

// O content_hash é definido pela documentação do Dropbox: SHA-256 da concatenação dos SHA-256 de blocos de 4 MiB.
func TestDropboxContentHash(t *testing.T) {
	data := bytes.Repeat([]byte("coruja!"), 1_500_000) // ~10 MB: três blocos, o último parcial
	var cat []byte
	for i := 0; i < len(data); i += 4 << 20 {
		s := sha256.Sum256(data[i:min(i+(4<<20), len(data))])
		cat = append(cat, s[:]...)
	}
	want := sha256.Sum256(cat)
	for _, step := range []int{len(data), 1 << 20, 4 << 20, 3333333, 7} {
		if step == 7 && testing.Short() {
			continue
		}
		h := NewDropboxHash()
		for i := 0; i < len(data); i += step {
			_, _ = h.Write(data[i:min(i+step, len(data))])
		}
		if h.Hex() != hex.EncodeToString(want[:]) || h.Hex() != h.Hex() {
			t.Fatalf("passo %d: hash diferente", step)
		}
	}
	empty := sha256.Sum256(nil)
	if NewDropboxHash().Hex() != hex.EncodeToString(empty[:]) {
		t.Fatal("hash de arquivo vazio")
	}
}

func TestASCIIJSON(t *testing.T) {
	got := ASCIIJSON(map[string]string{"path": "/Relatórios 😀/a.tar.gz"})
	u := func(h string) string { return string(rune(92)) + "u" + h }
	want := `{"path":"/Relat` + u("00f3") + "rios " + u("d83d") + u("de00") + `/a.tar.gz"}`
	if got != want {
		t.Fatalf("%s != %s", got, want)
	}
}
