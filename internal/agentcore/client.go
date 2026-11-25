package agentcore

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/metadata"

	"github.com/AprendendoLinux/corujabackup/internal/enroll"
	"github.com/AprendendoLinux/corujabackup/internal/pb"
	"github.com/AprendendoLinux/corujabackup/internal/tlsutil"
	"github.com/AprendendoLinux/corujabackup/internal/version"
)

// Credentials é o que o agente guarda depois de matriculado (arquivo agent.json, permissão 600).
type Credentials struct {
	Server      string `json:"server"`
	Fingerprint string `json:"fingerprint"`
	AgentID     int64  `json:"agent_id"`
	Credential  string `json:"credential"`
}

func Dial(addr, fp string) (*grpc.ClientConn, error) {
	return grpc.NewClient(addr,
		grpc.WithTransportCredentials(credentials.NewTLS(tlsutil.PinnedConfig(fp))),
		grpc.WithKeepaliveParams(keepalive.ClientParameters{Time: 30 * time.Second, Timeout: 10 * time.Second, PermitWithoutStream: true}),
	)
}

func Enroll(ctx context.Context, log *slog.Logger, server, tokenStr string) (*Credentials, error) {
	tk, err := enroll.Parse(tokenStr)
	if err != nil {
		return nil, err
	}
	conn, err := Dial(server, tk.Fingerprint)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	host, _ := os.Hostname()
	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	resp, err := pb.NewAgentServiceClient(conn).Enroll(cctx, &pb.EnrollRequest{
		Token: tokenStr, Hostname: host, Os: runtime.GOOS, Arch: runtime.GOARCH, AgentVersion: version.Version})
	if err != nil {
		return nil, err
	}
	return &Credentials{Server: server, Fingerprint: tk.Fingerprint, AgentID: resp.AgentId, Credential: resp.Credential}, nil
}

// run mantém o canal aberto, reconectando com espera crescente (1s até 30s).
func Run(ctx context.Context, log *slog.Logger, st *Credentials, core *Core) {
	backoff := time.Second
	for ctx.Err() == nil {
		start := time.Now()
		revoked, err := session(ctx, log, st, core)
		if revoked {
			log.Error("este agente foi revogado no Server; ficando ocioso (remova-o ou matricule de novo com um token novo)")
			<-ctx.Done()
			return
		}
		if err != nil && ctx.Err() == nil {
			log.Warn("canal encerrado", "erro", err.Error(), "nova_tentativa_em", backoff.String())
		}
		if time.Since(start) > 2*time.Minute {
			backoff = time.Second
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

func session(ctx context.Context, log *slog.Logger, st *Credentials, core *Core) (revoked bool, err error) {
	conn, err := Dial(st.Server, st.Fingerprint)
	if err != nil {
		return false, err
	}
	defer conn.Close()
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
