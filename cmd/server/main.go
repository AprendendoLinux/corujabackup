// Coruja Backup · Server: orquestra agentes, agendamentos e nuvem; serve a interface web e o canal dos agentes.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/AprendendoLinux/corujabackup/internal/agentsrv"
	"github.com/AprendendoLinux/corujabackup/internal/cloudmgr"
	"github.com/AprendendoLinux/corujabackup/internal/config"
	"github.com/AprendendoLinux/corujabackup/internal/db"
	"github.com/AprendendoLinux/corujabackup/internal/httpapi"
	"github.com/AprendendoLinux/corujabackup/internal/notify"
	"github.com/AprendendoLinux/corujabackup/internal/secrets"
	"github.com/AprendendoLinux/corujabackup/internal/store"
	"github.com/AprendendoLinux/corujabackup/internal/tlsutil"
	"github.com/AprendendoLinux/corujabackup/internal/version"
	"github.com/AprendendoLinux/corujabackup/internal/webui"
)

func main() {
	showVersion := flag.Bool("version", false, "mostra a versão e sai")
	healthcheck := flag.Bool("healthcheck", false, "consulta /api/health local (usado no HEALTHCHECK do Docker)")
	flag.Parse()
	if *showVersion {
		fmt.Printf("%s Server %s\n", version.Name, version.Version)
		return
	}
	cfg, err := config.Load(config.Getenv)
	if *healthcheck {
		os.Exit(probe(cfg.Listen))
	}
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))
	slog.SetDefault(log)
	if err != nil {
		log.Error("configuração inválida", "erro", err.Error())
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	log.Info("iniciando", "versão", version.Version, "banco", cfg.Redacted())

	cert, err := tlsutil.LoadOrCreate(cfg.DataDir + "/tls")
	if err != nil {
		log.Error("não foi possível preparar o certificado TLS (o volume /data é gravável?)", "erro", err.Error())
		os.Exit(1)
	}
	fp := tlsutil.Fingerprint(cert)
	log.Info("certificado do canal dos agentes pronto", "impressão_digital", fp[:32])

	d, err := db.Open(ctx, cfg.DSN(), 90*time.Second)
	if err != nil {
		log.Error("sem banco de dados", "erro", err.Error())
		os.Exit(1)
	}
	defer d.Close()
	applied, err := db.Migrate(ctx, d)
	if err != nil {
		log.Error("falha nas migrações", "erro", err.Error())
		os.Exit(1)
	}
	log.Info("banco pronto", "migrações_aplicadas", applied)

	box, err := secrets.Open(cfg.DataDir + "/secrets")
	if err != nil {
		log.Error("não foi possível preparar a chave de criptografia (o volume /data é gravável?)", "erro", err.Error())
		os.Exit(1)
	}
	st := store.New(d)
	clouds := cloudmgr.New(st, box)
	hub := agentsrv.New(st, version.Version)
	hub.Cloud = clouds
	hub.Box = box
	notifier := notify.New(st, box)
	hub.Notify = notifier
	_, agentPort, _ := net.SplitHostPort(cfg.AgentListen)
	api := &httpapi.Server{DB: d, Store: st, Pinger: d, Agents: hub, Clouds: clouds, Box: box, Notifier: notifier, Started: time.Now(), Web: webui.Handler(),
		Fingerprint: fp, AgentPort: agentPort, TrustProxy: cfg.TrustProxy}
	srv := &http.Server{Addr: cfg.Listen, Handler: api.Routes(), ReadHeaderTimeout: 10 * time.Second}

	errc := make(chan error, 2)
	go func() { errc <- hub.Listen(ctx, cfg.AgentListen, cert) }()
	go func() {
		log.Info("interface web/API", "endereço", cfg.Listen)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errc <- err
		}
	}()
	go hub.WatchMissed(ctx)
	go notifier.Run(ctx)
	go func() { // limpeza periódica de sessões e histórico antigo
		purge := func() {
			_ = st.PurgeSessions(ctx)
			_ = st.PurgeRuns(ctx, time.Now().AddDate(0, 0, -400))
			_ = st.PurgeNotifications(ctx, time.Now().AddDate(0, 0, -90))
		}
		purge()
		t := time.NewTicker(time.Hour)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				purge()
			case <-ctx.Done():
				return
			}
		}
	}()
	log.Info("canal dos agentes (gRPC/TLS)", "endereço", cfg.AgentListen)

	select {
	case err := <-errc:
		log.Error("servidor encerrou", "erro", err.Error())
		os.Exit(1)
	case <-ctx.Done():
	}
	sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(sctx)
	log.Info("encerrado")
}

func probe(listen string) int {
	addr := listen
	if strings.HasPrefix(addr, ":") {
		addr = "127.0.0.1" + addr
	}
	c := http.Client{Timeout: 3 * time.Second}
	resp, err := c.Get("http://" + addr + "/api/health")
	if err != nil || resp.StatusCode != 200 {
		return 1
	}
	return 0
}
