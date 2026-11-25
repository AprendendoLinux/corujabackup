package agentsrv

import (
	"context"
	"net"
	"strings"
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
	if m, err := st.Recv(); err != nil || m.GetAck() == nil {
		t.Fatalf("esperava Ack: %v %v", m, err)
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
