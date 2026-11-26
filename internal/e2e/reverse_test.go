package e2e

import (
	"context"
	"io"
	"log/slog"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AprendendoLinux/corujabackup/internal/agentcore"
	"github.com/AprendendoLinux/corujabackup/internal/agentsrv"
	"github.com/AprendendoLinux/corujabackup/internal/enroll"
	"github.com/AprendendoLinux/corujabackup/internal/store"
	"github.com/AprendendoLinux/corujabackup/internal/testutil"
	"github.com/AprendendoLinux/corujabackup/internal/tlsutil"
)

func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().String()
}

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// startServer sobe um Server (só o canal dos agentes) que disca os endereços cadastrados no banco.
func startServer(t *testing.T, ctx context.Context, st *store.Store) (*agentsrv.Server, *agentsrv.Dialer, string) {
	t.Helper()
	cert, err := tlsutil.LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv := agentsrv.New(st, "teste")
	d := agentsrv.NewDialer(st.DialTargets)
	d.Every = 150 * time.Millisecond
	srv.Dialer = d
	go d.Run(ctx)
	go srv.Serve(ctx, d, cert)
	return srv, d, tlsutil.Fingerprint(cert)
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatal("tempo esgotado esperando: " + what)
}

func agentNamed(st *store.Store, name string) *store.Agent {
	ags, _ := st.ListAgents(context.Background())
	for i := range ags {
		if ags[i].Name == name {
			return &ags[i]
		}
	}
	return nil
}

func TestConexaoInvertidaImpostorEReinicio(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	st := store.New(testutil.DB(t))
	_, _, fp := startServer(t, ctx, st)

	// 1) Um "Server" impostor, com outro certificado, conhece o endereço do agente e o disca primeiro: o agente recusa,
	//    porque a impressão digital fixada no token é a do Server verdadeiro.
	agentAddr := freeAddr(t)
	impCtx, stopImp := context.WithCancel(ctx)
	impStore := store.New(testutil.DB(t))
	impSrv, _, impFP := startServer(t, impCtx, impStore)
	impTok, _ := enroll.New(impFP)
	ip, _ := enroll.Parse(impTok)
	if err := impStore.CreateEnrollTokenAt(ctx, ip.Hash(), "isca", agentAddr, 0, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	tok, _ := enroll.New(fp)
	pt, _ := enroll.Parse(tok)
	statePath := filepath.Join(t.TempDir(), "agent.json")
	local, _ := agentcore.NewState(t.TempDir())
	core := agentcore.New(agentcore.Config{HostRoot: t.TempDir(), BackupDir: t.TempDir()}, local, quiet())
	go core.Run(ctx)
	agentCtx, stopAgent := context.WithCancel(ctx)
	go agentcore.Listen(agentCtx, quiet(), agentcore.ListenConfig{Addr: agentAddr, Token: tok, StatePath: statePath}, nil, core)

	time.Sleep(1500 * time.Millisecond) // o impostor tenta várias vezes
	if len(impSrv.Online()) != 0 || agentNamed(impStore, "isca") != nil {
		t.Fatal("o agente aceitou um Server com outro certificado")
	}
	if _, err := agentcore.LoadCredentials(statePath); err == nil {
		t.Fatal("o agente não deveria ter credencial antes do Server verdadeiro conectar")
	}
	stopImp()

	// 2) O Server verdadeiro tem o token com o endereço: conecta, matricula e o agente fica online
	if err := st.CreateEnrollTokenAt(ctx, pt.Hash(), "srv-inv", agentAddr, 0, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "matrícula", func() bool { return agentNamed(st, "srv-inv") != nil })
	a := agentNamed(st, "srv-inv")
	cr, err := agentcore.LoadCredentials(statePath)
	if err != nil || cr.AgentID != a.ID || cr.Server != "" {
		t.Fatalf("credencial gravada: %v %+v", err, cr)
	}

	// 3) O agente reinicia (sem token, só com a credencial gravada): o Server reconecta sozinho
	stopAgent()
	time.Sleep(300 * time.Millisecond)
	agentCtx2, stopAgent2 := context.WithCancel(ctx)
	defer stopAgent2()
	go agentcore.Listen(agentCtx2, quiet(), agentcore.ListenConfig{Addr: agentAddr, StatePath: statePath}, cr, core)
	waitFor(t, "reconexão", func() bool {
		g, _ := st.AgentByID(ctx, a.ID)
		return g.LastSeenAt != nil && time.Since(*g.LastSeenAt) < 3*time.Second && g.Address == agentAddr
	})
}

func TestDiscadorInformaPortaFechadaETempoEsgotado(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	st := store.New(testutil.DB(t))
	_, d, fp := startServer(t, ctx, st)
	closed := freeAddr(t) // ninguém escutando
	tok, _ := enroll.New(fp)
	pt, _ := enroll.Parse(tok)
	if err := st.CreateEnrollTokenAt(ctx, pt.Hash(), "srv-fechado", closed, 0, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "estado de erro", func() bool { return d.Links()[closed].State == "error" })
	if l := d.Links()[closed]; !strings.Contains(l.Error, "recusada") || l.NextTry.IsZero() {
		t.Fatalf("mensagem: %+v", l)
	}
	// o token excluído some da lista de conexões
	_ = st.DeleteToken(ctx, 1)
	ts, _ := st.ListPendingTokens(ctx)
	for _, x := range ts {
		_ = st.DeleteToken(ctx, x.ID)
	}
	waitFor(t, "endereço removido", func() bool { _, ok := d.Links()[closed]; return !ok })
}
