package httpapi

import (
	"context"
	"errors"
	"net/http"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/AprendendoLinux/corujabackup/internal/agentsrv"
	"github.com/AprendendoLinux/corujabackup/internal/auth"
	"github.com/AprendendoLinux/corujabackup/internal/cloud"
	"github.com/AprendendoLinux/corujabackup/internal/sched"
	"github.com/AprendendoLinux/corujabackup/internal/slug"
	"github.com/AprendendoLinux/corujabackup/internal/store"
)

var (
	jobNameRE = regexp.MustCompile(`^[A-Za-z0-9._][A-Za-z0-9._ -]{1,99}$`)
	subdirRE  = regexp.MustCompile(`^[A-Za-z0-9._-]+(/[A-Za-z0-9._-]+)*$`)
)

type jobIn struct {
	Name       *string   `json:"name"`
	AgentID    *int64    `json:"agent_id"`
	Enabled    *bool     `json:"enabled"`
	Cron       *string   `json:"cron"`
	Timezone   *string   `json:"timezone"`
	Paths      *[]string `json:"paths"`
	Excludes   *[]string `json:"excludes"`
	DestSubdir *string   `json:"dest_subdir"`
	Keep       *int      `json:"keep"`

	CloudAccountID *int64  `json:"cloud_account_id"`
	CloudFolder    *string `json:"cloud_folder"`
	CloudKeep      *int    `json:"cloud_keep"`
	CloudKeepDays  *int    `json:"cloud_keep_days"`
	ClearCloud     bool    `json:"clear_cloud"`
}

func slugify(s string) string { return slug.Of(s) }

func cleanList(in []string, max, maxLen int, requireAbs bool) ([]string, string) {
	out := make([]string, 0, len(in))
	for _, v := range in {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if len(v) > maxLen || strings.ContainsRune(v, 0) {
			return nil, "item inválido na lista"
		}
		if requireAbs {
			if !strings.HasPrefix(v, "/") {
				return nil, "use caminhos absolutos (começando com /): " + v
			}
			v = path.Clean(v)
		}
		out = append(out, v)
	}
	if len(out) > max {
		return nil, "itens demais na lista"
	}
	return out, ""
}

// apply copia os campos informados para o job validando cada um.
func (s *Server) applyJobIn(ctx context.Context, j *store.Job, in jobIn) string {
	if in.Name != nil {
		j.Name = strings.TrimSpace(*in.Name)
	}
	if !jobNameRE.MatchString(j.Name) {
		return "nome inválido: use 2 a 100 caracteres (letras, números, espaço, ponto, hífen ou sublinhado)"
	}
	if in.AgentID != nil {
		j.AgentID = *in.AgentID
	}
	a, err := s.Store.AgentByID(ctx, j.AgentID)
	if err != nil || a.RevokedAt != nil {
		return "agente inexistente ou revogado"
	}
	if in.Enabled != nil {
		j.Enabled = *in.Enabled
	}
	if in.Cron != nil {
		j.Cron = strings.TrimSpace(*in.Cron)
	}
	if in.Timezone != nil {
		j.Timezone = strings.TrimSpace(*in.Timezone)
	}
	if _, err := sched.Parse(j.Cron, j.Timezone); err != nil {
		return err.Error()
	}
	if in.Paths != nil {
		p, msg := cleanList(*in.Paths, 50, 1024, true)
		if msg != "" {
			return msg
		}
		j.Paths = p
	}
	if len(j.Paths) == 0 {
		return "informe pelo menos uma pasta ou arquivo para salvar"
	}
	if in.Excludes != nil {
		e, msg := cleanList(*in.Excludes, 100, 500, false)
		if msg != "" {
			return msg
		}
		j.Excludes = e
	}
	if in.DestSubdir != nil {
		d := strings.TrimSpace(*in.DestSubdir)
		if strings.HasPrefix(d, "/") {
			return "a pasta de destino deve ser relativa à pasta de backups do agente (sem / no começo)"
		}
		j.DestSubdir = strings.TrimRight(d, "/")
	}
	if j.DestSubdir == "" {
		j.DestSubdir = slugify(j.Name)
	}
	if !subdirRE.MatchString(j.DestSubdir) || strings.Contains(j.DestSubdir, "..") {
		return "pasta de destino inválida: use um nome relativo (letras, números, ponto, hífen, sublinhado e /), sem .."
	}
	if in.Keep != nil {
		j.Keep = *in.Keep
	}
	if in.ClearCloud {
		j.CloudAccountID, j.CloudFolder, j.CloudKeep, j.CloudKeepDays = nil, "", 0, 0
	}
	if in.CloudAccountID != nil && *in.CloudAccountID > 0 {
		acc, err := s.Store.GetCloud(ctx, *in.CloudAccountID)
		if err != nil {
			return "conta de nuvem inexistente"
		}
		if acc.Status != "connected" {
			return "a conta de nuvem ainda não está conectada"
		}
		id := acc.ID
		j.CloudAccountID = &id
	}
	if in.CloudKeep != nil {
		j.CloudKeep = *in.CloudKeep
	}
	if in.CloudKeepDays != nil {
		j.CloudKeepDays = *in.CloudKeepDays
	}
	if in.CloudFolder != nil {
		j.CloudFolder = strings.TrimSpace(*in.CloudFolder)
	}
	if j.CloudAccountID != nil {
		if j.CloudFolder == "" {
			j.CloudFolder = "Coruja Backup/" + a.Name + "/" + slugify(j.Name)
		}
		f, err := cloud.CleanFolder(j.CloudFolder)
		if err != nil {
			return err.Error()
		}
		j.CloudFolder = f
		if j.CloudKeep < 0 || j.CloudKeep > 3650 || j.CloudKeepDays < 0 || j.CloudKeepDays > 3650 {
			return "retenção na nuvem inválida (0 a 3650)"
		}
		if j.CloudKeep == 0 && j.CloudKeepDays == 0 {
			return "defina a retenção na nuvem: quantidade de cópias e/ou número de dias"
		}
	}
	minKeep := 1
	if j.CloudAccountID != nil {
		minKeep = 0 // 0 = apagar a cópia local depois que a nuvem confirmar
	}
	if j.Keep < minKeep || j.Keep > 365 {
		if minKeep == 0 {
			return "a quantidade de cópias locais deve ficar entre 0 e 365 (0 = apagar depois do envio à nuvem)"
		}
		return "a quantidade de cópias a manter deve ficar entre 1 e 365"
	}
	j.Kind = "files"
	return ""
}

type jobView struct {
	store.Job
	AgentName   string     `json:"agent_name"`
	AgentOnline bool       `json:"agent_online"`
	NextRunAt   *time.Time `json:"next_run_at"`
	LastRun     *store.Run `json:"last_run"`
}

func (s *Server) listJobs(w http.ResponseWriter, r *http.Request, _ *ctxUser) {
	jobs, err := s.Store.ListJobs(r.Context())
	if err != nil {
		writeErr(w, 500, "erro interno")
		return
	}
	ags, _ := s.Store.ListAgents(r.Context())
	names := map[int64]string{}
	for _, a := range ags {
		names[a.ID] = a.Name
	}
	online := map[int64]time.Time{}
	if s.Agents != nil {
		online = s.Agents.Online()
	}
	last, _ := s.Store.LastRuns(r.Context())
	out := make([]jobView, 0, len(jobs))
	for _, j := range jobs {
		v := jobView{Job: j, AgentName: names[j.AgentID]}
		_, v.AgentOnline = online[j.AgentID]
		if sc, err := sched.Parse(j.Cron, j.Timezone); err == nil && j.Enabled {
			n := sc.Next(s.clock())
			v.NextRunAt = &n
		}
		if lr, ok := last[j.ID]; ok {
			lr := lr
			v.LastRun = &lr
		}
		out = append(out, v)
	}
	writeJSON(w, 200, map[string]any{"jobs": out})
}

func (s *Server) createJob(w http.ResponseWriter, r *http.Request, cu *ctxUser) {
	var in jobIn
	if !decode(w, r, &in) {
		return
	}
	j := &store.Job{Enabled: true, Keep: 7, Timezone: "America/Sao_Paulo", Cron: "10 0 * * *"}
	if msg := s.applyJobIn(r.Context(), j, in); msg != "" {
		writeErr(w, 400, msg)
		return
	}
	id, err := s.Store.CreateJob(r.Context(), j)
	if errors.Is(err, store.ErrDuplicate) {
		writeErr(w, 409, "já existe um job com esse nome neste agente")
		return
	}
	if err != nil {
		writeErr(w, 500, "erro interno")
		return
	}
	s.pushAgent(r.Context(), j.AgentID)
	s.Store.Audit(r.Context(), cu.User.ID, cu.User.Username, "job.created", j.Name, s.clientIP(r))
	writeJSON(w, 201, map[string]int64{"id": id})
}

func (s *Server) updateJob(w http.ResponseWriter, r *http.Request, cu *ctxUser) {
	id, ok := pathID(r)
	var in jobIn
	if !ok || !decode(w, r, &in) {
		if !ok {
			writeErr(w, 400, "id inválido")
		}
		return
	}
	j, err := s.Store.GetJob(r.Context(), id)
	if err != nil {
		writeErr(w, 404, "job não encontrado")
		return
	}
	oldAgent := j.AgentID
	if msg := s.applyJobIn(r.Context(), j, in); msg != "" {
		writeErr(w, 400, msg)
		return
	}
	if err := s.Store.UpdateJob(r.Context(), j); err != nil {
		if errors.Is(err, store.ErrDuplicate) {
			writeErr(w, 409, "já existe um job com esse nome neste agente")
			return
		}
		writeErr(w, 500, "erro interno")
		return
	}
	s.pushAgent(r.Context(), j.AgentID)
	if oldAgent != j.AgentID {
		s.pushAgent(r.Context(), oldAgent)
	}
	s.Store.Audit(r.Context(), cu.User.ID, cu.User.Username, "job.updated", j.Name, s.clientIP(r))
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) deleteJob(w http.ResponseWriter, r *http.Request, cu *ctxUser) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, 400, "id inválido")
		return
	}
	j, err := s.Store.GetJob(r.Context(), id)
	if err != nil {
		writeErr(w, 404, "job não encontrado")
		return
	}
	_ = s.Store.DeleteJob(r.Context(), id)
	s.pushAgent(r.Context(), j.AgentID)
	s.Store.Audit(r.Context(), cu.User.ID, cu.User.Username, "job.deleted", j.Name, s.clientIP(r))
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) runJobNow(w http.ResponseWriter, r *http.Request, cu *ctxUser) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, 400, "id inválido")
		return
	}
	j, err := s.Store.GetJob(r.Context(), id)
	if err != nil {
		writeErr(w, 404, "job não encontrado")
		return
	}
	if s.Agents == nil {
		writeErr(w, 503, "canal dos agentes indisponível")
		return
	}
	uid := auth.NewUUID()
	if err := s.Store.CreateQueuedRun(r.Context(), uid, j.ID, j.AgentID); err != nil {
		writeErr(w, 500, "erro interno")
		return
	}
	if err := s.Agents.RunNow(j.AgentID, j.ID, uid); err != nil {
		_ = s.Store.DeleteQueuedRun(r.Context(), uid)
		if errors.Is(err, agentsrv.ErrOffline) {
			writeErr(w, 409, "o agente está offline; não foi possível iniciar o backup agora")
			return
		}
		writeErr(w, 502, err.Error())
		return
	}
	s.Store.Audit(r.Context(), cu.User.ID, cu.User.Username, "job.run_now", j.Name, s.clientIP(r))
	writeJSON(w, 202, map[string]string{"run_uid": uid})
}

func (s *Server) pushAgent(ctx context.Context, agentID int64) {
	if s.Agents != nil {
		s.Agents.PushJobs(context.WithoutCancel(ctx), agentID)
	}
}

// ---------------------------------------------------------------- execuções

func (s *Server) listRuns(w http.ResponseWriter, r *http.Request, _ *ctxUser) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	jobID, _ := strconv.ParseInt(q.Get("job_id"), 10, 64)
	status := q.Get("status")
	switch status {
	case "", "queued", "running", "ok", "warning", "failed", "canceled", "missed":
	default:
		writeErr(w, 400, "status inválido")
		return
	}
	runs, err := s.Store.ListRuns(r.Context(), jobID, status, limit)
	if err != nil {
		writeErr(w, 500, "erro interno")
		return
	}
	writeJSON(w, 200, map[string]any{"runs": runs})
}

func (s *Server) getRun(w http.ResponseWriter, r *http.Request, _ *ctxUser) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, 400, "id inválido")
		return
	}
	run, err := s.Store.GetRun(r.Context(), id)
	if err != nil {
		writeErr(w, 404, "execução não encontrada")
		return
	}
	logs, _ := s.Store.RunLogs(r.Context(), id)
	writeJSON(w, 200, map[string]any{"run": run, "logs": logs})
}

func (s *Server) cancelRun(w http.ResponseWriter, r *http.Request, cu *ctxUser) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, 400, "id inválido")
		return
	}
	run, err := s.Store.GetRun(r.Context(), id)
	if err != nil {
		writeErr(w, 404, "execução não encontrada")
		return
	}
	if store.IsFinal(run.Status) || s.Agents == nil {
		writeErr(w, 409, "a execução já terminou")
		return
	}
	if err := s.Agents.CancelRun(run.AgentID, run.UID); err != nil {
		writeErr(w, 409, "o agente está offline")
		return
	}
	s.Store.Audit(r.Context(), cu.User.ID, cu.User.Username, "run.cancel", run.JobName, s.clientIP(r))
	writeJSON(w, 202, map[string]bool{"ok": true})
}

func (s *Server) stats(w http.ResponseWriter, r *http.Request, _ *ctxUser) {
	st, err := s.Store.StatsSince(r.Context(), s.clock().Add(-24*time.Hour))
	if err != nil {
		writeErr(w, 500, "erro interno")
		return
	}
	jobs, _ := s.Store.ListJobs(r.Context())
	writeJSON(w, 200, map[string]any{"last_24h": map[string]int{"ok": st.OK, "warning": st.Warning, "failed": st.Failed, "missed": st.Missed, "running": st.Running}, "jobs": len(jobs)})
}
