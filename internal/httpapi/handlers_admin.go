package httpapi

import (
	"errors"
	"github.com/AprendendoLinux/corujabackup/internal/agentsrv"
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
	var in struct {
		Username, Password, Role string
		nameInput
	}
	if !decodeLoose(w, r, &in) {
		return
	}
	first, last, bad := in.resolve("", "")
	if bad != "" {
		writeErr(w, 400, bad)
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
	id, err := s.Store.CreateUser(r.Context(), in.Username, first, last, hash, in.Role)
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
		nameInput
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
	role, disabled := u.Role, u.Disabled
	first, last, bad := in.resolve(u.FirstName, u.LastName)
	if bad != "" {
		writeErr(w, 400, bad)
		return
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
	if err := s.Store.UpdateUser(r.Context(), id, first, last, role, disabled); err != nil {
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
	Online      bool                `json:"online"`
	ConnectedAt *time.Time          `json:"connected_at"`
	Link        *agentsrv.LinkState `json:"link,omitempty"` // situação da conexão do Server com o agente (quando offline)
}

type pendingView struct {
	store.PendingToken
	Link *agentsrv.LinkState `json:"link,omitempty"`
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
	links := map[string]agentsrv.LinkState{}
	if s.Agents != nil {
		links = s.Agents.Links()
	}
	out := make([]agentView, 0, len(ags))
	for _, a := range ags {
		v := agentView{Agent: a}
		if t, ok := online[a.ID]; ok && a.RevokedAt == nil {
			v.Online, v.ConnectedAt = true, &t
		}
		if l, ok := links[a.Address]; ok && a.RevokedAt == nil && !v.Online {
			v.Link = &l
		}
		out = append(out, v)
	}
	tokens, _ := s.Store.ListPendingTokens(r.Context())
	pending := make([]pendingView, 0, len(tokens))
	for _, t := range tokens {
		p := pendingView{PendingToken: t}
		if l, ok := links[t.Address]; ok {
			p.Link = &l
		}
		pending = append(pending, p)
	}
	writeJSON(w, 200, map[string]any{"agents": out, "pending_tokens": pending})
}

func (s *Server) createToken(w http.ResponseWriter, r *http.Request, cu *ctxUser) {
	var in struct {
		Name     string `json:"name"`
		TTLHours int    `json:"ttl_hours"`
		Address  string `json:"address"`
	}
	if !decodeLoose(w, r, &in) {
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if !agentNameRE.MatchString(in.Name) {
		writeErr(w, 400, "nome inválido: use 2 a 100 caracteres (letras, números, ponto, hífen ou sublinhado)")
		return
	}
	addr, ok := parseAgentAddress(in.Address)
	if !ok || addr == "" {
		writeErr(w, 400, "informe o endereço do agente: IP ou nome e a porta (ex.: 192.0.2.10:23847)")
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
	if err := s.Store.CreateEnrollTokenAt(r.Context(), parsed.Hash(), in.Name, addr, cu.User.ID, exp); err != nil {
		if errors.Is(err, store.ErrDuplicate) {
			writeErr(w, 409, "já existe um agente com esse nome")
			return
		}
		writeErr(w, 500, "erro interno")
		return
	}
	s.Store.Audit(r.Context(), cu.User.ID, cu.User.Username, "agent.token_created", in.Name+" "+addr, s.clientIP(r))
	if s.Agents != nil {
		s.Agents.Wake()
	}
	writeJSON(w, 201, map[string]any{"token": tok, "expires_at": exp, "name": in.Name, "address": addr}) // o token só aparece agora
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
	var in struct {
		Name    *string `json:"name"`
		Address *string `json:"address"`
	}
	if !ok || !decodeLoose(w, r, &in) {
		return
	}
	if in.Address != nil {
		addr, ok := parseAgentAddress(*in.Address)
		if !ok || addr == "" {
			writeErr(w, 400, "endereço inválido: use IP ou nome e a porta (ex.: 192.0.2.10:23847)")
			return
		}
		if err := s.Store.SetAgentAddress(r.Context(), id, addr); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				writeErr(w, 404, "agente não encontrado")
				return
			}
			writeErr(w, 500, "erro interno")
			return
		}
		s.Store.Audit(r.Context(), cu.User.ID, cu.User.Username, "agent.address", addr, s.clientIP(r))
		if s.Agents != nil {
			s.Agents.Wake()
		}
		if in.Name == nil {
			writeJSON(w, 200, map[string]bool{"ok": true})
			return
		}
	}
	if in.Name == nil {
		writeErr(w, 400, "nada para alterar")
		return
	}
	name := strings.TrimSpace(*in.Name)
	if !agentNameRE.MatchString(name) {
		writeErr(w, 400, "nome inválido")
		return
	}
	in.Name = &name
	if err := s.Store.RenameAgent(r.Context(), id, name); err != nil {
		if errors.Is(err, store.ErrDuplicate) {
			writeErr(w, 409, "já existe um agente com esse nome")
			return
		}
		writeErr(w, 500, "erro interno")
		return
	}
	s.Store.Audit(r.Context(), cu.User.ID, cu.User.Username, "agent.renamed", name, s.clientIP(r))
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
