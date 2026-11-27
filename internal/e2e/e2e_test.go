// Package e2e testa o fluxo inteiro: Server (gRPC + MySQL + gerenciador de nuvem) <-> agente real <-> nuvens falsas.
package e2e

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"math/rand"
	"net"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AprendendoLinux/corujabackup/internal/agentcore"
	"github.com/AprendendoLinux/corujabackup/internal/agentsrv"
	"github.com/AprendendoLinux/corujabackup/internal/auth"
	"github.com/AprendendoLinux/corujabackup/internal/cloud"
	"github.com/AprendendoLinux/corujabackup/internal/cloud/cloudtest"
	"github.com/AprendendoLinux/corujabackup/internal/cloudmgr"
	"github.com/AprendendoLinux/corujabackup/internal/dockerapi"
	"github.com/AprendendoLinux/corujabackup/internal/enroll"
	"github.com/AprendendoLinux/corujabackup/internal/netdest"
	"github.com/AprendendoLinux/corujabackup/internal/pb"
	"github.com/AprendendoLinux/corujabackup/internal/secrets"
	"github.com/AprendendoLinux/corujabackup/internal/store"
	"github.com/AprendendoLinux/corujabackup/internal/testutil"
	"github.com/AprendendoLinux/corujabackup/internal/tlsutil"
	"github.com/AprendendoLinux/corujabackup/internal/uploader"
)

type rig struct {
	t       *testing.T
	st      *store.Store
	srv     *agentsrv.Server
	mgr     *cloudmgr.Manager
	core    *agentcore.Core
	agent   *store.Agent
	hostDir string
	bkDir   string
	destDir string
	cancel  context.CancelFunc
}

func newRig(t *testing.T, ep cloudmgr.Endpoints) *rig {
	t.Helper()
	d := testutil.DB(t)
	st := store.New(d)
	box, err := secrets.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	mgr := cloudmgr.New(st, box)
	mgr.EP = ep
	cert, err := tlsutil.LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv := agentsrv.New(st, "teste")
	srv.Cloud = mgr
	srv.Box = box
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	// conexão invertida: o Server disca o endereço cadastrado do agente
	dialer := agentsrv.NewDialer(st.DialTargets)
	dialer.Every = 200 * time.Millisecond
	srv.Dialer = dialer
	go dialer.Run(ctx)
	go srv.Serve(ctx, dialer, cert)

	probe, _ := net.Listen("tcp", "127.0.0.1:0")
	agentAddr := probe.Addr().String()
	probe.Close()
	fp := tlsutil.Fingerprint(cert)
	tok, _ := enroll.New(fp)
	pt, _ := enroll.Parse(tok)
	if err := st.CreateEnrollTokenAt(ctx, pt.Hash(), "srv-e2e", agentAddr, 0, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	host, bk := t.TempDir(), t.TempDir()
	for name, content := range map[string]string{"srv/app/index.html": "olá mundo", "srv/app/dados.bin": strings.Repeat("0123456789", 50000), "etc/app.conf": "conf"} {
		p := filepath.Join(host, name)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(content), 0o644)
	}
	local, _ := agentcore.NewState(t.TempDir())
	destDir := t.TempDir()
	core := agentcore.New(agentcore.Config{HostRoot: host, BackupDir: bk, DestDir: destDir}, local, slog.New(slog.NewTextHandler(io.Discard, nil)))
	core.SetUploadOptions(uploader.Options{Retries: 2, Sleep: func(context.Context, time.Duration) {}})
	go core.Run(ctx)
	statePath := filepath.Join(t.TempDir(), "agent.json")
	go agentcore.Listen(ctx, slog.New(slog.NewTextHandler(io.Discard, nil)), agentcore.ListenConfig{Addr: agentAddr, Token: tok, StatePath: statePath}, nil, core)

	var a *store.Agent
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline) && a == nil; time.Sleep(100 * time.Millisecond) {
		ags, _ := st.ListAgents(ctx)
		for i := range ags {
			if ags[i].Name == "srv-e2e" {
				a = &ags[i]
			}
		}
	}
	if a == nil {
		t.Fatal("o agente não foi matriculado pela conexão invertida")
	}
	if a.Address != agentAddr {
		t.Fatalf("o endereço do token deveria passar para o agente: %q", a.Address)
	}
	r := &rig{t: t, st: st, srv: srv, mgr: mgr, core: core, agent: a, hostDir: host, bkDir: bk, destDir: destDir, cancel: cancel}
	r.waitOnline()
	return r
}

func (r *rig) waitOnline() {
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := r.srv.Online()[r.agent.ID]; ok {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	r.t.Fatal("o agente não conectou")
}

func (r *rig) connectCloud(typ string, cfg cloudmgr.AccountConfig) int64 {
	r.t.Helper()
	ctx := context.Background()
	id, dev, err := r.mgr.Create(ctx, "nuvem-"+typ, typ, cfg)
	if err != nil || dev.UserCode == "" {
		r.t.Fatalf("criar conta: %v", err)
	}
	for i := 0; i < 10; i++ {
		st, _, err := r.mgr.PollDevice(ctx, id)
		if err != nil {
			r.t.Fatal(err)
		}
		if st == "connected" {
			return id
		}
		time.Sleep(1100 * time.Millisecond) // respeita o intervalo de 1 s do falso
	}
	r.t.Fatal("conta não conectou")
	return 0
}

func (r *rig) newJob(name string, cloudID int64, keep, cloudKeep int) int64 {
	r.t.Helper()
	j := &store.Job{Name: name, AgentID: r.agent.ID, Kind: "files", Enabled: true, Cron: "0 0 1 1 *", Timezone: "UTC",
		Paths: []string{"/srv/app", "/etc"}, DestSubdir: "e2e", Keep: keep, CloudFolder: "Coruja Backup/srv-e2e/" + name, CloudKeep: cloudKeep}
	if cloudID != 0 {
		j.CloudAccountID = &cloudID
	}
	id, err := r.st.CreateJob(context.Background(), j)
	if err != nil {
		r.t.Fatal(err)
	}
	r.srv.PushJobs(context.Background(), r.agent.ID)
	time.Sleep(300 * time.Millisecond)
	return id
}

func (r *rig) runNow(jobID int64) *store.Run {
	r.t.Helper()
	ctx := context.Background()
	uid := auth.NewUUID()
	if err := r.st.CreateQueuedRun(ctx, uid, jobID, r.agent.ID); err != nil {
		r.t.Fatal(err)
	}
	if err := r.srv.RunNow(r.agent.ID, jobID, uid, false); err != nil {
		r.t.Fatal(err)
	}
	deadline := time.Now().Add(40 * time.Second)
	for time.Now().Before(deadline) {
		run, err := r.st.RunByUID(ctx, uid)
		if err == nil && store.IsFinal(run.Status) {
			return run
		}
		time.Sleep(100 * time.Millisecond)
	}
	r.t.Fatal("a execução não terminou a tempo")
	return nil
}

func (r *rig) logs(run *store.Run) string {
	ls, _ := r.st.RunLogs(context.Background(), run.ID)
	var b strings.Builder
	for _, l := range ls {
		b.WriteString(l.Level + ": " + l.Text + "\n")
	}
	return b.String()
}

func localFiles(t *testing.T, dir string) []string {
	ents, _ := os.ReadDir(filepath.Join(dir, "e2e"))
	var out []string
	for _, e := range ents {
		out = append(out, e.Name())
	}
	return out
}

func TestOneDriveDeBuildAFimComRetencaoELocalZero(t *testing.T) {
	fake := cloudtest.NewGraph("cid")
	defer fake.Close()
	r := newRig(t, cloudmgr.Endpoints{MSDevice: fake.URL + "/devicecode", MSToken: fake.URL + "/token", Graph: fake.GraphURL()})
	cid := r.connectCloud(cloud.TypeOneDrive, cloudmgr.AccountConfig{ClientID: "cid", HardDelete: true})

	// os tokens estão cifrados no banco
	var tokens, cfg string
	r.st.DB.QueryRow("SELECT tokens, config FROM cloud_accounts WHERE id=?", cid).Scan(&tokens, &cfg)
	if !strings.HasPrefix(tokens, "v1:") || strings.Contains(tokens, "access-") || strings.Contains(cfg, "cid\"") {
		t.Fatalf("segredos devem estar cifrados no banco: tokens=%.20s cfg=%.20s", tokens, cfg)
	}
	acc, _ := r.st.GetCloud(context.Background(), cid)
	if acc.Status != "connected" || acc.AccountLabel != "fulano@exemplo.onmicrosoft.com" || acc.QuotaTotal == 0 {
		t.Fatalf("conta: %+v", acc)
	}

	// 3 backups antigos na nuvem; retenção = manter 2 (o novo + 1)
	folder := "Coruja Backup/srv-e2e/zero"
	for _, n := range []string{"zero-20261101-001000.tar.gz", "zero-20261102-001000.tar.gz", "zero-20261103-001000.tar.gz"} {
		fake.Put(folder, n, []byte("antigo"))
	}
	jid := r.newJob("zero", cid, 0, 2) // keep local = 0: apagar depois da nuvem confirmar

	run := r.runNow(jid)
	if run.Status != "ok" || run.CloudStatus != "ok" || run.CloudVerifiedBy != "quickxor" || run.CloudHash == "" || run.Phase != "" {
		t.Fatalf("execução: %+v\n%s", run, r.logs(run))
	}
	if !strings.HasPrefix(run.CloudPath, folder+"/zero-") {
		t.Fatalf("caminho na nuvem: %s", run.CloudPath)
	}
	names := fake.Names(folder)
	if len(names) != 2 {
		t.Fatalf("retenção na nuvem deveria deixar 2 arquivos: %v", names)
	}
	if len(fake.PermanentlyDelete) != 2 {
		t.Fatalf("deveria apagar de vez (sem lixeira): %v", fake.PermanentlyDelete)
	}
	if lf := localFiles(t, r.bkDir); len(lf) != 0 {
		t.Fatalf("keep=0: a cópia local deveria ser removida após a nuvem confirmar: %v", lf)
	}
	logs := r.logs(run)
	for _, want := range []string{"enviando para a nuvem", "verificado por quickxor", "removido da nuvem: zero-20261101", "cópia local removida"} {
		if !strings.Contains(logs, want) {
			t.Errorf("log sem %q:\n%s", want, logs)
		}
	}
	// o conteúdo na nuvem é um tar.gz íntegro com os arquivos do "servidor"
	var remote []byte
	for _, n := range names {
		if strings.HasPrefix(n, "zero-2026") && fake.Content(folder, n) != nil && len(fake.Content(folder, n)) > 100 {
			remote = fake.Content(folder, n)
		}
	}
	if len(remote) == 0 || !bytes.HasPrefix(remote, []byte{0x1f, 0x8b}) {
		t.Fatalf("o arquivo na nuvem deveria ser um gzip: %d bytes", len(remote))
	}
}

func TestGoogleDriveMantemCopiaLocalEPreservaFalhas(t *testing.T) {
	fake := cloudtest.NewDrive("gid")
	defer fake.Close()
	r := newRig(t, cloudmgr.Endpoints{GDevice: fake.URL + "/device/code", GToken: fake.URL + "/token", GAPI: fake.APIURL(), GUpload: fake.UploadURL()})
	cid := r.connectCloud(cloud.TypeGDrive, cloudmgr.AccountConfig{ClientID: "gid", ClientSecret: "segredo"})
	jid := r.newJob("gd", cid, 2, 3)

	run := r.runNow(jid)
	if run.Status != "ok" || run.CloudStatus != "ok" || run.CloudVerifiedBy != "md5" {
		t.Fatalf("execução: %+v\n%s", run, r.logs(run))
	}
	if lf := localFiles(t, r.bkDir); len(lf) != 1 {
		t.Fatalf("keep=2: a cópia local deve ficar: %v", lf)
	}

	// falha no envio: a nuvem derruba todos os blocos => o backup local é preservado e a execução fica como falha
	fake.FailPuts = 1000
	run2 := r.runNow(jid)
	if run2.Status != "failed" || run2.CloudStatus != "failed" || !strings.Contains(run2.Error, "envio para a nuvem falhou") {
		t.Fatalf("execução com falha: %+v\n%s", run2, r.logs(run2))
	}
	if lf := localFiles(t, r.bkDir); len(lf) != 2 {
		t.Fatalf("o arquivo local da execução com falha deve ser preservado: %v", lf)
	}
	if n := len(fake.Names("Coruja Backup/srv-e2e/gd")); n != 1 {
		t.Fatalf("a nuvem deve manter só o envio bom: %d", n)
	}
}

func TestSessaoRecusadaPeloServerESemNuvemNoJob(t *testing.T) {
	fake := cloudtest.NewGraph("cid")
	defer fake.Close()
	r := newRig(t, cloudmgr.Endpoints{MSDevice: fake.URL + "/devicecode", MSToken: fake.URL + "/token", Graph: fake.GraphURL()})
	// job só local continua funcionando e não fala com a nuvem
	jid := r.newJob("local", 0, 2, 0)
	run := r.runNow(jid)
	if run.Status != "ok" || run.CloudStatus != "none" {
		t.Fatalf("%+v\n%s", run, r.logs(run))
	}
	// a nuvem ficou sem token válido (revogado): o envio falha com mensagem clara e o local é preservado
	cid := r.connectCloud(cloud.TypeOneDrive, cloudmgr.AccountConfig{ClientID: "cid"})
	jid2 := r.newJob("revogado", cid, 1, 2)
	fake.RefreshToken = "outro" // o refresh token guardado deixa de valer
	r.mgr.Invalidate(cid)
	r.st.DB.Exec("UPDATE cloud_accounts SET tokens=? WHERE id=?", mustSeal(t, r, `{"access_token":"velho","refresh_token":"refresh-0","expiry":"2000-01-01T00:00:00Z"}`), cid)
	run2 := r.runNow(jid2)
	if run2.Status != "failed" || !strings.Contains(run2.Error, "reconecte") {
		t.Fatalf("esperava falha pedindo reconexão: %+v\n%s", run2, r.logs(run2))
	}
	acc, _ := r.st.GetCloud(context.Background(), cid)
	if acc.Status != "error" {
		t.Fatalf("a conta deveria ficar em estado de erro: %+v", acc)
	}
}

func mustSeal(t *testing.T, r *rig, plain string) string {
	s, err := r.mgr.Box.Seal(plain)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestExecucaoOrfaDepoisDeReinicioEhMarcadaComoFalha(t *testing.T) {
	fake := cloudtest.NewGraph("cid")
	defer fake.Close()
	r := newRig(t, cloudmgr.Endpoints{MSDevice: fake.URL + "/devicecode", MSToken: fake.URL + "/token", Graph: fake.GraphURL()})
	jid := r.newJob("orfa", 0, 1, 0)
	// uma execução que o Server acha que está rodando há mais de 90 s, mas o agente não conhece
	if _, err := r.st.DB.Exec("INSERT INTO runs (uid, job_id, agent_id, trigger_kind, status, created_at) VALUES ('uid-orfao', ?, ?, 'manual', 'running', ?)", jid, r.agent.ID, time.Now().UTC().Add(-5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	n, err := r.st.FailStaleRuns(context.Background(), r.agent.ID, []string{})
	if err != nil || n != 1 {
		t.Fatalf("%d %v", n, err)
	}
	run, _ := r.st.RunByUID(context.Background(), "uid-orfao")
	if run.Status != "failed" || !strings.Contains(run.Error, "reiniciado") {
		t.Fatalf("%+v", run)
	}
}

// ---------------------------------------------------------------- banco de dados (contêiner MySQL real + nuvem falsa)

func TestJobDeBancoDeBuildAFimComRestauracaoRealENuvem(t *testing.T) {
	if os.Getenv("CORUJA_TEST_DOCKER") != "1" {
		t.Skip("CORUJA_TEST_DOCKER=1 não definido")
	}
	dk := dockerapi.New("")
	if !dk.Available(context.Background()) {
		t.Skip("Docker indisponível")
	}
	ctx := context.Background()
	if err := dk.Pull(ctx, "mysql:8.0"); err != nil {
		t.Fatal(err)
	}
	src, err := dk.Create(ctx, dockerapi.CreateOptions{Image: "mysql:8.0", Env: []string{"MYSQL_ROOT_PASSWORD=segredo-e2e"}, Memory: 2 << 30, Labels: map[string]string{"coruja.test": "1"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { dk.Remove(context.Background(), src) })
	if err := dk.Start(ctx, src); err != nil {
		t.Fatal(err)
	}
	// espera o MySQL aceitar conexões (TCP = servidor final) e cria dados
	deadline := time.Now().Add(3 * time.Minute)
	for {
		code, _ := dk.Exec(ctx, src, dockerapi.ExecOptions{Cmd: []string{"mysqladmin", "-h", "127.0.0.1", "-uroot", "-psegredo-e2e", "ping"}, Stdout: io.Discard, Stderr: io.Discard})
		if code == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("mysql não ficou pronto")
		}
		time.Sleep(2 * time.Second)
	}
	code, err := dk.Exec(ctx, src, dockerapi.ExecOptions{Cmd: []string{"mysql", "-uroot", "-psegredo-e2e"}, Stdin: strings.NewReader("CREATE DATABASE app; USE app; CREATE TABLE a (id INT PRIMARY KEY); CREATE TABLE b (id INT PRIMARY KEY); INSERT INTO a VALUES (1);"), Stdout: io.Discard, Stderr: io.Discard})
	if err != nil || code != 0 {
		t.Fatalf("seed: %d %v", code, err)
	}

	fake := cloudtest.NewGraph("cid")
	defer fake.Close()
	r := newRig(t, cloudmgr.Endpoints{MSDevice: fake.URL + "/devicecode", MSToken: fake.URL + "/token", Graph: fake.GraphURL()})
	cid := r.connectCloud(cloud.TypeOneDrive, cloudmgr.AccountConfig{ClientID: "cid", HardDelete: true})

	cfg := &store.DBConfig{Engine: "mysql", Mode: "docker", Container: src, RestoreTest: "full"}
	cfg.ExcludeDatabases = []string{"mysql"}
	sealed, err := cfg.Seal(r.mgr.Box) // sem senha: o agente a obtém do ambiente do contêiner
	if err != nil {
		t.Fatal(err)
	}
	j := &store.Job{Name: "bancos", AgentID: r.agent.ID, Kind: "database", Enabled: true, Cron: "0 0 1 1 *", Timezone: "UTC", Paths: []string{}, DestSubdir: "e2e",
		Keep: 0, CloudAccountID: &cid, CloudFolder: "Coruja Backup/srv-e2e/bancos", CloudKeep: 2, DBSealed: sealed}
	jid, err := r.st.CreateJob(ctx, j)
	if err != nil {
		t.Fatal(err)
	}
	r.srv.PushJobs(ctx, r.agent.ID)
	time.Sleep(400 * time.Millisecond)

	run := r.runNow(jid)
	if run.Status != "ok" || run.CloudStatus != "ok" || run.CloudVerifiedBy != "quickxor" || run.Files != 1 {
		t.Fatalf("execução: %+v\n%s", run, r.logs(run))
	}
	logs := r.logs(run)
	for _, want := range []string{"banco app", "2 tabela(s)/coleção", "teste de restauração real concluído", "verificado por quickxor", "cópia local removida"} {
		if !strings.Contains(logs, want) {
			t.Errorf("log sem %q:\n%s", want, logs)
		}
	}
	names := fake.Names("Coruja Backup/srv-e2e/bancos")
	if len(names) != 1 {
		t.Fatalf("nuvem: %v", names)
	}
	gz, err := gzip.NewReader(bytes.NewReader(fake.Content("Coruja Backup/srv-e2e/bancos", names[0])))
	if err != nil {
		t.Fatal(err)
	}
	var files []string
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err != nil {
			break
		}
		files = append(files, h.Name)
	}
	if strings.Join(files, ",") != "bancos/app.sql,bancos/manifest.json" {
		t.Fatalf("conteúdo do arquivo na nuvem: %v", files)
	}
	if lf := localFiles(t, r.bkDir); len(lf) != 0 {
		t.Fatalf("keep=0: sem cópia local: %v", lf)
	}
}

func (r *rig) waitRun(uid string, cond func(*store.Run) bool, what string) *store.Run {
	r.t.Helper()
	deadline := time.Now().Add(40 * time.Second)
	for time.Now().Before(deadline) {
		if run, err := r.st.RunByUID(context.Background(), uid); err == nil && cond(run) {
			return run
		}
		time.Sleep(150 * time.Millisecond)
	}
	r.t.Fatalf("tempo esgotado esperando: %s", what)
	return nil
}

func TestReenvioParaNuvemExtracaoEDownload(t *testing.T) {
	fake := cloudtest.NewGraph("cid")
	defer fake.Close()
	r := newRig(t, cloudmgr.Endpoints{MSDevice: fake.URL + "/devicecode", MSToken: fake.URL + "/token", Graph: fake.GraphURL()})
	cid := r.connectCloud(cloud.TypeOneDrive, cloudmgr.AccountConfig{ClientID: "cid", HardDelete: true})
	jid := r.newJob("reenvio", cid, 2, 3)
	ctx := context.Background()

	fake.FailPuts = 1000 // a nuvem recusa o envio
	run := r.runNow(jid)
	if run.Status != "failed" || run.CloudStatus != "failed" || run.OutputName == "" {
		t.Fatalf("o envio deveria falhar: %+v\n%s", run, r.logs(run))
	}
	fake.FailPuts = 0

	// reenviar o arquivo local que ficou preservado
	if err := r.srv.Action(r.agent.ID, &pb.AgentAction{Kind: "reupload", RunUid: run.UID, JobId: jid, FileName: run.OutputName, Subdir: "e2e"}); err != nil {
		t.Fatal(err)
	}
	fixed := r.waitRun(run.UID, func(x *store.Run) bool { return x.CloudStatus == "ok" }, "reenvio concluído")
	if fixed.Status != "ok" || fixed.Error != "" || fixed.CloudVerifiedBy != "quickxor" {
		t.Fatalf("execução corrigida: %+v", fixed)
	}
	logs := r.logs(fixed)
	if !strings.Contains(logs, "reenviando "+run.OutputName) || !strings.Contains(logs, "verificado por quickxor") {
		t.Fatalf("log do reenvio:\n%s", logs)
	}
	ls, _ := r.st.RunLogs(ctx, fixed.ID)
	if last := ls[len(ls)-1]; last.Seq < 10000 {
		t.Fatalf("as linhas das ações ficam a partir de 10000: %d", last.Seq)
	}
	folder := "Coruja Backup/srv-e2e/reenvio"
	if len(fake.Names(folder)) != 1 {
		t.Fatalf("nuvem: %v", fake.Names(folder))
	}

	// download pela nuvem = o mesmo conteúdo do arquivo local
	body, size, err := r.mgr.Open(ctx, cid, folder, run.OutputName)
	if err != nil {
		t.Fatal(err)
	}
	remote, _ := io.ReadAll(body)
	body.Close()
	local, _ := os.ReadFile(filepath.Join(r.bkDir, "e2e", run.OutputName))
	if !bytes.Equal(remote, local) || size != int64(len(local)) || len(local) == 0 {
		t.Fatalf("o download deveria ser idêntico ao arquivo local (%d x %d)", len(remote), len(local))
	}

	// extrair o backup numa pasta de restauração do agente
	if err := r.srv.Action(r.agent.ID, &pb.AgentAction{Kind: "extract", RunUid: run.UID, JobId: jid, FileName: run.OutputName, Subdir: "e2e", Target: "teste-restauracao"}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(20 * time.Second)
	dest := filepath.Join(r.bkDir, "restores", "teste-restauracao", "srv", "app", "index.html")
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(dest); err == nil {
			if string(b) != "olá mundo" {
				t.Fatalf("conteúdo restaurado: %q", b)
			}
			dest = ""
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if dest != "" {
		t.Fatal("o arquivo não foi extraído")
	}
	deadline = time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) && !strings.Contains(r.logs(fixed), "extração concluída") {
		fixed, _ = r.st.RunByUID(ctx, run.UID)
		time.Sleep(200 * time.Millisecond)
	}
	if !strings.Contains(r.logs(fixed), "extração concluída") {
		t.Fatalf("log da extração:\n%s", r.logs(fixed))
	}
	// extrair de novo na mesma pasta é recusado (não sobrescreve)
	r.srv.Action(r.agent.ID, &pb.AgentAction{Kind: "extract", RunUid: run.UID, JobId: jid, FileName: run.OutputName, Subdir: "e2e", Target: "teste-restauracao"})
	deadline = time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) && !strings.Contains(r.logs(fixed), "já existe") {
		fixed, _ = r.st.RunByUID(ctx, run.UID)
		time.Sleep(200 * time.Millisecond)
	}
	if !strings.Contains(r.logs(fixed), "já existe") {
		t.Fatalf("deveria recusar sobrescrever:\n%s", r.logs(fixed))
	}
}

// Com o aplicativo padrão compilado, a conta não guarda credenciais e funciona de ponta a ponta; sem ele, o modo some.
func TestGoogleDriveComAplicativoPadrao(t *testing.T) {
	oldID, oldSec := cloud.DefaultGoogleClientID, cloud.DefaultGoogleClientSecret
	defer func() { cloud.DefaultGoogleClientID, cloud.DefaultGoogleClientSecret = oldID, oldSec }()
	cloud.DefaultGoogleClientID, cloud.DefaultGoogleClientSecret = "gid", "segredo-compilado"

	fake := cloudtest.NewDrive("gid")
	defer fake.Close()
	r := newRig(t, cloudmgr.Endpoints{GDevice: fake.URL + "/device/code", GToken: fake.URL + "/token", GAPI: fake.APIURL(), GUpload: fake.UploadURL()})
	cid := r.connectCloud(cloud.TypeGDrive, cloudmgr.AccountConfig{}) // sem ID nem segredo
	acc, err := r.st.GetCloud(context.Background(), cid)
	if err != nil {
		t.Fatal(err)
	}
	cfg, _ := r.mgr.OpenConfigForTest(acc)
	if !cfg.UseDefault || cfg.ClientID != "" || cfg.ClientSecret != "" {
		t.Fatalf("a conta não deve gravar credenciais: %+v", cfg)
	}
	jid := r.newJob("gd-padrao", cid, 2, 3)
	if run := r.runNow(jid); run.Status != "ok" || run.CloudStatus != "ok" || run.CloudVerifiedBy != "md5" {
		t.Fatalf("execução: %+v\n%s", run, r.logs(run))
	}

	// sem aplicativo padrão compilado: pedir conta sem credenciais é recusado com mensagem clara
	cloud.DefaultGoogleClientSecret = ""
	if _, _, err := r.mgr.Create(context.Background(), "sem-padrao", cloud.TypeGDrive, cloudmgr.AccountConfig{}); err == nil || !strings.Contains(err.Error(), "Client ID") {
		t.Fatalf("esperava erro pedindo o Client ID: %v", err)
	}
	// uma conta criada no modo padrão não consegue renovar sem o aplicativo compilado
	r.mgr.Invalidate(cid)
	if _, err := r.mgr.Provider(context.Background(), cid); err == nil {
		t.Fatal("sem o aplicativo padrão a conta deveria falhar de forma explícita")
	}
}

// Login por popup: o Server gera o endereço do provedor (PKCE, state ligado à origem), recebe o código entregue pela página
// de retorno e cria a conta já conectada, usando o aplicativo "da Web" compilado e sem gravar credenciais.
func TestGoogleDriveLoginPorPopup(t *testing.T) {
	oID, oSec, oBroker := cloud.DefaultGoogleWebClientID, cloud.DefaultGoogleWebClientSecret, cloud.OAuthBrokerURL
	defer func() {
		cloud.DefaultGoogleWebClientID, cloud.DefaultGoogleWebClientSecret, cloud.OAuthBrokerURL = oID, oSec, oBroker
	}()
	cloud.DefaultGoogleWebClientID, cloud.DefaultGoogleWebClientSecret, cloud.OAuthBrokerURL = "gid", "seg-web", "https://broker.exemplo.test"

	fake := cloudtest.NewDrive("gid")
	defer fake.Close()
	r := newRig(t, cloudmgr.Endpoints{GDevice: fake.URL + "/device/code", GToken: fake.URL + "/token", GAPI: fake.APIURL(), GUpload: fake.UploadURL()})
	ctx := context.Background()

	for _, bad := range []string{"javascript:alert(1)", "http://evil.test/caminho", "ftp://x", "http://a b", ""} {
		if _, _, err := r.mgr.BeginWeb(ctx, "gd-web", cloud.TypeGDrive, bad); err == nil {
			t.Fatalf("origem %q deveria ser recusada", bad)
		}
	}
	const origin = "http://187.110.161.52:8081"
	loginURL, state, err := r.mgr.BeginWeb(ctx, "gd-web", cloud.TypeGDrive, origin)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(loginURL)
	q := u.Query()
	if u.Host != "accounts.google.com" || q.Get("client_id") != "gid" || q.Get("redirect_uri") != "https://broker.exemplo.test/oauth/google/callback" ||
		q.Get("code_challenge_method") != "S256" || q.Get("access_type") != "offline" || q.Get("state") != state || strings.Contains(loginURL, "seg-web") {
		t.Fatalf("endereço de login: %s", loginURL)
	}
	if o, _ := base64.RawURLEncoding.DecodeString(state[strings.IndexByte(state, '.')+1:]); string(o) != origin {
		t.Fatalf("o state deve carregar a origem: %q", o)
	}

	// state adulterado (outra origem) e código errado não criam nada; o state é de uso único
	forged := state[:strings.IndexByte(state, '.')+1] + base64.RawURLEncoding.EncodeToString([]byte("http://evil.test"))
	if _, err := r.mgr.FinishWeb(ctx, forged, "codigo-ok"); err == nil {
		t.Fatal("origem diferente da registrada deveria falhar")
	}
	if _, err := r.mgr.FinishWeb(ctx, state, "codigo-ok"); err == nil {
		t.Fatal("o state já foi consumido pela tentativa anterior")
	}
	_, state, _ = r.mgr.BeginWeb(ctx, "gd-web", cloud.TypeGDrive, origin)
	if _, err := r.mgr.FinishWeb(ctx, state, "codigo-errado"); err == nil {
		t.Fatal("código recusado pelo provedor deveria falhar")
	}
	if accs, _ := r.st.ListClouds(ctx); len(accs) != 0 {
		t.Fatalf("nenhuma conta deveria existir ainda: %d", len(accs))
	}

	loginURL, state, _ = r.mgr.BeginWeb(ctx, "gd-web", cloud.TypeGDrive, origin)
	q = mustParse(t, loginURL).Query()
	id, err := r.mgr.FinishWeb(ctx, state, "codigo-ok")
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(fake.LastVerifier))
	if base64.RawURLEncoding.EncodeToString(sum[:]) != q.Get("code_challenge") || fake.LastSecret != "seg-web" || fake.LastRedirect != "https://broker.exemplo.test/oauth/google/callback" {
		t.Fatalf("troca do código: verificador=%q segredo=%q redirect=%q", fake.LastVerifier, fake.LastSecret, fake.LastRedirect)
	}
	acc, _ := r.st.GetCloud(ctx, id)
	cfg, _ := r.mgr.OpenConfigForTest(acc)
	if acc.Status != "connected" || !cfg.UseDefault || !cfg.Web || cfg.ClientID != "" || cfg.ClientSecret != "" {
		t.Fatalf("conta: status=%s cfg=%+v", acc.Status, cfg)
	}
	if _, err := r.mgr.FinishWeb(ctx, state, "codigo-ok"); err == nil {
		t.Fatal("reutilizar o state deveria falhar")
	}
	// nome repetido é recusado antes de abrir o popup
	if _, _, err := r.mgr.BeginWeb(ctx, "gd-web", cloud.TypeGDrive, origin); !errors.Is(err, store.ErrDuplicate) {
		t.Fatalf("nome repetido: %v", err)
	}
	// e a conta funciona de ponta a ponta
	jid := r.newJob("gd-web-job", id, 2, 3)
	if run := r.runNow(jid); run.Status != "ok" || run.CloudStatus != "ok" {
		t.Fatalf("execução: %+v\n%s", run, r.logs(run))
	}
}

func mustParse(t *testing.T, s string) *url.URL {
	t.Helper()
	u, err := url.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

// OneDrive pelo popup: cliente público (sem segredo), retorno em /oauth/microsoft/callback e conta funcionando.
func TestOneDriveLoginPorPopup(t *testing.T) {
	oID, oBroker := cloud.DefaultMicrosoftClientID, cloud.OAuthBrokerURL
	defer func() { cloud.DefaultMicrosoftClientID, cloud.OAuthBrokerURL = oID, oBroker }()
	cloud.DefaultMicrosoftClientID, cloud.OAuthBrokerURL = "cid", "https://broker.exemplo.test"

	fake := cloudtest.NewGraph("cid")
	defer fake.Close()
	r := newRig(t, cloudmgr.Endpoints{MSDevice: fake.URL + "/devicecode", MSToken: fake.URL + "/token", Graph: fake.GraphURL()})
	ctx := context.Background()
	loginURL, state, err := r.mgr.BeginWeb(ctx, "od-web", cloud.TypeOneDrive, "http://187.110.161.52:8081")
	if err != nil {
		t.Fatal(err)
	}
	u := mustParse(t, loginURL)
	q := u.Query()
	if u.Host != "login.microsoftonline.com" || !strings.HasSuffix(u.Path, "/common/oauth2/v2.0/authorize") || q.Get("client_id") != "cid" ||
		q.Get("redirect_uri") != "https://broker.exemplo.test/oauth/microsoft/callback" || !strings.Contains(q.Get("scope"), "offline_access") || q.Get("code_challenge_method") != "S256" {
		t.Fatalf("endereço de login: %s", loginURL)
	}
	id, err := r.mgr.FinishWeb(ctx, state, "codigo-ok")
	if err != nil {
		t.Fatal(err)
	}
	if fake.LastSecret != "" || fake.LastRedirect != "https://broker.exemplo.test/oauth/microsoft/callback" || fake.LastVerifier == "" {
		t.Fatalf("troca do código: segredo=%q redirect=%q verificador=%q", fake.LastSecret, fake.LastRedirect, fake.LastVerifier)
	}
	acc, _ := r.st.GetCloud(ctx, id)
	cfg, _ := r.mgr.OpenConfigForTest(acc)
	if acc.Status != "connected" || !cfg.UseDefault || !cfg.Web || cfg.ClientID != "" {
		t.Fatalf("conta: %s %+v", acc.Status, cfg)
	}
	jid := r.newJob("od-web-job", id, 2, 3)
	if run := r.runNow(jid); run.Status != "ok" || run.CloudStatus != "ok" {
		t.Fatalf("execução: %+v\n%s", run, r.logs(run))
	}
}

// Dropbox de ponta a ponta: popup (cliente público), envio em partes com token só de escrita, retomada, conferência por
// content_hash, retenção, download e detecção de arquivo corrompido na nuvem.
func TestDropboxDePontaAPonta(t *testing.T) {
	oKey, oBroker := cloud.DefaultDropboxAppKey, cloud.OAuthBrokerURL
	defer func() { cloud.DefaultDropboxAppKey, cloud.OAuthBrokerURL = oKey, oBroker }()
	cloud.DefaultDropboxAppKey, cloud.OAuthBrokerURL = "dbxkey", "https://broker.exemplo.test"

	fake := cloudtest.NewDropbox("dbxkey")
	defer fake.Close()
	r := newRig(t, cloudmgr.Endpoints{DBXToken: fake.URL + "/oauth2/token", DBXAPI: fake.URL + "/2", DBXContent: fake.URL + "/2"})
	ctx := context.Background()

	loginURL, state, err := r.mgr.BeginWeb(ctx, "dbx", cloud.TypeDropbox, "http://187.110.161.52:8081")
	if err != nil {
		t.Fatal(err)
	}
	u := mustParse(t, loginURL)
	q := u.Query()
	if u.Host != "www.dropbox.com" || q.Get("client_id") != "dbxkey" || q.Get("token_access_type") != "offline" ||
		q.Get("redirect_uri") != "https://broker.exemplo.test/oauth/dropbox/callback" || q.Get("code_challenge_method") != "S256" ||
		strings.Contains(q.Get("scope"), "account_info.write") {
		t.Fatalf("endereço de login: %s", loginURL)
	}
	id, err := r.mgr.FinishWeb(ctx, state, "codigo-ok")
	if err != nil {
		t.Fatal(err)
	}
	if fake.LastSecret != "" || fake.LastVerifier == "" {
		t.Fatalf("cliente público: segredo=%q verificador=%q", fake.LastSecret, fake.LastVerifier)
	}
	acc, _ := r.st.GetCloud(ctx, id)
	if acc.Status != "connected" || acc.AccountLabel != "dono@dropbox.test" {
		t.Fatalf("conta: %+v", acc)
	}

	jid := r.newJob("dbx-job", id, 2, 2)
	fake.LoseAppendReply = true // arquivo pequeno não tem append; o teste de retomada em partes está no uploader
	run := r.runNow(jid)
	if run.Status != "ok" || run.CloudStatus != "ok" || run.CloudVerifiedBy != "dropbox" {
		t.Fatalf("execução: %+v\n%s", run, r.logs(run))
	}
	if len(fake.ScopesIssued) == 0 || fake.ScopesIssued[0] != "files.content.write" {
		t.Fatalf("o token do agente deve ser só de escrita: %v", fake.ScopesIssued)
	}
	time.Sleep(1100 * time.Millisecond)
	r.runNow(jid)
	time.Sleep(1100 * time.Millisecond)
	if run = r.runNow(jid); run.Status != "ok" {
		t.Fatalf("terceira execução: %+v\n%s", run, r.logs(run))
	}
	folder := "Coruja Backup/srv-e2e/dbx-job"
	if n := fake.Names(folder); len(n) != 2 {
		t.Fatalf("retenção na nuvem deve manter 2: %v (%v)", n, r.logs(run))
	}
	rc, _, err := r.mgr.Open(ctx, id, folder, fake.Names(folder)[1])
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(rc)
	rc.Close()
	if !bytes.HasPrefix(got, []byte{0x1f, 0x8b}) {
		t.Fatal("o download deveria ser um gzip")
	}

	// arquivo corrompido na nuvem: a execução falha e as cópias boas são preservadas
	before := fake.Names(folder)
	fake.CorruptUpload = true
	time.Sleep(1100 * time.Millisecond)
	bad := r.runNow(jid)
	if bad.Status != "failed" || !strings.Contains(bad.Error, "content_hash") {
		t.Fatalf("corrompido: %+v\n%s", bad, r.logs(bad))
	}
	if after := fake.Names(folder); len(after) != len(before) {
		t.Fatalf("as cópias antigas deveriam ficar: antes %v depois %v", before, after)
	}
}

// Destino de rede de ponta a ponta (pasta local do agente): o Server manda a configuração ao agente, que envia, confere
// relendo, aplica a retenção e relata; depois o arquivo é trazido de volta por uma ação.
func TestDestinoDeRedeDePontaAPonta(t *testing.T) {
	r := newRig(t, cloudmgr.Endpoints{})
	ctx := context.Background()
	id, err := r.mgr.CreateDestination(ctx, "NAS local", netdest.Config{Type: netdest.TypeLocal, BasePath: "nas"})
	if err != nil {
		t.Fatal(err)
	}
	acc, _ := r.st.GetCloud(ctx, id)
	if acc.Status != "connected" || acc.Type != "local" || acc.AccountLabel != "pasta /nas" {
		t.Fatalf("destino: %+v", acc)
	}
	if _, err := r.mgr.Provider(ctx, id); !errors.Is(err, cloudmgr.ErrAgentDestination) {
		t.Fatalf("o Server não opera destinos de rede: %v", err)
	}
	// teste pelo agente (RPC)
	payload, _ := json.Marshal(netdest.Config{Type: netdest.TypeLocal, BasePath: "nas"})
	out, err := r.srv.Call(ctx, r.agent.ID, "dest_test", payload, 30*time.Second)
	if err != nil || !strings.Contains(string(out), "funcionando") {
		t.Fatalf("teste do destino: %v %s", err, out)
	}

	jid := r.newJob("rede", id, 3, 2)
	var run *store.Run
	for i := 0; i < 3; i++ {
		if i > 0 {
			time.Sleep(1100 * time.Millisecond)
		}
		x := r.runNow(jid)
		if x.Status != "ok" || x.CloudStatus != "ok" || x.CloudVerifiedBy != "sha256" {
			t.Fatalf("execução %d: %+v\n%s", i, x, r.logs(x))
		}
		run = x
	}
	dir := filepath.Join(r.destDir, "nas", filepath.FromSlash(path.Dir(run.CloudPath)))
	es, err := os.ReadDir(dir)
	if err != nil || len(es) != 2 {
		t.Fatalf("a retenção deveria manter 2 no destino: %v %v (%s)", es, err, run.CloudPath)
	}
	// traz de volta o arquivo depois que a cópia local sumiu
	local := filepath.Join(r.bkDir, "e2e", run.OutputName)
	if err := os.Remove(local); err != nil {
		t.Fatal(err)
	}
	if err := r.srv.Action(r.agent.ID, &pb.AgentAction{Kind: "fetch", RunUid: run.UID, JobId: jid, FileName: path.Base(run.CloudPath), Subdir: "e2e", Sha256: run.SHA256}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 100; i++ {
		if _, err := os.Stat(local); err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	b, err := os.ReadFile(local)
	if err != nil {
		t.Fatalf("o arquivo deveria ter voltado: %v", err)
	}
	if sum := sha256.Sum256(b); hex.EncodeToString(sum[:]) != run.SHA256 {
		t.Fatal("o arquivo trazido difere do backup")
	}
}

// Backup que só está na nuvem (sem cópia local): a restauração de um arquivo pede ao Server só o bloco dele (um trecho
// do arquivo na nuvem), conferido pelo SHA-256 do catálogo; a extração completa traz o arquivo inteiro. Conteúdo
// adulterado na nuvem é recusado, e o agente só recebe arquivos que constam como enviados.
func TestRestaurarDeBackupQueSoEstaNaNuvem(t *testing.T) {
	defer agentcore.SetBlockTargetForTest(agentcore.SetBlockTargetForTest(64 << 10))
	fake := cloudtest.NewGraph("cid")
	defer fake.Close()
	r := newRig(t, cloudmgr.Endpoints{MSDevice: fake.URL + "/devicecode", MSToken: fake.URL + "/token", Graph: fake.GraphURL()})
	big := make([]byte, 2<<20)
	rand.New(rand.NewSource(1)).Read(big)
	os.WriteFile(filepath.Join(r.hostDir, "srv", "app", "grande.bin"), big, 0o644)
	cid := r.connectCloud(cloud.TypeOneDrive, cloudmgr.AccountConfig{ClientID: "cid", HardDelete: true})
	jid := r.newJob("so-nuvem", cid, 0, 3) // sem cópia local
	run := r.runNow(jid)
	if run.Status != "ok" || run.CloudStatus != "ok" {
		t.Fatalf("execução: %+v\n%s", run, r.logs(run))
	}
	if _, err := os.Stat(filepath.Join(r.bkDir, "e2e", run.OutputName)); err == nil {
		t.Fatal("o arquivo não deveria ter ficado no agente")
	}
	ctx := context.Background()
	waitLog := func(want string) string {
		t.Helper()
		var l string
		for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
			x, _ := r.st.RunByUID(ctx, run.UID)
			if l = r.logs(x); strings.Contains(l, want) {
				return l
			}
		}
		t.Fatalf("esperava %q no log:\n%s", want, l)
		return ""
	}
	act := func(kind, target string, paths ...string) *pb.AgentAction {
		return &pb.AgentAction{Kind: kind, RunUid: run.UID, JobId: jid, FileName: run.OutputName, Subdir: "e2e", Sha256: run.SHA256,
			Chain: []string{run.OutputName}, ChainSha256: []string{run.SHA256}, Paths: paths, Target: target}
	}
	// um arquivo pequeno: só o bloco dele sai da nuvem
	fake.Served.Store(0)
	if err := r.srv.Action(r.agent.ID, act("restore_files", "um-arquivo", "/srv/app/index.html")); err != nil {
		t.Fatal(err)
	}
	l := waitLog("restauração concluída")
	if !strings.Contains(l, "(nuvem) só 1 de ") {
		t.Fatalf("deveria ler só um bloco da nuvem:\n%s", l)
	}
	if b, err := os.ReadFile(filepath.Join(r.bkDir, "restores", "um-arquivo", "srv", "app", "index.html")); err != nil || string(b) != "olá mundo" {
		t.Fatalf("restaurado: %q %v", b, err)
	}
	if got := fake.Served.Load(); got == 0 || got > 256<<10 || run.BytesOutput < 2<<20 {
		t.Fatalf("da nuvem saíram %d bytes de um backup de %d", got, run.BytesOutput)
	}
	if ents, _ := os.ReadDir(filepath.Join(r.bkDir, "e2e")); len(ents) != 0 {
		t.Fatalf("nada deveria ser gravado na pasta do job: %v", ents)
	}
	// extração completa: o arquivo inteiro vem da nuvem e é apagado no fim
	if err := r.srv.Action(r.agent.ID, act("extract", "completo")); err != nil {
		t.Fatal(err)
	}
	waitLog("extração concluída")
	if b, err := os.ReadFile(filepath.Join(r.bkDir, "restores", "completo", "srv", "app", "grande.bin")); err != nil || !bytes.Equal(b, big) {
		t.Fatalf("extração completa: %v", err)
	}
	if ents, _ := os.ReadDir(filepath.Join(r.bkDir, "e2e")); len(ents) != 0 {
		t.Fatalf("o que veio da nuvem deveria ser apagado no fim: %v", ents)
	}
	// conteúdo adulterado na nuvem: recusado, sem deixar pasta de restauração
	key := "Coruja Backup/srv-e2e/so-nuvem/" + run.OutputName
	orig := append([]byte(nil), fake.Files[key].Content...)
	bad := append([]byte(nil), orig...)
	for i := range bad {
		bad[i] ^= 0x5a
	}
	fake.Files[key].Content = bad
	if err := r.srv.Action(r.agent.ID, act("restore_files", "adulterado", "/srv/app/index.html")); err != nil {
		t.Fatal(err)
	}
	waitLog("não confere com o catálogo")
	if _, err := os.Stat(filepath.Join(r.bkDir, "restores", "adulterado")); err == nil {
		t.Fatal("pasta de restauração não deveria existir")
	}
	fake.Files[key].Content = orig
	// arquivo que não é de uma execução enviada à nuvem: o Server não entrega
	a := act("extract", "inexistente")
	a.FileName, a.Chain, a.Sha256, a.ChainSha256 = "so-nuvem-20200101-000000.tar.gz", nil, "", nil
	if err := r.srv.Action(r.agent.ID, a); err != nil {
		t.Fatal(err)
	}
	waitLog("não consta como enviado à nuvem")
}
