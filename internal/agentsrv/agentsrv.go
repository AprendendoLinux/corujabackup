// Package agentsrv implementa o serviço gRPC que os agentes usam: matrícula e canal permanente.
package agentsrv

import (
	"context"
	"crypto/subtle"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	"github.com/AprendendoLinux/corujabackup/internal/auth"
	"github.com/AprendendoLinux/corujabackup/internal/enroll"
	"github.com/AprendendoLinux/corujabackup/internal/pb"
	"github.com/AprendendoLinux/corujabackup/internal/ratelimit"
	"github.com/AprendendoLinux/corujabackup/internal/store"
)

const (
	HeartbeatSeconds = 15
	idleTimeout      = 60 * time.Second // sem mensagem do agente por tanto tempo => canal encerrado
)

type conn struct {
	cancel context.CancelFunc
	since  time.Time
	send   chan *pb.ServerMessage
}

type Server struct {
	pb.UnimplementedAgentServiceServer
	Store         *store.Store
	ServerVersion string
	Limiter       *ratelimit.Limiter // freia tentativas de matrícula por IP

	mu     sync.Mutex
	online map[int64]*conn
}

var ErrOffline = errors.New("o agente está offline")

// SpecFromJob converte um job do banco na definição enviada ao agente.
func SpecFromJob(j store.Job) *pb.JobSpec {
	return &pb.JobSpec{JobId: j.ID, Name: j.Name, Kind: j.Kind, Enabled: j.Enabled, Cron: j.Cron, Timezone: j.Timezone,
		Paths: j.Paths, Excludes: j.Excludes, DestSubdir: j.DestSubdir, Keep: int32(j.Keep), Version: int32(j.Version)}
}

func (s *Server) enqueue(agentID int64, m *pb.ServerMessage) error {
	s.mu.Lock()
	c := s.online[agentID]
	s.mu.Unlock()
	if c == nil {
		return ErrOffline
	}
	select {
	case c.send <- m:
		return nil
	case <-time.After(3 * time.Second):
		return errors.New("fila do agente cheia")
	}
}

// PushJobs envia o conjunto completo de jobs do agente (se estiver online). Sem efeito se estiver offline: ele
// recebe tudo ao reconectar.
func (s *Server) PushJobs(ctx context.Context, agentID int64) {
	jobs, err := s.Store.JobsForAgent(ctx, agentID)
	if err != nil {
		return
	}
	specs := make([]*pb.JobSpec, 0, len(jobs))
	for _, j := range jobs {
		specs = append(specs, SpecFromJob(j))
	}
	_ = s.enqueue(agentID, &pb.ServerMessage{Body: &pb.ServerMessage_SyncJobs{SyncJobs: &pb.SyncJobs{Jobs: specs}}})
}

// RunNow pede ao agente que execute o job agora (ErrOffline se ele não estiver conectado).
func (s *Server) RunNow(agentID, jobID int64, runUID string) error {
	return s.enqueue(agentID, &pb.ServerMessage{Body: &pb.ServerMessage_RunNow{RunNow: &pb.RunNow{JobId: jobID, RunUid: runUID}}})
}

func (s *Server) CancelRun(agentID int64, runUID string) error {
	return s.enqueue(agentID, &pb.ServerMessage{Body: &pb.ServerMessage_CancelRun{CancelRun: &pb.CancelRun{RunUid: runUID}}})
}

func reportFrom(agentID int64, r *pb.RunReport) store.Report {
	ms := func(v int64) time.Time {
		if v <= 0 {
			return time.Time{}
		}
		return time.UnixMilli(v).UTC()
	}
	out := store.Report{UID: r.RunUid, JobID: r.JobId, AgentID: agentID, Trigger: r.Trigger, ScheduledFor: ms(r.ScheduledForMs),
		Final: r.State == pb.RunReport_FINISHED, Started: r.State == pb.RunReport_STARTED || r.StartedMs > 0,
		Status: r.Status, StartedAt: ms(r.StartedMs), FinishedAt: ms(r.FinishedMs), BytesSource: r.BytesSource, BytesOutput: r.BytesOutput,
		Files: r.Files, SHA256: clip(r.Sha256, 64), OutputName: clip(r.OutputName, 255), Error: clip(r.Error, 4000)}
	switch out.Status {
	case "ok", "warning", "failed", "canceled":
	default:
		if out.Final {
			out.Status = "failed"
		}
	}
	for i, l := range r.Logs {
		if i >= 3000 {
			break
		}
		lv := l.Level
		if lv != "warn" && lv != "error" {
			lv = "info"
		}
		out.Logs = append(out.Logs, store.RunLog{Seq: int(l.Seq), At: ms(l.UnixMs), Level: lv, Text: l.Text})
	}
	return out
}

func New(st *store.Store, version string) *Server {
	return &Server{Store: st, ServerVersion: version, Limiter: ratelimit.New(10, 10*time.Minute), online: map[int64]*conn{}}
}

// Online diz quais agentes têm o canal aberto agora.
func (s *Server) Online() map[int64]time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[int64]time.Time, len(s.online))
	for id, c := range s.online {
		out[id] = c.since
	}
	return out
}

// Revoke avisa o agente e derruba o canal dele.
func (s *Server) Revoke(id int64, reason string) {
	s.mu.Lock()
	c := s.online[id]
	s.mu.Unlock()
	if c == nil {
		return
	}
	select {
	case c.send <- &pb.ServerMessage{Body: &pb.ServerMessage_Revoked{Revoked: &pb.Revoked{Reason: reason}}}:
	default:
	}
	time.AfterFunc(500*time.Millisecond, c.cancel)
}

// Listen sobe o gRPC com TLS 1.3 em addr e bloqueia até ctx terminar.
func (s *Server) Listen(ctx context.Context, addr string, cert tls.Certificate) error {
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	return s.Serve(ctx, lis, cert)
}

func (s *Server) Serve(ctx context.Context, lis net.Listener, cert tls.Certificate) error {
	gs := grpc.NewServer(
		grpc.Creds(credentials.NewTLS(&tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS13})),
		grpc.MaxRecvMsgSize(1<<20),
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{MinTime: 10 * time.Second, PermitWithoutStream: true}),
	)
	pb.RegisterAgentServiceServer(gs, s)
	go func() {
		<-ctx.Done()
		done := make(chan struct{})
		go func() { gs.GracefulStop(); close(done) }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			gs.Stop()
		}
	}()
	return gs.Serve(lis)
}

func peerIP(ctx context.Context) string {
	if p, ok := peer.FromContext(ctx); ok {
		if h, _, err := net.SplitHostPort(p.Addr.String()); err == nil {
			return h
		}
	}
	return ""
}

func clip(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// Enroll troca o token de matrícula (uso único) por uma credencial do agente.
func (s *Server) Enroll(ctx context.Context, req *pb.EnrollRequest) (*pb.EnrollResponse, error) {
	ip := peerIP(ctx)
	if !s.Limiter.Allow(ip) {
		return nil, status.Error(codes.ResourceExhausted, "muitas tentativas; aguarde alguns minutos")
	}
	tk, err := enroll.Parse(req.Token)
	if err != nil {
		s.Limiter.Fail(ip)
		return nil, status.Error(codes.InvalidArgument, "token de matrícula inválido")
	}
	secret, err := auth.RandomToken(32)
	if err != nil {
		return nil, status.Error(codes.Internal, "falha ao gerar a credencial")
	}
	a, err := s.Store.Enroll(ctx, tk.Hash(), clip(req.Hostname, 255), clip(req.Os, 40), clip(req.Arch, 20), clip(req.AgentVersion, 40), ip, auth.SHA256Hex(secret))
	switch {
	case errors.Is(err, store.ErrNotFound):
		s.Limiter.Fail(ip)
		s.Store.Audit(ctx, 0, "", "agent.enroll_denied", "token inválido, expirado ou já usado", ip)
		return nil, status.Error(codes.PermissionDenied, "token inválido, expirado ou já utilizado")
	case errors.Is(err, store.ErrDuplicate):
		return nil, status.Error(codes.AlreadyExists, "já existe um agente com esse nome")
	case err != nil:
		slog.Error("matrícula falhou", "erro", err.Error())
		return nil, status.Error(codes.Internal, "erro interno")
	}
	s.Limiter.Reset(ip)
	s.Store.Audit(ctx, 0, "", "agent.enrolled", fmt.Sprintf("%s (%s)", a.Name, a.Hostname), ip)
	slog.Info("agente matriculado", "id", a.ID, "nome", a.Name)
	return &pb.EnrollResponse{AgentId: a.ID, Credential: secret, ServerVersion: s.ServerVersion}, nil
}

func (s *Server) authenticate(ctx context.Context) (*store.Agent, error) {
	md, _ := metadata.FromIncomingContext(ctx)
	v := md.Get("authorization")
	if len(v) == 0 || !strings.HasPrefix(v[0], "Bearer ") {
		return nil, status.Error(codes.Unauthenticated, "credencial ausente")
	}
	idStr, secret, ok := strings.Cut(strings.TrimPrefix(v[0], "Bearer "), ".")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if !ok || err != nil || secret == "" {
		return nil, status.Error(codes.Unauthenticated, "credencial inválida")
	}
	ip := peerIP(ctx)
	if !s.Limiter.Allow("auth:" + ip) {
		return nil, status.Error(codes.ResourceExhausted, "muitas tentativas")
	}
	a, err := s.Store.AgentByID(ctx, id)
	want := ""
	if err == nil {
		want = a.CredentialHash
	}
	// comparação em tempo constante mesmo quando o agente não existe
	okCred := subtle.ConstantTimeCompare([]byte(auth.SHA256Hex(secret)), []byte(want)) == 1
	if err != nil || !okCred {
		s.Limiter.Fail("auth:" + ip)
		return nil, status.Error(codes.Unauthenticated, "credencial inválida")
	}
	if a.RevokedAt != nil {
		return nil, status.Error(codes.PermissionDenied, "este agente foi revogado")
	}
	return a, nil
}

// Connect é o canal permanente: o agente se identifica (Hello) e envia batimentos; o Server responde.
func (s *Server) Connect(stream pb.AgentService_ConnectServer) error {
	ctx, cancel := context.WithCancel(stream.Context())
	defer cancel()
	a, err := s.authenticate(ctx)
	if err != nil {
		return err
	}
	ip := peerIP(ctx)

	first, err := recvWithTimeout(ctx, stream, 15*time.Second)
	if err != nil {
		return status.Error(codes.InvalidArgument, "esperava a mensagem Hello")
	}
	h := first.GetHello()
	if h == nil {
		return status.Error(codes.InvalidArgument, "a primeira mensagem deve ser Hello")
	}
	_ = s.Store.TouchAgent(ctx, a.ID, clip(h.Hostname, 255), clip(h.Os, 40), clip(h.Arch, 20), clip(h.AgentVersion, 40), h.DockerAvailable, ip)

	c := &conn{cancel: cancel, since: time.Now(), send: make(chan *pb.ServerMessage, 8)}
	s.mu.Lock()
	if old := s.online[a.ID]; old != nil {
		old.cancel() // uma conexão por agente: a nova substitui a antiga
	}
	s.online[a.ID] = c
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		if s.online[a.ID] == c {
			delete(s.online, a.ID)
		}
		s.mu.Unlock()
	}()
	slog.Info("agente conectado", "id", a.ID, "nome", a.Name, "ip", ip)

	if err := stream.Send(&pb.ServerMessage{Body: &pb.ServerMessage_Welcome{Welcome: &pb.Welcome{ServerVersion: s.ServerVersion, HeartbeatSeconds: HeartbeatSeconds}}}); err != nil {
		return err
	}
	s.PushJobs(ctx, a.ID)
	// escritor: tudo o que o Server envia passa por aqui (stream.Send não é seguro em paralelo)
	errc := make(chan error, 1)
	go func() {
		for {
			select {
			case m := <-c.send:
				if err := stream.Send(m); err != nil {
					errc <- err
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}()
	recv := make(chan *pb.AgentMessage)
	rerr := make(chan error, 1)
	go func() {
		for {
			m, err := stream.Recv()
			if err != nil {
				rerr <- err
				return
			}
			select {
			case recv <- m:
			case <-ctx.Done():
				return
			}
		}
	}()
	idle := time.NewTimer(idleTimeout)
	defer idle.Stop()
	for {
		select {
		case m := <-recv:
			if !idle.Stop() {
				select {
				case <-idle.C:
				default:
				}
			}
			idle.Reset(idleTimeout)
			if rr := m.GetRunReport(); rr != nil {
				err := s.Store.ApplyReport(ctx, reportFrom(a.ID, rr))
				if err != nil {
					slog.Warn("relatório de execução não gravado", "agente", a.ID, "execução", rr.RunUid, "erro", err.Error())
				}
				// confirma quando gravou ou quando o job não existe mais (reenviar não adiantaria); erro de banco => sem ack, o agente reenvia
				if rr.State == pb.RunReport_FINISHED && (err == nil || errors.Is(err, store.ErrNotFound)) {
					select {
					case c.send <- &pb.ServerMessage{Body: &pb.ServerMessage_ReportAck{ReportAck: &pb.ReportAck{RunUid: rr.RunUid}}}:
					default:
					}
				}
			}
			if hb := m.GetHeartbeat(); hb != nil {
				_ = s.Store.Seen(ctx, a.ID)
				select {
				case c.send <- &pb.ServerMessage{Body: &pb.ServerMessage_Ack{Ack: &pb.Ack{UnixMs: time.Now().UnixMilli()}}}:
				default:
				}
			}
		case err := <-rerr:
			slog.Info("agente desconectado", "id", a.ID, "nome", a.Name)
			if status.Code(err) == codes.Canceled {
				return nil
			}
			return nil
		case err := <-errc:
			return err
		case <-idle.C:
			slog.Warn("agente sem sinal; encerrando o canal", "id", a.ID)
			return status.Error(codes.DeadlineExceeded, "sem batimentos")
		case <-ctx.Done():
			return nil
		}
	}
}

func recvWithTimeout(ctx context.Context, st pb.AgentService_ConnectServer, d time.Duration) (*pb.AgentMessage, error) {
	type r struct {
		m   *pb.AgentMessage
		err error
	}
	ch := make(chan r, 1)
	go func() { m, err := st.Recv(); ch <- r{m, err} }()
	select {
	case v := <-ch:
		return v.m, v.err
	case <-time.After(d):
		return nil, errors.New("tempo esgotado")
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
