// Package httpapi expõe a API REST do Server: autenticação, usuários, agentes e identidade visual.
package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/AprendendoLinux/corujabackup/internal/ratelimit"
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
}

type Server struct {
	DB          *sql.DB
	Store       *store.Store
	Pinger      Pinger
	Agents      AgentHub
	Started     time.Time
	Web         http.Handler
	Fingerprint string // impressão digital do certificado do canal dos agentes
	AgentPort   string
	TrustProxy  bool

	LoginLimiter *ratelimit.Limiter
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
	mux.HandleFunc("GET /api/agents", s.authed(roleAny, s.listAgents))
	mux.HandleFunc("POST /api/agents/tokens", s.authed(roleOperator, s.createToken))
	mux.HandleFunc("DELETE /api/agents/tokens/{id}", s.authed(roleOperator, s.deleteToken))
	mux.HandleFunc("PATCH /api/agents/{id}", s.authed(roleOperator, s.renameAgent))
	mux.HandleFunc("POST /api/agents/{id}/revoke", s.authed(roleOperator, s.revokeAgent))
	mux.HandleFunc("DELETE /api/agents/{id}", s.authed(roleOperator, s.deleteAgent))
	mux.HandleFunc("GET /api/jobs", s.authed(roleAny, s.listJobs))
	mux.HandleFunc("POST /api/jobs", s.authed(roleOperator, s.createJob))
	mux.HandleFunc("PATCH /api/jobs/{id}", s.authed(roleOperator, s.updateJob))
	mux.HandleFunc("DELETE /api/jobs/{id}", s.authed(roleOperator, s.deleteJob))
	mux.HandleFunc("POST /api/jobs/{id}/run", s.authed(roleOperator, s.runJobNow))
	mux.HandleFunc("GET /api/runs", s.authed(roleAny, s.listRuns))
	mux.HandleFunc("GET /api/runs/{id}", s.authed(roleAny, s.getRun))
	mux.HandleFunc("POST /api/runs/{id}/cancel", s.authed(roleOperator, s.cancelRun))
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
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; frame-ancestors 'none'")
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
		if xf := r.Header.Get("X-Forwarded-For"); xf != "" {
			return strings.TrimSpace(strings.Split(xf, ",")[0])
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

// branding devolve nome e tema escolhidos na interface (white-label). A logo personalizada entra na Fase 5.
func (s *Server) branding(w http.ResponseWriter, r *http.Request) {
	out := map[string]string{"name": version.Name, "theme": "coruja"}
	if s.DB != nil {
		rows, err := s.DB.QueryContext(r.Context(), "SELECT k, COALESCE(v,'') FROM settings WHERE k IN ('brand.name','brand.theme')")
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var k, v string
				if rows.Scan(&k, &v) == nil && v != "" {
					out[k[len("brand."):]] = v
				}
			}
		}
	}
	writeJSON(w, 200, out)
}

func (s *Server) system(w http.ResponseWriter, r *http.Request, _ *ctxUser) {
	writeJSON(w, 200, map[string]any{"version": version.Version, "agent_port": s.AgentPort, "fingerprint": s.Fingerprint})
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
