package enroll

import (
	"strings"
	"testing"
)

func TestTokenRoundTrip(t *testing.T) {
	fp := strings.Repeat("ab", 32)
	s, err := New(fp)
	if err != nil {
		t.Fatal(err)
	}
	tk, err := Parse(s)
	if err != nil || tk.Fingerprint != fp[:32] || tk.Hash() == "" {
		t.Fatalf("%v %+v", err, tk)
	}
	for _, bad := range []string{"", "x", "cb1.a.b", "cb2." + fp[:32] + "." + strings.Repeat("x", 43), "cb1." + fp[:32]} {
		if _, err := Parse(bad); err == nil {
			t.Fatalf("aceitou token inválido %q", bad)
		}
	}
	if _, err := New("curta"); err == nil {
		t.Fatal("fingerprint curta aceita")
	}
}
