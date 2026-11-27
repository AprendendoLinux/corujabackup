package report

import (
	"sort"
	"time"
)

// DayStat resume as execuções de um dia (no fuso do relatório).
type DayStat struct {
	Day      time.Time `json:"-"`
	Date     string    `json:"date"` // AAAA-MM-DD
	OK       int       `json:"ok"`
	Warning  int       `json:"warning"`
	Failed   int       `json:"failed"`
	Missed   int       `json:"missed"`
	Canceled int       `json:"canceled"`
	Bytes    int64     `json:"bytes"` // gerado pelos backups concluídos (sucesso ou aviso)
}

// Finished é o total de execuções que chegaram a um resultado (sem as canceladas e as em andamento).
func (d DayStat) Finished() int { return d.OK + d.Warning + d.Failed + d.Missed }

// Totals soma vários dias.
type Totals struct {
	Runs     int   `json:"runs"`
	OK       int   `json:"ok"`
	Warning  int   `json:"warning"`
	Failed   int   `json:"failed"`
	Missed   int   `json:"missed"`
	Canceled int   `json:"canceled"`
	Bytes    int64 `json:"bytes"`
}

// SuccessRate é a fração de execuções concluídas (com ou sem aviso) entre as que chegaram a um resultado; -1 sem dados.
func (t Totals) SuccessRate() float64 {
	n := t.OK + t.Warning + t.Failed + t.Missed
	if n == 0 {
		return -1
	}
	return float64(t.OK+t.Warning) / float64(n)
}

func (t *Totals) add(r RunRow) {
	switch r.Status {
	case "ok":
		t.OK++
		t.Bytes += r.BytesOut
	case "warning":
		t.Warning++
		t.Bytes += r.BytesOut
	case "failed":
		t.Failed++
	case "missed":
		t.Missed++
	case "canceled":
		t.Canceled++
	default:
		return // em andamento
	}
	t.Runs++
}

// Sum soma as execuções terminadas.
func Sum(runs []RunRow) Totals {
	var t Totals
	for _, r := range runs {
		t.add(r)
	}
	return t
}

func runTime(r RunRow) *time.Time {
	if r.StartedAt != nil {
		return r.StartedAt
	}
	if r.Scheduled != nil {
		return r.Scheduled
	}
	return r.FinishedAt
}

// History devolve days dias seguidos terminando no dia de "to" (no fuso loc), um DayStat por dia, mesmo sem execuções.
func History(runs []RunRow, to time.Time, days int, loc *time.Location) []DayStat {
	if loc == nil {
		loc = time.UTC
	}
	end := to.In(loc)
	first := time.Date(end.Year(), end.Month(), end.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, -(days - 1))
	out := make([]DayStat, days)
	idx := map[string]int{}
	for i := range out {
		d := first.AddDate(0, 0, i)
		out[i] = DayStat{Day: d, Date: d.Format("2006-01-02")}
		idx[out[i].Date] = i
	}
	for _, r := range runs {
		t := runTime(r)
		if t == nil {
			continue
		}
		i, ok := idx[t.In(loc).Format("2006-01-02")]
		if !ok {
			continue
		}
		var tt Totals
		tt.add(r)
		x := &out[i]
		x.OK += tt.OK
		x.Warning += tt.Warning
		x.Failed += tt.Failed
		x.Missed += tt.Missed
		x.Canceled += tt.Canceled
		x.Bytes += tt.Bytes
	}
	return out
}

// JobVolume é o volume gerado por um job no período.
type JobVolume struct {
	Name  string
	Bytes int64
}

// VolumeByJob soma o volume gerado por job, do maior para o menor.
func VolumeByJob(runs []RunRow) []JobVolume {
	m := map[string]int64{}
	for _, r := range runs {
		if r.Status == "ok" || r.Status == "warning" {
			m[r.Job] += r.BytesOut
		}
	}
	out := make([]JobVolume, 0, len(m))
	for k, v := range m {
		out = append(out, JobVolume{k, v})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Bytes != out[j].Bytes {
			return out[i].Bytes > out[j].Bytes
		}
		return out[i].Name < out[j].Name
	})
	return out
}
