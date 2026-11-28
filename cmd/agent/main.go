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
	"time"

	"github.com/AprendendoLinux/corujabackup/internal/agentcore"
	"github.com/AprendendoLinux/corujabackup/internal/version"
)

func main() {
	listen := flag.String("listen", envOr("CORUJA_LISTEN", ":23847"), "porta onde o agente espera a conexão do Server")
	token := flag.String("token", os.Getenv("CORUJA_TOKEN"), "token de matrícula (gerado na interface web; uso único)")
	tokenFile := flag.String("token-file", os.Getenv("CORUJA_TOKEN_FILE"), "arquivo de onde ler o token de matrícula (agente local do Server: ele grava o token ali)")
	dataDir := flag.String("data", envOr("CORUJA_DATA_DIR", "/var/lib/coruja-agent"), "pasta onde o agente guarda a própria credencial e a fila local")
	hostRoot := flag.String("host-root", envOr("CORUJA_HOST_ROOT", "/"), "raiz da máquina vista pelo agente (use /host se montar a máquina nesse caminho)")
	backupDir := flag.String("backup-dir", envOr("CORUJA_BACKUP_DIR", "/backups"), "pasta onde os arquivos de backup são gravados")
	hostWrite := flag.String("host-write", os.Getenv("CORUJA_HOST_WRITE"), "raiz gravável da máquina, só para a pasta de backup local escolhida na interface (padrão: /host-rw no contêiner)")
	showVersion := flag.Bool("version", false, "mostra a versão e sai")
	flag.Parse()
	if *showVersion {
		fmt.Printf("%s Agente %s\n", version.Name, version.Version)
		return
	}
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if os.Getenv("CORUJA_SERVER") != "" {
		log.Warn("CORUJA_SERVER é ignorada: desde a versão 0.0.17 é o Server que conecta no agente (pode remover da stack)")
	}

	stPath := filepath.Join(*dataDir, "agent.json")
	st, err := agentcore.LoadCredentials(stPath)
	if err != nil {
		st = nil
		if strings.TrimSpace(*token) == "" && *tokenFile != "" {
			*token = waitTokenFile(ctx, log, *tokenFile)
		}
		if strings.TrimSpace(*token) == "" {
			log.Error("agente ainda não matriculado: informe o token gerado na interface (CORUJA_TOKEN)")
			os.Exit(2)
		}
	}
	local, err := agentcore.NewState(*dataDir)
	if err != nil {
		log.Error("não foi possível preparar a pasta de dados do agente", "erro", err.Error())
		os.Exit(1)
	}
	if *hostWrite == "" { // fora de contêiner o agente enxerga o próprio "/"; no contêiner, a montagem separada
		if *hostRoot == "/" {
			*hostWrite = "/"
		} else {
			*hostWrite = "/host-rw"
		}
	}
	core := agentcore.New(agentcore.Config{HostRoot: *hostRoot, HostWriteRoot: *hostWrite, BackupRoots: splitList(os.Getenv("CORUJA_BACKUP_ROOTS")), BackupDir: *backupDir,
		AllowedPaths: splitList(os.Getenv("CORUJA_ALLOWED_PATHS")), AllowedContainers: splitList(os.Getenv("CORUJA_ALLOWED_CONTAINERS")), DestDir: envOr("CORUJA_DEST_DIR", "/destinos")}, local, log)
	if err := os.MkdirAll(*backupDir, 0o750); err != nil {
		log.Warn("a pasta de backups não está disponível (monte um volume gravável)", "pasta", *backupDir, "erro", err.Error())
	}
	id := int64(0)
	if st != nil {
		id = st.AgentID
	}
	log.Info("agente iniciado", "versão", version.Version, "agente", id, "origem_do_host", *hostRoot, "pasta_de_backups", *backupDir)
	go core.Run(ctx)
	if err := agentcore.Listen(ctx, log, agentcore.ListenConfig{Addr: *listen, Token: strings.TrimSpace(*token), StatePath: stPath}, st, core); err != nil {
		log.Error("o agente não pôde iniciar", "erro", err.Error())
		os.Exit(1)
	}
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

// waitTokenFile espera o Server gravar o token de matrícula (agente local) e o devolve. "" se o agente for encerrado antes.
func waitTokenFile(ctx context.Context, log *slog.Logger, path string) string {
	logged := false
	for {
		if b, err := os.ReadFile(path); err == nil && strings.TrimSpace(string(b)) != "" {
			log.Info("token de matrícula lido do arquivo entregue pelo Server", "arquivo", path)
			return strings.TrimSpace(string(b))
		}
		if !logged {
			log.Info("aguardando o Server entregar o token de matrícula", "arquivo", path)
			logged = true
		}
		select {
		case <-ctx.Done():
			return ""
		case <-time.After(3 * time.Second):
		}
	}
}
