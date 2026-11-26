package httpapi

import (
	"crypto/subtle"
	"encoding/csv"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/AprendendoLinux/corujabackup/internal/auth"
	"github.com/AprendendoLinux/corujabackup/internal/store"
	"github.com/AprendendoLinux/corujabackup/internal/version"
)

type secPolicy struct {
	Require2FA   string // none | admins | all
	SessionHours int
	MetricsHash  string
	loaded       time.Time
}

type policyCache struct {
	mu sync.Mutex
	p  secPolicy
}

const (
	defaultSessionHours = 12
	policyTTL           = 20 * time.Second
)

// policy devolve a política de segurança (em cache por poucos segundos, para não consultar o banco a cada requisição).
func (s *Server) policy(r *http.Request) secPolicy {
	s.pol.mu.Lock()
	defer s.pol.mu.Unlock()
	if !s.pol.p.loaded.IsZero() && time.Since(s.pol.p.loaded) < policyTTL {
		return s.pol.p
	}
	p := secPolicy{Require2FA: "none", SessionHours: defaultSessionHours, loaded: time.Now()}
	if m, err := s.Store.GetSettings(r.Context(), "security.require_2fa", "security.session_hours", "security.metrics_hash"); err == nil {
		switch m["security.require_2fa"] {
		case "admins", "all":
			p.Require2FA = m["security.require_2fa"]
		}
		if h, err := strconv.Atoi(m["security.session_hours"]); err == nil && h >= 1 && h <= 168 {
			p.SessionHours = h
		}
		p.MetricsHash = m["security.metrics_hash"]
	}
	s.pol.p = p
	return p
}

func (s *Server) invalidatePolicy() {
	s.pol.mu.Lock()
	s.pol.p.loaded = time.Time{}
	s.pol.mu.Unlock()
}

func (s *Server) sessionTTL(r *http.Request) time.Duration {
	return time.Duration(s.policy(r).SessionHours) * time.Hour
}

// needs2FA diz se a política exige 2FA deste usuário e ele ainda não o ativou.
func (s *Server) needs2FA(r *http.Request, u *store.User) bool {
	if u.TOTPEnabled {
		return false
	}
	switch s.policy(r).Require2FA {
	case "all":
		return true
	case "admins":
		return u.Role == "admin"
	}
	return false
}

func (s *Server) getSecurity(w http.ResponseWriter, r *http.Request, _ *ctxUser) {
	p := s.policy(r)
	writeJSON(w, 200, map[string]any{"require_2fa": p.Require2FA, "session_hours": p.SessionHours, "metrics_enabled": p.MetricsHash != ""})
}

func (s *Server) saveSecurity(w http.ResponseWriter, r *http.Request, cu *ctxUser) {
	var in struct {
		Require2FA   string `json:"require_2fa"`
		SessionHours int    `json:"session_hours"`
	}
	if !decode(w, r, &in) {
		return
	}
	switch in.Require2FA {
	case "none", "admins", "all":
	default:
		writeErr(w, 400, "política de 2FA inválida (none, admins ou all)")
		return
	}
	if in.SessionHours < 1 || in.SessionHours > 168 {
		writeErr(w, 400, "a duração da sessão deve ficar entre 1 e 168 horas")
		return
	}
	if in.Require2FA != "none" && !cu.User.TOTPEnabled && (in.Require2FA == "all" || cu.User.Role == "admin") {
		writeErr(w, 409, "ative o seu próprio 2FA antes de exigi-lo (para não se trancar para fora)")
		return
	}
	_ = s.Store.SetSetting(r.Context(), "security.require_2fa", in.Require2FA)
	_ = s.Store.SetSetting(r.Context(), "security.session_hours", strconv.Itoa(in.SessionHours))
	s.invalidatePolicy()
	s.Store.Audit(r.Context(), cu.User.ID, cu.User.Username, "security.updated", fmt.Sprintf("2fa=%s sessão=%dh", in.Require2FA, in.SessionHours), s.clientIP(r))
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// ---------------------------------------------------------------- token de métricas (Prometheus)

func (s *Server) newMetricsToken(w http.ResponseWriter, r *http.Request, cu *ctxUser) {
	t, err := auth.RandomToken(32)
	if err != nil {
		writeErr(w, 500, "erro interno")
		return
	}
	tok := "cbm_" + t
	if err := s.Store.SetSetting(r.Context(), "security.metrics_hash", auth.SHA256Hex(tok)); err != nil {
		writeErr(w, 500, "erro interno")
		return
	}
	s.invalidatePolicy()
	s.Store.Audit(r.Context(), cu.User.ID, cu.User.Username, "security.metrics_token", "gerado", s.clientIP(r))
	writeJSON(w, 201, map[string]string{"token": tok}) // só aparece agora; o Server guarda apenas o hash
}

func (s *Server) deleteMetricsToken(w http.ResponseWriter, r *http.Request, cu *ctxUser) {
	_ = s.Store.DeleteSetting(r.Context(), "security.metrics_hash")
	s.invalidatePolicy()
	s.Store.Audit(r.Context(), cu.User.ID, cu.User.Username, "security.metrics_token", "revogado", s.clientIP(r))
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func esc(v string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace(v)
}

// metrics expõe métricas no formato texto do Prometheus. Exige o token (Authorization: Bearer cbm_...); sem token
// configurado o endereço responde 404 (não revela que existe).
func (s *Server) metrics(w http.ResponseWriter, r *http.Request) {
	p := s.policy(r)
	tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if p.MetricsHash == "" {
		http.NotFound(w, r)
		return
	}
	if subtle.ConstantTimeCompare([]byte(auth.SHA256Hex(tok)), []byte(p.MetricsHash)) != 1 {
		w.Header().Set("WWW-Authenticate", `Bearer realm="coruja"`)
		writeErr(w, 401, "token inválido")
		return
	}
	ctx := r.Context()
	var b strings.Builder
	line := func(f string, a ...any) { fmt.Fprintf(&b, f+"\n", a...) }
	line("# HELP coruja_info Versão do Coruja Backup.\n# TYPE coruja_info gauge")
	line(`coruja_info{version="%s"} 1`, esc(version.Version))

	agents, _ := s.Store.ListAgents(ctx)
	online := map[int64]time.Time{}
	if s.Agents != nil {
		online = s.Agents.Online()
	}
	var on, off, rev int
	for _, a := range agents {
		switch _, ok := online[a.ID]; {
		case a.RevokedAt != nil:
			rev++
		case ok:
			on++
		default:
			off++
		}
	}
	line("# HELP coruja_agents Agentes por estado.\n# TYPE coruja_agents gauge")
	line(`coruja_agents{state="online"} %d`, on)
	line(`coruja_agents{state="offline"} %d`, off)
	line(`coruja_agents{state="revoked"} %d`, rev)

	jobs, _ := s.Store.ListJobs(ctx)
	last, _ := s.Store.LastRuns(ctx)
	sort.Slice(jobs, func(i, j int) bool { return jobs[i].ID < jobs[j].ID })
	names := map[int64]string{}
	for _, a := range agents {
		names[a.ID] = a.Name
	}
	line("# HELP coruja_job_last_run_timestamp_seconds Início da última execução do job.\n# TYPE coruja_job_last_run_timestamp_seconds gauge")
	for _, j := range jobs {
		if lr, ok := last[j.ID]; ok {
			t := lr.StartedAt
			if t == nil {
				t = lr.ScheduledFor
			}
			if t != nil {
				line(`coruja_job_last_run_timestamp_seconds{job="%s",agent="%s"} %d`, esc(j.Name), esc(names[j.AgentID]), t.Unix())
			}
		}
	}
	line("# HELP coruja_job_last_run_ok 1 se a última execução do job foi bem-sucedida (ok ou com avisos).\n# TYPE coruja_job_last_run_ok gauge")
	line("# HELP coruja_job_last_run_bytes Tamanho do arquivo gerado pela última execução.\n# TYPE coruja_job_last_run_bytes gauge")
	for _, j := range jobs {
		if lr, ok := last[j.ID]; ok {
			v := 0
			if lr.Status == "ok" || lr.Status == "warning" {
				v = 1
			}
			line(`coruja_job_last_run_ok{job="%s",agent="%s",status="%s"} %d`, esc(j.Name), esc(names[j.AgentID]), esc(lr.Status), v)
			line(`coruja_job_last_run_bytes{job="%s",agent="%s"} %d`, esc(j.Name), esc(names[j.AgentID]), lr.BytesOutput)
		}
	}
	if st, err := s.Store.StatsSince(ctx, s.clock().Add(-24*time.Hour)); err == nil {
		line("# HELP coruja_runs_24h Execuções nas últimas 24 horas, por estado.\n# TYPE coruja_runs_24h gauge")
		line(`coruja_runs_24h{status="ok"} %d`, st.OK)
		line(`coruja_runs_24h{status="warning"} %d`, st.Warning)
		line(`coruja_runs_24h{status="failed"} %d`, st.Failed)
		line(`coruja_runs_24h{status="missed"} %d`, st.Missed)
		line(`coruja_runs_24h{status="running"} %d`, st.Running)
	}
	clouds, _ := s.Store.ListClouds(ctx)
	line("# HELP coruja_cloud_up 1 se a conta de nuvem está conectada.\n# TYPE coruja_cloud_up gauge")
	line("# HELP coruja_cloud_quota_bytes Cota da conta de nuvem.\n# TYPE coruja_cloud_quota_bytes gauge")
	for _, c := range clouds {
		up := 0
		if c.Status == "connected" {
			up = 1
		}
		line(`coruja_cloud_up{account="%s",type="%s"} %d`, esc(c.Name), esc(c.Type), up)
		line(`coruja_cloud_quota_bytes{account="%s",kind="used"} %d`, esc(c.Name), c.QuotaUsed)
		line(`coruja_cloud_quota_bytes{account="%s",kind="total"} %d`, esc(c.Name), c.QuotaTotal)
	}
	if ns, err := s.Store.ListNotifications(ctx, 500); err == nil {
		cnt := map[string]int{}
		for _, n := range ns {
			cnt[n.Status]++
		}
		line("# HELP coruja_notifications Envios recentes (últimos 500), por estado.\n# TYPE coruja_notifications gauge")
		for _, k := range []string{"pending", "sent", "failed"} {
			line(`coruja_notifications{status="%s"} %d`, k, cnt[k])
		}
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	_, _ = w.Write([]byte(b.String()))
}

// ---------------------------------------------------------------- sessões do próprio usuário

func (s *Server) mySessions(w http.ResponseWriter, r *http.Request, cu *ctxUser) {
	list, err := s.Store.ListSessions(r.Context(), cu.User.ID)
	if err != nil {
		writeErr(w, 500, "erro interno")
		return
	}
	type view struct {
		store.SessionInfo
		Current bool `json:"current"`
	}
	out := make([]view, 0, len(list))
	for _, x := range list {
		if x.Pending2FA {
			continue
		}
		out = append(out, view{x, x.ID == cu.Session.IDHash})
	}
	writeJSON(w, 200, map[string]any{"sessions": out})
}

func (s *Server) revokeSession(w http.ResponseWriter, r *http.Request, cu *ctxUser) {
	id := r.PathValue("id")
	if id == cu.Session.IDHash {
		writeErr(w, 409, "esta é a sua sessão atual; use Sair")
		return
	}
	ok, _ := s.Store.DeleteSessionOf(r.Context(), cu.User.ID, id)
	if !ok {
		writeErr(w, 404, "sessão não encontrada")
		return
	}
	s.Store.Audit(r.Context(), cu.User.ID, cu.User.Username, "session.revoked", "", s.clientIP(r))
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) revokeOtherSessions(w http.ResponseWriter, r *http.Request, cu *ctxUser) {
	_ = s.Store.DeleteUserSessions(r.Context(), cu.User.ID, cu.Session.IDHash)
	s.Store.Audit(r.Context(), cu.User.ID, cu.User.Username, "session.revoked_others", "", s.clientIP(r))
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// ---------------------------------------------------------------- auditoria em CSV

func (s *Server) auditCSV(w http.ResponseWriter, r *http.Request, cu *ctxUser) {
	n, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if n <= 0 || n > 20000 {
		n = 5000
	}
	es, err := s.Store.ListAudit(r.Context(), n)
	if err != nil {
		writeErr(w, 500, "erro interno")
		return
	}
	s.Store.Audit(r.Context(), cu.User.ID, cu.User.Username, "audit.exported", strconv.Itoa(len(es))+" registros", s.clientIP(r))
	h := w.Header()
	h.Set("Content-Type", "text/csv; charset=utf-8")
	h.Set("Content-Disposition", `attachment; filename="auditoria.csv"`)
	h.Set("Cache-Control", "no-store")
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"quando_utc", "usuario", "acao", "detalhe", "ip"})
	for _, e := range es {
		// neutraliza "injeção de fórmula" em planilhas: campos que começam com = + - @ ganham um apóstrofo
		safe := func(v string) string {
			if v != "" && strings.ContainsRune("=+-@\t\r", rune(v[0])) {
				return "'" + v
			}
			return v
		}
		_ = cw.Write([]string{e.At.UTC().Format(time.RFC3339), safe(e.Username), e.Action, safe(e.Detail), e.IP})
	}
	cw.Flush()
}
