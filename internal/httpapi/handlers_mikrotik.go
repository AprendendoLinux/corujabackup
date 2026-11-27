package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/AprendendoLinux/corujabackup/internal/mikrotik"
	"github.com/AprendendoLinux/corujabackup/internal/store"
)

// mtIn é o roteador informado no job. Senhas e chave vazias = manter as já salvas.
type mtIn struct {
	Host            string `json:"host"`
	Port            int    `json:"port"`
	User            string `json:"user"`
	Password        string `json:"password"`
	PrivateKey      string `json:"private_key"`
	HostKey         string `json:"host_key"`
	Export          bool   `json:"export"`
	ExportSensitive bool   `json:"export_sensitive"`
	Binary          bool   `json:"binary"`
	BackupPassword  string `json:"backup_password"`
}

func buildMikrotik(in *mtIn, old *mikrotik.Config) (mikrotik.Config, string) {
	c := mikrotik.Config{Host: in.Host, Port: in.Port, User: in.User, Password: in.Password, PrivateKey: strings.TrimSpace(in.PrivateKey), HostKey: strings.TrimSpace(in.HostKey),
		Export: in.Export, ExportSensitive: in.ExportSensitive, Binary: in.Binary, BackupPassword: in.BackupPassword}
	if old != nil && strings.EqualFold(strings.TrimSpace(old.Host), strings.TrimSpace(c.Host)) && old.User == strings.TrimSpace(c.User) {
		if c.Password == "" && c.PrivateKey == "" {
			c.Password, c.PrivateKey = old.Password, old.PrivateKey
		}
		if c.Binary && c.BackupPassword == "" {
			c.BackupPassword = old.BackupPassword
		}
	}
	if err := c.Normalize(); err != nil {
		return c, err.Error()
	}
	if c.HostKey != "" && !strings.HasPrefix(c.HostKey, "SHA256:") {
		return c, "impressão digital do roteador inválida"
	}
	return c, ""
}

func mikrotikView(c *mikrotik.Config) map[string]any {
	if c == nil {
		return nil
	}
	return map[string]any{"host": c.Host, "port": c.Port, "user": c.User, "password_set": c.Password != "", "key_set": c.PrivateKey != "",
		"host_key": c.HostKey, "export": c.Export, "export_sensitive": c.ExportSensitive, "binary": c.Binary, "backup_password_set": c.BackupPassword != ""}
}

// mikrotikTest pede ao agente que conecte no roteador e leia a identificação (devolve também a impressão digital
// vista, para o usuário confirmar no primeiro contato). Senhas vazias reaproveitam as do job informado.
func (s *Server) mikrotikTest(w http.ResponseWriter, r *http.Request, _ *ctxUser) {
	id, ok := pathID(r)
	var in struct {
		Mikrotik mtIn  `json:"mikrotik"`
		JobID    int64 `json:"job_id"`
	}
	if !ok || !decode(w, r, &in) {
		if !ok {
			writeErr(w, 400, "id inválido")
		}
		return
	}
	var old *mikrotik.Config
	if in.JobID > 0 && s.Box != nil {
		if j, err := s.Store.GetJob(r.Context(), in.JobID); err == nil && j.AgentID == id {
			old, _ = j.OpenMikrotik(s.Box)
		}
	}
	cfg, msg := buildMikrotik(&in.Mikrotik, old)
	if msg != "" {
		writeErr(w, 400, msg)
		return
	}
	if a, err := s.Store.AgentByID(r.Context(), id); err == nil && a.AgentVersion != "" && !versionAtLeast(a.AgentVersion, [3]int{0, 0, 22}) {
		writeErr(w, 422, "o agente "+a.Name+" (versão "+a.AgentVersion+") não faz backup de MikroTik: atualize-o para a versão 0.0.22 ou mais nova")
		return
	}
	payload, _ := json.Marshal(cfg)
	s.agentCall(w, r, id, "mikrotik_test", payload, 75*time.Second)
}

// applyMikrotik valida e cifra o roteador de um job de MikroTik.
func (s *Server) applyMikrotik(j *store.Job, in *mtIn, agentVersion, agentName string) string {
	var old *mikrotik.Config
	if j.DBSealed != "" && s.Box != nil {
		old, _ = j.OpenMikrotik(s.Box)
	}
	if in == nil && old == nil {
		return "informe o roteador MikroTik"
	}
	if agentVersion != "" && !versionAtLeast(agentVersion, [3]int{0, 0, 22}) {
		return "o agente " + agentName + " (versão " + agentVersion + ") não faz backup de MikroTik: atualize-o para a versão 0.0.22 ou mais nova"
	}
	if in != nil {
		if s.Box == nil {
			return "chave de criptografia indisponível"
		}
		cfg, msg := buildMikrotik(in, old)
		if msg != "" {
			return msg
		}
		if cfg.HostKey == "" {
			return "teste a conexão com o roteador e confirme a impressão digital dele antes de salvar"
		}
		sealed, err := store.SealMikrotik(s.Box, cfg)
		if err != nil {
			return "não foi possível proteger as credenciais"
		}
		j.DBSealed = sealed
	}
	j.Paths, j.Excludes, j.BackupMode = []string{}, []string{}, "full"
	return ""
}
