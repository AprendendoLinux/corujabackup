package mikrotik

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestNormalize(t *testing.T) {
	bad := []Config{
		{Host: "http://x", User: "a", Password: "p", Export: true},
		{Host: "r1", User: "", Password: "p", Export: true},
		{Host: "r1", User: "a", Export: true},
		{Host: "r1", User: "a", Password: "p"},
		{Host: "r1", User: "a", Password: "p", Binary: true, BackupPassword: "curta"},
		{Host: "r1", User: "a", Password: "p", Port: 70000, Export: true},
	}
	for i, c := range bad {
		if err := c.Normalize(); err == nil {
			t.Errorf("caso %d deveria falhar", i)
		}
	}
	c := Config{Host: " r1.local ", User: "coruja", Password: "p", Export: true, BackupPassword: "ignorada"}
	if err := c.Normalize(); err != nil || c.Port != 22 || c.Host != "r1.local" || c.BackupPassword != "" {
		t.Fatalf("%v %+v", err, c)
	}
	if q := quote(`a"b\c$d?e`); q != `"a\"b\\c\$d\?e"` {
		t.Fatal(q)
	}
}

// TestRoteadorReal roda contra um RouterOS de verdade: CORUJA_TEST_MIKROTIK=host, CORUJA_TEST_MIKROTIK_SENHA=senha dos
// usuários "coruja" (grupo com ssh,ftp,read,write,policy,test,sensitive) e "leitor" (grupo read).
func TestRoteadorReal(t *testing.T) {
	host := os.Getenv("CORUJA_TEST_MIKROTIK")
	if host == "" {
		t.Skip("defina CORUJA_TEST_MIKROTIK")
	}
	pw := os.Getenv("CORUJA_TEST_MIKROTIK_SENHA")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	c := Config{Host: host, User: "coruja", Password: pw, Export: true, ExportSensitive: true, Binary: true, BackupPassword: "senha-do-backup-1"}
	info, err := Test(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if info.Identity != "CHR-Coruja-Teste" || info.Major() != 7 || !strings.HasPrefix(info.HostKey, "SHA256:") {
		t.Fatalf("info: %+v", info)
	}
	t.Logf("roteador: %+v", info)
	c.HostKey = info.HostKey

	r, err := Dial(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	plain, err := r.Export(info, false)
	if err != nil {
		t.Fatal(err)
	}
	sens, err := r.Export(info, true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(plain), "\r\n/") || strings.Contains(strings.ReplaceAll(string(plain), "\r\n", ""), "\n") {
		t.Fatal("o export deve usar \\r\\n, como o do RouterOS")
	}
	if strings.Contains(string(plain), "SegredoPPP123") || !strings.Contains(string(sens), "SegredoPPP123") || !strings.Contains(string(plain), "teste-coruja") {
		t.Fatal("o export sem dados sensíveis não pode ter a senha do PPP; o com dados sensíveis deve ter")
	}
	var buf bytes.Buffer
	n, err := r.Backup(ctx, c.BackupPassword, &buf)
	if err != nil {
		t.Fatal(err)
	}
	if n < 1000 || int64(buf.Len()) != n {
		t.Fatalf(".backup pequeno demais: %d", n)
	}
	if left, _ := r.run(`/file print terse where name~"coruja-"`); strings.Contains(left, "coruja-") {
		t.Fatalf("o .backup temporário deveria ter sido apagado do roteador: %s", left)
	}

	// impressão digital trocada é recusada
	bad := c
	bad.HostKey = "SHA256:outra"
	if _, err := Test(ctx, bad); err == nil || !strings.Contains(err.Error(), "chave do roteador mudou") {
		t.Fatalf("chave trocada: %v", err)
	}
	// senha errada
	wrong := c
	wrong.Password = "errada"
	if _, err := Test(ctx, wrong); err == nil || !strings.Contains(err.Error(), "recusou o usuário ou a senha") {
		t.Fatalf("senha errada: %v", err)
	}
	// usuário só de leitura: export sem dados sensíveis funciona, o resto não
	ro := Config{Host: host, User: "leitor", Password: pw, Export: true}
	if _, err := Test(ctx, ro); err != nil {
		t.Fatalf("leitura simples deveria funcionar: %v", err)
	}
	r2, err := Dial(ctx, ro)
	if err != nil {
		t.Fatal(err)
	}
	defer r2.Close()
	if _, err := r2.Backup(ctx, "senha-do-backup-1", &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "não gerou") {
		t.Fatalf("usuário só de leitura não pode gerar .backup (e a mensagem deve dizer isso): %v", err)
	} else {
		t.Logf("leitor + .backup: %v", err)
	}
}
