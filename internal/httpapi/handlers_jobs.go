package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/AprendendoLinux/corujabackup/internal/agentcore"
	"github.com/AprendendoLinux/corujabackup/internal/agentsrv"
	"github.com/AprendendoLinux/corujabackup/internal/auth"
	"github.com/AprendendoLinux/corujabackup/internal/cloud"
	"github.com/AprendendoLinux/corujabackup/internal/dbdump"
	"github.com/AprendendoLinux/corujabackup/internal/netdest"
	"github.com/AprendendoLinux/corujabackup/internal/notify"
	"github.com/AprendendoLinux/corujabackup/internal/sched"
	"github.com/AprendendoLinux/corujabackup/internal/slug"
	"github.com/AprendendoLinux/corujabackup/internal/store"
)

var (
	jobNameRE = regexp.MustCompile(`^[\p{L}\p{N}._(][\p{L}\p{N}._ ()&,'-]{1,99}$`)
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
	CloudDirect    *bool   `json:"cloud_direct"`
	BackupMode     *string `json:"backup_mode"`
	FullWeekday    *int    `json:"full_weekday"`
	ClearCloud     bool    `json:"clear_cloud"`

	Kind     *string `json:"kind"`
	DB       *dbIn   `json:"db"`
	Mikrotik *mtIn   `json:"mikrotik"`
}

type dbIn struct {
	Engine           string    `json:"engine"`
	Mode             string    `json:"mode"`
	Container        string    `json:"container"`
	Host             string    `json:"host"`
	Port             int       `json:"port"`
	User             string    `json:"user"`
	Password         *string   `json:"password"` // nil ou vazio = manter a senha atual
	AuthDB           string    `json:"auth_db"`
	Databases        *[]string `json:"databases"`
	ExcludeDatabases *[]string `json:"exclude_databases"`
	SSLSkipVerify    bool      `json:"ssl_skip_verify"`
	RestoreTest      string    `json:"restore_test"`
	RestoreImage     string    `json:"restore_image"`
}

var (
	containerRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)
	hostRE      = regexp.MustCompile(`^[A-Za-z0-9._:\-]{1,255}$`)
	agentHostRE = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.\-]{0,251}[A-Za-z0-9])?$|^[0-9A-Fa-f:]+$`)
	imageRE     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/:@-]{0,200}$`)
)

// buildDB valida a configuração de banco e a mescla com a anterior (para não exigir a senha a cada edição).
func buildDB(in *dbIn, old *store.DBConfig) (*store.DBConfig, string) {
	c := &store.DBConfig{}
	if old != nil {
		c.Password = old.Password
	}
	c.Engine, c.Mode, c.Container, c.Host, c.Port = in.Engine, in.Mode, strings.TrimSpace(in.Container), strings.TrimSpace(in.Host), in.Port
	c.User, c.AuthDB, c.SSLSkipVerify = strings.TrimSpace(in.User), strings.TrimSpace(in.AuthDB), in.SSLSkipVerify
	if in.Password != nil && *in.Password != "" {
		c.Password = *in.Password
	}
	switch c.Engine {
	case "mysql", "mariadb", "postgres", "mongodb":
	default:
		return nil, "motor de banco inválido (mysql, mariadb, postgres ou mongodb)"
	}
	switch c.Mode {
	case "docker":
		if !containerRE.MatchString(c.Container) {
			return nil, "informe o nome do contêiner do banco"
		}
		c.Host, c.Port = "", 0
	case "network":
		if !hostRE.MatchString(c.Host) {
			return nil, "informe o endereço do servidor de banco"
		}
		if c.Port < 0 || c.Port > 65535 {
			return nil, "porta inválida"
		}
		c.Container = ""
	default:
		return nil, "modo de conexão inválido (docker ou network)"
	}
	if len(c.User) > 128 || len(c.Password) > 256 || len(c.AuthDB) > 64 || strings.ContainsAny(c.User+c.AuthDB, "\x00\n\r") {
		return nil, "usuário, senha ou banco de autenticação inválido"
	}
	list := func(p *[]string, old []string) ([]string, string) {
		if p == nil {
			return old, ""
		}
		var out []string
		for _, d := range *p {
			d = strings.TrimSpace(d)
			if d == "" {
				continue
			}
			if !dbdump.ValidName(d) {
				return nil, "nome de banco inválido: " + d
			}
			out = append(out, d)
		}
		if len(out) > 200 {
			return nil, "bancos demais na lista"
		}
		return out, ""
	}
	var msg string
	var oldDBs, oldEx []string
	if old != nil {
		oldDBs, oldEx = old.Databases, old.ExcludeDatabases
	}
	if c.Databases, msg = list(in.Databases, oldDBs); msg != "" {
		return nil, msg
	}
	if c.ExcludeDatabases, msg = list(in.ExcludeDatabases, oldEx); msg != "" {
		return nil, msg
	}
	c.RestoreTest = in.RestoreTest
	switch c.RestoreTest {
	case "":
		c.RestoreTest = "structural"
	case "none", "structural", "full":
	default:
		return nil, "teste de restauração inválido (none, structural ou full)"
	}
	c.RestoreImage = strings.TrimSpace(in.RestoreImage)
	if c.RestoreImage != "" && !imageRE.MatchString(c.RestoreImage) {
		return nil, "imagem do teste de restauração inválida"
	}
	return c, ""
}

func dbView(c *store.DBConfig) map[string]any {
	if c == nil {
		return nil
	}
	return map[string]any{"engine": c.Engine, "mode": c.Mode, "container": c.Container, "host": c.Host, "port": c.Port, "user": c.User,
		"password_set": c.Password != "", "auth_db": c.AuthDB, "databases": nonNil(c.Databases), "exclude_databases": nonNil(c.ExcludeDatabases),
		"ssl_skip_verify": c.SSLSkipVerify, "restore_test": c.RestoreTest, "restore_image": c.RestoreImage}
}

func nonNil(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
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
	if j.Kind == "mikrotik" {
		if msg := s.applyMikrotik(j, in.Mikrotik, a.AgentVersion, a.Name); msg != "" {
			return msg
		}
	} else if j.Kind == "database" {
		var old *store.DBConfig
		if j.DBSealed != "" && s.Box != nil {
			old, _ = j.OpenDB(s.Box)
		}
		if in.DB == nil && old == nil {
			return "informe a configuração do banco de dados"
		}
		if in.DB != nil {
			if s.Box == nil {
				return "chave de criptografia indisponível"
			}
			cfg, msg := buildDB(in.DB, old)
			if msg != "" {
				return msg
			}
			sealed, err := cfg.Seal(s.Box)
			if err != nil {
				return "não foi possível proteger as credenciais"
			}
			j.DBSealed = sealed
		}
		j.Paths, j.Excludes = []string{}, []string{}
	} else {
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
	}
	if in.Excludes != nil && j.Kind == "files" {
		e, msg := cleanList(*in.Excludes, 100, 500, false)
		if msg != "" {
			return msg
		}
		j.Excludes = e
	}
	if in.DestSubdir != nil {
		j.DestSubdir = strings.TrimSpace(*in.DestSubdir)
	}
	if j.DestSubdir == "" {
		j.DestSubdir = slugify(j.Name)
	}
	if strings.HasPrefix(j.DestSubdir, "/") { // pasta do servidor, escolhida na interface
		clean, err := agentcore.CheckLocalDir(j.DestSubdir)
		if err != nil {
			return err.Error()
		}
		if len([]rune(clean)) > 255 {
			return "o caminho da pasta de backup local é longo demais (máximo de 255 caracteres)"
		}
		j.DestSubdir = clean
	} else {
		j.DestSubdir = strings.TrimRight(j.DestSubdir, "/")
		if !subdirRE.MatchString(j.DestSubdir) || strings.Contains(j.DestSubdir, "..") {
			return "pasta de destino inválida: use um caminho completo do agente ou um nome relativo (letras, números, ponto, hífen, sublinhado e /), sem .."
		}
	}
	if in.Keep != nil {
		j.Keep = *in.Keep
	}
	if in.ClearCloud {
		j.CloudAccountID, j.CloudFolder, j.CloudKeep, j.CloudKeepDays, j.CloudDirect = nil, "", 0, 0, false
	}
	if in.CloudDirect != nil {
		j.CloudDirect = *in.CloudDirect
	}
	if in.BackupMode != nil {
		j.BackupMode = *in.BackupMode
	}
	if in.FullWeekday != nil {
		j.FullWeekday = *in.FullWeekday
	}
	switch j.BackupMode {
	case "", "full":
		j.BackupMode = "full"
	case "incremental":
		if j.Kind == "database" {
			return "jobs de banco de dados são sempre completos (o dump é inteiro a cada execução)"
		}
		if a.AgentVersion != "" && !versionAtLeast(a.AgentVersion, [3]int{0, 0, 21}) {
			return "o agente " + a.Name + " (versão " + a.AgentVersion + ") não faz backups incrementais: atualize-o para a versão 0.0.21 ou mais nova"
		}
	default:
		return "tipo de backup inválido"
	}
	if j.FullWeekday < 0 || j.FullWeekday > 6 {
		return "dia do backup full inválido"
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
	var netDest *netdest.Config
	if j.CloudAccountID != nil && s.Clouds != nil {
		if d, err := s.Clouds.DestConfig(ctx, *j.CloudAccountID); err == nil && d != nil {
			if msg := destAgentCheck(d, a); msg != "" {
				return msg
			}
			netDest = d
		}
	}
	if j.CloudDirect {
		if netDest == nil {
			return "a gravação direta só vale para storages de rede ou pasta local (nas nuvens, o backup é gerado no agente e depois enviado)"
		}
		if a.AgentVersion != "" && !versionAtLeast(a.AgentVersion, [3]int{0, 0, 19}) {
			return "o agente " + a.Name + " (versão " + a.AgentVersion + ") não grava direto no storage: atualize-o para a versão 0.0.19 ou mais nova"
		}
		j.Keep = 0 // não há cópia no agente
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
	return ""
}

type jobView struct {
	store.Job
	AgentName   string         `json:"agent_name"`
	AgentOnline bool           `json:"agent_online"`
	NextRunAt   *time.Time     `json:"next_run_at"`
	LastRun     *store.Run     `json:"last_run"`
	DB          map[string]any `json:"db"`
	Mikrotik    map[string]any `json:"mikrotik"`
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
		if j.Kind == "database" {
			if cfg, err := j.OpenDB(s.Box); err == nil {
				v.DB = dbView(cfg)
			}
		}
		if j.Kind == "mikrotik" {
			if cfg, err := j.OpenMikrotik(s.Box); err == nil {
				v.Mikrotik = mikrotikView(cfg)
			}
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
	j := &store.Job{Enabled: true, Keep: 7, Timezone: "America/Sao_Paulo", Cron: "10 0 * * *", Kind: "files"}
	if in.Kind != nil {
		if *in.Kind != "files" && *in.Kind != "database" && *in.Kind != "mikrotik" {
			writeErr(w, 400, "tipo de job inválido (files, database ou mikrotik)")
			return
		}
		j.Kind = *in.Kind
	}
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
	if in.Kind != nil && *in.Kind != j.Kind {
		writeErr(w, 400, "o tipo do job não pode ser alterado (crie outro job)")
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
	full := r.URL.Query().Get("full") == "1" // "executar full agora" num job incremental
	uid := auth.NewUUID()
	if err := s.Store.CreateQueuedRun(r.Context(), uid, j.ID, j.AgentID); err != nil {
		writeErr(w, 500, "erro interno")
		return
	}
	if err := s.Agents.RunNow(j.AgentID, j.ID, uid, full); err != nil {
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
	from, to, perr := periodParams(q.Get("from"), q.Get("to"), q.Get("tz"))
	if perr != "" {
		writeErr(w, 400, perr)
		return
	}
	var start, stop time.Time
	if !from.IsZero() {
		start, stop = from, to.AddDate(0, 0, 1)
	}
	runs, err := s.Store.ListRunsBetween(r.Context(), jobID, status, start, stop, limit)
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
	info := map[string]any{}
	if j, err := s.Store.GetJob(r.Context(), run.JobID); err == nil {
		info = map[string]any{"kind": j.Kind, "has_cloud": j.CloudAccountID != nil, "keep": j.Keep}
		if j.CloudAccountID != nil && s.Clouds != nil {
			d, derr := s.Clouds.DestConfig(r.Context(), *j.CloudAccountID)
			info["net_destination"] = derr == nil && d != nil
		}
		if j.Kind == "database" {
			if cfg, err := j.OpenDB(s.Box); err == nil && cfg != nil {
				info["engine"] = cfg.Engine
			}
		}
	}
	writeJSON(w, 200, map[string]any{"run": run, "logs": logs, "job": info})
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
	_ = s.Store.RequestCancel(r.Context(), run.ID)
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

// periodParams lê um período de dias inteiros (?from=AAAA-MM-DD&to=AAAA-MM-DD, inclusive) no fuso ?tz. Sem from, devolve
// datas zeradas (sem filtro). O período vai até 366 dias e não começa no futuro.
func periodParams(fromS, toS, tz string) (time.Time, time.Time, string) {
	if fromS == "" && toS == "" {
		return time.Time{}, time.Time{}, ""
	}
	loc, err := time.LoadLocation(tz)
	if err != nil || tz == "" {
		if loc, err = time.LoadLocation(notify.DefaultTZ); err != nil {
			loc = time.UTC
		}
	}
	if toS == "" {
		toS = fromS
	}
	from, err1 := time.ParseInLocation("2006-01-02", fromS, loc)
	to, err2 := time.ParseInLocation("2006-01-02", toS, loc)
	if err1 != nil || err2 != nil {
		return time.Time{}, time.Time{}, "período inválido: use datas no formato AAAA-MM-DD"
	}
	if to.Before(from) {
		return time.Time{}, time.Time{}, "período inválido: a data final é anterior à inicial"
	}
	if to.Sub(from) > 365*24*time.Hour+time.Hour {
		return time.Time{}, time.Time{}, "período longo demais: escolha até 366 dias"
	}
	return from, to, ""
}

// dashboard devolve os dados do painel: ?days=7|14|30 (padrão 14) e ?tz= (fuso do navegador, para agrupar por dia).
func (s *Server) dashboard(w http.ResponseWriter, r *http.Request, _ *ctxUser) {
	days := 14
	switch r.URL.Query().Get("days") {
	case "7":
		days = 7
	case "30":
		days = 30
	}
	loc, err := time.LoadLocation(r.URL.Query().Get("tz"))
	if err != nil || r.URL.Query().Get("tz") == "" {
		if loc, err = time.LoadLocation(notify.DefaultTZ); err != nil {
			loc = time.UTC
		}
	}
	var o *notify.Overview
	if r.URL.Query().Get("from") != "" {
		from, to, perr := periodParams(r.URL.Query().Get("from"), r.URL.Query().Get("to"), r.URL.Query().Get("tz"))
		if perr != "" {
			writeErr(w, 400, perr)
			return
		}
		o, err = notify.BuildOverviewRange(r.Context(), s.Store, s.clock(), from.Location(), from, to)
	} else {
		o, err = notify.BuildOverview(r.Context(), s.Store, s.clock(), loc, days)
	}
	if err != nil {
		writeErr(w, 500, "erro interno")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, o)
}

// ---------------------------------------------------------------- consultas ao agente (contêineres, teste de banco)

func (s *Server) agentCall(w http.ResponseWriter, r *http.Request, agentID int64, kind string, payload []byte, timeout time.Duration) {
	if s.Agents == nil {
		writeErr(w, 503, "canal dos agentes indisponível")
		return
	}
	out, err := s.Agents.Call(r.Context(), agentID, kind, payload, timeout)
	switch {
	case errors.Is(err, agentsrv.ErrOffline):
		writeErr(w, 409, "o agente está offline")
	case err != nil && (strings.HasPrefix(err.Error(), "chamada desconhecida") || err.Error() == "área de navegação inválida"):
		writeErr(w, 422, "este agente é de uma versão antiga e não tem esta função: atualize o agente para a versão do Server")
	case err != nil:
		writeErr(w, 422, err.Error())
	default:
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(out)
	}
}

func (s *Server) agentContainers(w http.ResponseWriter, r *http.Request, _ *ctxUser) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, 400, "id inválido")
		return
	}
	s.agentCall(w, r, id, "containers", nil, 20*time.Second)
}

// dbTest valida a conexão com o banco (lista os bancos) usando a configuração informada; se a senha vier vazia e o
// job_id for informado, reaproveita a senha já salva do job.
func (s *Server) dbTest(w http.ResponseWriter, r *http.Request, _ *ctxUser) {
	id, ok := pathID(r)
	var in struct {
		DB    dbIn  `json:"db"`
		JobID int64 `json:"job_id"`
	}
	if !ok || !decode(w, r, &in) {
		if !ok {
			writeErr(w, 400, "id inválido")
		}
		return
	}
	var old *store.DBConfig
	if in.JobID > 0 && s.Box != nil {
		if j, err := s.Store.GetJob(r.Context(), in.JobID); err == nil && j.AgentID == id {
			old, _ = j.OpenDB(s.Box)
		}
	}
	cfg, msg := buildDB(&in.DB, old)
	if msg != "" {
		writeErr(w, 400, msg)
		return
	}
	payload, _ := json.Marshal(cfg)
	s.agentCall(w, r, id, "db_test", payload, 70*time.Second)
}
