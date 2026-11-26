package httpapi

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/AprendendoLinux/corujabackup/internal/auth"
	"github.com/AprendendoLinux/corujabackup/internal/store"
	"github.com/AprendendoLinux/corujabackup/internal/version"
)

var usernameRE = regexp.MustCompile(`^[a-z0-9._-]{3,64}$`)

const loginFailMsg = "Usuário ou senha incorretos"

func (s *Server) setupStatus(w http.ResponseWriter, r *http.Request) {
	n, err := s.Store.CountUsers(r.Context())
	if err != nil {
		writeErr(w, 500, "erro interno")
		return
	}
	writeJSON(w, 200, map[string]bool{"needs_setup": n == 0, "needs_code": n == 0 && s.SetupCode != ""})
}

// setup cria o primeiro administrador (só funciona enquanto não existe nenhum usuário).
func (s *Server) setup(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Username, DisplayName, Password string
		SetupCode                       string `json:"setup_code"`
	}
	if !decodeLoose(w, r, &in) {
		return
	}
	if s.SetupCode != "" {
		ip := s.clientIP(r)
		if !s.LoginLimiter.Allow("setup:" + ip) {
			writeErr(w, 429, "muitas tentativas; aguarde alguns minutos")
			return
		}
		if subtle.ConstantTimeCompare([]byte(strings.TrimSpace(in.SetupCode)), []byte(s.SetupCode)) != 1 {
			s.LoginLimiter.Fail("setup:" + ip)
			writeErr(w, 403, "código de configuração incorreto: veja nos logs do Server (docker logs ...) o código desta execução")
			return
		}
	}
	in.Username = strings.ToLower(strings.TrimSpace(in.Username))
	if !usernameRE.MatchString(in.Username) {
		writeErr(w, 400, "usuário inválido: use 3 a 64 caracteres (letras minúsculas, números, ponto, hífen ou sublinhado)")
		return
	}
	if err := auth.ValidatePassword(in.Password); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	hash, err := auth.HashPassword(in.Password)
	if err != nil {
		writeErr(w, 500, "erro interno")
		return
	}
	id, err := s.Store.CreateFirstAdmin(r.Context(), in.Username, strings.TrimSpace(in.DisplayName), hash)
	if errors.Is(err, store.ErrDuplicate) {
		writeErr(w, 409, "a configuração inicial já foi feita")
		return
	}
	if err != nil {
		writeErr(w, 500, "erro interno")
		return
	}
	s.Store.Audit(r.Context(), id, in.Username, "setup", "primeiro administrador criado", s.clientIP(r))
	if err := s.startSession(r.Context(), w, r, id, false); err != nil {
		writeErr(w, 500, "erro interno")
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var in struct{ Username, Password string }
	if !decodeLoose(w, r, &in) {
		return
	}
	ip := s.clientIP(r)
	name := strings.ToLower(strings.TrimSpace(in.Username))
	if !s.LoginLimiter.Allow("ip:"+ip) || !s.LoginLimiter.Allow("u:"+name) {
		writeErr(w, 429, "muitas tentativas; aguarde alguns minutos")
		return
	}
	u, err := s.Store.UserByUsername(r.Context(), name)
	hash := auth.DummyHash // gasta o mesmo tempo quando o usuário não existe
	if err == nil {
		hash = u.PasswordHash
	}
	okPass := auth.CheckPassword(in.Password, hash)
	if err != nil || !okPass || u.Disabled {
		s.LoginLimiter.Fail("ip:" + ip)
		s.LoginLimiter.Fail("u:" + name)
		s.Store.Audit(r.Context(), 0, name, "login.failed", "", ip)
		writeErr(w, 401, loginFailMsg)
		return
	}
	if u.TOTPEnabled {
		if err := s.startSession(r.Context(), w, r, u.ID, true); err != nil {
			writeErr(w, 500, "erro interno")
			return
		}
		writeJSON(w, 200, map[string]bool{"ok": true, "needs_2fa": true})
		return
	}
	s.finishLogin(w, r, u)
}

func (s *Server) finishLogin(w http.ResponseWriter, r *http.Request, u *store.User) {
	s.LoginLimiter.Reset("u:" + u.Username)
	_ = s.Store.TouchLogin(r.Context(), u.ID)
	s.Store.Audit(r.Context(), u.ID, u.Username, "login", "", s.clientIP(r))
	if err := s.startSession(r.Context(), w, r, u.ID, false); err != nil {
		writeErr(w, 500, "erro interno")
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true, "needs_2fa": false})
}

// login2FA conclui o login com o código TOTP (ou um código de recuperação). A sessão é trocada por uma nova
// (evita fixação de sessão).
func (s *Server) login2FA(w http.ResponseWriter, r *http.Request) {
	var in struct{ Code string }
	if !decodeLoose(w, r, &in) {
		return
	}
	cu, err := s.currentUser(r, true)
	if err != nil || !cu.Session.Pending2FA {
		writeErr(w, 401, "faça login novamente")
		return
	}
	key := "2fa:" + cu.User.Username
	if !s.LoginLimiter.Allow(key) {
		_ = s.Store.DeleteSession(r.Context(), cu.Session.IDHash)
		writeErr(w, 429, "muitas tentativas; faça login novamente mais tarde")
		return
	}
	if !s.checkSecondFactor(r, cu.User, in.Code) {
		s.LoginLimiter.Fail(key)
		s.Store.Audit(r.Context(), cu.User.ID, cu.User.Username, "login.2fa_failed", "", s.clientIP(r))
		writeErr(w, 401, "código inválido")
		return
	}
	s.LoginLimiter.Reset(key)
	_ = s.Store.DeleteSession(r.Context(), cu.Session.IDHash)
	s.finishLogin(w, r, cu.User)
}

// checkSecondFactor valida TOTP (com anti-replay) ou consome um código de recuperação.
func (s *Server) checkSecondFactor(r *http.Request, u *store.User, code string) bool {
	code = strings.TrimSpace(strings.ToLower(code))
	if len(code) == 6 {
		fresh, err := s.Store.UserByID(r.Context(), u.ID)
		if err != nil {
			return false
		}
		if c, ok := auth.VerifyTOTP(fresh.TOTPSecret, code, s.clock(), fresh.TOTPLastCounter); ok {
			_ = s.Store.SetTOTPCounter(r.Context(), u.ID, c)
			return true
		}
		return false
	}
	var hashes []string
	if u.RecoveryCodes == "" || json.Unmarshal([]byte(u.RecoveryCodes), &hashes) != nil {
		return false
	}
	h := auth.SHA256Hex(code)
	for i, x := range hashes {
		if x == h {
			hashes = append(hashes[:i], hashes[i+1:]...)
			b, _ := json.Marshal(hashes)
			_ = s.Store.SetRecoveryCodes(r.Context(), u.ID, string(b))
			s.Store.Audit(r.Context(), u.ID, u.Username, "2fa.recovery_used", "", s.clientIP(r))
			return true
		}
	}
	return false
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(cookieName); err == nil {
		_ = s.Store.DeleteSession(r.Context(), auth.SHA256Hex(c.Value))
	}
	clearCookie(w, r, s.isHTTPS(r))
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) me(w http.ResponseWriter, r *http.Request, cu *ctxUser) {
	left := 0
	var codes []string
	if json.Unmarshal([]byte(cu.User.RecoveryCodes), &codes) == nil {
		left = len(codes)
	}
	writeJSON(w, 200, map[string]any{"user": cu.User, "recovery_codes_left": left, "version": version.Version})
}

func (s *Server) changePassword(w http.ResponseWriter, r *http.Request, cu *ctxUser) {
	var in struct{ Current, New string }
	if !decodeLoose(w, r, &in) {
		return
	}
	if !auth.CheckPassword(in.Current, cu.User.PasswordHash) {
		writeErr(w, 400, "a senha atual está incorreta")
		return
	}
	if err := auth.ValidatePassword(in.New); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	hash, err := auth.HashPassword(in.New)
	if err != nil {
		writeErr(w, 500, "erro interno")
		return
	}
	_ = s.Store.SetPassword(r.Context(), cu.User.ID, hash)
	_ = s.Store.DeleteUserSessions(r.Context(), cu.User.ID, cu.Session.IDHash)
	s.Store.Audit(r.Context(), cu.User.ID, cu.User.Username, "password.changed", "", s.clientIP(r))
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// twoFABegin gera o segredo e devolve o link otpauth:// (a interface desenha o QR). Só vale após /enable.
func (s *Server) twoFABegin(w http.ResponseWriter, r *http.Request, cu *ctxUser) {
	if cu.User.TOTPEnabled {
		writeErr(w, 409, "o 2FA já está ativo")
		return
	}
	secret, err := auth.NewTOTPSecret()
	if err != nil {
		writeErr(w, 500, "erro interno")
		return
	}
	if err := s.Store.SetTOTP(r.Context(), cu.User.ID, secret, false, ""); err != nil {
		writeErr(w, 500, "erro interno")
		return
	}
	writeJSON(w, 200, map[string]string{"secret": secret, "uri": auth.OTPAuthURI(version.Name, cu.User.Username, secret)})
}

func (s *Server) twoFAEnable(w http.ResponseWriter, r *http.Request, cu *ctxUser) {
	var in struct{ Code string }
	if !decodeLoose(w, r, &in) {
		return
	}
	if cu.User.TOTPEnabled || cu.User.TOTPSecret == "" {
		writeErr(w, 409, "inicie a ativação do 2FA primeiro")
		return
	}
	c, ok := auth.VerifyTOTP(cu.User.TOTPSecret, in.Code, s.clock(), 0)
	if !ok {
		writeErr(w, 400, "código inválido")
		return
	}
	codes, err := auth.NewRecoveryCodes(8)
	if err != nil {
		writeErr(w, 500, "erro interno")
		return
	}
	hashes := make([]string, len(codes))
	for i, x := range codes {
		hashes[i] = auth.SHA256Hex(x)
	}
	hj, _ := json.Marshal(hashes)
	if err := s.Store.SetTOTP(r.Context(), cu.User.ID, cu.User.TOTPSecret, true, string(hj)); err != nil {
		writeErr(w, 500, "erro interno")
		return
	}
	_ = s.Store.SetTOTPCounter(r.Context(), cu.User.ID, c)
	s.Store.Audit(r.Context(), cu.User.ID, cu.User.Username, "2fa.enabled", "", s.clientIP(r))
	writeJSON(w, 200, map[string]any{"recovery_codes": codes}) // mostrados UMA vez
}

func (s *Server) twoFADisable(w http.ResponseWriter, r *http.Request, cu *ctxUser) {
	var in struct{ Password, Code string }
	if !decodeLoose(w, r, &in) {
		return
	}
	if !cu.User.TOTPEnabled {
		writeErr(w, 409, "o 2FA não está ativo")
		return
	}
	if !auth.CheckPassword(in.Password, cu.User.PasswordHash) || !s.checkSecondFactor(r, cu.User, in.Code) {
		writeErr(w, 400, "senha ou código incorretos")
		return
	}
	_ = s.Store.SetTOTP(r.Context(), cu.User.ID, "", false, "")
	s.Store.Audit(r.Context(), cu.User.ID, cu.User.Username, "2fa.disabled", "", s.clientIP(r))
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// decodeLoose aceita campos em minúsculas (username) mapeados para os campos exportados do struct.
func decodeLoose(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		writeErr(w, 400, "requisição inválida")
		return false
	}
	return true
}
