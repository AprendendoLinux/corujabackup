package report

import (
	"fmt"
	"html"
	"strings"
)

func (d Daily) Text() string {
	loc := d.loc()
	var sb strings.Builder
	fmt.Fprintf(&sb, "%s\n%s\nPeríodo: %s a %s\n\n", d.Subject(), strings.Repeat("=", 40), d.From.In(loc).Format("02/01/2006 15:04"), d.To.In(loc).Format("02/01/2006 15:04"))
	fmt.Fprintf(&sb, "Sucesso: %d · Com avisos: %d · Falhas: %d · Perdidos: %d · Em execução: %d\n", d.Counts.OK, d.Counts.Warning, d.Counts.Failed, d.Counts.Missed, d.Counts.Running)
	if len(d.Attention) > 0 {
		sb.WriteString("\nPRECISA DE ATENÇÃO\n")
		for _, a := range d.Attention {
			sb.WriteString(" - " + a + "\n")
		}
	}
	sb.WriteString("\nJOBS\n")
	for _, j := range d.Jobs {
		st, size := "nunca executou", ""
		if j.Last != nil {
			st = StatusLabel(j.Last.Status) + " em " + fmtTime(j.Last.StartedAt, loc)
			size = " · " + FmtBytes(j.Last.BytesOut)
			if j.Last.CloudStatus == "ok" {
				size += " · nuvem ok"
			} else if j.Last.CloudStatus == "failed" {
				size += " · nuvem FALHOU"
			}
		}
		fmt.Fprintf(&sb, " - %s (%s): %s%s\n", j.Name, j.Agent, st, size)
	}
	credit := d.Brand.Credit()
	if credit != "" && d.Brand.CreditURL != "" {
		credit += " (" + d.Brand.CreditURL + ")"
	}
	fmt.Fprintf(&sb, "\n%s - Versão %s%s\n", d.Name, d.Version, credit)
	return sb.String()
}

const tgLimit = 3800 // abaixo do limite de 4096 caracteres do Telegram

func h(s string) string { return html.EscapeString(s) }

// Telegram devolve o relatório diário em HTML do Telegram (negrito, código), limitado ao tamanho aceito.
func (d Daily) Telegram() string {
	loc := d.loc()
	var sb strings.Builder
	icon := map[string]string{"ok": "🟢", "warn": "🟡", "fail": "🔴"}[d.Overall]
	fmt.Fprintf(&sb, "<b>%s %s</b>\n%s · %s\n", icon, h("Relatório de backup · "+d.Generated.In(loc).Format("02/01/2006")), h(d.OverallLabel()), h(d.Name))
	fmt.Fprintf(&sb, "✅ %d · ⚠️ %d · ❌ %d · 🚫 %d", d.Counts.OK, d.Counts.Warning, d.Counts.Failed, d.Counts.Missed)
	if d.Counts.Running > 0 {
		fmt.Fprintf(&sb, " · ⏳ %d", d.Counts.Running)
	}
	sb.WriteString("\n")
	if len(d.Attention) > 0 {
		sb.WriteString("\n<b>Atenção</b>\n")
		for i, a := range d.Attention {
			if i >= 8 {
				fmt.Fprintf(&sb, "… e mais %d\n", len(d.Attention)-i)
				break
			}
			sb.WriteString("• " + h(a) + "\n")
		}
	}
	sb.WriteString("\n<b>Jobs</b>\n")
	for i, j := range d.Jobs {
		line := "⚪ <b>" + h(j.Name) + "</b> · " + h(j.Agent)
		if j.Last != nil {
			line = StatusIcon(j.Last.Status) + " <b>" + h(j.Name) + "</b> · " + h(j.Agent) + " · " + h(StatusLabel(j.Last.Status))
			if j.Last.BytesOut > 0 {
				line += " · " + h(FmtBytes(j.Last.BytesOut))
			}
			switch j.Last.CloudStatus {
			case "ok":
				line += " ☁✔"
			case "failed":
				line += " ☁✖"
			}
		}
		if sb.Len()+len(line) > tgLimit {
			fmt.Fprintf(&sb, "… e mais %d job(s)\n", len(d.Jobs)-i)
			break
		}
		sb.WriteString(line + "\n")
	}
	return sb.String()
}

func (e RunEvent) Telegram() string {
	r, loc := e.Run, e.loc()
	var sb strings.Builder
	fmt.Fprintf(&sb, "<b>%s %s</b>\n<b>%s</b> · %s\n", StatusIcon(r.Status), h(StatusLabel(r.Status)), h(r.Job), h(r.Agent))
	fmt.Fprintf(&sb, "🕒 %s · duração %s", h(fmtTime(r.StartedAt, loc)), h(fmtDur(r.StartedAt, r.FinishedAt)))
	if r.BytesOut > 0 {
		fmt.Fprintf(&sb, " · %s", h(FmtBytes(r.BytesOut)))
	}
	switch r.CloudStatus {
	case "ok":
		sb.WriteString("\n☁ nuvem: enviada e conferida (" + h(r.VerifiedBy) + ")")
	case "failed":
		sb.WriteString("\n☁ nuvem: <b>o envio falhou</b>")
	}
	if r.Error != "" {
		msg := r.Error
		if len(msg) > 600 {
			msg = msg[:600] + "…"
		}
		sb.WriteString("\n\n<code>" + h(msg) + "</code>")
	}
	if u := link(e.BaseURL, "/#/historico"); u != "" {
		sb.WriteString("\n" + u)
	}
	return sb.String()
}

func (e RunEvent) Text() string {
	r, loc := e.Run, e.loc()
	s := fmt.Sprintf("%s\n\nJob: %s\nAgente: %s\nEstado: %s\nInício: %s\nDuração: %s\n", e.Subject(), r.Job, r.Agent, StatusLabel(r.Status), fmtTime(r.StartedAt, loc), fmtDur(r.StartedAt, r.FinishedAt))
	if r.BytesOut > 0 {
		s += "Arquivo: " + FmtBytes(r.BytesOut) + "\n"
	}
	switch r.CloudStatus {
	case "ok":
		s += "Nuvem: enviada e conferida (" + r.VerifiedBy + ")\n"
	case "failed":
		s += "Nuvem: o envio falhou\n"
	}
	if r.Error != "" {
		s += "\n" + r.Error + "\n"
	}
	return s + fmt.Sprintf("\n%s - Versão %s%s\n", e.Name, e.Version, e.Brand.Credit())
}

func (e AgentEvent) Telegram() string {
	loc := e.Loc
	if loc == nil {
		loc = timeUTC
	}
	return fmt.Sprintf("<b>🔴 Agente offline</b>\n<b>%s</b> sem sinal há mais de %d min\nVisto por último: %s", h(e.Agent.Name), e.Minutes, h(fmtTime(e.Agent.LastSeen, loc)))
}

func (e AgentEvent) Text() string {
	loc := e.Loc
	if loc == nil {
		loc = timeUTC
	}
	return fmt.Sprintf("%s\n\nO agente %s não dá sinal há mais de %d minutos.\nVisto por último: %s\n\n%s - Versão %s\n", e.Subject(), e.Agent.Name, e.Minutes, fmtTime(e.Agent.LastSeen, loc), e.Name, e.Version)
}
