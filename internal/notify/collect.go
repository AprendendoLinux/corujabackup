package notify

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/AprendendoLinux/corujabackup/internal/report"
	"github.com/AprendendoLinux/corujabackup/internal/sched"
	"github.com/AprendendoLinux/corujabackup/internal/store"
	"github.com/AprendendoLinux/corujabackup/internal/version"
)

var builtinColors = map[string]map[string]string{
	"coruja":   {"bg": "#f6f5fb", "surface": "#ffffff", "text": "#1e1b2e", "muted": "#6b6784", "border": "#e3e0f0", "primary": "#3b2a6b", "accent": "#f5a524", "ok": "#1f9d55", "danger": "#d64545"},
	"noturno":  {"bg": "#f6f5fb", "surface": "#ffffff", "text": "#1e1b2e", "muted": "#6b6784", "border": "#e3e0f0", "primary": "#5b45b8", "accent": "#f5a524", "ok": "#1f9d55", "danger": "#d64545"},
	"floresta": {"bg": "#f1f6f2", "surface": "#ffffff", "text": "#16251b", "muted": "#5c7364", "border": "#d9e6dc", "primary": "#1f6b45", "accent": "#e0a526", "ok": "#1f9d55", "danger": "#c93c3c"},
}

// LoadBrand lê nome, cores do tema padrão, logo e endereço público configurados na interface. E-mails e PDFs usam sempre fundo
// claro (legível em qualquer cliente): do tema noturno só se aproveita a cor principal.
func LoadBrand(ctx context.Context, st *store.Store) report.Brand {
	b := report.DefaultBrand()
	b.Version = version.Version
	m, err := st.GetSettings(ctx, "brand.name", "brand.theme", "brand.themes", "brand.logo", "brand.logo_type", "app.base_url")
	if err != nil {
		return b
	}
	if v := m["brand.name"]; v != "" {
		b.Name = v
	}
	vars := builtinColors["coruja"]
	theme := m["brand.theme"]
	if c, ok := builtinColors[theme]; ok {
		vars = c
	} else if theme != "" {
		var custom []struct {
			ID   string            `json:"id"`
			Vars map[string]string `json:"vars"`
		}
		if json.Unmarshal([]byte(m["brand.themes"]), &custom) == nil {
			for _, t := range custom {
				if t.ID == theme {
					vars = map[string]string{"primary": t.Vars["primary"], "accent": t.Vars["accent"], "ok": t.Vars["ok"], "danger": t.Vars["danger"], "muted": b.Muted, "text": b.Text, "border": b.Border, "bg": b.Bg}
					if lum(t.Vars["bg"]) > 0.6 { // tema claro: aproveita também o fundo e o texto
						vars["bg"], vars["text"], vars["muted"], vars["border"] = t.Vars["bg"], t.Vars["text"], t.Vars["muted"], t.Vars["border"]
					}
				}
			}
		}
	}
	set := func(dst *string, k string) {
		if v := vars[k]; len(v) == 7 {
			*dst = v
		}
	}
	set(&b.Primary, "primary")
	set(&b.Accent, "accent")
	set(&b.Ok, "ok")
	set(&b.Danger, "danger")
	set(&b.Bg, "bg")
	set(&b.Text, "text")
	set(&b.Muted, "muted")
	set(&b.Border, "border")
	b.BaseURL = m["app.base_url"]
	if lg := m["brand.logo"]; lg != "" {
		switch typ := m["brand.logo_type"]; typ {
		case "image/png", "image/jpeg", "image/gif": // SVG/WebP não funcionam em e-mail nem no PDF: usa só o nome
			if raw, err := base64.StdEncoding.DecodeString(lg); err == nil {
				b.LogoPNG, b.LogoMIME = raw, typ
			}
		}
	}
	return b
}

func lum(hex string) float64 {
	if len(hex) != 7 {
		return 0
	}
	var r, g, bl int
	fmt.Sscanf(hex[1:], "%02x%02x%02x", &r, &g, &bl)
	return (0.2126*float64(r) + 0.7152*float64(g) + 0.0722*float64(bl)) / 255
}

func rowFrom(r store.Run, kind string) report.RunRow {
	return report.RunRow{ID: r.ID, Job: r.JobName, Agent: r.AgentName, Kind: kind, Trigger: r.Trigger, Status: r.Status, StartedAt: r.StartedAt, FinishedAt: r.FinishedAt,
		BytesOut: r.BytesOutput, Files: r.Files, CloudStatus: r.CloudStatus, CloudPath: r.CloudPath, VerifiedBy: r.CloudVerifiedBy, Error: r.Error, OutputName: r.OutputName}
}

const onlineWindow = 90 * time.Second

// Collect monta o relatório diário das últimas 24 h (até "now").
func Collect(ctx context.Context, st *store.Store, brand report.Brand, now time.Time, loc *time.Location) (*report.Daily, error) {
	from := now.Add(-24 * time.Hour)
	runs, err := st.RunsBetween(ctx, from, now)
	if err != nil {
		return nil, err
	}
	jobs, err := st.ListJobs(ctx)
	if err != nil {
		return nil, err
	}
	last, _ := st.LastRuns(ctx)
	agents, _ := st.ListAgents(ctx)
	clouds, _ := st.ListClouds(ctx)

	d := &report.Daily{Brand: brand, Loc: loc, Generated: now, From: from, To: now}
	agentName := map[int64]string{}
	for _, a := range agents {
		agentName[a.ID] = a.Name
	}
	kind := map[int64]string{}
	for _, j := range jobs {
		kind[j.ID] = j.Kind
	}
	for _, r := range runs {
		d.Runs = append(d.Runs, rowFrom(r, kind[r.JobID]))
		switch r.Status {
		case "ok":
			d.Counts.OK++
		case "warning":
			d.Counts.Warning++
		case "failed", "canceled":
			if r.Status == "failed" {
				d.Counts.Failed++
			}
		case "missed":
			d.Counts.Missed++
		case "running", "queued":
			d.Counts.Running++
		}
	}
	sort.SliceStable(jobs, func(i, j int) bool { return jobs[i].Name < jobs[j].Name })
	for _, j := range jobs {
		jr := report.JobRow{Name: j.Name, Agent: agentName[j.AgentID], Kind: j.Kind, Enabled: j.Enabled}
		if lr, ok := last[j.ID]; ok {
			row := rowFrom(lr, j.Kind)
			jr.Last = &row
		}
		if sc, err := sched.Parse(j.Cron, j.Timezone); err == nil && j.Enabled {
			n := sc.Next(now)
			jr.NextRun = &n
		}
		d.Jobs = append(d.Jobs, jr)
	}

	overall := "ok"
	bump := func(level string) {
		if level == "fail" || (level == "warn" && overall == "ok") {
			overall = level
		}
	}
	short := func(s string) string {
		s = strings.TrimSpace(s)
		if len(s) > 160 {
			s = s[:160] + "…"
		}
		return s
	}
	for _, r := range d.Runs {
		switch r.Status {
		case "failed":
			d.Attention = append(d.Attention, fmt.Sprintf("%s (%s): falhou — %s", r.Job, r.Agent, short(r.Error)))
			bump("fail")
		case "missed":
			d.Attention = append(d.Attention, fmt.Sprintf("%s (%s): backup perdido — %s", r.Job, r.Agent, short(r.Error)))
			bump("fail")
		case "warning":
			d.Attention = append(d.Attention, fmt.Sprintf("%s (%s): concluído com avisos", r.Job, r.Agent))
			bump("warn")
		}
	}
	for _, a := range agents {
		row := report.AgentRow{Name: a.Name, LastSeen: a.LastSeenAt, Version: a.AgentVersion, Revoked: a.RevokedAt != nil}
		row.Online = a.LastSeenAt != nil && now.Sub(*a.LastSeenAt) < onlineWindow
		d.Agents = append(d.Agents, row)
		if !row.Revoked && !row.Online && a.LastSeenAt != nil {
			d.Attention = append(d.Attention, fmt.Sprintf("Agente %s está offline desde %s", a.Name, a.LastSeenAt.In(loc).Format("02/01 15:04")))
			bump("fail")
		}
	}
	for _, c := range clouds {
		row := report.CloudRow{Name: c.Name, Type: map[string]string{"onedrive": "OneDrive", "gdrive": "Google Drive"}[c.Type], Account: c.AccountLabel, Status: c.Status, Total: c.QuotaTotal, Used: c.QuotaUsed, Err: c.LastError}
		d.Clouds = append(d.Clouds, row)
		if c.Status == "error" {
			d.Attention = append(d.Attention, fmt.Sprintf("Nuvem %s: %s", c.Name, firstNonEmpty(c.LastError, "com erro")))
			bump("fail")
		}
		if c.QuotaTotal > 0 {
			if pct := int(float64(c.QuotaUsed) / float64(c.QuotaTotal) * 100); pct >= 85 {
				d.Attention = append(d.Attention, fmt.Sprintf("Nuvem %s está com %d%% da cota usada", c.Name, pct))
				bump("warn")
			}
		}
	}
	d.Attention = dedupe(d.Attention)
	d.Overall = overall
	return d, nil
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

// dedupe junta linhas idênticas ("x (×2)"), preservando a ordem da primeira aparição.
func dedupe(in []string) []string {
	count := map[string]int{}
	var order []string
	for _, l := range in {
		if count[l] == 0 {
			order = append(order, l)
		}
		count[l]++
	}
	out := make([]string, 0, len(order))
	for _, l := range order {
		if n := count[l]; n > 1 {
			l = fmt.Sprintf("%s (×%d)", l, n)
		}
		out = append(out, l)
	}
	return out
}
