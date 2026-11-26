package agentsrv

import (
	"context"
	"log/slog"
	"time"

	"github.com/AprendendoLinux/corujabackup/internal/auth"
	"github.com/AprendendoLinux/corujabackup/internal/sched"
)

const (
	missedGrace  = 15 * time.Minute // tolerância após o horário previsto antes de dar como "perdido"
	missedWindow = 36 * time.Hour   // até onde olhar para trás
)

// WatchMissed registra como "perdido" todo horário previsto em que o agente não iniciou a execução (offline, parado,
// agenda não recebida...). Roda a cada minuto até ctx terminar.
func (s *Server) WatchMissed(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		s.CheckMissed(ctx, time.Now())
		select {
		case <-t.C:
		case <-ctx.Done():
			return
		}
	}
}

func (s *Server) CheckMissed(ctx context.Context, now time.Time) {
	jobs, err := s.Store.ListJobs(ctx)
	if err != nil {
		return
	}
	for _, j := range jobs {
		if !j.Enabled {
			continue
		}
		sc, err := sched.Parse(j.Cron, j.Timezone)
		if err != nil {
			continue
		}
		from := now.Add(-missedWindow)
		if j.UpdatedAt.After(from) {
			from = j.UpdatedAt // não cobra horários anteriores à última edição/criação do job
		}
		last := sc.Prev(from, now.Add(-missedGrace))
		if last.IsZero() {
			continue
		}
		if has, err := s.Store.HasRunAt(ctx, j.ID, last); err != nil || has {
			continue
		}
		reason := "o agente não iniciou o backup no horário previsto"
		if _, on := s.Online()[j.AgentID]; !on {
			reason = "o agente estava offline no horário previsto"
		}
		if id, err := s.Store.MarkMissed(ctx, auth.NewUUID(), j.ID, j.AgentID, last, reason); err == nil && id > 0 {
			slog.Warn("backup perdido", "job", j.Name, "previsto", last.Format(time.RFC3339))
			if s.Notify != nil {
				s.Notify.OnRunFinished(ctx, id)
			}
		}
	}
}
