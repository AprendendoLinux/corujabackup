package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/AprendendoLinux/corujabackup/internal/auth"
	"github.com/AprendendoLinux/corujabackup/internal/store"
)

const (
	cookieName   = "coruja_session"
	sessionTTL   = 12 * time.Hour
	pendingTTL   = 5 * time.Minute
	roleAny      = 0
	roleOperator = 1
	roleAdmin    = 2
)

type ctxUser struct {
	User    *store.User
	Session *store.Session
}

func rank(role string) int {
	switch role {
	case "admin":
		return roleAdmin
	case "operator":
		return roleOperator
	}
	return roleAny
}

// authed exige sessão completa (2FA concluído, se ativo) e o papel mínimo.
func (s *Server) authed(min int, h func(http.ResponseWriter, *http.Request, *ctxUser)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cu, err := s.currentUser(r, false)
		if err != nil {
			writeErr(w, 401, "sessão inválida ou expirada")
			return
		}
		if rank(cu.User.Role) < min {
			writeErr(w, 403, "sem permissão para esta ação")
			return
		}
		// senha padrão (admin/admin da primeira subida): até trocar, só vê os próprios dados e troca a senha
		if cu.User.MustChangePassword && !(r.Method == http.MethodGet && r.URL.Path == "/api/me") && !(r.Method == http.MethodPost && r.URL.Path == "/api/me/password") {
			writeJSON(w, 403, map[string]string{"error": "troque a senha padrão antes de continuar", "code": "password_change_required"})
			return
		}
		// política "exigir 2FA": enquanto o usuário não ativar, só acessa a própria conta (para poder ativar)
		if !strings.HasPrefix(r.URL.Path, "/api/me") && s.needs2FA(r, cu.User) {
			writeJSON(w, 403, map[string]string{"error": "a política de segurança exige a verificação em duas etapas: ative o 2FA em Minha conta", "code": "2fa_required"})
			return
		}
		h(w, r, cu)
	}
}

// currentUser lê o cookie. allowPending aceita a sessão "meio logada" (senha ok, falta o código 2FA).
func (s *Server) currentUser(r *http.Request, allowPending bool) (*ctxUser, error) {
	c, err := r.Cookie(cookieName)
	if err != nil || c.Value == "" {
		return nil, errors.New("sem sessão")
	}
	se, err := s.Store.GetSession(r.Context(), auth.SHA256Hex(c.Value))
	if err != nil {
		return nil, err
	}
	if se.User.Disabled || (se.Pending2FA && !allowPending) {
		return nil, errors.New("sessão não autorizada")
	}
	if !se.Pending2FA {
		_ = s.Store.ExtendSession(r.Context(), se.IDHash, s.clock().Add(s.sessionTTL(r)))
	}
	return &ctxUser{User: se.User, Session: se}, nil
}

func (s *Server) startSession(ctx context.Context, w http.ResponseWriter, r *http.Request, userID int64, pending bool) error {
	tok, err := auth.RandomToken(32)
	if err != nil {
		return err
	}
	ttl := s.sessionTTL(r)
	if pending {
		ttl = pendingTTL
	}
	if err := s.Store.CreateSession(ctx, auth.SHA256Hex(tok), userID, pending, s.clock().Add(ttl), s.clientIP(r), r.UserAgent()); err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: tok, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode,
		Secure: s.isHTTPS(r), MaxAge: int(ttl.Seconds())})
	return nil
}

func clearCookie(w http.ResponseWriter, r *http.Request, secure bool) {
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: secure, MaxAge: -1})
}
