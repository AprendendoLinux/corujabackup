package cloud

import (
	"encoding/base64"
	"encoding/hex"
	"testing"
)

func qx(b []byte) string {
	s := NewSums()
	s.Write(b)
	return s.Digest().QuickXor
}

func TestQuickXorVazio(t *testing.T) {
	if got := qx(nil); got != "AAAAAAAAAAAAAAAAAAAAAAAAAAA=" {
		t.Fatalf("hash do arquivo vazio: %s", got)
	}
}

func pattern(n int, f func(i int) int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(f(i))
	}
	return b
}

// Vetores gerados com o rclone (rclone hashsum quickxor) sobre os mesmos bytes.
func TestQuickXorVetoresDoRclone(t *testing.T) {
	cases := []struct {
		name string
		data []byte
		want string
	}{
		{"a", []byte("a"), "6100000000000000000000000100000000000000"},
		{"hello", []byte("hello world\n"), "6828031bd8f00610dce10d726c03191400000000"},
		{"p1000", pattern(1000, func(i int) int { return i*7 + i>>8 }), "bd4976b307b9be6b0291d48d4e627040ebf91c4e"},
		{"p100k", pattern(100000, func(i int) int { return i*7 + i>>8 }), "70bfed1d5ec0506c733b192aa0c0a2cad4688d38"},
		{"p3m", pattern(3_000_001, func(i int) int { return i*13 + i>>9 + i>>17 }), "d718f95e5ba0816e103e7e6c7b690e3460e2d84a"},
	}
	for _, c := range cases {
		raw, _ := base64.StdEncoding.DecodeString(qx(c.data))
		if got := hex.EncodeToString(raw); got != c.want {
			t.Errorf("%s: esperava %s, veio %s", c.name, c.want, got)
		}
	}
}

func TestQuickXorIndependeDoTamanhoDosBlocos(t *testing.T) {
	data := make([]byte, 100_000)
	for i := range data {
		data[i] = byte(i*7 + i>>8)
	}
	whole := qx(data)
	s := NewSums()
	for off := 0; off < len(data); {
		n := 1 + (off*31)%977
		if off+n > len(data) {
			n = len(data) - off
		}
		s.Write(data[off : off+n])
		off += n
	}
	if s.Digest().QuickXor != whole {
		t.Fatal("o hash não pode depender de como os bytes são fatiados")
	}
}
