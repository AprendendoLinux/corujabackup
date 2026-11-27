package bkname

import (
	"strings"
	"testing"
	"time"
)

func files(names ...string) []File {
	var o []File
	for _, n := range names {
		o = append(o, File{Name: n})
	}
	return o
}

func names(fs []File) string {
	var s []string
	for _, f := range fs {
		s = append(s, f.Name)
	}
	return strings.Join(s, ",")
}

func TestParseENome(t *testing.T) {
	at := time.Date(2026, 11, 27, 3, 4, 5, 0, time.UTC)
	if n := Name("web", at, true); n != "web-20261127-030405-inc.tar.gz" || !Any.MatchString(n) {
		t.Fatal(n)
	}
	if s, inc, ok := Parse("web-20261127-030405-inc.tar.gz", "web"); !ok || !inc || s != "20261127-030405" {
		t.Fatal(s, inc, ok)
	}
	if _, inc, ok := Parse("web-20261127-030405.tar.gz", "web"); !ok || inc {
		t.Fatal("full")
	}
	for _, bad := range []string{"web-app-20261127-030405.tar.gz", "web-20261127-030405-inc.tar", "web-2026.tar.gz", "outro-20261127-030405.tar.gz"} {
		if _, _, ok := Parse(bad, "web"); ok {
			t.Errorf("%s não é do job web", bad)
		}
	}
}

func TestCadeiasERetencao(t *testing.T) {
	fs := files(
		"j-20261101-000000-inc.tar.gz", // incremental órfão (o full dele foi apagado)
		"j-20261102-000000.tar.gz", "j-20261103-000000-inc.tar.gz", "j-20261104-000000-inc.tar.gz",
		"j-20261109-000000.tar.gz", "j-20261110-000000-inc.tar.gz",
		"j-20261116-000000.tar.gz",
		"outro-20261101-000000.tar.gz", "leia-me.txt")
	ch := Chains(fs, "j")
	if len(ch) != 4 || len(ch[0]) != 1 || len(ch[1]) != 3 || len(ch[2]) != 2 || len(ch[3]) != 1 {
		t.Fatalf("cadeias: %v", ch)
	}
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	// manter 2 cadeias: apaga a de 09/11? não: mantém 16/11 e 09/11, apaga 02/11 (inteira) e o órfão
	if got := names(Retention(fs, "j", 2, 0, now)); got != "j-20261104-000000-inc.tar.gz,j-20261103-000000-inc.tar.gz,j-20261102-000000.tar.gz,j-20261101-000000-inc.tar.gz" {
		t.Fatalf("por cadeias: %s", got)
	}
	// por dias: só cai a cadeia cujo arquivo mais NOVO passou do prazo (a de 09/11 termina em 10/11: 6 dias)
	if got := names(Retention(fs, "j", 0, 7, now)); got != "j-20261104-000000-inc.tar.gz,j-20261103-000000-inc.tar.gz,j-20261102-000000.tar.gz,j-20261101-000000-inc.tar.gz" {
		t.Fatalf("por dias: %s", got)
	}
	// a cadeia em andamento (a mais nova) nunca cai, mesmo velha
	if got := names(Retention(files("j-20200101-000000.tar.gz", "j-20200102-000000-inc.tar.gz"), "j", 0, 1, now)); got != "" {
		t.Fatalf("a cadeia mais nova deve ficar inteira: %s", got)
	}
	if got := ChainUpTo(fs, "j", "j-20261104-000000-inc.tar.gz"); strings.Join(got, ",") != "j-20261102-000000.tar.gz,j-20261103-000000-inc.tar.gz,j-20261104-000000-inc.tar.gz" {
		t.Fatalf("cadeia até o incremental: %v", got)
	}
	if got := ChainUpTo(fs, "j", "j-20261116-000000.tar.gz"); len(got) != 1 {
		t.Fatalf("um full basta sozinho: %v", got)
	}
}
