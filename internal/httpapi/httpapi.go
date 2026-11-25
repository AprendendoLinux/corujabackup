// Package httpapi expõe a API REST do Server (Fase 0: saúde, versão e identidade visual).
package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"time"

	"github.com/AprendendoLinux/corujabackup/internal/version"
)

type Pinger interface {
	PingContext(ctx context.Context) error
}

type Server struct {
	DB      *sql.DB // pode ser nil nos testes
	Pinger  Pinger
	Started time.Time
	Web     http.Handler
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", s.health)
	mux.HandleFunc("GET /api/version", s.version)
	mux.HandleFunc("GET /api/branding", s.branding)
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 404, map[string]string{"error": "rota inexistente"}) })
	if s.Web != nil {
		mux.Handle("/", s.Web)
	}
	return secure(mux)
}

func secure(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
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

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
