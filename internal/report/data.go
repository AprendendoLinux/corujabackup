// Package report monta e-mails (HTML e texto), mensagens de Telegram e o PDF A4 dos relatórios do Coruja Backup, a partir
// de dados já coletados (sem acesso a banco): fácil de testar e de pré-visualizar.
package report

import (
	"fmt"
	"strings"
	"time"
)

type Brand struct {
	Name     string
	Primary  string // #rrggbb
	Accent   string
	Bg       string
	Text     string
	Muted    string
	Border   string
	Ok       string
	Danger   string
	LogoPNG  []byte // PNG/JPEG/GIF usável em e-mail e PDF (nil = sem logo; usa o nome)
	LogoMIME string
	Version  string
	BaseURL  string // endereço público do sistema (para links); vazio = sem links
}

func DefaultBrand() Brand {
	return Brand{Name: "Coruja Backup", Primary: "#3b2a6b", Accent: "#f5a524", Bg: "#f6f5fb", Text: "#1e1b2e", Muted: "#6b6784", Border: "#e3e0f0", Ok: "#1f9d55", Danger: "#d64545", Version: "0.0.8"}
}

type RunRow struct {
	ID          int64
	Job         string
	Agent       string
	Kind        string // files | database
	Trigger     string
	Status      string // ok warning failed canceled missed running queued
	StartedAt   *time.Time
	FinishedAt  *time.Time
	BytesOut    int64
	Files       int64
	CloudStatus string // none ok failed
	CloudPath   string
	VerifiedBy  string
	Error       string
	OutputName  string
}

type JobRow struct {
	Name    string
	Agent   string
	Kind    string
	Enabled bool
	Last    *RunRow
	NextRun *time.Time
}

type AgentRow struct {
	Name     string
	Online   bool
	LastSeen *time.Time
	Version  string
	Revoked  bool
}

type CloudRow struct {
	Name    string
	Type    string
	Account string
	Status  string
	Total   int64
	Used    int64
	Err     string
}

type Counts struct{ OK, Warning, Failed, Missed, Running int }

type Daily struct {
	Brand
	Loc       *time.Location
	Generated time.Time
	From, To  time.Time
	Overall   string // ok | warn | fail
	Counts    Counts
	Jobs      []JobRow
	Runs      []RunRow
	Agents    []AgentRow
	Clouds    []CloudRow
	Attention []string
}

// RunEvent é a notificação de uma execução (falha, aviso ou sucesso).
type RunEvent struct {
	Brand
	Loc *time.Location
	Run RunRow
}

type AgentEvent struct {
	Brand
	Loc     *time.Location
	Agent   AgentRow
	Minutes int
}

var statusPT = map[string]string{"ok": "Sucesso", "warning": "Com avisos", "failed": "Falhou", "canceled": "Cancelado", "missed": "Perdido", "running": "Executando", "queued": "Na fila"}

func StatusLabel(s string) string {
	if v, ok := statusPT[s]; ok {
		return v
	}
	return s
}

func StatusIcon(s string) string {
	switch s {
	case "ok":
		return "🟢"
	case "warning":
		return "🟡"
	case "failed", "missed":
		return "🔴"
	}
	return "⚪"
}

func (b Brand) statusColor(s string) string {
	switch s {
	case "ok":
		return b.Ok
	case "warning":
		return b.Accent
	case "failed", "missed":
		return b.Danger
	}
	return b.Muted
}

func (d Daily) loc() *time.Location {
	if d.Loc != nil {
		return d.Loc
	}
	return time.UTC
}

func fmtTime(t *time.Time, loc *time.Location) string {
	if t == nil {
		return "—"
	}
	return t.In(loc).Format("02/01/2006 15:04")
}

func fmtDur(a, b *time.Time) string {
	if a == nil || b == nil {
		return "—"
	}
	s := int(b.Sub(*a).Round(time.Second).Seconds())
	if s < 0 {
		s = 0
	}
	switch {
	case s < 60:
		return fmt.Sprintf("%ds", s)
	case s < 3600:
		return fmt.Sprintf("%dmin %ds", s/60, s%60)
	}
	return fmt.Sprintf("%dh %dmin", s/3600, s%3600/60)
}

func FmtBytes(n int64) string {
	if n <= 0 {
		return "—"
	}
	const u = 1024.0
	v, i := float64(n), 0
	units := []string{"B", "KiB", "MiB", "GiB", "TiB"}
	for v >= u && i < len(units)-1 {
		v /= u
		i++
	}
	if i == 0 {
		return fmt.Sprintf("%d B", n)
	}
	return fmt.Sprintf("%.1f %s", v, units[i])
}

func (d Daily) OverallLabel() string {
	switch d.Overall {
	case "fail":
		return "Atenção: há falhas"
	case "warn":
		return "Concluído com avisos"
	}
	return "Tudo certo"
}

func (d Daily) overallColor() string {
	switch d.Overall {
	case "fail":
		return d.Danger
	case "warn":
		return d.Accent
	}
	return d.Ok
}

func (d Daily) Subject() string {
	icon := map[string]string{"ok": "🟢", "warn": "🟡", "fail": "🔴"}[d.Overall]
	return fmt.Sprintf("%s Relatório de backup · %s · %s", icon, d.Generated.In(d.loc()).Format("02/01/2006"), d.OverallLabel())
}

func (e RunEvent) loc() *time.Location {
	if e.Loc != nil {
		return e.Loc
	}
	return time.UTC
}

func (e RunEvent) Subject() string {
	r := e.Run
	return fmt.Sprintf("%s %s: %s em %s", StatusIcon(r.Status), StatusLabel(r.Status), r.Job, r.Agent)
}

func (e AgentEvent) Subject() string {
	return fmt.Sprintf("🔴 Agente offline: %s", e.Agent.Name)
}

func link(base, path string) string {
	base = strings.TrimRight(base, "/")
	if base == "" {
		return ""
	}
	return base + path
}

var timeUTC = time.UTC
