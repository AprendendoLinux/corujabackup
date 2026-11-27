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
	"github.com/AprendendoLinux/corujabackup/internal/bkname"
	"github.com/AprendendoLinux/corujabackup/internal/cloud"
	"github.com/AprendendoLinux/corujabackup/internal/pb"
	"github.com/AprendendoLinux/corujabackup/internal/slug"
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
	if d, err := s.Clouds.DestConfig(r.Context(), *job.CloudAccountID); err == nil && d != nil {
		writeErr(w, 409, "este backup está num destino de rede, que o Server não acessa: use \"Trazer do destino\" para o agente buscar o arquivo")
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
	act := &pb.AgentAction{Kind: "extract", RunUid: run.UID, JobId: job.ID, FileName: run.OutputName, Subdir: job.DestSubdir, Target: in.Target}
	if _, inc, _ := bkname.Parse(run.OutputName, slug.Of(job.Name)); inc {
		// incremental: o agente precisa do full e de todos os incrementais anteriores da cadeia, em ordem
		chain, shas, msg := s.chainFor(r, job, run)
		if msg != "" {
			writeErr(w, 409, msg)
			return
		}
		act.Chain, act.ChainSha256 = chain, shas
	}
	err := s.Agents.Action(run.AgentID, act)
	if s.actionErr(w, err) {
		return
	}
	s.Store.Audit(r.Context(), cu.User.ID, cu.User.Username, "run.extract", run.JobName+" / "+run.OutputName+" -> "+in.Target, s.clientIP(r))
	writeJSON(w, 202, map[string]string{"target": in.Target})
}

// chainFor monta a cadeia (full + incrementais até run) a partir das execuções bem-sucedidas do job.
func (s *Server) chainFor(r *http.Request, job *store.Job, run *store.Run) ([]string, []string, string) {
	runs, err := s.Store.ListRuns(r.Context(), job.ID, "", 5000)
	if err != nil {
		return nil, nil, "erro interno"
	}
	var files []bkname.File
	sha := map[string]string{}
	for _, x := range runs {
		if (x.Status == "ok" || x.Status == "warning") && x.OutputName != "" {
			files = append(files, bkname.File{Name: x.OutputName})
			sha[x.OutputName] = x.SHA256
		}
	}
	chain := bkname.ChainUpTo(files, slug.Of(job.Name), run.OutputName)
	if len(chain) == 0 {
		return nil, nil, "esta execução não terminou bem e não pode ser restaurada"
	}
	if _, inc, _ := bkname.Parse(chain[0], slug.Of(job.Name)); inc {
		return nil, nil, "a cadeia deste incremental está incompleta: o backup full dela não está mais no histórico"
	}
	shas := make([]string, len(chain))
	for i, n := range chain {
		shas[i] = sha[n]
	}
	return chain, shas, ""
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
