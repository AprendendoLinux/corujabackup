// Package store concentra o acesso ao MySQL (usuários, sessões, agentes, matrículas e auditoria).
package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

type Store struct{ DB *sql.DB }

var (
	ErrNotFound  = errors.New("não encontrado")
	ErrDuplicate = errors.New("já existe")
)

func New(db *sql.DB) *Store { return &Store{DB: db} }

func isDup(err error) bool {
	return err != nil && (strings.Contains(err.Error(), "Error 1062") || strings.Contains(err.Error(), "Duplicate entry"))
}

// ---------------------------------------------------------------- usuários

type User struct {
	ID              int64      `json:"id"`
	Username        string     `json:"username"`
	DisplayName     string     `json:"display_name"` // sempre "nome sobrenome" (ou vazio)
	FirstName       string     `json:"first_name"`
	LastName        string     `json:"last_name"`
	PasswordHash    string     `json:"-"`
	Role            string     `json:"role"`
	TOTPSecret      string     `json:"-"`
	TOTPEnabled     bool       `json:"totp_enabled"`
	TOTPLastCounter int64      `json:"-"`
	RecoveryCodes   string     `json:"-"`
	Disabled        bool       `json:"disabled"`
	CreatedAt       time.Time  `json:"created_at"`
	LastLoginAt     *time.Time `json:"last_login_at"`
	AvatarVersion   string     `json:"avatar_version"` // vazio = sem foto
}

const userCols = "id, username, display_name, password_hash, role, COALESCE(totp_secret,''), totp_enabled, totp_last_counter, COALESCE(recovery_codes,''), disabled, created_at, last_login_at, avatar_version, first_name, last_name"

func scanUser(r interface{ Scan(...any) error }) (*User, error) {
	var u User
	var ll sql.NullTime
	if err := r.Scan(&u.ID, &u.Username, &u.DisplayName, &u.PasswordHash, &u.Role, &u.TOTPSecret, &u.TOTPEnabled,
		&u.TOTPLastCounter, &u.RecoveryCodes, &u.Disabled, &u.CreatedAt, &ll, &u.AvatarVersion, &u.FirstName, &u.LastName); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if ll.Valid {
		t := ll.Time
		u.LastLoginAt = &t
	}
	return &u, nil
}

func (s *Store) CountUsers(ctx context.Context) (n int, err error) {
	err = s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM users").Scan(&n)
	return
}

// CountActiveAdmins evita que o último administrador seja removido/desativado.
func (s *Store) CountActiveAdmins(ctx context.Context) (n int, err error) {
	err = s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM users WHERE role='admin' AND disabled=0").Scan(&n)
	return
}

// FullName junta nome e sobrenome (o display_name guardado).
func FullName(first, last string) string {
	return strings.TrimSpace(strings.TrimSpace(first) + " " + strings.TrimSpace(last))
}

func (s *Store) CreateUser(ctx context.Context, username, first, last, hash, role string) (int64, error) {
	res, err := s.DB.ExecContext(ctx, "INSERT INTO users (username, display_name, first_name, last_name, password_hash, role) VALUES (?,?,?,?,?,?)",
		username, FullName(first, last), first, last, hash, role)
	if err != nil {
		if isDup(err) {
			return 0, ErrDuplicate
		}
		return 0, err
	}
	return res.LastInsertId()
}

// CreateFirstAdmin cria o primeiro usuário só se a tabela estiver vazia (atômico: dois pedidos simultâneos no
// assistente inicial não criam dois administradores).
func (s *Store) CreateFirstAdmin(ctx context.Context, username, first, last, hash string) (int64, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var n int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM users FOR UPDATE").Scan(&n); err != nil {
		return 0, err
	}
	if n > 0 {
		return 0, ErrDuplicate
	}
	res, err := tx.ExecContext(ctx, "INSERT INTO users (username, display_name, first_name, last_name, password_hash, role) VALUES (?,?,?,?,?, 'admin')",
		username, FullName(first, last), first, last, hash)
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	return id, tx.Commit()
}

func (s *Store) UserByUsername(ctx context.Context, name string) (*User, error) {
	return scanUser(s.DB.QueryRowContext(ctx, "SELECT "+userCols+" FROM users WHERE username=?", name))
}

func (s *Store) UserByID(ctx context.Context, id int64) (*User, error) {
	return scanUser(s.DB.QueryRowContext(ctx, "SELECT "+userCols+" FROM users WHERE id=?", id))
}

func (s *Store) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT "+userCols+" FROM users ORDER BY username")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *u)
	}
	return out, rows.Err()
}

func (s *Store) UpdateUser(ctx context.Context, id int64, first, last, role string, disabled bool) error {
	_, err := s.DB.ExecContext(ctx, "UPDATE users SET display_name=?, first_name=?, last_name=?, role=?, disabled=? WHERE id=?",
		FullName(first, last), first, last, role, disabled, id)
	return err
}

// SetUserName troca nome e sobrenome (o próprio usuário, em Minha conta).
func (s *Store) SetUserName(ctx context.Context, id int64, first, last string) error {
	_, err := s.DB.ExecContext(ctx, "UPDATE users SET display_name=?, first_name=?, last_name=? WHERE id=?", FullName(first, last), first, last, id)
	return err
}

func (s *Store) SetPassword(ctx context.Context, id int64, hash string) error {
	_, err := s.DB.ExecContext(ctx, "UPDATE users SET password_hash=? WHERE id=?", hash, id)
	return err
}

func (s *Store) SetTOTP(ctx context.Context, id int64, secret string, enabled bool, recoveryJSON string) error {
	var sec any
	if secret != "" {
		sec = secret
	}
	_, err := s.DB.ExecContext(ctx, "UPDATE users SET totp_secret=?, totp_enabled=?, totp_last_counter=0, recovery_codes=? WHERE id=?", sec, enabled, recoveryJSON, id)
	return err
}

func (s *Store) SetTOTPCounter(ctx context.Context, id, counter int64) error {
	_, err := s.DB.ExecContext(ctx, "UPDATE users SET totp_last_counter=? WHERE id=?", counter, id)
	return err
}

func (s *Store) SetRecoveryCodes(ctx context.Context, id int64, recoveryJSON string) error {
	_, err := s.DB.ExecContext(ctx, "UPDATE users SET recovery_codes=? WHERE id=?", recoveryJSON, id)
	return err
}

func (s *Store) TouchLogin(ctx context.Context, id int64) error {
	_, err := s.DB.ExecContext(ctx, "UPDATE users SET last_login_at=UTC_TIMESTAMP() WHERE id=?", id)
	return err
}

func (s *Store) DeleteUser(ctx context.Context, id int64) error {
	_, err := s.DB.ExecContext(ctx, "DELETE FROM users WHERE id=?", id)
	return err
}

// ---------------------------------------------------------------- sessões

type Session struct {
	IDHash     string
	UserID     int64
	Pending2FA bool
	ExpiresAt  time.Time
	User       *User
}

func (s *Store) CreateSession(ctx context.Context, idHash string, userID int64, pending bool, expires time.Time, ip, ua string) error {
	if len(ua) > 255 {
		ua = ua[:255]
	}
	_, err := s.DB.ExecContext(ctx, "INSERT INTO sessions (id, user_id, pending_2fa, expires_at, ip, user_agent) VALUES (?,?,?,?,?,?)",
		idHash, userID, pending, expires.UTC(), ip, ua)
	return err
}

func (s *Store) GetSession(ctx context.Context, idHash string) (*Session, error) {
	var se Session
	err := s.DB.QueryRowContext(ctx, "SELECT user_id, pending_2fa, expires_at FROM sessions WHERE id=?", idHash).Scan(&se.UserID, &se.Pending2FA, &se.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	se.IDHash = idHash
	if se.ExpiresAt.Before(time.Now()) {
		_ = s.DeleteSession(ctx, idHash)
		return nil, ErrNotFound
	}
	u, err := s.UserByID(ctx, se.UserID)
	if err != nil {
		return nil, err
	}
	se.User = u
	return &se, nil
}

func (s *Store) PromoteSession(ctx context.Context, idHash string, expires time.Time) error {
	_, err := s.DB.ExecContext(ctx, "UPDATE sessions SET pending_2fa=0, expires_at=?, last_seen_at=UTC_TIMESTAMP() WHERE id=?", expires.UTC(), idHash)
	return err
}

func (s *Store) ExtendSession(ctx context.Context, idHash string, expires time.Time) error {
	_, err := s.DB.ExecContext(ctx, "UPDATE sessions SET expires_at=?, last_seen_at=UTC_TIMESTAMP() WHERE id=?", expires.UTC(), idHash)
	return err
}

func (s *Store) DeleteSession(ctx context.Context, idHash string) error {
	_, err := s.DB.ExecContext(ctx, "DELETE FROM sessions WHERE id=?", idHash)
	return err
}

// DeleteUserSessions encerra todas as sessões do usuário, exceto (opcionalmente) a atual.
func (s *Store) DeleteUserSessions(ctx context.Context, userID int64, exceptHash string) error {
	_, err := s.DB.ExecContext(ctx, "DELETE FROM sessions WHERE user_id=? AND id<>?", userID, exceptHash)
	return err
}

func (s *Store) PurgeSessions(ctx context.Context) error {
	_, err := s.DB.ExecContext(ctx, "DELETE FROM sessions WHERE expires_at < UTC_TIMESTAMP()")
	return err
}

// ---------------------------------------------------------------- auditoria

type AuditEntry struct {
	ID       int64     `json:"id"`
	At       time.Time `json:"at"`
	Username string    `json:"username"`
	Action   string    `json:"action"`
	Detail   string    `json:"detail"`
	IP       string    `json:"ip"`
}

func (s *Store) Audit(ctx context.Context, userID int64, username, action, detail, ip string) {
	var uid any
	if userID > 0 {
		uid = userID
	}
	if len(detail) > 500 {
		detail = detail[:500]
	}
	_, _ = s.DB.ExecContext(ctx, "INSERT INTO audit_log (user_id, username, action, detail, ip) VALUES (?,?,?,?,?)", uid, username, action, detail, ip)
}

func (s *Store) ListAudit(ctx context.Context, limit int) ([]AuditEntry, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT id, at, username, action, detail, ip FROM audit_log ORDER BY id DESC LIMIT ?", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AuditEntry{}
	for rows.Next() {
		var e AuditEntry
		if err := rows.Scan(&e.ID, &e.At, &e.Username, &e.Action, &e.Detail, &e.IP); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------- sessões (gestão)

type SessionInfo struct {
	ID         string    `json:"id"`
	CreatedAt  time.Time `json:"created_at"`
	LastSeenAt time.Time `json:"last_seen_at"`
	ExpiresAt  time.Time `json:"expires_at"`
	IP         string    `json:"ip"`
	UserAgent  string    `json:"user_agent"`
	Pending2FA bool      `json:"pending_2fa"`
}

func (s *Store) ListSessions(ctx context.Context, userID int64) ([]SessionInfo, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT id, created_at, last_seen_at, expires_at, ip, user_agent, pending_2fa FROM sessions WHERE user_id=? AND expires_at > UTC_TIMESTAMP() ORDER BY last_seen_at DESC", userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SessionInfo{}
	for rows.Next() {
		var x SessionInfo
		if err := rows.Scan(&x.ID, &x.CreatedAt, &x.LastSeenAt, &x.ExpiresAt, &x.IP, &x.UserAgent, &x.Pending2FA); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// DeleteSessionOf encerra uma sessão específica do usuário (outro usuário não consegue encerrar sessões alheias).
func (s *Store) DeleteSessionOf(ctx context.Context, userID int64, id string) (bool, error) {
	res, err := s.DB.ExecContext(ctx, "DELETE FROM sessions WHERE id=? AND user_id=?", id, userID)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

func (s *Store) PurgeAudit(ctx context.Context, olderThan time.Time) error {
	_, err := s.DB.ExecContext(ctx, "DELETE FROM audit_log WHERE at < ?", olderThan.UTC())
	return err
}

// SealedSamples devolve alguns valores cifrados do banco, para conferir se a chave de criptografia ainda os abre.
func (s *Store) SealedSamples(ctx context.Context) []string {
	var out []string
	for _, q := range []string{
		"SELECT tokens FROM cloud_accounts WHERE tokens IS NOT NULL AND tokens<>'' LIMIT 3",
		"SELECT config FROM cloud_accounts WHERE config LIKE 'v1:%' LIMIT 3",
		"SELECT config FROM notification_channels LIMIT 3",
		"SELECT db_config FROM jobs WHERE db_config IS NOT NULL AND db_config<>'' LIMIT 3",
	} {
		rows, err := s.DB.QueryContext(ctx, q)
		if err != nil {
			continue
		}
		for rows.Next() {
			var v string
			if rows.Scan(&v) == nil && v != "" {
				out = append(out, v)
			}
		}
		rows.Close()
	}
	return out
}

// SetAvatar grava a foto do usuário (nil apaga).
func (s *Store) SetAvatar(ctx context.Context, id int64, data []byte, typ, ver string) error {
	if data == nil {
		typ, ver = "", ""
	}
	_, err := s.DB.ExecContext(ctx, "UPDATE users SET avatar=?, avatar_type=?, avatar_version=? WHERE id=?", data, typ, ver, id)
	return err
}

// Avatar devolve a foto do usuário; ErrNotFound se ele não tiver uma.
func (s *Store) Avatar(ctx context.Context, id int64) ([]byte, string, error) {
	var b []byte
	var typ string
	err := s.DB.QueryRowContext(ctx, "SELECT avatar, avatar_type FROM users WHERE id=?", id).Scan(&b, &typ)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && len(b) == 0) {
		return nil, "", ErrNotFound
	}
	return b, typ, err
}
