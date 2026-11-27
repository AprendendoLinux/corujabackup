package netdest

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

func localFile(t *testing.T, n int) (string, string) {
	t.Helper()
	b := make([]byte, n)
	_, _ = rand.Read(b)
	p := filepath.Join(t.TempDir(), "arquivo")
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
	s := sha256.Sum256(b)
	return p, hex.EncodeToString(s[:])
}

// exercise valida o ciclo completo num destino real: teste, três envios com retenção, download conferido e limpeza.
func exercise(t *testing.T, c Config, env Env) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	tr, err := Test(ctx, c, env)
	if err != nil {
		t.Fatalf("teste do destino: %v", err)
	}
	if c.Type == TypeSFTP {
		if !strings.HasPrefix(tr.HostKey, "SHA256:") {
			t.Fatalf("impressão digital: %q", tr.HostKey)
		}
		if _, err := Send(ctx, c, env, Job{Folder: "x", LocalPath: os.Args[0], Name: "a", Prefix: "a"}, nil); err == nil {
			t.Fatal("SFTP sem chave fixada não pode enviar")
		}
		bad := c
		bad.HostKey = "SHA256:outra"
		if _, err := Test(ctx, bad, env); err == nil || !strings.Contains(err.Error(), "chave do servidor mudou") {
			t.Fatalf("chave trocada deveria falhar: %v", err)
		}
		c.HostKey = tr.HostKey
	}
	folder := "Coruja Backup/servidor ção/job-" + c.Type + "-" + time.Now().Format("150405.000")
	var last, lastSHA, lastPath string
	for i, stamp := range []string{"20260101-010101", "20260102-010101", "20260103-010101"} {
		src, sha := localFile(t, 3<<20+i*1000)
		name := "web-" + stamp + ".tar.gz"
		var sent int64
		res, err := Send(ctx, c, env, Job{Folder: folder, LocalPath: src, Name: name, SHA256: sha, Prefix: "web", Keep: 2, Now: time.Now()}, func(s, _ int64) { sent = s })
		if err != nil {
			t.Fatalf("envio %d: %v", i, err)
		}
		if res.VerifiedBy != "sha256" || res.Hash != sha || sent == 0 || res.Path != folder+"/"+name {
			t.Fatalf("resultado: %+v (enviado %d)", res, sent)
		}
		if i == 2 && (len(res.Retention) != 1 || !strings.Contains(res.Retention[0], "20260101")) {
			t.Fatalf("a retenção deveria remover o mais antigo: %v", res.Retention)
		}
		last, lastSHA, lastPath = name, sha, src
	}
	_ = lastPath
	conn, _, err := Dial(ctx, c, env)
	if err != nil {
		t.Fatal(err)
	}
	files, err := conn.List(folder)
	if err != nil || len(files) != 2 {
		t.Fatalf("lista: %v %+v", err, files)
	}
	// escolha de pasta na interface: lista só as subpastas, relativas à pasta base
	parent, leaf := path.Split(folder)
	if dirs, err := Folders(ctx, c, env, strings.TrimSuffix(parent, "/")); err != nil || !slices.Contains(dirs, leaf) {
		t.Fatalf("subpastas de %q: %v %v", parent, dirs, err)
	}
	if dirs, err := Folders(ctx, c, env, ""); err != nil || !slices.Contains(dirs, "Coruja Backup") {
		t.Fatalf("subpastas da pasta base: %v %v", dirs, err)
	}
	if dirs, err := Folders(ctx, c, env, folder); err != nil || len(dirs) != 0 {
		t.Fatalf("pasta só com arquivos não tem subpastas: %v %v", dirs, err)
	}
	if dirs, err := Folders(ctx, c, env, "nao/existe"); err != nil || len(dirs) != 0 {
		t.Fatalf("pasta inexistente: %v %v", dirs, err)
	}
	if _, err := Folders(ctx, c, env, "../fora"); err == nil {
		t.Fatal("'..' deveria ser recusado")
	}
	if missing, err := conn.List(folder + "/nao-existe"); err != nil || len(missing) != 0 {
		t.Fatalf("pasta inexistente deveria dar lista vazia: %v %v", missing, err)
	}
	conn.Close()

	// arquivo cujo conteúdo não confere é descartado e os antigos ficam
	src, _ := localFile(t, 100000)
	if _, err := Send(ctx, c, env, Job{Folder: folder, LocalPath: src, Name: "web-20260104-010101.tar.gz", SHA256: strings.Repeat("0", 64), Prefix: "web", Keep: 1, Now: time.Now()}, nil); err == nil || !strings.Contains(err.Error(), "descartado") {
		t.Fatalf("deveria recusar o arquivo divergente: %v", err)
	}
	out := filepath.Join(t.TempDir(), "volta")
	n, err := Fetch(ctx, c, env, folder, last, out, lastSHA)
	if err != nil || n == 0 {
		t.Fatalf("trazer de volta: %v (%d)", err, n)
	}
	if _, err := Fetch(ctx, c, env, folder, last, out+"2", strings.Repeat("f", 64)); err == nil {
		t.Fatal("download com hash errado deveria falhar")
	}
	conn, _, err = Dial(ctx, c, env)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	files, _ = conn.List(folder)
	if len(files) != 2 {
		t.Fatalf("após a falha deveriam restar 2 arquivos: %+v", files)
	}
	for _, f := range files {
		_ = conn.Remove(folder, f.Name)
	}

	// senha errada tem mensagem clara
	if c.Type != TypeLocal && c.Type != TypeNFS {
		bad := c
		bad.Password = "errada"
		if _, err := Test(ctx, bad, env); err == nil {
			t.Fatal("senha errada deveria falhar")
		}
	}
}

func TestDestinoLocal(t *testing.T) {
	root := t.TempDir()
	exercise(t, Config{Type: TypeLocal, BasePath: "nfs/backups"}, Env{LocalRoot: root})
	// não escapa da raiz: nem com "..", nem por link simbólico
	for _, p := range []string{"../fora", "a/../../fora"} {
		if _, err := Test(context.Background(), Config{Type: TypeLocal, BasePath: p}, Env{LocalRoot: root}); err == nil {
			t.Fatalf("%q deveria ser recusado", p)
		}
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "atalho")); err != nil {
		t.Fatal(err)
	}
	if _, err := Test(context.Background(), Config{Type: TypeLocal, BasePath: "atalho"}, Env{LocalRoot: root}); err == nil {
		t.Fatal("link simbólico para fora da raiz deveria ser recusado")
	}
	if es, _ := os.ReadDir(outside); len(es) != 0 {
		t.Fatalf("nada poderia ter sido escrito fora: %v", es)
	}
}

// Pasta local escolhida na interface: um caminho absoluto da máquina do agente, traduzido pelo agente (HostDir).
func TestDestinoPastaEscolhida(t *testing.T) {
	host := t.TempDir() // faz o papel do "/" da máquina do agente (montado em /host-rw)
	env := Env{HostDir: func(abs string) (string, error) { return filepath.Join(host, abs), nil }}
	exercise(t, Config{Type: TypeLocal, Path: "/mnt/hd2/backups"}, env)
	if st, err := os.Stat(filepath.Join(host, "mnt/hd2/backups")); err != nil || !st.IsDir() {
		t.Fatal("a pasta escolhida deveria ter sido criada", err)
	}
	tr, err := Test(context.Background(), Config{Type: TypeLocal, Path: "/mnt/hd2/backups"}, env)
	if err != nil || tr.Total <= 0 || tr.Free <= 0 {
		t.Fatalf("o teste deveria informar o espaço: %+v %v", tr, err)
	}
	if _, err := Test(context.Background(), Config{Type: TypeLocal, Path: "/mnt/x"}, Env{}); err == nil || !strings.Contains(err.Error(), "/:/host-rw") {
		t.Fatalf("sem a montagem gravável deveria explicar: %v", err)
	}
	for _, p := range []string{"/", "/proc/x", "relativa", "/a/../b"} {
		c := Config{Type: TypeLocal, Path: p}
		if err := c.Normalize(); err == nil {
			t.Errorf("%q deveria ser recusado", p)
		}
	}
	c := Config{Type: TypeLocal, Path: "/mnt//hd2/"}
	if err := c.Normalize(); err != nil || c.Path != "/mnt/hd2" || c.Label() != "pasta /mnt/hd2" {
		t.Fatalf("normalização: %v %+v", err, c)
	}
}

// TestNFSReal: CORUJA_TEST_NFS=host:porta:/exportação (porta vazia = portmapper).
func TestNFSReal(t *testing.T) {
	parts := strings.SplitN(netHost(t, "CORUJA_TEST_NFS"), ":", 3)
	if len(parts) != 3 {
		t.Fatal("formato: host:porta:/exportação")
	}
	port := 0
	if parts[1] != "" {
		port, _ = strconv.Atoi(parts[1])
	}
	c := Config{Type: TypeNFS, Host: parts[0], Port: port, Export: parts[2], BasePath: path.Join(os.Getenv("CORUJA_TEST_NFS_BASE"), "base")}
	if v := os.Getenv("CORUJA_TEST_NFS_UID"); v != "" { // com root_squash: o dono da pasta de teste
		n, _ := strconv.Atoi(v)
		c.UID, c.GID = &n, &n
	}
	exercise(t, c, Env{})
	// cancelamento no meio do envio não deixa o temporário para trás
	src, sha := localFile(t, 8<<20)
	ctx, cancel := context.WithCancel(context.Background())
	_, err := Send(ctx, c, Env{}, Job{Folder: "cancelado", LocalPath: src, Name: "web-20260105-010101.tar.gz", SHA256: sha, Prefix: "web", Keep: 1, Now: time.Now()},
		func(sent, _ int64) {
			if sent > 1<<20 {
				cancel()
			}
		})
	if err == nil {
		t.Fatal("o envio cancelado deveria falhar")
	}
	conn, _, err := Dial(context.Background(), c, Env{})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if files, _ := conn.List("cancelado"); len(files) != 0 {
		t.Fatalf("restou na pasta após o cancelamento: %+v", files)
	}
}

// Os testes abaixo usam servidores reais (veja scripts/test-netdest.sh).
func netHost(t *testing.T, env string) string {
	h := os.Getenv(env)
	if h == "" {
		t.Skip("defina " + env + " (scripts/test-netdest.sh)")
	}
	return h
}

func TestSFTPReal(t *testing.T) {
	exercise(t, Config{Type: TypeSFTP, Host: netHost(t, "CORUJA_TEST_SFTP"), User: "coruja", Password: "s3nh@-forte", BasePath: "upload/base"}, Env{})
}

func TestFTPReal(t *testing.T) {
	h := netHost(t, "CORUJA_TEST_FTP")
	t.Run("ftps", func(t *testing.T) {
		exercise(t, Config{Type: TypeFTP, Host: h, User: "coruja", Password: "s3nh@-forte", TLS: TLSExplicit, SkipVerify: true, BasePath: "base"}, Env{})
		if _, err := Test(context.Background(), Config{Type: TypeFTP, Host: h, User: "coruja", Password: "s3nh@-forte", TLS: TLSExplicit}, Env{}); err == nil {
			t.Fatal("certificado autoassinado sem 'ignorar verificação' deveria falhar")
		}
	})
	t.Run("sem-tls", func(t *testing.T) {
		exercise(t, Config{Type: TypeFTP, Host: h, User: "coruja", Password: "s3nh@-forte", TLS: TLSNone}, Env{})
	})
}

func TestSMBReal(t *testing.T) {
	exercise(t, Config{Type: TypeSMB, Host: netHost(t, "CORUJA_TEST_SMB"), User: "coruja", Password: "s3nh@-forte", Share: "backups", BasePath: "base"}, Env{})
}

func TestNormalize(t *testing.T) {
	bad := []Config{{Type: "x"}, {Type: TypeFTP, Host: "ftp://x", User: "a"}, {Type: TypeFTP, Host: "x", User: ""}, {Type: TypeSFTP, Host: "x", User: "a"},
		{Type: TypeSMB, Host: "x", User: "a", Share: "a/b"}, {Type: TypeFTP, Host: "x", User: "a\r\nDELE y"}, {Type: TypeLocal, BasePath: "../x"},
		{Type: TypeNFS, Host: "x", Export: "relativa"}, {Type: TypeNFS, Host: "x", Export: "/a/../b"}, {Type: TypeNFS, Host: "", Export: "/x"},
		{Type: TypeNFS, Host: "x", Export: "/x", Port: 70000}, {Type: TypeNFS, Host: "x", Export: "/x", UID: new(-1)}}
	for i, c := range bad {
		if err := c.Normalize(); err == nil {
			t.Errorf("caso %d deveria falhar: %+v", i, c)
		}
	}
	n := Config{Type: TypeNFS, Host: "nas", Export: "/srv/backups/", User: "ignorado", Password: "x"}
	if err := n.Normalize(); err != nil || n.Port != 0 || n.Export != "/srv/backups" || n.User != "" || n.Password != "" || n.Label() != "nas:/srv/backups" {
		t.Fatalf("NFS: %v %+v", err, n)
	}
	c := Config{Type: TypeFTP, Host: "10.0.0.1", User: "a"}
	if err := c.Normalize(); err != nil || c.TLS != TLSExplicit || c.Port != 21 || c.Verify != VerifyFull {
		t.Fatalf("padrões: %v %+v", err, c)
	}
}
