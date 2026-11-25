package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type Agent struct {
	ID              int64      `json:"id"`
	Name            string     `json:"name"`
	Hostname        string     `json:"hostname"`
	OS              string     `json:"os"`
	Arch            string     `json:"arch"`
	AgentVersion    string     `json:"agent_version"`
	DockerAvailable bool       `json:"docker_available"`
	IP              string     `json:"ip"`
	CredentialHash  string     `json:"-"`
	CreatedAt       time.Time  `json:"created_at"`
	LastSeenAt      *time.Time `json:"last_seen_at"`
	RevokedAt       *time.Time `json:"revoked_at"`
}

const agentCols = "id, name, hostname, os, arch, agent_version, docker_available, ip, credential_hash, created_at, last_seen_at, revoked_at"

func scanAgent(r interface{ Scan(...any) error }) (*Agent, error) {
	var a Agent
	var ls, rv sql.NullTime
	if err := r.Scan(&a.ID, &a.Name, &a.Hostname, &a.OS, &a.Arch, &a.AgentVersion, &a.DockerAvailable, &a.IP, &a.CredentialHash, &a.CreatedAt, &ls, &rv); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if ls.Valid {
		t := ls.Time
		a.LastSeenAt = &t
	}
	if rv.Valid {
		t := rv.Time
		a.RevokedAt = &t
	}
	return &a, nil
}

// CreateEnrollToken registra (só o hash de) um token de matrícula de uso único.
func (s *Store) CreateEnrollToken(ctx context.Context, tokenHash, name string, createdBy int64, expires time.Time) error {
	var by any
	if createdBy > 0 {
		by = createdBy
	}
	var n int
	if err := s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM agents WHERE name=?", name).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return ErrDuplicate
	}
	_, err := s.DB.ExecContext(ctx, "INSERT INTO enrollment_tokens (token_hash, name, created_by, expires_at) VALUES (?,?,?,?)", tokenHash, name, by, expires.UTC())
	return err
}

type PendingToken struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

func (s *Store) ListPendingTokens(ctx context.Context) ([]PendingToken, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT id, name, created_at, expires_at FROM enrollment_tokens WHERE used_at IS NULL AND expires_at > UTC_TIMESTAMP() ORDER BY id DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PendingToken{}
	for rows.Next() {
		var t PendingToken
		if err := rows.Scan(&t.ID, &t.Name, &t.CreatedAt, &t.ExpiresAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) DeleteToken(ctx context.Context, id int64) error {
	_, err := s.DB.ExecContext(ctx, "DELETE FROM enrollment_tokens WHERE id=? AND used_at IS NULL", id)
	return err
}

// Enroll consome o token (uso único, atômico) e cria o agente. Devolve ErrNotFound se o token for inválido,
// expirado ou já usado.
func (s *Store) Enroll(ctx context.Context, tokenHash, hostname, os_, arch, version, ip, credHash string) (*Agent, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var tokID int64
	var name string
	err = tx.QueryRowContext(ctx, "SELECT id, name FROM enrollment_tokens WHERE token_hash=? AND used_at IS NULL AND expires_at > UTC_TIMESTAMP() FOR UPDATE", tokenHash).Scan(&tokID, &name)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	res, err := tx.ExecContext(ctx, "INSERT INTO agents (name, hostname, os, arch, agent_version, ip, credential_hash, last_seen_at) VALUES (?,?,?,?,?,?,?, UTC_TIMESTAMP())",
		name, hostname, os_, arch, version, ip, credHash)
	if err != nil {
		if isDup(err) {
			return nil, ErrDuplicate
		}
		return nil, err
	}
	id, _ := res.LastInsertId()
	if _, err := tx.ExecContext(ctx, "UPDATE enrollment_tokens SET used_at=UTC_TIMESTAMP(), agent_id=? WHERE id=?", id, tokID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.AgentByID(ctx, id)
}

func (s *Store) AgentByID(ctx context.Context, id int64) (*Agent, error) {
	return scanAgent(s.DB.QueryRowContext(ctx, "SELECT "+agentCols+" FROM agents WHERE id=?", id))
}

func (s *Store) ListAgents(ctx context.Context) ([]Agent, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT "+agentCols+" FROM agents ORDER BY name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Agent{}
	for rows.Next() {
		a, err := scanAgent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *a)
	}
	return out, rows.Err()
}

// TouchAgent atualiza o "visto por último" e os dados informados no Hello.
func (s *Store) TouchAgent(ctx context.Context, id int64, hostname, os_, arch, version string, docker bool, ip string) error {
	_, err := s.DB.ExecContext(ctx, "UPDATE agents SET hostname=?, os=?, arch=?, agent_version=?, docker_available=?, ip=?, last_seen_at=UTC_TIMESTAMP() WHERE id=?",
		hostname, os_, arch, version, docker, ip, id)
	return err
}

func (s *Store) Seen(ctx context.Context, id int64) error {
	_, err := s.DB.ExecContext(ctx, "UPDATE agents SET last_seen_at=UTC_TIMESTAMP() WHERE id=?", id)
	return err
}

func (s *Store) RenameAgent(ctx context.Context, id int64, name string) error {
	_, err := s.DB.ExecContext(ctx, "UPDATE agents SET name=? WHERE id=?", name, id)
	if isDup(err) {
		return ErrDuplicate
	}
	return err
}

func (s *Store) RevokeAgent(ctx context.Context, id int64) error {
	_, err := s.DB.ExecContext(ctx, "UPDATE agents SET revoked_at=UTC_TIMESTAMP() WHERE id=? AND revoked_at IS NULL", id)
	return err
}

func (s *Store) DeleteAgent(ctx context.Context, id int64) error {
	_, err := s.DB.ExecContext(ctx, "DELETE FROM agents WHERE id=?", id)
	return err
}
