package nfs3

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestXDR(t *testing.T) {
	var e enc
	e.u32(7)
	e.str("abcde")
	e.u64(1 << 40)
	e.bool(true)
	if len(e.b) != 4+4+8+8+4 {
		t.Fatalf("tamanho %d", len(e.b))
	}
	d := dec{b: e.b}
	if d.u32() != 7 || string(d.opaque(10)) != "abcde" || d.u64() != 1<<40 || !d.bool() || d.err != nil {
		t.Fatal("decodificação")
	}
	d = dec{b: e.b[:6]}
	d.u32()
	d.opaque(10)
	if d.err == nil {
		t.Fatal("truncado devia falhar")
	}
	big := enc{}
	big.u32(1 << 30)
	d = dec{b: big.b}
	if d.opaque(64); d.err == nil {
		t.Fatal("tamanho absurdo devia falhar")
	}
}

func TestParseReply(t *testing.T) {
	var ok enc
	ok.u32(1)
	ok.u32(0)
	ok.u32(0)
	ok.u32(0)
	ok.u32(0)
	ok.u32(42)
	b, err := parseReply(ok.b)
	if err != nil || len(b) != 4 {
		t.Fatal(b, err)
	}
	var weak enc
	weak.u32(1)
	weak.u32(1)
	weak.u32(1)
	weak.u32(5)
	if _, err := parseReply(weak.b); err == nil || !strings.Contains(err.Error(), "Kerberos") {
		t.Fatal(err)
	}
}

// TestServer roda contra um servidor NFSv3 de verdade: CORUJA_TEST_NFS=host:porta:/exportação (porta do NFS e do MOUNT
// juntas, como no go-nfs) ou host::/exportação (portmapper).
func TestServer(t *testing.T) {
	spec := os.Getenv("CORUJA_TEST_NFS")
	if spec == "" {
		t.Skip("CORUJA_TEST_NFS não definido")
	}
	parts := strings.SplitN(spec, ":", 3)
	o := Options{Host: parts[0], Export: parts[2]}
	if parts[1] != "" {
		o.Port, _ = strconv.Atoi(parts[1])
		o.MountPort = o.Port
	}
	if v := os.Getenv("CORUJA_TEST_NFS_UID"); v != "" { // com root_squash: o dono da pasta de teste
		n, _ := strconv.Atoi(v)
		o.UID, o.GID = uint32(n), uint32(n)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	c, err := Dial(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	base := path.Join(os.Getenv("CORUJA_TEST_NFS_BASE"), "coruja-teste-"+strconv.FormatInt(time.Now().UnixNano(), 36))
	dir := base + "/a/b"
	if err := c.MkdirAll(ctx, dir); err != nil {
		t.Fatal("mkdir:", err)
	}
	if err := c.MkdirAll(ctx, dir); err != nil {
		t.Fatal("mkdir repetido:", err)
	}
	data := make([]byte, int(c.wsize)*5+12345) // vários blocos em paralelo e um resto
	_, _ = rand.Read(data)
	w, err := c.Create(ctx, dir+"/x.part")
	if err != nil {
		t.Fatal("create:", err)
	}
	if _, err := io.CopyBuffer(w, bytes.NewReader(data), make([]byte, 7777)); err != nil {
		t.Fatal("write:", err)
	}
	if err := w.Close(); err != nil {
		t.Fatal("commit:", err)
	}
	if err := c.Rename(ctx, dir+"/x.part", dir+"/x"); err != nil {
		t.Fatal("rename:", err)
	}
	a, err := c.Stat(ctx, dir+"/x")
	if err != nil || a.Size != int64(len(data)) || !a.Regular {
		t.Fatal("stat:", a, err)
	}
	rc, err := c.Open(ctx, dir+"/x")
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(rc)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("releitura: %d bytes, %v", len(got), err)
	}
	es, err := c.ReadDir(ctx, dir)
	if err != nil || len(es) != 1 || es[0].Name != "x" || es[0].Size != int64(len(data)) {
		t.Fatal("readdir:", es, err)
	}
	if _, err := c.Stat(ctx, dir+"/x.part"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("o temporário devia ter sumido:", err)
	}
	if es, err := c.ReadDir(ctx, base); err != nil || len(es) != 1 || !es[0].Dir {
		t.Fatal("pasta:", es, err)
	}
	if total, avail, err := c.FSStat(ctx); err != nil || total <= 0 || avail < 0 {
		t.Log("fsstat:", total, avail, err) // nem todo servidor informa
	}
	if err := c.Remove(ctx, dir+"/x"); err != nil {
		t.Fatal("remove:", err)
	}
	if _, err := c.Stat(ctx, dir+"/x"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("devia ter sido apagado:", err)
	}
	// cancelamento interrompe a conexão
	cctx, ccancel := context.WithCancel(ctx)
	c2, err := Dial(cctx, o)
	if err != nil {
		t.Fatal(err)
	}
	ccancel()
	if _, err := c2.ReadDir(cctx, ""); err == nil {
		t.Fatal("devia falhar depois de cancelado")
	}
	c2.Close()
	// exportação inexistente
	bad := o
	bad.Export = "/nao-existe-" + base
	if c3, err := Dial(ctx, bad); err == nil {
		c3.Close()
		t.Log("o servidor aceitou qualquer exportação (go-nfs faz isso)")
	}
}
