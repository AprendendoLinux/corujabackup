package uploader

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/AprendendoLinux/corujabackup/internal/cloud"
	"github.com/AprendendoLinux/corujabackup/internal/cloud/cloudtest"
)

func TestDropboxEnvioEmPartesRetomaEExpira(t *testing.T) {
	fake := cloudtest.NewDropbox("k")
	defer fake.Close()
	o := cloud.DropboxOAuth("k", "")
	o.TokenURL = fake.URL + "/oauth2/token"
	tok, err := o.RefreshScoped(context.Background(), "dbx-refresh", cloud.DropboxAgentScope, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	data := make([]byte, 13<<20+123)
	_, _ = rand.Read(data)
	p := filepath.Join(t.TempDir(), "a.bin")
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatal(err)
	}
	sess := Session{Protocol: "dropbox", URL: fake.URL + "/2/files/upload_session", ChunkSize: 4 << 20, Token: tok.Access, Path: "/Backups ção/x.bin"}
	opt := Options{Sleep: func(context.Context, time.Duration) {}}
	fake.FailAppends, fake.LoseAppendReply = 2, true // 2 falhas e uma resposta perdida (o Dropbox devolve correct_offset)
	var last int64
	opt.Progress = func(s, _ int64) { last = s }
	res, err := Upload(context.Background(), sess, p, opt)
	if err != nil {
		t.Fatal(err)
	}
	if res.VerifiedBy != "dropbox" || !bytes.Equal(fake.Files["/Backups ção/x.bin"], data) || last == 0 {
		t.Fatalf("resultado: %+v (%d bytes gravados)", res, len(fake.Files["/Backups ção/x.bin"]))
	}
	sess.Token = "vencido"
	if _, err := Upload(context.Background(), sess, p, opt); !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("token vencido deveria pedir nova sessão: %v", err)
	}
	sess.Protocol = "inventado"
	if _, err := Upload(context.Background(), sess, p, opt); err == nil {
		t.Fatal("protocolo desconhecido deveria falhar")
	}
}
