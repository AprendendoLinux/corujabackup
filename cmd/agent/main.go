// Coruja Backup · Agente: leve, roda no servidor de destino e fala só com o Server (IP:porta + token).
// Fase 1: matrícula com o token (uma vez), canal permanente gRPC/TLS com batimentos e reconexão automática.
// Os jobs de backup chegam na Fase 2.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
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

type state struct {
	Server      string `json:"server"`
	Fingerprint string `json:"fingerprint"`
	AgentID     int64  `json:"agent_id"`
	Credential  string `json:"credential"`
}

func main() {
	server := flag.String("server", os.Getenv("CORUJA_SERVER"), "endereço do canal dos agentes no Server (IP:porta)")
	token := flag.String("token", os.Getenv("CORUJA_TOKEN"), "token de matrícula (gerado na interface web; uso único)")
	dataDir := flag.String("data", envOr("CORUJA_DATA_DIR", "/var/lib/coruja-agent"), "pasta onde o agente guarda a própria credencial")
	showVersion := flag.Bool("version", false, "mostra a versão e sai")
	flag.Parse()
	if *showVersion {
		fmt.Printf("%s Agente %s\n", version.Name, version.Version)
		return
	}
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	stPath := filepath.Join(*dataDir, "agent.json")
	st, err := loadState(stPath)
	if err != nil {
		if strings.TrimSpace(*server) == "" || strings.TrimSpace(*token) == "" {
			log.Error("agente ainda não matriculado: informe --server IP:porta e --token (ou CORUJA_SERVER e CORUJA_TOKEN)")
			os.Exit(2)
		}
		st, err = enrollAgent(ctx, log, *server, *token)
		if err != nil {
			log.Error("matrícula recusada", "erro", err.Error())
			os.Exit(1)
		}
		if err := saveState(stPath, st); err != nil {
			log.Error("não foi possível gravar a credencial (a pasta de dados é gravável?)", "pasta", *dataDir, "erro", err.Error())
			os.Exit(1)
		}
		log.Info("matrícula concluída", "agente", st.AgentID)
	} else if s := strings.TrimSpace(*server); s != "" && s != st.Server {
		st.Server = s // o Server mudou de endereço; a credencial e a impressão digital continuam valendo
		_ = saveState(stPath, st)
	}
	log.Info("agente iniciado", "versão", version.Version, "server", st.Server, "agente", st.AgentID)
	run(ctx, log, st)
	log.Info("agente encerrado")
}

func dial(addr, fp string) (*grpc.ClientConn, error) {
	return grpc.NewClient(addr,
		grpc.WithTransportCredentials(credentials.NewTLS(tlsutil.PinnedConfig(fp))),
		grpc.WithKeepaliveParams(keepalive.ClientParameters{Time: 30 * time.Second, Timeout: 10 * time.Second, PermitWithoutStream: true}),
	)
}

func enrollAgent(ctx context.Context, log *slog.Logger, server, tokenStr string) (*state, error) {
	tk, err := enroll.Parse(tokenStr)
	if err != nil {
		return nil, err
	}
	conn, err := dial(server, tk.Fingerprint)
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
	return &state{Server: server, Fingerprint: tk.Fingerprint, AgentID: resp.AgentId, Credential: resp.Credential}, nil
}

// run mantém o canal aberto, reconectando com espera crescente (1s até 60s).
func run(ctx context.Context, log *slog.Logger, st *state) {
	backoff := time.Second
	for ctx.Err() == nil {
		start := time.Now()
		revoked, err := session(ctx, log, st)
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
		if backoff < 60*time.Second {
			backoff *= 2
		}
	}
}

func session(ctx context.Context, log *slog.Logger, st *state) (revoked bool, err error) {
	conn, err := dial(st.Server, st.Fingerprint)
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
		AgentVersion: version.Version, Hostname: host, Os: runtime.GOOS, Arch: runtime.GOARCH, DockerAvailable: dockerAvailable()}}}); err != nil {
		return false, err
	}
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
		return stream.Send(&pb.AgentMessage{Body: &pb.AgentMessage_Heartbeat{Heartbeat: &pb.Heartbeat{
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

func loadState(p string) (*state, error) {
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	var s state
	if err := json.Unmarshal(b, &s); err != nil || s.AgentID == 0 || s.Credential == "" || s.Fingerprint == "" {
		return nil, fmt.Errorf("arquivo de estado inválido")
	}
	return &s, nil
}

func saveState(p string, s *state) error {
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

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
