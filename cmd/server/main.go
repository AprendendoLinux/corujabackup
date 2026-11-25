// Coruja Backup · Server: orquestra agentes, agendamentos e nuvem; serve a interface web.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/AprendendoLinux/corujabackup/internal/config"
	"github.com/AprendendoLinux/corujabackup/internal/db"
	"github.com/AprendendoLinux/corujabackup/internal/httpapi"
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

	api := &httpapi.Server{DB: d, Pinger: d, Started: time.Now(), Web: webui.Handler()}
	srv := &http.Server{Addr: cfg.Listen, Handler: api.Routes(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()
	log.Info("escutando", "endereço", cfg.Listen)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Error("servidor encerrou", "erro", err.Error())
		os.Exit(1)
	}
	log.Info("encerrado")
}

func probe(listen string) int {
	addr := listen
	if len(addr) > 0 && addr[0] == ':' {
		addr = "127.0.0.1" + addr
	}
	c := http.Client{Timeout: 3 * time.Second}
	resp, err := c.Get("http://" + addr + "/api/health")
	if err != nil || resp.StatusCode != 200 {
		return 1
	}
	return 0
}
