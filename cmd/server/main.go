// Coruja Backup · Server: orquestra agentes, agendamentos e nuvem; serve a interface web e o canal dos agentes.
package main

import (
	"context"
	"crypto/tls"
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

	"github.com/AprendendoLinux/corujabackup/internal/adminops"
	"github.com/AprendendoLinux/corujabackup/internal/agentsrv"
	"github.com/AprendendoLinux/corujabackup/internal/auth"
	"github.com/AprendendoLinux/corujabackup/internal/cloud"
	"github.com/AprendendoLinux/corujabackup/internal/cloudmgr"
	"github.com/AprendendoLinux/corujabackup/internal/config"
	"github.com/AprendendoLinux/corujabackup/internal/db"
	"github.com/AprendendoLinux/corujabackup/internal/httpapi"
	"github.com/AprendendoLinux/corujabackup/internal/localagent"
	"github.com/AprendendoLinux/corujabackup/internal/notify"
	"github.com/AprendendoLinux/corujabackup/internal/secrets"
	"github.com/AprendendoLinux/corujabackup/internal/store"
	"github.com/AprendendoLinux/corujabackup/internal/tlsutil"
	"github.com/AprendendoLinux/corujabackup/internal/version"
	"github.com/AprendendoLinux/corujabackup/internal/webui"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "theme" {
		os.Exit(themeCmd(os.Args[2:]))
	}
	if len(os.Args) > 1 && os.Args[1] == "admin" {
		os.Exit(adminCmd(os.Args[2:]))
	}
	showVersion := flag.Bool("version", false, "mostra a versão e sai")
	healthcheck := flag.Bool("healthcheck", false, "consulta /api/health local (usado no HEALTHCHECK do Docker)")
	flag.Parse()
	if *showVersion {
		fmt.Printf("%s Server %s\n", version.Name, version.Version)
		return
	}
	if v := os.Getenv("CORUJA_OAUTH_BROKER"); v != "" { // página de retorno do login por popup (padrão: a do projeto)
		cloud.OAuthBrokerURL = v
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
	if err := box.Verify(st.SealedSamples(ctx)); err != nil {
		log.Error("ATENÇÃO: "+err.Error()+". Restaure o secret.key do backup, ou reconecte as nuvens e recadastre senhas de canais/bancos", "arquivo", cfg.DataDir+"/secrets/secret.key")
	}
	clouds := cloudmgr.New(st, box)
	hub := agentsrv.New(st, version.Version)
	hub.Cloud = clouds
	hub.Box = box
	notifier := notify.New(st, box)
	hub.Notify = notifier
	dialer := agentsrv.NewDialer(st.DialTargets) // o Server conecta em cada agente cadastrado (host:porta)
	hub.Dialer = dialer
	api := &httpapi.Server{DB: d, Store: st, Pinger: d, Agents: hub, Clouds: clouds, Box: box, Notifier: notifier, Started: time.Now(), Web: webui.Handler(),
		Fingerprint: fp, TrustProxy: cfg.TrustProxy}
	// primeira subida: administrador padrão admin/admin, obrigado a trocar a senha no primeiro login
	if created, err := adminops.EnsureDefaultAdmin(ctx, st); err != nil {
		log.Error("não foi possível criar o administrador padrão", "erro", err.Error())
	} else if created {
		log.Warn("PRIMEIRA SUBIDA: entre com o usuário admin e a senha admin; o sistema pede uma senha nova antes de liberar o uso")
	}
	// agente local: o Server já nasce com um agente que faz backup da própria máquina
	la := localagent.Config{Address: cfg.LocalAgent, Name: cfg.LocalAgentName, TokenFile: cfg.LocalAgentTokenFile}
	if created, err := localagent.Ensure(ctx, st, fp, la); err != nil {
		log.Error("não foi possível preparar a matrícula do agente local", "erro", err.Error())
	} else if created {
		log.Info("matrícula do agente local preparada: ele se conecta sozinho", "agente", la.Name, "endereço", la.Address)
		go localagent.Watch(ctx, st, la, log)
	}
	if n, err := st.CountUsers(ctx); err == nil && n == 0 { // sem o padrão (falhou): configuração inicial pela interface
		code, _ := auth.RandomToken(9)
		api.SetupCode = code
		log.Warn("CONFIGURAÇÃO INICIAL PENDENTE: abra a interface e informe este código para criar o administrador", "código", code)
	}
	srv := &http.Server{Addr: cfg.Listen, Handler: api.Routes(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute, MaxHeaderBytes: 64 << 10}

	errc := make(chan error, 2)
	go dialer.Run(ctx)
	go func() {
		if err := hub.Serve(ctx, dialer, cert); err != nil && ctx.Err() == nil && !errors.Is(err, net.ErrClosed) {
			errc <- err
		}
	}()
	go func() {
		log.Info("interface web/API", "endereço", cfg.Listen, "https", cfg.TLSCert != "")
		var err error
		if cfg.TLSCert != "" {
			err = srv.ListenAndServeTLS(cfg.TLSCert, cfg.TLSKey)
		} else {
			err = srv.ListenAndServe()
		}
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
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
	log.Info("canal dos agentes (gRPC/TLS): o Server conecta nos agentes cadastrados")

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
	scheme := "http"
	c := http.Client{Timeout: 3 * time.Second}
	if os.Getenv("CORUJA_TLS_CERT") != "" {
		scheme = "https"
		c.Transport = &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}} //nolint:gosec // só consulta o próprio processo (127.0.0.1)
	}
	resp, err := c.Get(scheme + "://" + addr + "/api/health")
	if err != nil || resp.StatusCode != 200 {
		return 1
	}
	return 0
}
