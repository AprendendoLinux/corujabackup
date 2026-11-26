package agentcore

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/metadata"

	"github.com/AprendendoLinux/corujabackup/internal/enroll"
	"github.com/AprendendoLinux/corujabackup/internal/pb"
	"github.com/AprendendoLinux/corujabackup/internal/tlsutil"
	"github.com/AprendendoLinux/corujabackup/internal/version"
)

// Credentials é o que o agente guarda depois de matriculado (arquivo agent.json, permissão 600).
type Credentials struct {
	Server      string `json:"server,omitempty"` // só de versões antigas (antes da 0.0.17 o agente discava o Server)
	Fingerprint string `json:"fingerprint"`
	AgentID     int64  `json:"agent_id"`
	Credential  string `json:"credential"`
}

// ListenConfig descreve como o agente espera o Server: ele escuta numa porta e o Server conecta nele.
type ListenConfig struct {
	Addr      string // ex.: ":23847"
	Token     string // token de matrícula (só na primeira vez)
	StatePath string // onde a credencial fica gravada
}

// oneShot entrega ao gRPC a conexão já aberta pelo Server (uma única vez: sem ela, a sessão termina).
func oneShot(c net.Conn) func(context.Context, string) (net.Conn, error) {
	var used atomic.Bool
	return func(context.Context, string) (net.Conn, error) {
		if used.Swap(true) {
			return nil, errors.New("conexão encerrada: aguardando o Server conectar de novo")
		}
		return c, nil
	}
}

// Listen recebe as conexões do Server. Cada uma só vira sessão se o TLS provar que do outro lado está o Server certo
// (impressão digital fixada, vinda do token de matrícula). A primeira conexão faz a matrícula; as seguintes usam a
// credencial gravada. Uma conexão nova substitui a anterior.
func Listen(ctx context.Context, log *slog.Logger, cfg ListenConfig, st *Credentials, core *Core) error {
	var fp string
	if st != nil {
		fp = st.Fingerprint
	} else {
		tk, err := enroll.Parse(cfg.Token)
		if err != nil {
			return err
		}
		fp = tk.Fingerprint
	}
	ln, err := net.Listen("tcp", cfg.Addr)
	if err != nil {
		return fmt.Errorf("não foi possível escutar em %s: %w", cfg.Addr, err)
	}
	go func() { <-ctx.Done(); _ = ln.Close() }()
	log.Info("aguardando o Server conectar", "porta", cfg.Addr)

	var mu sync.Mutex
	cur := st
	var stopCur context.CancelFunc
	revoked := false
	slots := make(chan struct{}, 4) // limita handshakes simultâneos (conexões de estranhos não esgotam o agente)
	lastWarn := time.Time{}
	for {
		raw, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			time.Sleep(200 * time.Millisecond)
			continue
		}
		select {
		case slots <- struct{}{}:
		default:
			_ = raw.Close()
			continue
		}
		go func() {
			defer func() { <-slots }()
			mu.Lock()
			pin, isRevoked := fp, revoked
			mu.Unlock()
			if isRevoked {
				_ = raw.Close()
				return
			}
			_ = raw.SetDeadline(time.Now().Add(15 * time.Second))
			tc := tls.Client(raw, tlsutil.PinnedConfig(pin))
			if err := tc.HandshakeContext(ctx); err != nil {
				_ = raw.Close()
				mu.Lock()
				if time.Since(lastWarn) > time.Minute { // conexões de estranhos não enchem o log
					lastWarn = time.Now()
					log.Warn("conexão recusada: não é o Server deste agente (ou o certificado do Server mudou)", "de", raw.RemoteAddr().String(), "erro", err.Error())
				}
				mu.Unlock()
				return
			}
			_ = raw.SetDeadline(time.Time{})
			cc, err := grpc.NewClient("passthrough:///coruja-server",
				grpc.WithTransportCredentials(insecure.NewCredentials()), // o TLS (com pinning) já foi feito acima, nesta mesma conexão
				grpc.WithContextDialer(oneShot(tc)),
				grpc.WithKeepaliveParams(keepalive.ClientParameters{Time: 30 * time.Second, Timeout: 10 * time.Second, PermitWithoutStream: true}))
			if err != nil {
				_ = tc.Close()
				return
			}
			defer cc.Close()
			mu.Lock()
			creds := cur
			mu.Unlock()
			if creds == nil {
				nc, err := enrollOver(ctx, cc, cfg.Token, pin)
				if err != nil {
					log.Error("matrícula recusada", "erro", err.Error())
					return
				}
				if err := SaveCredentials(cfg.StatePath, nc); err != nil {
					log.Error("não foi possível gravar a credencial (a pasta de dados é gravável?)", "erro", err.Error())
					return
				}
				log.Info("matrícula concluída", "agente", nc.AgentID)
				mu.Lock()
				if cur == nil {
					cur = nc
				}
				creds = cur
				mu.Unlock()
			}
			sctx, cancel := context.WithCancel(ctx)
			mu.Lock()
			if stopCur != nil {
				stopCur() // a conexão nova substitui a anterior
			}
			stopCur = cancel
			mu.Unlock()
			defer cancel()
			wasRevoked, err := session(sctx, ctx, log, creds, core, cc)
			if wasRevoked {
				mu.Lock()
				revoked = true
				mu.Unlock()
				log.Error("este agente foi revogado no Server; recusando novas conexões (remova-o ou matricule de novo com um token novo)")
				return
			}
			if err != nil && sctx.Err() == nil {
				log.Warn("canal encerrado; aguardando o Server conectar de novo", "erro", err.Error())
			}
		}()
	}
}

func enrollOver(ctx context.Context, cc *grpc.ClientConn, token, fp string) (*Credentials, error) {
	host, _ := os.Hostname()
	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	resp, err := pb.NewAgentServiceClient(cc).Enroll(cctx, &pb.EnrollRequest{
		Token: token, Hostname: host, Os: runtime.GOOS, Arch: runtime.GOARCH, AgentVersion: version.Version})
	if err != nil {
		return nil, err
	}
	return &Credentials{Fingerprint: fp, AgentID: resp.AgentId, Credential: resp.Credential}, nil
}

// session conduz um canal. ctx termina com o canal; base é o contexto do agente (ações e consultas em andamento não
// são interrompidas quando o Server reconecta).
func session(ctx, base context.Context, log *slog.Logger, st *Credentials, core *Core, conn *grpc.ClientConn) (revoked bool, err error) {
	sctx, cancel := context.WithCancel(metadata.AppendToOutgoingContext(ctx, "authorization", fmt.Sprintf("Bearer %d.%s", st.AgentID, st.Credential)))
	defer cancel()
	stream, err := pb.NewAgentServiceClient(conn).Connect(sctx)
	if err != nil {
		return false, err
	}
	host, _ := os.Hostname()
	if err := stream.Send(&pb.AgentMessage{Body: &pb.AgentMessage_Hello{Hello: &pb.Hello{
		AgentVersion: version.Version, Hostname: host, Os: runtime.GOOS, Arch: runtime.GOARCH, DockerAvailable: dockerAvailable(), ActiveRuns: core.ActiveRuns()}}}); err != nil {
		return false, err
	}
	var smu sync.Mutex // stream.Send não é seguro em paralelo (batimentos x relatórios de backup)
	safeSend := func(m *pb.AgentMessage) error {
		smu.Lock()
		defer smu.Unlock()
		return stream.Send(m)
	}
	core.SetSender(func(m *pb.AgentMessage) bool { return safeSend(m) == nil })
	defer core.SetSender(nil)
	in := make(chan *pb.ServerMessage)
	rerr := make(chan error, 1)
	go func() {
		for {
			m, err := stream.Recv()
			if err != nil {
				rerr <- err
				return
			}
			in <- m
		}
	}()
	interval := 15 * time.Second
	tick := time.NewTicker(interval)
	defer tick.Stop()
	sendHB := func() error {
		return safeSend(&pb.AgentMessage{Body: &pb.AgentMessage_Heartbeat{Heartbeat: &pb.Heartbeat{
			UnixMs: time.Now().UnixMilli(), UptimeSeconds: uptime(), Load1: load1()}}})
	}
	for {
		select {
		case m := <-in:
			switch {
			case m.GetWelcome() != nil:
				w := m.GetWelcome()
				log.Info("conectado ao Server", "versão_do_server", w.ServerVersion)
				if s := time.Duration(w.HeartbeatSeconds) * time.Second; s >= 5*time.Second && s != interval {
					interval = s
					tick.Reset(interval)
				}
				go core.Replay() // reenvia resultados que ficaram na fila local
			case m.GetSyncJobs() != nil:
				core.SyncJobs(m.GetSyncJobs().Jobs)
			case m.GetRunNow() != nil:
				core.Enqueue(m.GetRunNow().JobId, m.GetRunNow().RunUid, "manual", time.Time{})
			case m.GetCancelRun() != nil:
				core.Cancel(m.GetCancelRun().RunUid)
			case m.GetAction() != nil:
				core.HandleAction(base, m.GetAction())
			case m.GetRpc() != nil:
				rpc := m.GetRpc()
				go func() { _ = safeSend(core.HandleRPC(base, rpc)) }()
			case m.GetUploadSession() != nil:
				core.OnUploadSession(m.GetUploadSession())
			case m.GetUploadVerdict() != nil:
				core.OnUploadVerdict(m.GetUploadVerdict())
			case m.GetReportAck() != nil:
				core.Ack(m.GetReportAck().RunUid)
			case m.GetRevoked() != nil:
				log.Error("revogado pelo Server", "motivo", m.GetRevoked().Reason)
				return true, nil
			}
		case err := <-rerr:
			return false, err
		case <-tick.C:
			if err := sendHB(); err != nil {
				return false, err
			}
		case <-ctx.Done():
			_ = stream.CloseSend()
			return false, nil
		}
	}
}

func dockerAvailable() bool {
	_, err := os.Stat("/var/run/docker.sock")
	return err == nil
}

func uptime() int64 {
	b, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0
	}
	f := strings.Fields(string(b))
	if len(f) == 0 {
		return 0
	}
	v, _ := strconv.ParseFloat(f[0], 64)
	return int64(v)
}

func load1() float64 {
	f, err := os.Open("/proc/loadavg")
	if err != nil {
		return 0
	}
	defer f.Close()
	var v float64
	line, _ := bufio.NewReader(f).ReadString('\n')
	if fs := strings.Fields(line); len(fs) > 0 {
		v, _ = strconv.ParseFloat(fs[0], 64)
	}
	return v
}

func LoadCredentials(p string) (*Credentials, error) {
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	var s Credentials
	if err := json.Unmarshal(b, &s); err != nil || s.AgentID == 0 || s.Credential == "" || s.Fingerprint == "" {
		return nil, fmt.Errorf("arquivo de estado inválido")
	}
	return &s, nil
}

func SaveCredentials(p string, s *Credentials) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(s, "", "  ")
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}
