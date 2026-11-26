// Package httpapi expõe a API REST do Server: autenticação, usuários, agentes e identidade visual.
package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/AprendendoLinux/corujabackup/internal/cloud"
	"github.com/AprendendoLinux/corujabackup/internal/cloudmgr"
	"github.com/AprendendoLinux/corujabackup/internal/netdest"
	"github.com/AprendendoLinux/corujabackup/internal/pb"
	"github.com/AprendendoLinux/corujabackup/internal/ratelimit"
	"github.com/AprendendoLinux/corujabackup/internal/secrets"
	"github.com/AprendendoLinux/corujabackup/internal/store"
	"github.com/AprendendoLinux/corujabackup/internal/version"
)

type Pinger interface {
	PingContext(ctx context.Context) error
}

// AgentHub é o que a API precisa do serviço gRPC (quem está online; derrubar um agente revogado).
type AgentHub interface {
	Online() map[int64]time.Time
	Revoke(id int64, reason string)
	PushJobs(ctx context.Context, agentID int64)
	RunNow(agentID, jobID int64, runUID string) error
	CancelRun(agentID int64, runUID string) error
	Call(ctx context.Context, agentID int64, kind string, payload []byte, timeout time.Duration) ([]byte, error)
	Action(agentID int64, act *pb.AgentAction) error
}

// CloudAdmin é o que a API precisa do gerenciador de contas de nuvem (cloudmgr.Manager).
type CloudAdmin interface {
	Create(ctx context.Context, name, typ string, cfg cloudmgr.AccountConfig) (int64, *cloud.Device, error)
	Reauthorize(ctx context.Context, id int64) (*cloud.Device, error)
	PollDevice(ctx context.Context, id int64) (string, *cloud.Device, error)
	Test(ctx context.Context, id int64) (string, cloud.Quota, error)
	Invalidate(id int64)
	BeginWeb(ctx context.Context, name, typ, origin string) (loginURL, state string, err error)
	BeginWebReauth(ctx context.Context, id int64, origin string) (loginURL, state string, err error)
	UsesPopup(ctx context.Context, id int64) bool
	CreateDestination(ctx context.Context, name string, cfg netdest.Config) (int64, error)
	DestConfig(ctx context.Context, id int64) (*netdest.Config, error)
	FinishWeb(ctx context.Context, state, code string) (int64, error)
	Open(ctx context.Context, accountID int64, folder, name string) (io.ReadCloser, int64, error)
	Folders(ctx context.Context, accountID int64, parent string) ([]string, error)
}

type Server struct {
	DB          *sql.DB
	Store       *store.Store
	Pinger      Pinger
	Agents      AgentHub
	Clouds      CloudAdmin
	Box         *secrets.Box
	Notifier    NotifyAdmin
	Started     time.Time
	Web         http.Handler
	Fingerprint string // impressão digital do certificado do canal dos agentes
	AgentPort   string
	TrustProxy  bool
	SetupCode   string // código de uma execução, mostrado nos logs, exigido na configuração inicial

	LoginLimiter *ratelimit.Limiter
	pol          policyCache
	sseTotal     atomic.Int32
	sseUser      sync.Map
	now          func() time.Time
}

func (s *Server) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

func (s *Server) Routes() http.Handler {
	if s.LoginLimiter == nil {
		s.LoginLimiter = ratelimit.New(5, 15*time.Minute)
	}
	mux := http.NewServeMux()
	// públicas
	mux.HandleFunc("GET /api/health", s.health)
	mux.HandleFunc("GET /api/version", s.version)
	mux.HandleFunc("GET /api/branding", s.branding)
	mux.HandleFunc("GET /api/branding/logo", s.logo)
	mux.HandleFunc("PUT /api/branding", s.authed(roleAdmin, s.saveBranding))
	mux.HandleFunc("POST /api/branding/logo", s.authed(roleAdmin, s.uploadLogo))
	mux.HandleFunc("GET /api/branding/themes/{id}/logo", s.themeLogo)
	mux.HandleFunc("POST /api/branding/themes/import", s.authed(roleAdmin, s.importTheme))
	mux.HandleFunc("DELETE /api/branding/logo", s.authed(roleAdmin, s.deleteLogo))
	mux.HandleFunc("GET /api/setup/status", s.setupStatus)
	mux.HandleFunc("POST /api/setup", s.setup)
	mux.HandleFunc("POST /api/login", s.login)
	mux.HandleFunc("POST /api/login/2fa", s.login2FA)
	mux.HandleFunc("POST /api/logout", s.logout)
	// autenticadas
	mux.HandleFunc("GET /api/me", s.authed(roleAny, s.me))
	mux.HandleFunc("POST /api/me/password", s.authed(roleAny, s.changePassword))
	mux.HandleFunc("POST /api/me/2fa/begin", s.authed(roleAny, s.twoFABegin))
	mux.HandleFunc("POST /api/me/2fa/enable", s.authed(roleAny, s.twoFAEnable))
	mux.HandleFunc("POST /api/me/2fa/disable", s.authed(roleAny, s.twoFADisable))
	mux.HandleFunc("GET /api/system", s.authed(roleAny, s.system))
	mux.HandleFunc("GET /api/users", s.authed(roleAdmin, s.listUsers))
	mux.HandleFunc("POST /api/users", s.authed(roleAdmin, s.createUser))
	mux.HandleFunc("PATCH /api/users/{id}", s.authed(roleAdmin, s.updateUser))
	mux.HandleFunc("DELETE /api/users/{id}", s.authed(roleAdmin, s.deleteUser))
	mux.HandleFunc("GET /api/audit", s.authed(roleAdmin, s.audit))
	mux.HandleFunc("GET /api/audit.csv", s.authed(roleAdmin, s.auditCSV))
	mux.HandleFunc("GET /api/security", s.authed(roleAdmin, s.getSecurity))
	mux.HandleFunc("PUT /api/security", s.authed(roleAdmin, s.saveSecurity))
	mux.HandleFunc("POST /api/security/metrics-token", s.authed(roleAdmin, s.newMetricsToken))
	mux.HandleFunc("DELETE /api/security/metrics-token", s.authed(roleAdmin, s.deleteMetricsToken))
	mux.HandleFunc("GET /metrics", s.metrics)
	mux.HandleFunc("GET /api/me/sessions", s.authed(roleAny, s.mySessions))
	mux.HandleFunc("DELETE /api/me/sessions/{id}", s.authed(roleAny, s.revokeSession))
	mux.HandleFunc("POST /api/me/sessions/revoke-others", s.authed(roleAny, s.revokeOtherSessions))
	mux.HandleFunc("GET /api/agents", s.authed(roleAny, s.listAgents))
	mux.HandleFunc("POST /api/agents/tokens", s.authed(roleOperator, s.createToken))
	mux.HandleFunc("DELETE /api/agents/tokens/{id}", s.authed(roleOperator, s.deleteToken))
	mux.HandleFunc("PATCH /api/agents/{id}", s.authed(roleOperator, s.renameAgent))
	mux.HandleFunc("POST /api/agents/{id}/revoke", s.authed(roleOperator, s.revokeAgent))
	mux.HandleFunc("DELETE /api/agents/{id}", s.authed(roleOperator, s.deleteAgent))
	mux.HandleFunc("GET /api/agents/{id}/containers", s.authed(roleOperator, s.agentContainers))
	mux.HandleFunc("GET /api/agents/{id}/browse", s.authed(roleOperator, s.agentBrowse))
	mux.HandleFunc("PUT /api/system/agent-address", s.authed(roleAdmin, s.saveAgentAddress))
	mux.HandleFunc("POST /api/agents/{id}/db-test", s.authed(roleOperator, s.dbTest))
	mux.HandleFunc("GET /api/notifications/channels", s.authed(roleAdmin, s.listChannels))
	mux.HandleFunc("POST /api/notifications/channels", s.authed(roleAdmin, s.createChannel))
	mux.HandleFunc("PATCH /api/notifications/channels/{id}", s.authed(roleAdmin, s.updateChannel))
	mux.HandleFunc("DELETE /api/notifications/channels/{id}", s.authed(roleAdmin, s.deleteChannel))
	mux.HandleFunc("POST /api/notifications/channels/{id}/test", s.authed(roleAdmin, s.testChannel))
	mux.HandleFunc("GET /api/notifications/rules", s.authed(roleAdmin, s.listRules))
	mux.HandleFunc("POST /api/notifications/rules", s.authed(roleAdmin, s.createRule))
	mux.HandleFunc("PATCH /api/notifications/rules/{id}", s.authed(roleAdmin, s.updateRule))
	mux.HandleFunc("DELETE /api/notifications/rules/{id}", s.authed(roleAdmin, s.deleteRule))
	mux.HandleFunc("POST /api/notifications/rules/{id}/send-now", s.authed(roleAdmin, s.sendRuleNow))
	mux.HandleFunc("GET /api/notifications/log", s.authed(roleAdmin, s.notifyLog))
	mux.HandleFunc("GET /api/notifications/settings", s.authed(roleAdmin, s.getNotifySettings))
	mux.HandleFunc("PUT /api/notifications/settings", s.authed(roleAdmin, s.saveNotifySettings))
	mux.HandleFunc("GET /api/reports/daily.pdf", s.authed(roleOperator, s.dailyPDF))
	mux.HandleFunc("GET /api/clouds", s.authed(roleAny, s.listClouds))
	mux.HandleFunc("POST /api/clouds", s.authed(roleAdmin, s.createCloud))
	mux.HandleFunc("POST /api/destinations/test", s.authed(roleAdmin, s.testDestination))
	mux.HandleFunc("POST /api/destinations", s.authed(roleAdmin, s.createDestination))
	mux.HandleFunc("POST /api/runs/{id}/fetch", s.authed(roleOperator, s.fetchRun))
	mux.HandleFunc("POST /api/clouds/oauth/begin", s.authed(roleAdmin, s.beginCloudWeb))
	mux.HandleFunc("POST /api/clouds/oauth/finish", s.authed(roleAdmin, s.finishCloudWeb))
	mux.HandleFunc("GET /api/clouds/{id}/folders", s.authed(roleOperator, s.destFolders))
	mux.HandleFunc("POST /api/clouds/{id}/poll", s.authed(roleAdmin, s.pollCloud))
	mux.HandleFunc("POST /api/clouds/{id}/reauthorize", s.authed(roleAdmin, s.reauthCloud))
	mux.HandleFunc("POST /api/clouds/{id}/test", s.authed(roleOperator, s.testCloud))
	mux.HandleFunc("DELETE /api/clouds/{id}", s.authed(roleAdmin, s.deleteCloud))
	mux.HandleFunc("GET /api/jobs", s.authed(roleAny, s.listJobs))
	mux.HandleFunc("POST /api/jobs", s.authed(roleOperator, s.createJob))
	mux.HandleFunc("PATCH /api/jobs/{id}", s.authed(roleOperator, s.updateJob))
	mux.HandleFunc("DELETE /api/jobs/{id}", s.authed(roleOperator, s.deleteJob))
	mux.HandleFunc("POST /api/jobs/{id}/run", s.authed(roleOperator, s.runJobNow))
	mux.HandleFunc("GET /api/runs", s.authed(roleAny, s.listRuns))
	mux.HandleFunc("GET /api/runs/{id}", s.authed(roleAny, s.getRun))
	mux.HandleFunc("GET /api/runs/{id}/stream", s.authed(roleAny, s.streamRun))
	mux.HandleFunc("POST /api/runs/{id}/cancel", s.authed(roleOperator, s.cancelRun))
	mux.HandleFunc("GET /api/runs/{id}/download", s.authed(roleOperator, s.downloadRun))
	mux.HandleFunc("POST /api/runs/{id}/reupload", s.authed(roleOperator, s.reuploadRun))
	mux.HandleFunc("POST /api/runs/{id}/extract", s.authed(roleOperator, s.extractRun))
	mux.HandleFunc("GET /api/stats", s.authed(roleAny, s.stats))
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 404, map[string]string{"error": "rota inexistente"})
	})
	if s.Web != nil {
		mux.Handle("/", s.Web)
	}
	return s.secure(mux)
}

// secure: cabeçalhos de segurança + proteção CSRF (Origin igual ao Host e cabeçalho customizado nas escritas).
func (s *Server) secure(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=(), usb=()")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		h.Set("Cross-Origin-Resource-Policy", "same-origin")
		if s.isHTTPS(r) {
			h.Set("Strict-Transport-Security", "max-age=15552000")
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions {
			if o := r.Header.Get("Origin"); o != "" {
				if u, err := url.Parse(o); err != nil || u.Host != r.Host {
					writeJSON(w, 403, map[string]string{"error": "origem não permitida"})
					return
				}
			}
			if strings.HasPrefix(r.URL.Path, "/api/") && r.Header.Get("X-Coruja-CSRF") != "1" {
				writeJSON(w, 403, map[string]string{"error": "requisição recusada (CSRF)"})
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) clientIP(r *http.Request) string {
	if s.TrustProxy {
		// O proxy confiável ACRESCENTA o endereço de quem o contatou ao fim da lista; os da esquerda podem ter sido forjados pelo
		// cliente. Vale, portanto, o último.
		if xf := r.Header.Get("X-Forwarded-For"); xf != "" {
			parts := strings.Split(xf, ",")
			if ip := strings.TrimSpace(parts[len(parts)-1]); net.ParseIP(ip) != nil {
				return ip
			}
		}
	}
	h, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return h
}

func (s *Server) isHTTPS(r *http.Request) bool {
	return r.TLS != nil || (s.TrustProxy && strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https"))
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	status, code, dbOK := "ok", 200, true
	if s.Pinger != nil {
		if err := s.Pinger.PingContext(ctx); err != nil {
			status, code, dbOK = "degraded", 503, false
		}
	}
	writeJSON(w, code, map[string]any{"status": status, "database": dbOK, "version": version.Version,
		"uptime_seconds": int(time.Since(s.Started).Seconds())})
}

func (s *Server) version(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]string{"name": version.Name, "version": version.Version})
}

func (s *Server) system(w http.ResponseWriter, r *http.Request, _ *ctxUser) {
	set, err := s.Store.GetSettings(r.Context(), settingAgentAddress)
	if err != nil {
		writeErr(w, 500, "erro interno")
		return
	}
	writeJSON(w, 200, map[string]any{"version": version.Version, "agent_port": s.AgentPort, "agent_address": set[settingAgentAddress], "fingerprint": s.Fingerprint})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeErr(w, 400, "requisição inválida")
		return false
	}
	return true
}

// Housekeeping faz a limpeza periódica de memória (limitadores de tentativas).
func (s *Server) Housekeeping() {
	if s.LoginLimiter != nil {
		s.LoginLimiter.Sweep()
	}
}
