package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

type Job struct {
	ID         int64    `json:"id"`
	Name       string   `json:"name"`
	AgentID    int64    `json:"agent_id"`
	Kind       string   `json:"kind"`
	Enabled    bool     `json:"enabled"`
	Cron       string   `json:"cron"`
	Timezone   string   `json:"timezone"`
	Paths      []string `json:"paths"`
	Excludes   []string `json:"excludes"`
	DestSubdir string   `json:"dest_subdir"`
	Keep       int      `json:"keep"`
	Version    int      `json:"version"`

	CloudAccountID *int64    `json:"cloud_account_id"`
	CloudFolder    string    `json:"cloud_folder"`
	CloudKeep      int       `json:"cloud_keep"`
	CloudKeepDays  int       `json:"cloud_keep_days"`
	CloudDirect    bool      `json:"cloud_direct"` // grava direto no storage do agente, sem arquivo local
	BackupMode     string    `json:"backup_mode"`  // full | incremental
	FullWeekday    int       `json:"full_weekday"` // incremental: dia do full (0 = domingo)
	Format         string    `json:"format"`       // tar (.tar.gz) | dir (pasta com os arquivos abertos)
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`

	DBSealed string `json:"-"` // configuração do banco (com senha), cifrada
}

const jobCols = "id, name, agent_id, kind, enabled, cron, timezone, paths, excludes, dest_subdir, keep_copies, version, created_at, updated_at, cloud_account_id, cloud_folder, cloud_keep, cloud_keep_days, COALESCE(db_config,''), cloud_direct, backup_mode, full_weekday, format"

func scanJob(r interface{ Scan(...any) error }) (*Job, error) {
	var j Job
	var p, e string
	var ca sql.NullInt64
	if err := r.Scan(&j.ID, &j.Name, &j.AgentID, &j.Kind, &j.Enabled, &j.Cron, &j.Timezone, &p, &e, &j.DestSubdir, &j.Keep, &j.Version, &j.CreatedAt, &j.UpdatedAt,
		&ca, &j.CloudFolder, &j.CloudKeep, &j.CloudKeepDays, &j.DBSealed, &j.CloudDirect, &j.BackupMode, &j.FullWeekday, &j.Format); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if ca.Valid {
		v := ca.Int64
		j.CloudAccountID = &v
	}
	_ = json.Unmarshal([]byte(p), &j.Paths)
	_ = json.Unmarshal([]byte(e), &j.Excludes)
	if j.Paths == nil {
		j.Paths = []string{}
	}
	if j.Excludes == nil {
		j.Excludes = []string{}
	}
	return &j, nil
}

func jsonList(v []string) string {
	if v == nil {
		v = []string{}
	}
	b, _ := json.Marshal(v)
	return string(b)
}

func (s *Store) CreateJob(ctx context.Context, j *Job) (int64, error) {
	res, err := s.DB.ExecContext(ctx, "INSERT INTO jobs (name, agent_id, kind, enabled, cron, timezone, paths, excludes, dest_subdir, keep_copies, cloud_account_id, cloud_folder, cloud_keep, cloud_keep_days, db_config, cloud_direct, backup_mode, full_weekday, format) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)",
		j.Name, j.AgentID, j.Kind, j.Enabled, j.Cron, j.Timezone, jsonList(j.Paths), jsonList(j.Excludes), j.DestSubdir, j.Keep, j.CloudAccountID, j.CloudFolder, j.CloudKeep, j.CloudKeepDays, nullStr(j.DBSealed), j.CloudDirect, modeOr(j.BackupMode), j.FullWeekday, formatOr(j.Format))
	if err != nil {
		if isDup(err) {
			return 0, ErrDuplicate
		}
		return 0, err
	}
	return res.LastInsertId()
}

// UpdateJob grava as alterações e incrementa a versão (o agente descarta agendas antigas).
func (s *Store) UpdateJob(ctx context.Context, j *Job) error {
	_, err := s.DB.ExecContext(ctx, "UPDATE jobs SET name=?, agent_id=?, enabled=?, cron=?, timezone=?, paths=?, excludes=?, dest_subdir=?, keep_copies=?, cloud_account_id=?, cloud_folder=?, cloud_keep=?, cloud_keep_days=?, db_config=?, cloud_direct=?, backup_mode=?, full_weekday=?, format=?, version=version+1 WHERE id=?",
		j.Name, j.AgentID, j.Enabled, j.Cron, j.Timezone, jsonList(j.Paths), jsonList(j.Excludes), j.DestSubdir, j.Keep, j.CloudAccountID, j.CloudFolder, j.CloudKeep, j.CloudKeepDays, nullStr(j.DBSealed), j.CloudDirect, modeOr(j.BackupMode), j.FullWeekday, formatOr(j.Format), j.ID)
	if isDup(err) {
		return ErrDuplicate
	}
	return err
}

func (s *Store) GetJob(ctx context.Context, id int64) (*Job, error) {
	return scanJob(s.DB.QueryRowContext(ctx, "SELECT "+jobCols+" FROM jobs WHERE id=?", id))
}

func (s *Store) ListJobs(ctx context.Context) ([]Job, error) {
	return s.queryJobs(ctx, "SELECT "+jobCols+" FROM jobs ORDER BY name")
}

func (s *Store) JobsForAgent(ctx context.Context, agentID int64) ([]Job, error) {
	return s.queryJobs(ctx, "SELECT "+jobCols+" FROM jobs WHERE agent_id=? ORDER BY id", agentID)
}

func (s *Store) queryJobs(ctx context.Context, q string, args ...any) ([]Job, error) {
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Job{}
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *j)
	}
	return out, rows.Err()
}

func (s *Store) DeleteJob(ctx context.Context, id int64) error {
	_, err := s.DB.ExecContext(ctx, "DELETE FROM jobs WHERE id=?", id)
	return err
}

func (s *Store) SetJobEnabled(ctx context.Context, id int64, enabled bool) error {
	_, err := s.DB.ExecContext(ctx, "UPDATE jobs SET enabled=?, version=version+1 WHERE id=?", enabled, id)
	return err
}

// JobsUsingCloud conta os jobs que usam a conta de nuvem (impede excluí-la).
func (s *Store) JobsUsingCloud(ctx context.Context, accountID int64) (n int, err error) {
	err = s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM jobs WHERE cloud_account_id=?", accountID).Scan(&n)
	return
}

func formatOr(f string) string {
	if f == "" {
		return "tar"
	}
	return f
}

func modeOr(m string) string {
	if m == "" {
		return "full"
	}
	return m
}
