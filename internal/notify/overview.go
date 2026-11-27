package notify

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/AprendendoLinux/corujabackup/internal/report"
	"github.com/AprendendoLinux/corujabackup/internal/store"
)

// Overview é o que o painel mostra: execuções por dia, totais do período (e do anterior, para comparar), a saúde de
// cada job, o uso dos storages e os agentes.
type Overview struct {
	Days        int              `json:"days"`
	From        string           `json:"from"` // AAAA-MM-DD
	To          string           `json:"to"`
	History     []report.DayStat `json:"history"`
	Totals      report.Totals    `json:"totals"`
	Previous    report.Totals    `json:"previous"`
	Jobs        []JobHealth      `json:"jobs"`
	Storages    []StorageUse     `json:"storages"`
	AgentsTotal int              `json:"agents_total"`
	AgentsOn    int              `json:"agents_online"`
	Running     int              `json:"running"`
}

type JobHealth struct {
	ID        int64      `json:"id"`
	Name      string     `json:"name"`
	Agent     string     `json:"agent"`
	Enabled   bool       `json:"enabled"`
	Runs      int        `json:"runs"`      // terminadas no período
	Good      int        `json:"good"`      // sucesso ou aviso
	Bytes     int64      `json:"bytes"`     // gerado no período
	Last      string     `json:"last"`      // estado da última execução ("" = nunca rodou)
	LastAt    *time.Time `json:"last_at"`   // início da última execução
	LastBytes int64      `json:"last_size"` // tamanho da última
	LastSecs  int64      `json:"last_secs"` // duração da última
	Storage   string     `json:"storage"`   // nome do storage ("" = só local)
}

type StorageUse struct {
	Name   string `json:"name"`
	Type   string `json:"type"`
	Status string `json:"status"`
	Used   int64  `json:"used"`
	Total  int64  `json:"total"`
	Jobs   int    `json:"jobs"`
}

// BuildOverview monta o painel dos últimos days dias (até now, no fuso loc).
func BuildOverview(ctx context.Context, st *store.Store, now time.Time, loc *time.Location, days int) (*Overview, error) {
	end := now.In(loc)
	return BuildOverviewRange(ctx, st, now, loc, time.Date(end.Year(), end.Month(), end.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, -(days-1)), end)
}

// BuildOverviewRange monta o painel dos dias de "from" a "to" (inclusive, no fuso loc). O período anterior, usado nas
// comparações, tem o mesmo número de dias e termina na véspera de "from".
func BuildOverviewRange(ctx context.Context, st *store.Store, now time.Time, loc *time.Location, from, to time.Time) (*Overview, error) {
	start := time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, loc)
	lastDay := time.Date(to.Year(), to.Month(), to.Day(), 0, 0, 0, 0, loc)
	days := int(lastDay.Sub(start).Hours()/24+0.5) + 1
	stop := lastDay.AddDate(0, 0, 1)
	if stop.After(now) {
		stop = now
	}
	runs, err := st.RunsBetween(ctx, start.AddDate(0, 0, -days), stop)
	if err != nil {
		return nil, err
	}
	jobs, err := st.ListJobs(ctx)
	if err != nil {
		return nil, err
	}
	agents, _ := st.ListAgents(ctx)
	clouds, _ := st.ListClouds(ctx)
	last, _ := st.LastRuns(ctx)

	agentName := map[int64]string{}
	for _, a := range agents {
		agentName[a.ID] = a.Name
	}
	kind := map[int64]string{}
	for _, j := range jobs {
		kind[j.ID] = j.Kind
	}
	o := &Overview{Days: days}
	if days < 1 {
		return nil, fmt.Errorf("período inválido")
	}
	var cur, prev []report.RunRow
	perJob := map[int64]*report.Totals{}
	for _, r := range runs {
		row := rowFrom(r, kind[r.JobID])
		t := runTime(r)
		if r.Status == "running" || r.Status == "queued" {
			o.Running++
		}
		if t != nil && t.Before(start) {
			prev = append(prev, row)
			continue
		}
		cur = append(cur, row)
		if perJob[r.JobID] == nil {
			perJob[r.JobID] = &report.Totals{}
		}
		*perJob[r.JobID] = sumOne(*perJob[r.JobID], row)
	}
	o.History = report.History(cur, lastDay, days, loc)
	o.From, o.To = start.Format("2006-01-02"), lastDay.Format("2006-01-02")
	o.Totals, o.Previous = report.Sum(cur), report.Sum(prev)

	cloudName := map[int64]string{}
	usage := map[int64]int{}
	for _, c := range clouds {
		cloudName[c.ID] = c.Name
	}
	for _, j := range jobs {
		h := JobHealth{ID: j.ID, Name: j.Name, Agent: agentName[j.AgentID], Enabled: j.Enabled}
		if j.CloudAccountID != nil {
			h.Storage = cloudName[*j.CloudAccountID]
			usage[*j.CloudAccountID]++
		}
		if t := perJob[j.ID]; t != nil {
			h.Runs, h.Good, h.Bytes = t.Runs-t.Canceled, t.OK+t.Warning, t.Bytes
		}
		if lr, ok := last[j.ID]; ok {
			h.Last, h.LastAt, h.LastBytes = lr.Status, lr.StartedAt, lr.BytesOutput
			if lr.StartedAt != nil && lr.FinishedAt != nil {
				h.LastSecs = int64(lr.FinishedAt.Sub(*lr.StartedAt).Seconds())
			}
		}
		o.Jobs = append(o.Jobs, h)
	}
	sort.SliceStable(o.Jobs, func(a, b int) bool { return o.Jobs[a].Name < o.Jobs[b].Name })
	for _, c := range clouds {
		o.Storages = append(o.Storages, StorageUse{Name: c.Name, Type: c.Type, Status: c.Status, Used: c.QuotaUsed, Total: c.QuotaTotal, Jobs: usage[c.ID]})
	}
	for _, a := range agents {
		if a.RevokedAt != nil {
			continue
		}
		o.AgentsTotal++
		if a.LastSeenAt != nil && now.Sub(*a.LastSeenAt) < onlineWindow {
			o.AgentsOn++
		}
	}
	return o, nil
}

func sumOne(t report.Totals, r report.RunRow) report.Totals {
	s := report.Sum([]report.RunRow{r})
	t.Runs += s.Runs
	t.OK += s.OK
	t.Warning += s.Warning
	t.Failed += s.Failed
	t.Missed += s.Missed
	t.Canceled += s.Canceled
	t.Bytes += s.Bytes
	return t
}

func runTime(r store.Run) *time.Time {
	if r.StartedAt != nil {
		return r.StartedAt
	}
	if r.ScheduledFor != nil {
		return r.ScheduledFor
	}
	return &r.CreatedAt
}
