package agentsrv

import (
	"context"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/AprendendoLinux/corujabackup/internal/enroll"
	"github.com/AprendendoLinux/corujabackup/internal/pb"
	"github.com/AprendendoLinux/corujabackup/internal/store"
	"github.com/AprendendoLinux/corujabackup/internal/testutil"
	"github.com/AprendendoLinux/corujabackup/internal/tlsutil"
)

type rig struct {
	srv  *Server
	st   *store.Store
	addr string
	fp   string
}

func newRig(t *testing.T) *rig {
	t.Helper()
	d := testutil.DB(t)
	st := store.New(d)
	cert, err := tlsutil.LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s := New(st, "test")
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go s.Serve(ctx, lis, cert)
	return &rig{srv: s, st: st, addr: lis.Addr().String(), fp: tlsutil.Fingerprint(cert)}
}

func (r *rig) newToken(t *testing.T, name string) string {
	t.Helper()
	tok, _ := enroll.New(r.fp)
	p, _ := enroll.Parse(tok)
	if err := r.st.CreateEnrollToken(context.Background(), p.Hash(), name, 0, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	return tok
}

func dial(t *testing.T, addr, fp string) *grpc.ClientConn {
	t.Helper()
	c, err := grpc.NewClient(addr, grpc.WithTransportCredentials(credentials.NewTLS(tlsutil.PinnedConfig(fp[:32]))))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func TestMatriculaUmaVezSo(t *testing.T) {
	r := newRig(t)
	tok := r.newToken(t, "srv-a")
	cli := pb.NewAgentServiceClient(dial(t, r.addr, r.fp))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	resp, err := cli.Enroll(ctx, &pb.EnrollRequest{Token: tok, Hostname: "h", Os: "linux", Arch: "amd64", AgentVersion: "0.0.2"})
	if err != nil || resp.AgentId == 0 || len(resp.Credential) < 40 {
		t.Fatalf("matrícula falhou: %v %+v", err, resp)
	}
	if _, err := cli.Enroll(ctx, &pb.EnrollRequest{Token: tok}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("token reutilizado deveria ser negado, veio %v", err)
	}
	if _, err := cli.Enroll(ctx, &pb.EnrollRequest{Token: "lixo"}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("token malformado: %v", err)
	}
	a, _ := r.st.AgentByID(ctx, resp.AgentId)
	if a.Name != "srv-a" || a.Hostname != "h" || a.CredentialHash == resp.Credential || a.CredentialHash == "" {
		t.Fatalf("agente gravado incorretamente (a credencial não pode ficar em claro): %+v", a)
	}
}

func TestImpressaoDigitalErradaRecusaConexao(t *testing.T) {
	r := newRig(t)
	cli := pb.NewAgentServiceClient(dial(t, r.addr, strings.Repeat("0", 64)))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := cli.Enroll(ctx, &pb.EnrollRequest{Token: r.newToken(t, "x1")}); err == nil || status.Code(err) == codes.PermissionDenied {
		t.Fatalf("com fingerprint errada a conexão TLS deveria falhar, veio %v", err)
	}
}

func authCtx(parent context.Context, id int64, cred string) context.Context {
	return metadata.AppendToOutgoingContext(parent, "authorization", "Bearer "+itoa(id)+"."+cred)
}

func itoa(i int64) string {
	s := ""
	if i == 0 {
		return "0"
	}
	for i > 0 {
		s = string(rune('0'+i%10)) + s
		i /= 10
	}
	return s
}

func TestCanalBatimentoOnlineERevogacao(t *testing.T) {
	r := newRig(t)
	cli := pb.NewAgentServiceClient(dial(t, r.addr, r.fp))
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	en, err := cli.Enroll(ctx, &pb.EnrollRequest{Token: r.newToken(t, "srv-b"), Hostname: "h"})
	if err != nil {
		t.Fatal(err)
	}
	// credencial errada
	bad, _ := cli.Connect(authCtx(ctx, en.AgentId, "errada"))
	if _, err := bad.Recv(); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("credencial errada deveria ser Unauthenticated, veio %v", err)
	}
	// conexão válida
	st, err := cli.Connect(authCtx(ctx, en.AgentId, en.Credential))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Send(&pb.AgentMessage{Body: &pb.AgentMessage_Hello{Hello: &pb.Hello{AgentVersion: "0.0.1", Hostname: "novo-host", Os: "linux", Arch: "arm64", DockerAvailable: true}}}); err != nil {
		t.Fatal(err)
	}
	m, err := st.Recv()
	if err != nil || m.GetWelcome() == nil || m.GetWelcome().HeartbeatSeconds != HeartbeatSeconds {
		t.Fatalf("esperava Welcome: %v %v", m, err)
	}
	if _, ok := r.srv.Online()[en.AgentId]; !ok {
		t.Fatal("agente deveria constar como online")
	}
	st.Send(&pb.AgentMessage{Body: &pb.AgentMessage_Heartbeat{Heartbeat: &pb.Heartbeat{UnixMs: 1}}})
	for { // o Server também envia SyncJobs logo após o Welcome; o que importa é o Ack
		m, err := st.Recv()
		if err != nil {
			t.Fatalf("esperava Ack: %v", err)
		}
		if m.GetAck() != nil {
			break
		}
	}
	a, _ := r.st.AgentByID(ctx, en.AgentId)
	if a.Hostname != "novo-host" || a.Arch != "arm64" || !a.DockerAvailable || a.LastSeenAt == nil {
		t.Fatalf("o Hello deveria atualizar o cadastro: %+v", a)
	}
	// revogação: o agente é avisado e o canal cai
	r.st.RevokeAgent(ctx, en.AgentId)
	r.srv.Revoke(en.AgentId, "teste")
	m, err = st.Recv()
	if err != nil || m.GetRevoked() == nil {
		t.Fatalf("esperava Revoked: %v %v", m, err)
	}
	time.Sleep(900 * time.Millisecond)
	if _, ok := r.srv.Online()[en.AgentId]; ok {
		t.Fatal("agente revogado não deveria seguir online")
	}
	again, _ := cli.Connect(authCtx(ctx, en.AgentId, en.Credential))
	if _, err := again.Recv(); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("agente revogado não pode reconectar: %v", err)
	}
}

func TestPrimeiraMensagemPrecisaSerHello(t *testing.T) {
	r := newRig(t)
	cli := pb.NewAgentServiceClient(dial(t, r.addr, r.fp))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	en, _ := cli.Enroll(ctx, &pb.EnrollRequest{Token: r.newToken(t, "srv-c")})
	st, _ := cli.Connect(authCtx(ctx, en.AgentId, en.Credential))
	st.Send(&pb.AgentMessage{Body: &pb.AgentMessage_Heartbeat{Heartbeat: &pb.Heartbeat{}}})
	if _, err := st.Recv(); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("sem Hello deveria ser InvalidArgument: %v", err)
	}
}

// ---------------------------------------------------------------- Fase 2: agenda e relatórios pelo canal

func TestAgendaChegaAoAgenteERelatorioEhGravadoEConfirmado(t *testing.T) {
	r := newRig(t)
	cli := pb.NewAgentServiceClient(dial(t, r.addr, r.fp))
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	en, _ := cli.Enroll(ctx, &pb.EnrollRequest{Token: r.newToken(t, "srv-j")})
	jid, err := r.st.CreateJob(ctx, &store.Job{Name: "etc", AgentID: en.AgentId, Kind: "files", Enabled: true, Cron: "10 0 * * *", Timezone: "America/Sao_Paulo",
		Paths: []string{"/etc"}, Excludes: []string{"*.tmp"}, DestSubdir: "etc", Keep: 3})
	if err != nil {
		t.Fatal(err)
	}
	st, _ := cli.Connect(authCtx(ctx, en.AgentId, en.Credential))
	st.Send(&pb.AgentMessage{Body: &pb.AgentMessage_Hello{Hello: &pb.Hello{AgentVersion: "0.0.2"}}})
	var sync *pb.SyncJobs
	for sync == nil {
		m, err := st.Recv()
		if err != nil {
			t.Fatal(err)
		}
		sync = m.GetSyncJobs()
	}
	if len(sync.Jobs) != 1 || sync.Jobs[0].JobId != jid || sync.Jobs[0].Cron != "10 0 * * *" || sync.Jobs[0].Timezone != "America/Sao_Paulo" || sync.Jobs[0].Excludes[0] != "*.tmp" || sync.Jobs[0].Keep != 3 {
		t.Fatalf("agenda recebida incorreta: %+v", sync.Jobs)
	}
	// o Server empurra a ordem de execução manual e o cancelamento
	if err := r.srv.RunNow(en.AgentId, jid, "uid-manual"); err != nil {
		t.Fatal(err)
	}
	if m, _ := st.Recv(); m.GetRunNow() == nil || m.GetRunNow().RunUid != "uid-manual" {
		t.Fatalf("esperava RunNow: %v", m)
	}
	// relatório final do agente
	now := time.Now().UnixMilli()
	st.Send(&pb.AgentMessage{Body: &pb.AgentMessage_RunReport{RunReport: &pb.RunReport{RunUid: "uid-1", JobId: jid, State: pb.RunReport_FINISHED, Trigger: "schedule",
		ScheduledForMs: now - 60000, StartedMs: now - 50000, FinishedMs: now, Status: "ok", BytesOutput: 99, Files: 5, Sha256: "abc", OutputName: "etc.tar.gz",
		Logs: []*pb.LogLine{{Seq: 1, UnixMs: now, Level: "info", Text: "pronto"}}}}})
	var ack *pb.ReportAck
	for ack == nil {
		m, err := st.Recv()
		if err != nil {
			t.Fatal(err)
		}
		ack = m.GetReportAck()
	}
	if ack.RunUid != "uid-1" {
		t.Fatalf("ack para o uid errado: %v", ack)
	}
	run, err := r.st.RunByUID(ctx, "uid-1")
	if err != nil || run.Status != "ok" || run.OutputName != "etc.tar.gz" || run.BytesOutput != 99 || run.JobName != "etc" {
		t.Fatalf("execução gravada incorretamente: %+v %v", run, err)
	}
	if logs, _ := r.st.RunLogs(ctx, run.ID); len(logs) != 1 || logs[0].Text != "pronto" {
		t.Fatalf("logs: %v", logs)
	}
	// reenvio do mesmo relatório (fila local) é idempotente e também confirmado
	st.Send(&pb.AgentMessage{Body: &pb.AgentMessage_RunReport{RunReport: &pb.RunReport{RunUid: "uid-1", JobId: jid, State: pb.RunReport_FINISHED, Status: "ok", ScheduledForMs: now - 60000}}})
	for ack = nil; ack == nil; {
		m, _ := st.Recv()
		ack = m.GetReportAck()
	}
	if rs, _ := r.st.ListRuns(ctx, jid, "", 10); len(rs) != 1 {
		t.Fatalf("reenvio não pode duplicar: %d", len(rs))
	}
	// job inexistente: confirmado mesmo assim (reenviar não adiantaria)
	st.Send(&pb.AgentMessage{Body: &pb.AgentMessage_RunReport{RunReport: &pb.RunReport{RunUid: "uid-2", JobId: 987654, State: pb.RunReport_FINISHED, Status: "ok"}}})
	for ack = nil; ack == nil; {
		m, _ := st.Recv()
		ack = m.GetReportAck()
	}
	if ack.RunUid != "uid-2" {
		t.Fatalf("%v", ack)
	}
}

func TestRunNowComAgenteOfflineEPerdidos(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	if err := r.srv.RunNow(12345, 1, "u"); err != ErrOffline {
		t.Fatalf("esperava ErrOffline: %v", err)
	}
	cli := pb.NewAgentServiceClient(dial(t, r.addr, r.fp))
	en, _ := cli.Enroll(ctx, &pb.EnrollRequest{Token: r.newToken(t, "srv-m")})
	jid, _ := r.st.CreateJob(ctx, &store.Job{Name: "m", AgentID: en.AgentId, Kind: "files", Enabled: true, Cron: "10 0 * * *", Timezone: "UTC", Paths: []string{"/etc"}, DestSubdir: "m", Keep: 1})
	// empurra a edição do job para a "criação" no passado, para o horário 00:10 de ontem contar
	r.st.DB.Exec("UPDATE jobs SET updated_at = ? WHERE id = ?", time.Now().UTC().Add(-72*time.Hour), jid)
	now := time.Now().UTC()
	r.srv.CheckMissed(ctx, now)
	runs, _ := r.st.ListRuns(ctx, jid, "missed", 10)
	if len(runs) == 0 || runs[0].Error == "" {
		t.Fatalf("deveria registrar horários perdidos com o motivo: %+v", runs)
	}
	n := len(runs)
	r.srv.CheckMissed(ctx, now) // idempotente
	if again, _ := r.st.ListRuns(ctx, jid, "missed", 10); len(again) != n {
		t.Fatalf("não pode duplicar perdidos: %d -> %d", n, len(again))
	}
	// se o agente recupera o atraso, a linha "missed" é adotada (vira a execução real)
	last := runs[0]
	err := r.st.ApplyReport(ctx, store.Report{UID: "recuperado", JobID: jid, AgentID: en.AgentId, ScheduledFor: *last.ScheduledFor, Final: true, Status: "ok", FinishedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	got, err := r.st.RunByUID(ctx, "recuperado")
	if err != nil || got.ID != last.ID || got.Status != "ok" {
		t.Fatalf("a linha perdida deveria ser adotada: %+v %v", got, err)
	}
}

type recNotifier struct {
	mu  sync.Mutex
	ids []int64
}

func (r *recNotifier) OnRunFinished(_ context.Context, id int64) {
	r.mu.Lock()
	r.ids = append(r.ids, id)
	r.mu.Unlock()
}

func (r *recNotifier) got() []int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]int64(nil), r.ids...)
}

func TestNotificaUmaVezQuandoAExecucaoTerminaEQuandoPerde(t *testing.T) {
	r := newRig(t)
	n := &recNotifier{}
	r.srv.Notify = n
	cli := pb.NewAgentServiceClient(dial(t, r.addr, r.fp))
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	en, _ := cli.Enroll(ctx, &pb.EnrollRequest{Token: r.newToken(t, "srv-ntf")})
	jid, _ := r.st.CreateJob(ctx, &store.Job{Name: "n", AgentID: en.AgentId, Kind: "files", Enabled: true, Cron: "10 0 * * *", Timezone: "UTC", Paths: []string{"/etc"}, DestSubdir: "n", Keep: 1})
	st, _ := cli.Connect(authCtx(ctx, en.AgentId, en.Credential))
	st.Send(&pb.AgentMessage{Body: &pb.AgentMessage_Hello{Hello: &pb.Hello{AgentVersion: "x"}}})
	now := time.Now().UnixMilli()
	st.Send(&pb.AgentMessage{Body: &pb.AgentMessage_RunReport{RunReport: &pb.RunReport{RunUid: "r1", JobId: jid, State: pb.RunReport_STARTED, StartedMs: now}}})
	st.Send(&pb.AgentMessage{Body: &pb.AgentMessage_RunReport{RunReport: &pb.RunReport{RunUid: "r1", JobId: jid, State: pb.RunReport_PROGRESS, Files: 3}}})
	fin := &pb.RunReport{RunUid: "r1", JobId: jid, State: pb.RunReport_FINISHED, Status: "failed", Error: "x", StartedMs: now, FinishedMs: now}
	st.Send(&pb.AgentMessage{Body: &pb.AgentMessage_RunReport{RunReport: fin}})
	for { // espera o ack do final
		m, err := st.Recv()
		if err != nil {
			t.Fatal(err)
		}
		if m.GetReportAck() != nil {
			break
		}
	}
	st.Send(&pb.AgentMessage{Body: &pb.AgentMessage_RunReport{RunReport: fin}}) // reenvio da fila local
	for {
		m, _ := st.Recv()
		if m.GetReportAck() != nil {
			break
		}
	}
	time.Sleep(200 * time.Millisecond)
	if ids := n.got(); len(ids) != 1 {
		t.Fatalf("a notificação deve sair uma única vez, mesmo com reenvio: %v", ids)
	}
	// horário perdido também notifica (uma vez)
	r.st.DB.Exec("UPDATE jobs SET updated_at = ? WHERE id = ?", time.Now().UTC().Add(-72*time.Hour), jid)
	r.srv.CheckMissed(ctx, time.Now().UTC())
	after := len(n.got())
	if after < 2 {
		t.Fatalf("o perdido deveria notificar: %v", n.got())
	}
	r.srv.CheckMissed(ctx, time.Now().UTC())
	if len(n.got()) != after {
		t.Fatal("o mesmo perdido não notifica de novo")
	}
}
