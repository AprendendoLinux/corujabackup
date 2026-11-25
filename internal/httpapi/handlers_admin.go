package httpapi

import (
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/AprendendoLinux/corujabackup/internal/auth"
	"github.com/AprendendoLinux/corujabackup/internal/enroll"
	"github.com/AprendendoLinux/corujabackup/internal/store"
)

var agentNameRE = regexp.MustCompile(`^[A-Za-z0-9._-]{2,100}$`)

func pathID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id, err == nil && id > 0
}

func validRole(r string) bool { return r == "admin" || r == "operator" || r == "viewer" }

// ---------------------------------------------------------------- usuários

func (s *Server) listUsers(w http.ResponseWriter, r *http.Request, _ *ctxUser) {
	us, err := s.Store.ListUsers(r.Context())
	if err != nil {
		writeErr(w, 500, "erro interno")
		return
	}
	writeJSON(w, 200, map[string]any{"users": us})
}

func (s *Server) createUser(w http.ResponseWriter, r *http.Request, cu *ctxUser) {
	var in struct{ Username, DisplayName, Password, Role string }
	if !decodeLoose(w, r, &in) {
		return
	}
	in.Username = strings.ToLower(strings.TrimSpace(in.Username))
	if !usernameRE.MatchString(in.Username) || !validRole(in.Role) {
		writeErr(w, 400, "usuário ou perfil inválido")
		return
	}
	if err := auth.ValidatePassword(in.Password); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	hash, _ := auth.HashPassword(in.Password)
	id, err := s.Store.CreateUser(r.Context(), in.Username, strings.TrimSpace(in.DisplayName), hash, in.Role)
	if errors.Is(err, store.ErrDuplicate) {
		writeErr(w, 409, "já existe um usuário com esse nome")
		return
	}
	if err != nil {
		writeErr(w, 500, "erro interno")
		return
	}
	s.Store.Audit(r.Context(), cu.User.ID, cu.User.Username, "user.created", in.Username+" ("+in.Role+")", s.clientIP(r))
	writeJSON(w, 201, map[string]int64{"id": id})
}

func (s *Server) updateUser(w http.ResponseWriter, r *http.Request, cu *ctxUser) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, 400, "id inválido")
		return
	}
	var in struct {
		DisplayName *string `json:"display_name"`
		Role        *string `json:"role"`
		Disabled    *bool   `json:"disabled"`
		NewPassword *string `json:"new_password"`
		Reset2FA    bool    `json:"reset_2fa"`
	}
	if !decodeLoose(w, r, &in) {
		return
	}
	u, err := s.Store.UserByID(r.Context(), id)
	if err != nil {
		writeErr(w, 404, "usuário não encontrado")
		return
	}
	display, role, disabled := u.DisplayName, u.Role, u.Disabled
	if in.DisplayName != nil {
		display = strings.TrimSpace(*in.DisplayName)
	}
	if in.Role != nil {
		if !validRole(*in.Role) {
			writeErr(w, 400, "perfil inválido")
			return
		}
		role = *in.Role
	}
	if in.Disabled != nil {
		disabled = *in.Disabled
	}
	// nunca deixar o sistema sem administrador ativo, nem o administrador se trancar para fora
	losesAdmin := u.Role == "admin" && !u.Disabled && (role != "admin" || disabled)
	if losesAdmin {
		if n, _ := s.Store.CountActiveAdmins(r.Context()); n <= 1 {
			writeErr(w, 409, "é preciso manter pelo menos um administrador ativo")
			return
		}
	}
	if err := s.Store.UpdateUser(r.Context(), id, display, role, disabled); err != nil {
		writeErr(w, 500, "erro interno")
		return
	}
	kill := disabled && !u.Disabled || role != u.Role
	if in.NewPassword != nil {
		if err := auth.ValidatePassword(*in.NewPassword); err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		h, _ := auth.HashPassword(*in.NewPassword)
		_ = s.Store.SetPassword(r.Context(), id, h)
		kill = true
	}
	if in.Reset2FA {
		_ = s.Store.SetTOTP(r.Context(), id, "", false, "")
		kill = true
	}
	if kill {
		keep := ""
		if id == cu.User.ID {
			keep = cu.Session.IDHash
		}
		_ = s.Store.DeleteUserSessions(r.Context(), id, keep)
	}
	s.Store.Audit(r.Context(), cu.User.ID, cu.User.Username, "user.updated", u.Username, s.clientIP(r))
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) deleteUser(w http.ResponseWriter, r *http.Request, cu *ctxUser) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, 400, "id inválido")
		return
	}
	if id == cu.User.ID {
		writeErr(w, 409, "você não pode excluir o próprio usuário")
		return
	}
	u, err := s.Store.UserByID(r.Context(), id)
	if err != nil {
		writeErr(w, 404, "usuário não encontrado")
		return
	}
	if u.Role == "admin" && !u.Disabled {
		if n, _ := s.Store.CountActiveAdmins(r.Context()); n <= 1 {
			writeErr(w, 409, "é preciso manter pelo menos um administrador ativo")
			return
		}
	}
	_ = s.Store.DeleteUser(r.Context(), id)
	s.Store.Audit(r.Context(), cu.User.ID, cu.User.Username, "user.deleted", u.Username, s.clientIP(r))
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) audit(w http.ResponseWriter, r *http.Request, _ *ctxUser) {
	n, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if n <= 0 || n > 500 {
		n = 100
	}
	es, err := s.Store.ListAudit(r.Context(), n)
	if err != nil {
		writeErr(w, 500, "erro interno")
		return
	}
	writeJSON(w, 200, map[string]any{"entries": es})
}

// ---------------------------------------------------------------- agentes

type agentView struct {
	store.Agent
	Online      bool       `json:"online"`
	ConnectedAt *time.Time `json:"connected_at"`
}

func (s *Server) listAgents(w http.ResponseWriter, r *http.Request, _ *ctxUser) {
	ags, err := s.Store.ListAgents(r.Context())
	if err != nil {
		writeErr(w, 500, "erro interno")
		return
	}
	online := map[int64]time.Time{}
	if s.Agents != nil {
		online = s.Agents.Online()
	}
	out := make([]agentView, 0, len(ags))
	for _, a := range ags {
		v := agentView{Agent: a}
		if t, ok := online[a.ID]; ok && a.RevokedAt == nil {
			v.Online, v.ConnectedAt = true, &t
		}
		out = append(out, v)
	}
	tokens, _ := s.Store.ListPendingTokens(r.Context())
	writeJSON(w, 200, map[string]any{"agents": out, "pending_tokens": tokens})
}

func (s *Server) createToken(w http.ResponseWriter, r *http.Request, cu *ctxUser) {
	var in struct {
		Name     string `json:"name"`
		TTLHours int    `json:"ttl_hours"`
	}
	if !decodeLoose(w, r, &in) {
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if !agentNameRE.MatchString(in.Name) {
		writeErr(w, 400, "nome inválido: use 2 a 100 caracteres (letras, números, ponto, hífen ou sublinhado)")
		return
	}
	if in.TTLHours <= 0 {
		in.TTLHours = 24
	}
	if in.TTLHours > 168 {
		in.TTLHours = 168
	}
	tok, err := enroll.New(s.Fingerprint)
	if err != nil {
		writeErr(w, 500, "erro interno")
		return
	}
	parsed, _ := enroll.Parse(tok)
	exp := s.clock().Add(time.Duration(in.TTLHours) * time.Hour)
	if err := s.Store.CreateEnrollToken(r.Context(), parsed.Hash(), in.Name, cu.User.ID, exp); err != nil {
		if errors.Is(err, store.ErrDuplicate) {
			writeErr(w, 409, "já existe um agente com esse nome")
			return
		}
		writeErr(w, 500, "erro interno")
		return
	}
	s.Store.Audit(r.Context(), cu.User.ID, cu.User.Username, "agent.token_created", in.Name, s.clientIP(r))
	writeJSON(w, 201, map[string]any{"token": tok, "expires_at": exp, "name": in.Name}) // o token só aparece agora
}

func (s *Server) deleteToken(w http.ResponseWriter, r *http.Request, cu *ctxUser) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, 400, "id inválido")
		return
	}
	_ = s.Store.DeleteToken(r.Context(), id)
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) renameAgent(w http.ResponseWriter, r *http.Request, cu *ctxUser) {
	id, ok := pathID(r)
	var in struct{ Name string }
	if !ok || !decodeLoose(w, r, &in) {
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if !agentNameRE.MatchString(in.Name) {
		writeErr(w, 400, "nome inválido")
		return
	}
	if err := s.Store.RenameAgent(r.Context(), id, in.Name); err != nil {
		if errors.Is(err, store.ErrDuplicate) {
			writeErr(w, 409, "já existe um agente com esse nome")
			return
		}
		writeErr(w, 500, "erro interno")
		return
	}
	s.Store.Audit(r.Context(), cu.User.ID, cu.User.Username, "agent.renamed", in.Name, s.clientIP(r))
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) revokeAgent(w http.ResponseWriter, r *http.Request, cu *ctxUser) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, 400, "id inválido")
		return
	}
	a, err := s.Store.AgentByID(r.Context(), id)
	if err != nil {
		writeErr(w, 404, "agente não encontrado")
		return
	}
	_ = s.Store.RevokeAgent(r.Context(), id)
	if s.Agents != nil {
		s.Agents.Revoke(id, "revogado pelo administrador")
	}
	s.Store.Audit(r.Context(), cu.User.ID, cu.User.Username, "agent.revoked", a.Name, s.clientIP(r))
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) deleteAgent(w http.ResponseWriter, r *http.Request, cu *ctxUser) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, 400, "id inválido")
		return
	}
	a, err := s.Store.AgentByID(r.Context(), id)
	if err != nil {
		writeErr(w, 404, "agente não encontrado")
		return
	}
	_ = s.Store.RevokeAgent(r.Context(), id)
	if s.Agents != nil {
		s.Agents.Revoke(id, "removido pelo administrador")
	}
	_ = s.Store.DeleteAgent(r.Context(), id)
	s.Store.Audit(r.Context(), cu.User.ID, cu.User.Username, "agent.deleted", a.Name, s.clientIP(r))
	writeJSON(w, 200, map[string]bool{"ok": true})
}
