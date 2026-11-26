package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

type Channel struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Type      string    `json:"type"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
	Config    string    `json:"-"` // cifrado
}

func (s *Store) CreateChannel(ctx context.Context, name, typ, sealedConfig string, enabled bool) (int64, error) {
	res, err := s.DB.ExecContext(ctx, "INSERT INTO notification_channels (name, type, config, enabled) VALUES (?,?,?,?)", name, typ, sealedConfig, enabled)
	if err != nil {
		if isDup(err) {
			return 0, ErrDuplicate
		}
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) UpdateChannel(ctx context.Context, id int64, name, sealedConfig string, enabled bool) error {
	_, err := s.DB.ExecContext(ctx, "UPDATE notification_channels SET name=?, config=?, enabled=? WHERE id=?", name, sealedConfig, enabled, id)
	if isDup(err) {
		return ErrDuplicate
	}
	return err
}

func scanChannel(r interface{ Scan(...any) error }) (*Channel, error) {
	var c Channel
	if err := r.Scan(&c.ID, &c.Name, &c.Type, &c.Config, &c.Enabled, &c.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &c, nil
}

const chCols = "id, name, type, config, enabled, created_at"

func (s *Store) GetChannel(ctx context.Context, id int64) (*Channel, error) {
	return scanChannel(s.DB.QueryRowContext(ctx, "SELECT "+chCols+" FROM notification_channels WHERE id=?", id))
}

func (s *Store) ListChannels(ctx context.Context) ([]Channel, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT "+chCols+" FROM notification_channels ORDER BY name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Channel{}
	for rows.Next() {
		c, err := scanChannel(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

func (s *Store) DeleteChannel(ctx context.Context, id int64) error {
	_, err := s.DB.ExecContext(ctx, "DELETE FROM notification_channels WHERE id=?", id)
	return err
}

// ---------------------------------------------------------------- regras

type RuleParams struct {
	Time         string `json:"time,omitempty"`            // daily_report: HH:MM no fuso abaixo
	Timezone     string `json:"timezone,omitempty"`        // daily_report
	OfflineMin   int    `json:"offline_minutes,omitempty"` // agent_offline
	AttachPDF    bool   `json:"attach_pdf"`
	OnlyProblems bool   `json:"only_problems,omitempty"` // daily_report: não envia se tudo estiver bem
}

type Rule struct {
	ID         int64      `json:"id"`
	Name       string     `json:"name"`
	Event      string     `json:"event"` // run_failed | run_warning | run_ok | daily_report | agent_offline
	Enabled    bool       `json:"enabled"`
	JobIDs     []int64    `json:"job_ids"`
	ChannelIDs []int64    `json:"channel_ids"`
	Params     RuleParams `json:"params"`
	CreatedAt  time.Time  `json:"created_at"`
}

func ruleJSON(r *Rule) (jobs, chans, params string) {
	if r.JobIDs == nil {
		r.JobIDs = []int64{}
	}
	if r.ChannelIDs == nil {
		r.ChannelIDs = []int64{}
	}
	a, _ := json.Marshal(r.JobIDs)
	b, _ := json.Marshal(r.ChannelIDs)
	c, _ := json.Marshal(r.Params)
	return string(a), string(b), string(c)
}

func (s *Store) CreateRule(ctx context.Context, r *Rule) (int64, error) {
	j, c, p := ruleJSON(r)
	res, err := s.DB.ExecContext(ctx, "INSERT INTO notification_rules (name, event, enabled, job_ids, channel_ids, params) VALUES (?,?,?,?,?,?)", r.Name, r.Event, r.Enabled, j, c, p)
	if err != nil {
		if isDup(err) {
			return 0, ErrDuplicate
		}
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) UpdateRule(ctx context.Context, r *Rule) error {
	j, c, p := ruleJSON(r)
	_, err := s.DB.ExecContext(ctx, "UPDATE notification_rules SET name=?, event=?, enabled=?, job_ids=?, channel_ids=?, params=? WHERE id=?", r.Name, r.Event, r.Enabled, j, c, p, r.ID)
	if isDup(err) {
		return ErrDuplicate
	}
	return err
}

func scanRule(r interface{ Scan(...any) error }) (*Rule, error) {
	var x Rule
	var j, c, p string
	if err := r.Scan(&x.ID, &x.Name, &x.Event, &x.Enabled, &j, &c, &p, &x.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	_ = json.Unmarshal([]byte(j), &x.JobIDs)
	_ = json.Unmarshal([]byte(c), &x.ChannelIDs)
	_ = json.Unmarshal([]byte(p), &x.Params)
	if x.JobIDs == nil {
		x.JobIDs = []int64{}
	}
	if x.ChannelIDs == nil {
		x.ChannelIDs = []int64{}
	}
	return &x, nil
}

const ruleCols = "id, name, event, enabled, job_ids, channel_ids, params, created_at"

func (s *Store) GetRule(ctx context.Context, id int64) (*Rule, error) {
	return scanRule(s.DB.QueryRowContext(ctx, "SELECT "+ruleCols+" FROM notification_rules WHERE id=?", id))
}

func (s *Store) ListRules(ctx context.Context) ([]Rule, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT "+ruleCols+" FROM notification_rules ORDER BY name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Rule{}
	for rows.Next() {
		r, err := scanRule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

func (s *Store) DeleteRule(ctx context.Context, id int64) error {
	_, err := s.DB.ExecContext(ctx, "DELETE FROM notification_rules WHERE id=?", id)
	return err
}

// ---------------------------------------------------------------- fila e histórico

type Notification struct {
	ID          int64      `json:"id"`
	RuleID      *int64     `json:"rule_id"`
	RuleName    string     `json:"rule_name"`
	ChannelID   *int64     `json:"channel_id"`
	ChannelName string     `json:"channel_name"`
	Kind        string     `json:"kind"`
	Ref         string     `json:"ref"`
	Subject     string     `json:"subject"`
	Status      string     `json:"status"`
	Attempts    int        `json:"attempts"`
	Error       string     `json:"error"`
	CreatedAt   time.Time  `json:"created_at"`
	SentAt      *time.Time `json:"sent_at"`
}

// Enqueue registra um envio pendente (idempotente: o mesmo evento não é enfileirado duas vezes para a mesma regra e canal).
// Devolve false se já existia.
func (s *Store) Enqueue(ctx context.Context, ruleID, channelID int64, kind, ref string, at time.Time) (bool, error) {
	res, err := s.DB.ExecContext(ctx, "INSERT IGNORE INTO notifications (rule_id, channel_id, kind, ref, next_attempt_at) VALUES (?,?,?,?,?)", ruleID, channelID, kind, ref, at.UTC())
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

func (s *Store) DuePending(ctx context.Context, now time.Time, limit int) ([]Notification, error) {
	return s.queryNotifs(ctx, "WHERE n.status='pending' AND n.next_attempt_at <= ? ORDER BY n.id LIMIT ?", now.UTC(), limit)
}

func (s *Store) queryNotifs(ctx context.Context, tail string, args ...any) ([]Notification, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT n.id, n.rule_id, COALESCE(r.name,''), n.channel_id, COALESCE(c.name,''), n.kind, n.ref, n.subject, n.status, n.attempts, n.error, n.created_at, n.sent_at
		FROM notifications n LEFT JOIN notification_rules r ON r.id=n.rule_id LEFT JOIN notification_channels c ON c.id=n.channel_id `+tail, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Notification{}
	for rows.Next() {
		var n Notification
		var rid, cid sql.NullInt64
		var sent sql.NullTime
		if err := rows.Scan(&n.ID, &rid, &n.RuleName, &cid, &n.ChannelName, &n.Kind, &n.Ref, &n.Subject, &n.Status, &n.Attempts, &n.Error, &n.CreatedAt, &sent); err != nil {
			return nil, err
		}
		if rid.Valid {
			n.RuleID = &rid.Int64
		}
		if cid.Valid {
			n.ChannelID = &cid.Int64
		}
		if sent.Valid {
			n.SentAt = &sent.Time
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func (s *Store) ListNotifications(ctx context.Context, limit int) ([]Notification, error) {
	return s.queryNotifs(ctx, "ORDER BY n.id DESC LIMIT ?", limit)
}

func (s *Store) MarkSent(ctx context.Context, id int64, subject string) error {
	_, err := s.DB.ExecContext(ctx, "UPDATE notifications SET status='sent', attempts=attempts+1, error='', subject=?, sent_at=UTC_TIMESTAMP() WHERE id=?", subject, id)
	return err
}

// MarkAttemptFailed registra uma falha; com retryAt zero o envio é dado como definitivamente falho.
func (s *Store) MarkAttemptFailed(ctx context.Context, id int64, errText string, retryAt time.Time) error {
	if len(errText) > 500 {
		errText = errText[:500]
	}
	if retryAt.IsZero() {
		_, err := s.DB.ExecContext(ctx, "UPDATE notifications SET status='failed', attempts=attempts+1, error=? WHERE id=?", errText, id)
		return err
	}
	_, err := s.DB.ExecContext(ctx, "UPDATE notifications SET attempts=attempts+1, error=?, next_attempt_at=? WHERE id=?", errText, retryAt.UTC(), id)
	return err
}

func (s *Store) PurgeNotifications(ctx context.Context, olderThan time.Time) error {
	_, err := s.DB.ExecContext(ctx, "DELETE FROM notifications WHERE created_at < ? AND status <> 'pending'", olderThan.UTC())
	return err
}

// ---------------------------------------------------------------- consultas para relatórios

// RunsBetween devolve as execuções iniciadas (ou previstas) no intervalo, da mais recente para a mais antiga.
func (s *Store) RunsBetween(ctx context.Context, from, to time.Time) ([]Run, error) {
	rows, err := s.DB.QueryContext(ctx, runSelect+"WHERE COALESCE(r.started_at, r.scheduled_for, r.created_at) >= ? AND COALESCE(r.started_at, r.scheduled_for, r.created_at) < ? ORDER BY COALESCE(r.started_at, r.scheduled_for, r.created_at) DESC, r.id DESC", from.UTC(), to.UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Run{}
	for rows.Next() {
		x, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *x)
	}
	return out, rows.Err()
}
