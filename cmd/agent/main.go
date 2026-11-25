// Coruja Backup · Agente: leve, roda no servidor de destino e fala só com o Server (IP:porta + token).
// Fase 0: apenas verifica a conectividade com o Server. A matrícula e os jobs chegam na Fase 1/2.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/AprendendoLinux/corujabackup/internal/version"
)

func main() {
	server := flag.String("server", os.Getenv("CORUJA_SERVER"), "endereço do Server (IP:porta)")
	token := flag.String("token", os.Getenv("CORUJA_TOKEN"), "token de matrícula (gerado na interface web)")
	showVersion := flag.Bool("version", false, "mostra a versão e sai")
	flag.Parse()
	if *showVersion {
		fmt.Printf("%s Agente %s\n", version.Name, version.Version)
		return
	}
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))
	if strings.TrimSpace(*server) == "" {
		log.Error("informe o Server: --server IP:porta (ou CORUJA_SERVER)")
		os.Exit(2)
	}
	_ = token // usado a partir da Fase 1 (matrícula)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	log.Info("agente iniciado", "versão", version.Version, "server", *server)

	cli := http.Client{Timeout: 5 * time.Second}
	tick := time.NewTicker(30 * time.Second)
	defer tick.Stop()
	for {
		resp, err := cli.Get("http://" + *server + "/api/health")
		switch {
		case err != nil:
			log.Warn("Server inacessível", "erro", err.Error())
		default:
			resp.Body.Close()
			log.Info("Server respondeu", "http", resp.StatusCode)
		}
		select {
		case <-ctx.Done():
			log.Info("agente encerrado")
			return
		case <-tick.C:
		}
	}
}
