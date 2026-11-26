package httpapi

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/AprendendoLinux/corujabackup/internal/agentsrv"
	"github.com/AprendendoLinux/corujabackup/internal/cloud"
	"github.com/AprendendoLinux/corujabackup/internal/pb"
	"github.com/AprendendoLinux/corujabackup/internal/store"
)

var extractTargetRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// downloadRun entrega ao navegador a cópia que está na nuvem (o Server só repassa o fluxo; nada fica no disco).
func (s *Server) downloadRun(w http.ResponseWriter, r *http.Request, cu *ctxUser) {
	id, ok := pathID(r)
	if !ok || s.Clouds == nil {
		writeErr(w, 400, "id inválido")
		return
	}
	run, err := s.Store.GetRun(r.Context(), id)
	if err != nil {
		writeErr(w, 404, "execução não encontrada")
		return
	}
	job, err := s.Store.GetJob(r.Context(), run.JobID)
	if err != nil || job.CloudAccountID == nil || run.CloudStatus != "ok" || run.CloudPath == "" {
		writeErr(w, 409, "esta execução não tem cópia conferida na nuvem")
		return
	}
	folder, name := path.Split(run.CloudPath)
	body, size, err := s.Clouds.Open(r.Context(), *job.CloudAccountID, strings.TrimSuffix(folder, "/"), name)
	if errors.Is(err, cloud.ErrNotFound) {
		writeErr(w, 404, "o arquivo não existe mais na nuvem (a retenção pode tê-lo removido)")
		return
	}
	if err != nil {
		writeErr(w, 502, "não foi possível baixar da nuvem: "+err.Error())
		return
	}
	defer body.Close()
	s.Store.Audit(r.Context(), cu.User.ID, cu.User.Username, "run.download", run.JobName+" / "+name, s.clientIP(r))
	h := w.Header()
	h.Set("Content-Type", "application/gzip")
	h.Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, name))
	h.Set("Cache-Control", "no-store")
	if size > 0 {
		h.Set("Content-Length", fmt.Sprint(size))
	}
	_, _ = io.Copy(w, body)
}

func (s *Server) runForAction(w http.ResponseWriter, r *http.Request) (*store.Run, *store.Job, bool) {
	id, ok := pathID(r)
	if !ok || s.Agents == nil {
		writeErr(w, 400, "id inválido")
		return nil, nil, false
	}
	run, err := s.Store.GetRun(r.Context(), id)
	if err != nil {
		writeErr(w, 404, "execução não encontrada")
		return nil, nil, false
	}
	job, err := s.Store.GetJob(r.Context(), run.JobID)
	if err != nil {
		writeErr(w, 404, "o job desta execução não existe mais")
		return nil, nil, false
	}
	if !store.IsFinal(run.Status) || run.OutputName == "" {
		writeErr(w, 409, "esta execução não gerou um arquivo de backup")
		return nil, nil, false
	}
	return run, job, true
}

// reuploadRun pede ao agente que envie de novo à nuvem o arquivo local de uma execução cujo envio falhou.
func (s *Server) reuploadRun(w http.ResponseWriter, r *http.Request, cu *ctxUser) {
	run, job, ok := s.runForAction(w, r)
	if !ok {
		return
	}
	if job.CloudAccountID == nil {
		writeErr(w, 409, "o job não tem destino de nuvem")
		return
	}
	if run.CloudStatus == "ok" {
		writeErr(w, 409, "esta execução já está na nuvem")
		return
	}
	err := s.Agents.Action(run.AgentID, &pb.AgentAction{Kind: "reupload", RunUid: run.UID, JobId: job.ID, FileName: run.OutputName, Subdir: job.DestSubdir})
	if s.actionErr(w, err) {
		return
	}
	s.Store.Audit(r.Context(), cu.User.ID, cu.User.Username, "run.reupload", run.JobName+" / "+run.OutputName, s.clientIP(r))
	writeJSON(w, 202, map[string]bool{"ok": true})
}

// extractRun pede ao agente que extraia o backup (arquivo local) para <pasta de backups>/restores/<nome>.
func (s *Server) extractRun(w http.ResponseWriter, r *http.Request, cu *ctxUser) {
	run, job, ok := s.runForAction(w, r)
	if !ok {
		return
	}
	var in struct {
		Target string `json:"target"`
	}
	if r.ContentLength > 0 && !decode(w, r, &in) {
		return
	}
	in.Target = strings.TrimSpace(in.Target)
	if in.Target == "" {
		in.Target = fmt.Sprintf("run-%d-%s", run.ID, time.Now().UTC().Format("20060102-150405"))
	}
	if !extractTargetRE.MatchString(in.Target) || strings.Contains(in.Target, "..") {
		writeErr(w, 400, "nome de pasta inválido: use letras, números, ponto, hífen e sublinhado")
		return
	}
	err := s.Agents.Action(run.AgentID, &pb.AgentAction{Kind: "extract", RunUid: run.UID, JobId: job.ID, FileName: run.OutputName, Subdir: job.DestSubdir, Target: in.Target})
	if s.actionErr(w, err) {
		return
	}
	s.Store.Audit(r.Context(), cu.User.ID, cu.User.Username, "run.extract", run.JobName+" / "+run.OutputName+" -> "+in.Target, s.clientIP(r))
	writeJSON(w, 202, map[string]string{"target": in.Target})
}

func (s *Server) actionErr(w http.ResponseWriter, err error) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, agentsrv.ErrOffline):
		writeErr(w, 409, "o agente está offline")
	default:
		writeErr(w, 502, err.Error())
	}
	return true
}
