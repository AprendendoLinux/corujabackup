// Package localagent matricula sozinho o agente que roda ao lado do Server (o serviço "agent" do compose do Server), para o
// Server já nascer com um agente que faz backup da própria máquina.
//
// Como funciona: enquanto não existe um agente com o nome configurado, o Server gera um token de matrícula de uso único
// (o mesmo da interface: leva a impressão digital do certificado do Server) e o grava num arquivo de uma pasta que só os
// dois contêineres montam. O agente lê o token, o Server disca no endereço interno dele e a matrícula segue o caminho de
// sempre. Assim que o agente aparece cadastrado, o arquivo é apagado. Se o agente for excluído na interface, a próxima
// subida do Server gera um token novo (para matricular de novo, apague também a pasta de dados do agente).
package localagent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/AprendendoLinux/corujabackup/internal/enroll"
	"github.com/AprendendoLinux/corujabackup/internal/store"
)

// Config vem do ambiente do Server.
type Config struct {
	Address   string // host:porta do agente na rede interna (ex.: corujabackup-agent:23847); vazio = desligado
	Name      string // nome do agente na interface
	TokenFile string // arquivo onde o token é entregue ao agente
}

var nameRE = regexp.MustCompile(`^[A-Za-z0-9._-]{2,100}$`)

// tokenTTL: o token vale enquanto o Server estiver no ar sem o agente matriculado (é renovado a cada subida).
const tokenTTL = 7 * 24 * time.Hour

// Store é o que o pacote usa do banco.
type Store interface {
	AgentNameExists(ctx context.Context, name string) (bool, error)
	ReplacePendingToken(ctx context.Context, tokenHash, name, address string, expires time.Time) error
}

var _ Store = (*store.Store)(nil)

// Ensure prepara a matrícula do agente local, se ainda não houver um agente com esse nome. Devolve true se gerou um
// token novo. Depois, Watch apaga o arquivo quando a matrícula termina.
func Ensure(ctx context.Context, st Store, fingerprint string, c Config) (bool, error) {
	if c.Address == "" {
		return false, nil
	}
	if !nameRE.MatchString(c.Name) {
		return false, fmt.Errorf("nome do agente local inválido: %q", c.Name)
	}
	exists, err := st.AgentNameExists(ctx, c.Name)
	if err != nil {
		return false, err
	}
	if exists {
		removeToken(c.TokenFile)
		return false, nil
	}
	tok, err := enroll.New(fingerprint)
	if err != nil {
		return false, err
	}
	parsed, _ := enroll.Parse(tok)
	if err := writeToken(c.TokenFile, tok); err != nil {
		return false, err
	}
	if err := st.ReplacePendingToken(ctx, parsed.Hash(), c.Name, c.Address, time.Now().Add(tokenTTL)); err != nil {
		removeToken(c.TokenFile)
		return false, err
	}
	return true, nil
}

// Watch apaga o arquivo do token assim que o agente local aparece matriculado (ou quando ctx termina).
func Watch(ctx context.Context, st Store, c Config, log *slog.Logger) {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if ok, err := st.AgentNameExists(ctx, c.Name); err == nil && ok {
				removeToken(c.TokenFile)
				log.Info("agente local matriculado: o Server já faz backup da própria máquina", "agente", c.Name)
				return
			}
		}
	}
}

// writeToken grava o token de forma atômica (o agente nunca lê um arquivo pela metade).
func writeToken(path, tok string) error {
	if path == "" {
		return errors.New("arquivo do token do agente local não definido")
	}
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".token-*")
	if err != nil {
		return fmt.Errorf("não foi possível gravar o token do agente local em %s (a pasta está montada e é do usuário do Server?): %w", dir, err)
	}
	tmp := f.Name()
	_, err = f.WriteString(tok + "\n")
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(tmp, 0o640)
	}
	if err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		_ = os.Remove(tmp)
	}
	return err
}

func removeToken(path string) {
	if path != "" {
		_ = os.Remove(path)
	}
}
