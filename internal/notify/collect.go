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
	"coruja":    {"bg": "#f6f5fb", "surface": "#ffffff", "text": "#1e1b2e", "muted": "#6b6784", "border": "#e3e0f0", "primary": "#3b2a6b", "accent": "#f5a524", "ok": "#1f9d55", "danger": "#d64545"},
	"noturno":   {"bg": "#f6f5fb", "surface": "#ffffff", "text": "#1e1b2e", "muted": "#6b6784", "border": "#e3e0f0", "primary": "#5b45b8", "accent": "#f5a524", "ok": "#1f9d55", "danger": "#d64545"},
	"floresta":  {"bg": "#f1f6f2", "surface": "#ffffff", "text": "#16251b", "muted": "#5c7364", "border": "#d9e6dc", "primary": "#1f6b45", "accent": "#e0a526", "ok": "#1f9d55", "danger": "#c93c3c"},
	"oceano":    {"bg": "#f1f7f9", "surface": "#ffffff", "text": "#0f252d", "muted": "#4f6b74", "border": "#d3e4ea", "primary": "#0b5f74", "accent": "#e8693f", "ok": "#187f4e", "danger": "#c8363f"},
	"grafite":   {"bg": "#f3f4f6", "surface": "#ffffff", "text": "#1a1e24", "muted": "#5b636e", "border": "#dde1e6", "primary": "#2c3541", "accent": "#1f7fd1", "ok": "#1a8a4c", "danger": "#cc3b3b"},
	"bordo":     {"bg": "#faf5f6", "surface": "#ffffff", "text": "#2a1419", "muted": "#77575e", "border": "#ecdde0", "primary": "#7b1e34", "accent": "#a97616", "ok": "#1a8a4c", "danger": "#c22f2f"},
	"terracota": {"bg": "#fbf6f2", "surface": "#ffffff", "text": "#2b1a11", "muted": "#7a6052", "border": "#eedfd4", "primary": "#a8441a", "accent": "#6b4fc4", "ok": "#1a8a4c", "danger": "#c22f2f"},
	"onix":      {"bg": "#f5f5f4", "surface": "#ffffff", "text": "#0c0c0d", "muted": "#57575c", "border": "#e4e4e7", "primary": "#111113", "accent": "#9a7413", "ok": "#1a8a4c", "danger": "#c22f2f"},
}

// LoadBrand lê nome, cores do tema padrão, logo e endereço público configurados na interface. E-mails e PDFs usam sempre fundo
// claro (legível em qualquer cliente): do tema noturno só se aproveita a cor principal.
func LoadBrand(ctx context.Context, st *store.Store) report.Brand {
	b := report.DefaultBrand()
	b.Version = version.Version
	m, err := st.GetSettings(ctx, "brand.name", "brand.theme", "brand.themes", "brand.logo", "brand.logo_type", "app.base_url", "brand.credit")
	themeLogo := ""
	if err != nil {
		return b
	}
	if v := m["brand.name"]; v != "" {
		b.Name = v
	}
	if v := m["brand.credit"]; v != "" {
		var c struct{ Label, Name, URL string }
		if json.Unmarshal([]byte(v), &c) == nil {
			b.CreditLabel, b.CreditName, b.CreditURL = c.Label, c.Name, c.URL
		}
	}
	vars := builtinColors["coruja"]
	theme := m["brand.theme"]
	if c, ok := builtinColors[theme]; ok {
		vars = c
	} else if theme != "" {
		var custom []struct {
			ID   string            `json:"id"`
			Vars map[string]string `json:"vars"`
			Pkg  *struct {
				Doc     map[string]string `json:"doc"`
				LogoDoc string            `json:"logo_doc"`
			} `json:"pkg"`
		}
		if json.Unmarshal([]byte(m["brand.themes"]), &custom) == nil {
			for _, t := range custom {
				if t.ID == theme {
					vars = map[string]string{"primary": t.Vars["primary"], "accent": t.Vars["accent"], "ok": t.Vars["ok"], "danger": t.Vars["danger"], "muted": b.Muted, "text": b.Text, "border": b.Border, "bg": b.Bg}
					if lum(t.Vars["bg"]) > 0.6 { // tema claro: aproveita também o fundo e o texto
						vars["bg"], vars["text"], vars["muted"], vars["border"] = t.Vars["bg"], t.Vars["text"], t.Vars["muted"], t.Vars["border"]
					}
					if t.Pkg != nil { // pacote de tema: cores próprias de e-mail/PDF e logo próprio têm prioridade
						for k, v := range t.Pkg.Doc {
							vars[k] = v
						}
						if t.Pkg.LogoDoc != "" {
							themeLogo = t.ID
						}
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
	if themeLogo != "" {
		a, e := st.GetSettings(ctx, "brand.asset."+themeLogo+".doc", "brand.asset."+themeLogo+".doc_type")
		if raw, de := base64.StdEncoding.DecodeString(a["brand.asset."+themeLogo+".doc"]); e == nil && de == nil && len(raw) > 0 {
			b.LogoPNG, b.LogoMIME = raw, a["brand.asset."+themeLogo+".doc_type"]
			return b
		}
	}
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
	return report.RunRow{ID: r.ID, Job: r.JobName, Agent: r.AgentName, Kind: kind, Trigger: r.Trigger, Status: r.Status, StartedAt: r.StartedAt, FinishedAt: r.FinishedAt, Scheduled: r.ScheduledFor,
		BytesOut: r.BytesOutput, Files: r.Files, CloudStatus: r.CloudStatus, CloudPath: r.CloudPath, VerifiedBy: r.CloudVerifiedBy, Error: r.Error, OutputName: r.OutputName}
}

const onlineWindow = 90 * time.Second

// HistoryDays é quantos dias o gráfico do relatório mostra.
const HistoryDays = 14

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
	if hist, err := st.RunsBetween(ctx, now.AddDate(0, 0, -HistoryDays), now); err == nil {
		rows := make([]report.RunRow, 0, len(hist))
		for _, r := range hist {
			rows = append(rows, rowFrom(r, kind[r.JobID]))
		}
		d.History = report.History(rows, now, HistoryDays, loc)
	}
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
