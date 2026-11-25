package cloud_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AprendendoLinux/corujabackup/internal/cloud"
	"github.com/AprendendoLinux/corujabackup/internal/cloud/cloudtest"
	"github.com/AprendendoLinux/corujabackup/internal/uploader"
)

func TestCleanFolder(t *testing.T) {
	for _, bad := range []string{"", "/", "a/../b", "..", "a//b", "a/./b", `a\b`, "a:b"} {
		if _, err := cloud.CleanFolder(bad); err == nil {
			t.Errorf("aceitou %q", bad)
		}
	}
	if f, err := cloud.CleanFolder("/Coruja Backup/srv 1/etc/"); err != nil || f != "Coruja Backup/srv 1/etc" {
		t.Fatalf("%q %v", f, err)
	}
}

func TestRetention(t *testing.T) {
	mk := func(names ...string) []cloud.RemoteFile {
		var o []cloud.RemoteFile
		for _, n := range names {
			o = append(o, cloud.RemoteFile{Name: n})
		}
		return o
	}
	files := mk("job-20261120-001000.tar.gz", "job-20261121-001000.tar.gz", "job-20261122-001000.tar.gz", "job-20261123-001000.tar.gz",
		"outro-20260101-000000.tar.gz", "leia-me.txt", "job-notas.tar.gz")
	names := func(fs []cloud.RemoteFile) string {
		var s []string
		for _, f := range fs {
			s = append(s, f.Name)
		}
		return strings.Join(s, ",")
	}
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	if got := names(cloud.Retention(files, "job", 2, 0, now)); got != "job-20261121-001000.tar.gz,job-20261120-001000.tar.gz" {
		t.Fatalf("por quantidade: %s", got)
	}
	if got := names(cloud.Retention(files, "job", 0, 3, now)); got != "job-20261120-001000.tar.gz" {
		t.Fatalf("por dias (>3 dias): %s", got)
	}
	// nunca apaga o mais novo, mesmo que todos estejam "velhos"
	if got := names(cloud.Retention(mk("job-20200101-000000.tar.gz", "job-20200102-000000.tar.gz"), "job", 0, 1, now)); got != "job-20200101-000000.tar.gz" {
		t.Fatalf("o mais novo deve ficar: %s", got)
	}
	if got := cloud.Retention(files, "job", 0, 0, now); len(got) != 0 {
		t.Fatalf("sem regra, nada é apagado: %v", got)
	}
}

type memSave struct{ saved []cloud.Token }

func (m *memSave) save(_ context.Context, t cloud.Token) error {
	m.saved = append(m.saved, t)
	return nil
}

func writeFile(t *testing.T, size int) (string, []byte) {
	t.Helper()
	data := make([]byte, size)
	for i := range data {
		data[i] = byte(i*31 + i>>7)
	}
	p := filepath.Join(t.TempDir(), "backup.tar.gz")
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return p, data
}

// ---------------------------------------------------------------- OneDrive

func oneDrive(t *testing.T, fake *cloudtest.Graph) (*cloud.OneDrive, *memSave) {
	t.Helper()
	oa := &cloud.OAuth{ClientID: "cid", DeviceURL: fake.URL + "/devicecode", TokenURL: fake.URL + "/token", Scope: "x"}
	ms := &memSave{}
	// fluxo real: device code -> tokens
	dev, err := oa.StartDevice(context.Background(), time.Now())
	if err != nil || dev.UserCode != "ABCD-1234" {
		t.Fatalf("device: %v %+v", err, dev)
	}
	var tok *cloud.Token
	for i := 0; i < 5 && tok == nil; i++ {
		res, tk, err := oa.PollDevice(context.Background(), dev, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		if res == cloud.PollOK {
			tok = tk
		}
	}
	if tok == nil {
		t.Fatal("não obteve token")
	}
	ts := cloud.NewTokenSource(oa, *tok, ms.save)
	return &cloud.OneDrive{TS: ts, Graph: fake.GraphURL(), HardDelete: true}, ms
}

func TestOneDriveDeviceFlowEPendente(t *testing.T) {
	fake := cloudtest.NewGraph("cid")
	defer fake.Close()
	fake.PollsUntilOK = 2
	oa := &cloud.OAuth{ClientID: "cid", DeviceURL: fake.URL + "/devicecode", TokenURL: fake.URL + "/token"}
	dev, _ := oa.StartDevice(context.Background(), time.Now())
	for i := 0; i < 2; i++ {
		if res, _, err := oa.PollDevice(context.Background(), dev, time.Now()); err != nil || res != cloud.PollPending {
			t.Fatalf("esperava pendente: %v %v", res, err)
		}
	}
	if res, tok, err := oa.PollDevice(context.Background(), dev, time.Now()); err != nil || res != cloud.PollOK || tok.Refresh == "" {
		t.Fatalf("esperava ok: %v %v", res, err)
	}
	bad := &cloud.OAuth{ClientID: "errado", DeviceURL: fake.URL + "/devicecode", TokenURL: fake.URL + "/token"}
	if _, err := bad.StartDevice(context.Background(), time.Now()); err == nil {
		t.Fatal("client_id inválido deveria falhar")
	}
}

func TestOneDriveEnvioVerificadoRetencaoERenovacao(t *testing.T) {
	fake := cloudtest.NewGraph("cid")
	defer fake.Close()
	od, ms := oneDrive(t, fake)
	ctx := context.Background()

	if who, err := od.Account(ctx); err != nil || who != "fulano@exemplo.onmicrosoft.com" {
		t.Fatalf("%q %v", who, err)
	}
	if q, err := od.Quota(ctx); err != nil || q.Total != 1<<40 || q.Used != 5<<30 {
		t.Fatalf("%+v %v", q, err)
	}
	// arquivo de ~25 MiB: vários blocos de 10 MiB (o último menor)
	path, data := writeFile(t, 25*1024*1024+123)
	sess, err := od.NewUpload(ctx, "Coruja Backup/srv1/etc", "etc-20261126-001000.tar.gz", int64(len(data)))
	if err != nil || sess.Protocol != "graph" || sess.ChunkSize%327680 != 0 {
		t.Fatalf("%+v %v", sess, err)
	}
	var lastPct int64
	res, err := uploader.Upload(ctx, uploader.Session{Protocol: sess.Protocol, URL: sess.URL, ChunkSize: sess.ChunkSize}, path,
		uploader.Options{Progress: func(sent, total int64) { lastPct = sent * 100 / total }})
	if err != nil {
		t.Fatal(err)
	}
	if res.VerifiedBy != "quickxor" || res.RemoteQuickXor != res.Digest.QuickXor || lastPct != 100 {
		t.Fatalf("verificação: %+v pct=%d", res, lastPct)
	}
	if !bytes.Equal(fake.Content("Coruja Backup/srv1/etc", "etc-20261126-001000.tar.gz"), data) {
		t.Fatal("o conteúdo na nuvem difere do enviado")
	}
	// o Server confere por conta própria (Stat) e vê o mesmo hash
	rf, err := od.Stat(ctx, "Coruja Backup/srv1/etc", "etc-20261126-001000.tar.gz")
	if err != nil || rf.QuickXor != res.Digest.QuickXor || rf.Size != int64(len(data)) {
		t.Fatalf("%+v %v", rf, err)
	}
	if _, err := od.Stat(ctx, "Coruja Backup/srv1/etc", "nao-existe.tar.gz"); err != cloud.ErrNotFound {
		t.Fatalf("esperava ErrNotFound: %v", err)
	}
	// listagem e exclusão definitiva
	fake.Put("Coruja Backup/srv1/etc", "etc-20261101-001000.tar.gz", []byte("velho"))
	list, err := od.List(ctx, "Coruja Backup/srv1/etc")
	if err != nil || len(list) != 2 {
		t.Fatalf("%v %v", list, err)
	}
	for _, f := range list {
		if f.Name == "etc-20261101-001000.tar.gz" {
			if err := od.Delete(ctx, "Coruja Backup/srv1/etc", f); err != nil {
				t.Fatal(err)
			}
		}
	}
	if len(fake.PermanentlyDelete) != 1 || len(fake.Deleted) != 0 {
		t.Fatalf("deveria apagar de vez (sem lixeira): perm=%v lixeira=%v", fake.PermanentlyDelete, fake.Deleted)
	}
	if l, _ := od.List(ctx, "Pasta/que/nao/existe"); len(l) != 0 {
		t.Fatalf("pasta inexistente lista vazio: %v", l)
	}
	_ = ms
}

func TestOneDriveContaPessoalCaiParaExclusaoComum(t *testing.T) {
	fake := cloudtest.NewGraph("cid")
	defer fake.Close()
	fake.SetNoPermanentDelete(true)
	od, _ := oneDrive(t, fake)
	fake.Put("A/B", "x-20261101-000000.tar.gz", []byte("v"))
	l, _ := od.List(context.Background(), "A/B")
	if err := od.Delete(context.Background(), "A/B", l[0]); err != nil {
		t.Fatal(err)
	}
	if len(fake.Deleted) != 1 {
		t.Fatalf("sem permanentDelete deveria usar DELETE: %v", fake.Deleted)
	}
}

func TestRenovacaoDeTokenRotacionaEPersiste(t *testing.T) {
	fake := cloudtest.NewGraph("cid")
	defer fake.Close()
	oa := &cloud.OAuth{ClientID: "cid", DeviceURL: fake.URL + "/devicecode", TokenURL: fake.URL + "/token"}
	ms := &memSave{}
	// token já vencido: o primeiro uso renova
	ts := cloud.NewTokenSource(oa, cloud.Token{Access: "velho", Refresh: "refresh-0", Expiry: time.Now().Add(-time.Hour)}, ms.save)
	od := &cloud.OneDrive{TS: ts, Graph: fake.GraphURL()}
	if _, err := od.Account(context.Background()); err != nil {
		t.Fatal(err)
	}
	if fake.RefreshCount != 1 || len(ms.saved) != 1 || ms.saved[0].Refresh != "refresh-1" {
		t.Fatalf("o novo refresh token deve ser persistido: %+v (renovações=%d)", ms.saved, fake.RefreshCount)
	}
	if _, err := od.Account(context.Background()); err != nil || fake.RefreshCount != 1 {
		t.Fatalf("token ainda válido não deve renovar de novo: %v %d", err, fake.RefreshCount)
	}
	// refresh token revogado
	ts2 := cloud.NewTokenSource(oa, cloud.Token{Refresh: "revogado"}, nil)
	if _, err := ts2.Access(context.Background()); err == nil || !strings.Contains(err.Error(), "reconecte") {
		t.Fatalf("esperava pedido de reconexão: %v", err)
	}
}

func TestUploadRetomaAposFalhaDeRede(t *testing.T) {
	fake := cloudtest.NewGraph("cid")
	defer fake.Close()
	od, _ := oneDrive(t, fake)
	path, data := writeFile(t, 21*1024*1024)
	sess, _ := od.NewUpload(context.Background(), "A/B", "j-20261126-000000.tar.gz", int64(len(data)))
	fake.FailPuts = 2 // dois 503 seguidos; o cliente consulta o status e continua
	slept := 0
	res, err := uploader.Upload(context.Background(), uploader.Session{Protocol: "graph", URL: sess.URL, ChunkSize: sess.ChunkSize}, path,
		uploader.Options{Sleep: func(context.Context, time.Duration) { slept++ }})
	if err != nil || res.VerifiedBy != "quickxor" || slept != 2 {
		t.Fatalf("%+v %v (esperas=%d)", res, err, slept)
	}
	if !bytes.Equal(fake.Content("A/B", "j-20261126-000000.tar.gz"), data) {
		t.Fatal("conteúdo difere após retomada")
	}
}

func TestUploadSessaoExpiradaEHashDiferente(t *testing.T) {
	fake := cloudtest.NewGraph("cid")
	defer fake.Close()
	od, _ := oneDrive(t, fake)
	path, data := writeFile(t, 1<<20)
	sess, _ := od.NewUpload(context.Background(), "A", "j-20261126-000000.tar.gz", int64(len(data)))
	fake.ExpireSessions = true
	if _, err := uploader.Upload(context.Background(), uploader.Session{Protocol: "graph", URL: sess.URL, ChunkSize: sess.ChunkSize}, path, uploader.Options{}); err != uploader.ErrSessionExpired {
		t.Fatalf("esperava sessão expirada: %v", err)
	}
	fake.ExpireSessions = false
	// a nuvem devolve falha após esgotar as tentativas
	sess2, _ := od.NewUpload(context.Background(), "A", "k-20261126-000000.tar.gz", int64(len(data)))
	fake.FailPuts = 100
	_, err := uploader.Upload(context.Background(), uploader.Session{Protocol: "graph", URL: sess2.URL, ChunkSize: sess2.ChunkSize}, path,
		uploader.Options{Retries: 2, Sleep: func(context.Context, time.Duration) {}})
	if err == nil || !strings.Contains(err.Error(), "tentativas") {
		t.Fatalf("esperava desistir após as tentativas: %v", err)
	}
}

func TestUploadCancelamento(t *testing.T) {
	fake := cloudtest.NewGraph("cid")
	defer fake.Close()
	od, _ := oneDrive(t, fake)
	path, data := writeFile(t, 30*1024*1024)
	sess, _ := od.NewUpload(context.Background(), "A", "j-20261126-000000.tar.gz", int64(len(data)))
	ctx, cancel := context.WithCancel(context.Background())
	_, err := uploader.Upload(ctx, uploader.Session{Protocol: "graph", URL: sess.URL, ChunkSize: sess.ChunkSize}, path,
		uploader.Options{Progress: func(sent, total int64) { cancel() }})
	if err != context.Canceled {
		t.Fatalf("esperava cancelamento: %v", err)
	}
}

// ---------------------------------------------------------------- Google Drive

func gdrive(t *testing.T, fake *cloudtest.Drive) *cloud.GDrive {
	t.Helper()
	oa := &cloud.OAuth{ClientID: "gid", ClientSecret: "segredo", DeviceURL: fake.URL + "/device/code", TokenURL: fake.URL + "/token"}
	dev, err := oa.StartDevice(context.Background(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	res, tok, err := oa.PollDevice(context.Background(), dev, time.Now())
	if err != nil || res != cloud.PollOK {
		t.Fatalf("%v %v", res, err)
	}
	return &cloud.GDrive{TS: cloud.NewTokenSource(oa, *tok, nil), API: fake.APIURL(), Upload: fake.UploadURL()}
}

func TestGDriveEnvioVerificadoPorMD5ERetencao(t *testing.T) {
	fake := cloudtest.NewDrive("gid")
	defer fake.Close()
	gd := gdrive(t, fake)
	ctx := context.Background()
	if who, err := gd.Account(ctx); err != nil || who != "fulano@gmail.com" {
		t.Fatalf("%q %v", who, err)
	}
	if q, err := gd.Quota(ctx); err != nil || q.Total != 15<<30 {
		t.Fatalf("%+v %v", q, err)
	}
	path, data := writeFile(t, 17*1024*1024+7)
	folder := "Coruja Backup/srv'1/etc"
	sess, err := gd.NewUpload(ctx, folder, "etc-20261126-001000.tar.gz", int64(len(data)))
	if err != nil || sess.Protocol != "gdrive" {
		t.Fatalf("%+v %v", sess, err)
	}
	res, err := uploader.Upload(ctx, uploader.Session{Protocol: "gdrive", URL: sess.URL, ChunkSize: sess.ChunkSize}, path, uploader.Options{})
	if err != nil || res.VerifiedBy != "md5" || res.RemoteMD5 != res.Digest.MD5 {
		t.Fatalf("%+v %v", res, err)
	}
	if !bytes.Equal(fake.Content(folder, "etc-20261126-001000.tar.gz"), data) {
		t.Fatal("conteúdo difere")
	}
	rf, err := gd.Stat(ctx, folder, "etc-20261126-001000.tar.gz")
	if err != nil || rf.MD5 != res.Digest.MD5 {
		t.Fatalf("%+v %v", rf, err)
	}
	// segunda sessão reutiliza as pastas (cache) e a listagem enxerga os dois arquivos
	fake.Put(folder, "etc-20261101-001000.tar.gz", []byte("velho"))
	l, err := gd.List(ctx, folder)
	if err != nil || len(l) != 2 {
		t.Fatalf("%v %v", l, err)
	}
	for _, f := range l {
		if f.Name == "etc-20261101-001000.tar.gz" {
			if err := gd.Delete(ctx, folder, f); err != nil {
				t.Fatal(err)
			}
		}
	}
	if len(fake.PermanentlyDelete) != 1 || len(fake.Names(folder)) != 1 {
		t.Fatalf("exclusão definitiva: %v %v", fake.PermanentlyDelete, fake.Names(folder))
	}
	if l, err := gd.List(ctx, "Pasta/inexistente"); err != nil || len(l) != 0 {
		t.Fatalf("%v %v", l, err)
	}
	if _, err := gd.Stat(ctx, "Pasta/inexistente", "x"); err != cloud.ErrNotFound {
		t.Fatalf("%v", err)
	}
}

func TestGDriveRetomaComConsultaDeStatus(t *testing.T) {
	fake := cloudtest.NewDrive("gid")
	defer fake.Close()
	gd := gdrive(t, fake)
	path, data := writeFile(t, 20*1024*1024)
	sess, _ := gd.NewUpload(context.Background(), "A/B", "j-20261126-000000.tar.gz", int64(len(data)))
	fake.FailPuts = 1
	res, err := uploader.Upload(context.Background(), uploader.Session{Protocol: "gdrive", URL: sess.URL, ChunkSize: sess.ChunkSize}, path,
		uploader.Options{Sleep: func(context.Context, time.Duration) {}})
	if err != nil || res.VerifiedBy != "md5" {
		t.Fatalf("%+v %v", res, err)
	}
	if !bytes.Equal(fake.Content("A/B", "j-20261126-000000.tar.gz"), data) {
		t.Fatal("conteúdo difere após retomada")
	}
}

func TestUploadSemHashDaNuvemConfereSoOTamanho(t *testing.T) {
	fake := cloudtest.NewDrive("gid")
	defer fake.Close()
	fake.NoHashes = true
	gd := gdrive(t, fake)
	path, data := writeFile(t, 300_000)
	sess, _ := gd.NewUpload(context.Background(), "A", "j-20261126-000000.tar.gz", int64(len(data)))
	res, err := uploader.Upload(context.Background(), uploader.Session{Protocol: "gdrive", URL: sess.URL, ChunkSize: sess.ChunkSize}, path, uploader.Options{})
	if err != nil || res.VerifiedBy != "size" {
		t.Fatalf("%+v %v", res, err)
	}
}
