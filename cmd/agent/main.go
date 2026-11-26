// Coruja Backup · Agente: leve, roda no servidor de destino e fala só com o Server (IP:porta + token).
// Matrícula com o token (uma vez), canal permanente gRPC/TLS com batimentos e reconexão automática e, desde a Fase 2,
// execução dos jobs de arquivos (tar.gz) com agenda própria e fila local de resultados.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/AprendendoLinux/corujabackup/internal/agentcore"
	"github.com/AprendendoLinux/corujabackup/internal/version"
)

func main() {
	server := flag.String("server", os.Getenv("CORUJA_SERVER"), "endereço do canal dos agentes no Server (IP:porta)")
	token := flag.String("token", os.Getenv("CORUJA_TOKEN"), "token de matrícula (gerado na interface web; uso único)")
	dataDir := flag.String("data", envOr("CORUJA_DATA_DIR", "/var/lib/coruja-agent"), "pasta onde o agente guarda a própria credencial e a fila local")
	hostRoot := flag.String("host-root", envOr("CORUJA_HOST_ROOT", "/"), "raiz do servidor vista pelo agente (use /host se montar o host nesse caminho)")
	backupDir := flag.String("backup-dir", envOr("CORUJA_BACKUP_DIR", "/backups"), "pasta onde os arquivos de backup são gravados")
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
	st, err := agentcore.LoadCredentials(stPath)
	if err != nil {
		if strings.TrimSpace(*server) == "" || strings.TrimSpace(*token) == "" {
			log.Error("agente ainda não matriculado: informe --server IP:porta e --token (ou CORUJA_SERVER e CORUJA_TOKEN)")
			os.Exit(2)
		}
		st, err = agentcore.Enroll(ctx, log, *server, *token)
		if err != nil {
			log.Error("matrícula recusada", "erro", err.Error())
			os.Exit(1)
		}
		if err := agentcore.SaveCredentials(stPath, st); err != nil {
			log.Error("não foi possível gravar a credencial (a pasta de dados é gravável?)", "pasta", *dataDir, "erro", err.Error())
			os.Exit(1)
		}
		log.Info("matrícula concluída", "agente", st.AgentID)
	} else if s := strings.TrimSpace(*server); s != "" && s != st.Server {
		st.Server = s // o Server mudou de endereço; a credencial e a impressão digital continuam valendo
		_ = agentcore.SaveCredentials(stPath, st)
	}
	log.Info("agente iniciado", "versão", version.Version, "server", st.Server, "agente", st.AgentID)
	local, err := agentcore.NewState(*dataDir)
	if err != nil {
		log.Error("não foi possível preparar a pasta de dados do agente", "erro", err.Error())
		os.Exit(1)
	}
	core := agentcore.New(agentcore.Config{HostRoot: *hostRoot, BackupDir: *backupDir, AllowedPaths: splitList(os.Getenv("CORUJA_ALLOWED_PATHS")), AllowedContainers: splitList(os.Getenv("CORUJA_ALLOWED_CONTAINERS"))}, local, log)
	if err := os.MkdirAll(*backupDir, 0o750); err != nil {
		log.Warn("a pasta de backups não está disponível (monte um volume gravável)", "pasta", *backupDir, "erro", err.Error())
	}
	log.Info("áreas de trabalho", "origem_do_host", *hostRoot, "pasta_de_backups", *backupDir)
	go core.Run(ctx)
	agentcore.Run(ctx, log, st, core)
	log.Info("agente encerrado")
}

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func splitList(v string) []string {
	var out []string
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
