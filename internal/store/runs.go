package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

type Run struct {
	ID           int64      `json:"id"`
	UID          string     `json:"uid"`
	JobID        int64      `json:"job_id"`
	JobName      string     `json:"job_name"`
	AgentID      int64      `json:"agent_id"`
	AgentName    string     `json:"agent_name"`
	Trigger      string     `json:"trigger"`
	ScheduledFor *time.Time `json:"scheduled_for"`
	Status       string     `json:"status"`
	StartedAt    *time.Time `json:"started_at"`
	FinishedAt   *time.Time `json:"finished_at"`
	BytesSource  int64      `json:"bytes_source"`
	BytesOutput  int64      `json:"bytes_output"`
	Files        int64      `json:"files"`
	SHA256       string     `json:"sha256"`
	OutputName   string     `json:"output_name"`
	Error        string     `json:"error"`
	CreatedAt    time.Time  `json:"created_at"`

	Phase           string `json:"phase"`
	ProgressPct     int    `json:"progress_pct"`
	CloudStatus     string `json:"cloud_status"`
	CloudPath       string `json:"cloud_path"`
	CloudHash       string `json:"cloud_hash"`
	CloudVerifiedBy string `json:"cloud_verified_by"`
}

type RunLog struct {
	Seq   int       `json:"seq"`
	At    time.Time `json:"at"`
	Level string    `json:"level"`
	Text  string    `json:"text"`
}

const runSelect = `SELECT r.id, r.uid, r.job_id, j.name, r.agent_id, a.name, r.trigger_kind, r.scheduled_for, r.status, r.started_at, r.finished_at,
  r.bytes_source, r.bytes_output, r.files, r.sha256, r.output_name, COALESCE(r.error,''), r.created_at,
  r.phase, r.progress_pct, r.cloud_status, r.cloud_path, r.cloud_hash, r.cloud_verified_by
  FROM runs r JOIN jobs j ON j.id=r.job_id JOIN agents a ON a.id=r.agent_id `

func scanRun(r interface{ Scan(...any) error }) (*Run, error) {
	var x Run
	var sf, st, fi sql.NullTime
	if err := r.Scan(&x.ID, &x.UID, &x.JobID, &x.JobName, &x.AgentID, &x.AgentName, &x.Trigger, &sf, &x.Status, &st, &fi,
		&x.BytesSource, &x.BytesOutput, &x.Files, &x.SHA256, &x.OutputName, &x.Error, &x.CreatedAt,
		&x.Phase, &x.ProgressPct, &x.CloudStatus, &x.CloudPath, &x.CloudHash, &x.CloudVerifiedBy); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if sf.Valid {
		x.ScheduledFor = &sf.Time
	}
	if st.Valid {
		x.StartedAt = &st.Time
	}
	if fi.Valid {
		x.FinishedAt = &fi.Time
	}
	return &x, nil
}

func IsFinal(status string) bool {
	switch status {
	case "ok", "warning", "failed", "canceled", "missed":
		return true
	}
	return false
}

// CreateQueuedRun registra uma execução manual pedida pela interface (o agente a assume pelo run_uid).
func (s *Store) CreateQueuedRun(ctx context.Context, uid string, jobID, agentID int64) error {
	_, err := s.DB.ExecContext(ctx, "INSERT INTO runs (uid, job_id, agent_id, trigger_kind, status) VALUES (?,?,?, 'manual', 'queued')", uid, jobID, agentID)
	return err
}

type Report struct {
	UID          string
	JobID        int64
	AgentID      int64
	Trigger      string
	ScheduledFor time.Time // zero = nenhum
	Final        bool
	Started      bool
	Status       string
	StartedAt    time.Time
	FinishedAt   time.Time
	BytesSource  int64
	BytesOutput  int64
	Files        int64
	SHA256       string
	OutputName   string
	Error        string
	Logs         []RunLog

	Phase           string
	ProgressPct     int
	CloudStatus     string
	CloudPath       string
	CloudHash       string
	CloudVerifiedBy string
}

// ApplyReport grava (de forma idempotente) o que o agente informou. Nunca tira uma execução do estado final.
// ApplyReport grava o relatório e ignora o detalhe de término (veja ApplyReportX).
func (s *Store) ApplyReport(ctx context.Context, r Report) error {
	_, _, err := s.ApplyReportX(ctx, r)
	return err
}

// ApplyReportX grava o relatório e informa se esta chamada fez a execução terminar (e qual é o id dela), para disparar notificações.
func (s *Store) ApplyReportX(ctx context.Context, r Report) (finished bool, runID int64, err error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return false, 0, err
	}
	defer tx.Rollback()
	var jobAgent int64
	if err := tx.QueryRowContext(ctx, "SELECT agent_id FROM jobs WHERE id=?", r.JobID).Scan(&jobAgent); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, 0, ErrNotFound
		}
		return false, 0, err
	}
	if jobAgent != r.AgentID {
		return false, 0, ErrNotFound // um agente só relata jobs que são dele
	}
	var id int64
	var cur string
	err = tx.QueryRowContext(ctx, "SELECT id, status FROM runs WHERE uid=? FOR UPDATE", r.UID).Scan(&id, &cur)
	if errors.Is(err, sql.ErrNoRows) {
		var sched any
		if !r.ScheduledFor.IsZero() {
			sched = r.ScheduledFor.UTC()
			// adota uma linha "missed" do mesmo horário (o agente recuperou o atraso)
			var mid int64
			e := tx.QueryRowContext(ctx, "SELECT id FROM runs WHERE job_id=? AND scheduled_for=? AND status='missed' FOR UPDATE", r.JobID, r.ScheduledFor.UTC()).Scan(&mid)
			if e == nil {
				if _, err := tx.ExecContext(ctx, "UPDATE runs SET uid=?, status='running', error=NULL WHERE id=?", r.UID, mid); err != nil {
					return false, 0, err
				}
				id, cur = mid, "running"
			}
		}
		if id == 0 {
			trig := r.Trigger
			if trig != "manual" {
				trig = "schedule"
			}
			res, err := tx.ExecContext(ctx, "INSERT INTO runs (uid, job_id, agent_id, trigger_kind, scheduled_for, status) VALUES (?,?,?,?,?, 'queued')", r.UID, r.JobID, r.AgentID, trig, sched)
			if err != nil {
				if isDup(err) { // outra execução do mesmo horário já existe: ignora o duplicado
					return false, 0, nil
				}
				return false, 0, err
			}
			id, _ = res.LastInsertId()
			cur = "queued"
		}
	} else if err != nil {
		return false, 0, err
	}
	if IsFinal(cur) && cur != "missed" {
		return false, id, tx.Commit() // já finalizada: relatórios repetidos não alteram nada
	}
	if r.Started && !r.StartedAt.IsZero() {
		if _, err := tx.ExecContext(ctx, "UPDATE runs SET status='running', started_at=? WHERE id=? AND status IN ('queued','running','missed')", r.StartedAt.UTC(), id); err != nil {
			return false, 0, err
		}
	}
	if r.Final {
		if _, err := tx.ExecContext(ctx, `UPDATE runs SET status=?, started_at=COALESCE(started_at, ?), finished_at=?, bytes_source=?, bytes_output=?, files=?, sha256=?, output_name=?, error=? WHERE id=?`,
			r.Status, nullTime(r.StartedAt), nullTime(r.FinishedAt), r.BytesSource, r.BytesOutput, r.Files, r.SHA256, r.OutputName, nullStr(r.Error), id); err != nil {
			return false, 0, err
		}
		if _, err := tx.ExecContext(ctx, "UPDATE runs SET phase='', progress_pct=?, cloud_status=?, cloud_path=?, cloud_hash=?, cloud_verified_by=? WHERE id=?",
			r.ProgressPct, cloudStatusOrNone(r.CloudStatus), r.CloudPath, r.CloudHash, r.CloudVerifiedBy, id); err != nil {
			return false, 0, err
		}
	} else {
		if r.BytesSource > 0 || r.Files > 0 {
			if _, err := tx.ExecContext(ctx, "UPDATE runs SET bytes_source=?, bytes_output=?, files=? WHERE id=?", r.BytesSource, r.BytesOutput, r.Files, id); err != nil {
				return false, 0, err
			}
		}
		if r.Phase != "" {
			if _, err := tx.ExecContext(ctx, "UPDATE runs SET phase=?, progress_pct=? WHERE id=? AND status IN ('queued','running')", r.Phase, r.ProgressPct, id); err != nil {
				return false, 0, err
			}
		}
	}
	for _, l := range r.Logs {
		txt := l.Text
		if len(txt) > 1000 {
			txt = txt[:1000]
		}
		if _, err := tx.ExecContext(ctx, "INSERT IGNORE INTO run_logs (run_id, seq, at, level, text) VALUES (?,?,?,?,?)", id, l.Seq, l.At.UTC(), l.Level, txt); err != nil {
			return false, 0, err
		}
	}
	return r.Final, id, tx.Commit()
}

func nullTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.UTC()
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// MarkMissed registra um horário previsto que o agente não cumpriu (ignora se já existir execução desse horário).
func (s *Store) MarkMissed(ctx context.Context, uid string, jobID, agentID int64, scheduledFor time.Time, reason string) (int64, error) {
	res, err := s.DB.ExecContext(ctx, "INSERT IGNORE INTO runs (uid, job_id, agent_id, trigger_kind, scheduled_for, status, error) VALUES (?,?,?, 'schedule', ?, 'missed', ?)",
		uid, jobID, agentID, scheduledFor.UTC(), reason)
	if err != nil {
		return 0, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return 0, nil // já existia uma execução desse horário
	}
	return res.LastInsertId()
}

func (s *Store) HasRunAt(ctx context.Context, jobID int64, scheduledFor time.Time) (bool, error) {
	var n int
	err := s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM runs WHERE job_id=? AND scheduled_for=?", jobID, scheduledFor.UTC()).Scan(&n)
	return n > 0, err
}

func (s *Store) GetRun(ctx context.Context, id int64) (*Run, error) {
	return scanRun(s.DB.QueryRowContext(ctx, runSelect+"WHERE r.id=?", id))
}

func (s *Store) RunByUID(ctx context.Context, uid string) (*Run, error) {
	return scanRun(s.DB.QueryRowContext(ctx, runSelect+"WHERE r.uid=?", uid))
}

func (s *Store) RunLogs(ctx context.Context, runID int64) ([]RunLog, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT seq, at, level, text FROM run_logs WHERE run_id=? ORDER BY seq", runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RunLog{}
	for rows.Next() {
		var l RunLog
		if err := rows.Scan(&l.Seq, &l.At, &l.Level, &l.Text); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// ListRuns lista as execuções mais recentes (filtros opcionais: job e status).
func (s *Store) ListRuns(ctx context.Context, jobID int64, status string, limit int) ([]Run, error) {
	return s.ListRunsBetween(ctx, jobID, status, time.Time{}, time.Time{}, limit)
}

// ListRunsBetween é ListRuns com um intervalo [from, to) opcional (zero = sem limite), pelo horário de início (ou o
// agendado, nas perdidas).
func (s *Store) ListRunsBetween(ctx context.Context, jobID int64, status string, from, to time.Time, limit int) ([]Run, error) {
	q, args := runSelect+"WHERE 1=1", []any{}
	if !from.IsZero() {
		q += " AND COALESCE(r.started_at, r.scheduled_for, r.created_at) >= ?"
		args = append(args, from.UTC())
	}
	if !to.IsZero() {
		q += " AND COALESCE(r.started_at, r.scheduled_for, r.created_at) < ?"
		args = append(args, to.UTC())
	}
	if jobID > 0 {
		q += " AND r.job_id=?"
		args = append(args, jobID)
	}
	if status != "" {
		q += " AND r.status=?"
		args = append(args, status)
	}
	q += " ORDER BY COALESCE(r.started_at, r.scheduled_for, r.created_at) DESC, r.id DESC LIMIT ?"
	args = append(args, limit)
	rows, err := s.DB.QueryContext(ctx, q, args...)
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

// LastRuns devolve a execução mais recente de cada job.
func (s *Store) LastRuns(ctx context.Context) (map[int64]Run, error) {
	rows, err := s.DB.QueryContext(ctx, runSelect+"WHERE r.id = (SELECT MAX(id) FROM runs WHERE job_id=r.job_id)")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]Run{}
	for rows.Next() {
		x, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out[x.JobID] = *x
	}
	return out, rows.Err()
}

type RunStats struct {
	OK, Warning, Failed, Missed, Running int
}

func (s *Store) StatsSince(ctx context.Context, since time.Time) (RunStats, error) {
	var st RunStats
	rows, err := s.DB.QueryContext(ctx, "SELECT status, COUNT(*) FROM runs WHERE created_at >= ? GROUP BY status", since.UTC())
	if err != nil {
		return st, err
	}
	defer rows.Close()
	for rows.Next() {
		var k string
		var n int
		if err := rows.Scan(&k, &n); err != nil {
			return st, err
		}
		switch k {
		case "ok":
			st.OK = n
		case "warning":
			st.Warning = n
		case "failed":
			st.Failed = n
		case "missed":
			st.Missed = n
		case "running", "queued":
			st.Running += n
		}
	}
	return st, rows.Err()
}

func (s *Store) PurgeRuns(ctx context.Context, olderThan time.Time) error {
	_, err := s.DB.ExecContext(ctx, "DELETE FROM runs WHERE created_at < ? AND status NOT IN ('queued','running')", olderThan.UTC())
	return err
}

// DeleteQueuedRun desfaz o registro de uma execução manual que não chegou a ser entregue ao agente.
func (s *Store) DeleteQueuedRun(ctx context.Context, uid string) error {
	_, err := s.DB.ExecContext(ctx, "DELETE FROM runs WHERE uid=? AND status='queued'", uid)
	return err
}

func cloudStatusOrNone(s string) string {
	switch s {
	case "ok", "failed":
		return s
	}
	return "none"
}

// FailStaleRuns dá como falhas as execuções que o Server acha que estão rodando num agente que acabou de se
// (re)conectar mas que o agente não lista como ativas (ele foi reiniciado no meio do backup).
func (s *Store) FailStaleRuns(ctx context.Context, agentID int64, activeUIDs []string) (int64, error) {
	q := "UPDATE runs SET status='failed', phase='', finished_at=UTC_TIMESTAMP(3), error='o agente foi reiniciado ou perdeu a execução durante o backup' WHERE agent_id=? AND status IN ('queued','running') AND created_at < ?"
	args := []any{agentID, time.Now().UTC().Add(-90 * time.Second)}
	if len(activeUIDs) > 0 {
		q += " AND uid NOT IN (?" + strings.Repeat(",?", len(activeUIDs)-1) + ")"
		for _, u := range activeUIDs {
			args = append(args, u)
		}
	}
	res, err := s.DB.ExecContext(ctx, q, args...)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// RunLogsAfter devolve as linhas do log com seq maior que after (para a transmissão ao vivo).
func (s *Store) RunLogsAfter(ctx context.Context, runID int64, after int, limit int) ([]RunLog, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT seq, at, level, text FROM run_logs WHERE run_id=? AND seq>? ORDER BY seq LIMIT ?", runID, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RunLog{}
	for rows.Next() {
		var l RunLog
		if err := rows.Scan(&l.Seq, &l.At, &l.Level, &l.Text); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

type ActionResult struct {
	RunUID          string
	Kind            string
	State           string // running | ok | failed
	Message         string
	Lines           []RunLog // sem seq: é atribuído aqui (a partir de 10000)
	CloudStatus     string
	CloudPath       string
	CloudHash       string
	CloudVerifiedBy string
}

const actionSeqBase = 10000

// ApplyActionLog grava o andamento de uma ação posterior (reenvio à nuvem, extração) no log da execução e, quando um reenvio
// termina bem, atualiza os dados de nuvem da execução (e corrige o estado se a única falha era o envio).
func (s *Store) ApplyActionLog(ctx context.Context, agentID int64, a ActionResult) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var id, runAgent int64
	var status, errText string
	err = tx.QueryRowContext(ctx, "SELECT id, agent_id, status, COALESCE(error,'') FROM runs WHERE uid=? FOR UPDATE", a.RunUID).Scan(&id, &runAgent, &status, &errText)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && runAgent != agentID) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	var next int
	if err := tx.QueryRowContext(ctx, "SELECT COALESCE(MAX(seq), ?) FROM run_logs WHERE run_id=? AND seq>=?", actionSeqBase-1, id, actionSeqBase).Scan(&next); err != nil {
		return err
	}
	for _, l := range a.Lines {
		next++
		txt := l.Text
		if len(txt) > 1000 {
			txt = txt[:1000]
		}
		if _, err := tx.ExecContext(ctx, "INSERT IGNORE INTO run_logs (run_id, seq, at, level, text) VALUES (?,?,?,?,?)", id, next, l.At.UTC(), l.Level, txt); err != nil {
			return err
		}
	}
	if a.Kind == "reupload" && a.State == "ok" {
		if _, err := tx.ExecContext(ctx, "UPDATE runs SET cloud_status='ok', cloud_path=?, cloud_hash=?, cloud_verified_by=? WHERE id=?", a.CloudPath, a.CloudHash, a.CloudVerifiedBy, id); err != nil {
			return err
		}
		if status == "failed" && (strings.Contains(errText, "envio para a nuvem falhou") || strings.Contains(errText, "envio para o storage falhou")) {
			if _, err := tx.ExecContext(ctx, "UPDATE runs SET status='ok', error=NULL WHERE id=?", id); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}
