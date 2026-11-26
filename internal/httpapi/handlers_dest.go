package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/AprendendoLinux/corujabackup/internal/netdest"
	"github.com/AprendendoLinux/corujabackup/internal/pb"
	"github.com/AprendendoLinux/corujabackup/internal/store"
)

// testDestination pede a um agente que teste o destino (é ele quem alcança a rede). Para um destino já salvo, informe id:
// a configuração guardada é usada e as credenciais não voltam para o navegador.
func (s *Server) testDestination(w http.ResponseWriter, r *http.Request, _ *ctxUser) {
	var in struct {
		AgentID int64          `json:"agent_id"`
		ID      int64          `json:"id"`
		Config  netdest.Config `json:"config"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.AgentID <= 0 {
		writeErr(w, 400, "escolha o agente que vai testar o destino")
		return
	}
	cfg := in.Config
	if in.ID > 0 {
		if s.Clouds == nil {
			writeErr(w, 503, "indisponível")
			return
		}
		saved, err := s.Clouds.DestConfig(r.Context(), in.ID)
		if err != nil || saved == nil {
			writeErr(w, 404, "destino não encontrado")
			return
		}
		cfg = *saved
	}
	if err := cfg.Normalize(); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	payload, _ := json.Marshal(cfg)
	s.agentCall(w, r, in.AgentID, "dest_test", payload, 85*time.Second)
}

func (s *Server) createDestination(w http.ResponseWriter, r *http.Request, cu *ctxUser) {
	if s.Clouds == nil {
		writeErr(w, 503, "indisponível")
		return
	}
	var in struct {
		Name   string         `json:"name"`
		Config netdest.Config `json:"config"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if !cloudNameRE.MatchString(in.Name) {
		writeErr(w, 400, "nome inválido: use 2 a 100 caracteres (letras, números, espaço, ponto, hífen ou sublinhado)")
		return
	}
	id, err := s.Clouds.CreateDestination(r.Context(), in.Name, in.Config)
	if errors.Is(err, store.ErrDuplicate) {
		writeErr(w, 409, "já existe um destino com esse nome")
		return
	}
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	s.Store.Audit(r.Context(), cu.User.ID, cu.User.Username, "destination.created", in.Name+" ("+in.Config.Type+")", s.clientIP(r))
	writeJSON(w, 201, map[string]any{"id": id})
}

// fetchRun pede ao agente que traga do destino de rede o arquivo de uma execução (para extrair ou copiar).
func (s *Server) fetchRun(w http.ResponseWriter, r *http.Request, cu *ctxUser) {
	run, job, ok := s.runForAction(w, r)
	if !ok {
		return
	}
	if job.CloudAccountID == nil || s.Clouds == nil || run.CloudStatus != "ok" || run.CloudPath == "" {
		writeErr(w, 409, "esta execução não tem cópia conferida no destino")
		return
	}
	if d, err := s.Clouds.DestConfig(r.Context(), *job.CloudAccountID); err != nil || d == nil {
		writeErr(w, 409, "o destino deste job não é um destino de rede")
		return
	}
	_, name := path.Split(run.CloudPath)
	err := s.Agents.Action(run.AgentID, &pb.AgentAction{Kind: "fetch", RunUid: run.UID, JobId: job.ID, FileName: name, Subdir: job.DestSubdir, Sha256: run.SHA256})
	if s.actionErr(w, err) {
		return
	}
	s.Store.Audit(r.Context(), cu.User.ID, cu.User.Username, "run.fetch", run.JobName+" / "+name, s.clientIP(r))
	writeJSON(w, 202, map[string]bool{"ok": true})
}
