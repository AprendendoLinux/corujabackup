package report

import (
	"fmt"
	"html"
	"strings"
)

var esc = html.EscapeString

const font = "-apple-system,'Segoe UI',Roboto,Helvetica,Arial,sans-serif"

// shell monta o invólucro HTML do e-mail (tabelas + CSS inline, compatível com clientes de e-mail). A logo vai por CID
// ("cid:logo") quando houver.
func shell(b Brand, title, banner, bannerColor, body string, hasLogo bool) string {
	var head string
	if hasLogo {
		head = fmt.Sprintf(`<img src="cid:logo" alt="%s" height="40" style="display:block;height:40px;max-width:200px;border:0">`, esc(b.Name))
	} else {
		head = fmt.Sprintf(`<span style="font-size:22px;font-weight:700;color:#ffffff">%s</span>`, esc(b.Name))
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, `<!doctype html><html lang="pt-BR"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>%s</title></head>`, esc(title))
	fmt.Fprintf(&sb, `<body style="margin:0;padding:0;background:%s;font-family:%s;color:%s">`, b.Bg, font, b.Text)
	fmt.Fprintf(&sb, `<table role="presentation" width="100%%" cellpadding="0" cellspacing="0" style="background:%s"><tr><td align="center" style="padding:24px 12px">`, b.Bg)
	fmt.Fprintf(&sb, `<table role="presentation" width="640" cellpadding="0" cellspacing="0" style="width:100%%;max-width:640px;background:#ffffff;border:1px solid %s;border-radius:12px;overflow:hidden">`, b.Border)
	fmt.Fprintf(&sb, `<tr><td style="background:%s;padding:18px 24px;border-bottom:4px solid %s">%s</td></tr>`, b.Primary, b.Accent, head)
	if banner != "" {
		fmt.Fprintf(&sb, `<tr><td style="padding:20px 24px 4px"><div style="display:inline-block;background:%s;color:#ffffff;border-radius:999px;padding:6px 14px;font-size:14px;font-weight:600">%s</div></td></tr>`, bannerColor, esc(banner))
	}
	fmt.Fprintf(&sb, `<tr><td style="padding:12px 24px 24px;font-size:14px;line-height:1.5">%s</td></tr>`, body)
	fmt.Fprintf(&sb, `<tr><td style="padding:14px 24px;background:%s;border-top:1px solid %s;font-size:12px;color:%s;text-align:center">%s - Versão %s - Desenvolvido por <a href="https://henrique.tec.br" style="color:%s">Henrique Fagundes</a></td></tr>`,
		b.Bg, b.Border, b.Muted, esc(b.Name), esc(b.Version), b.Primary)
	sb.WriteString(`</table></td></tr></table></body></html>`)
	return sb.String()
}

func badge(b Brand, status string) string {
	return fmt.Sprintf(`<span style="display:inline-block;background:%s;color:#ffffff;border-radius:999px;padding:2px 10px;font-size:12px;font-weight:600;white-space:nowrap">%s</span>`, b.statusColor(status), esc(StatusLabel(status)))
}

func kv(b Brand, k, v string) string {
	return fmt.Sprintf(`<tr><td style="padding:6px 0;color:%s;width:150px;vertical-align:top">%s</td><td style="padding:6px 0;font-weight:500">%s</td></tr>`, b.Muted, esc(k), v)
}

func th(b Brand, s string) string {
	return fmt.Sprintf(`<th align="left" style="padding:8px 10px;font-size:12px;color:%s;border-bottom:2px solid %s;white-space:nowrap">%s</th>`, b.Muted, b.Border, esc(s))
}

func td(b Brand, s string) string {
	return fmt.Sprintf(`<td style="padding:8px 10px;border-bottom:1px solid %s;vertical-align:top">%s</td>`, b.Border, s)
}

func h2(b Brand, s string) string {
	return fmt.Sprintf(`<h2 style="font-size:16px;margin:22px 0 8px;color:%s">%s</h2>`, b.Primary, esc(s))
}

func counter(b Brand, n int, label, color string) string {
	return fmt.Sprintf(`<td align="center" style="padding:10px 6px;border:1px solid %s;border-radius:10px;width:20%%"><div style="font-size:24px;font-weight:700;color:%s">%d</div><div style="font-size:11px;color:%s">%s</div></td><td style="width:6px"></td>`, b.Border, color, n, b.Muted, esc(label))
}

func (d Daily) HTML() string {
	b, loc := d.Brand, d.loc()
	var sb strings.Builder
	fmt.Fprintf(&sb, `<p style="margin:8px 0 14px;color:%s">Período: %s a %s</p>`, b.Muted, d.From.In(loc).Format("02/01/2006 15:04"), d.To.In(loc).Format("02/01/2006 15:04"))
	sb.WriteString(`<table role="presentation" width="100%" cellpadding="0" cellspacing="0"><tr>`)
	sb.WriteString(counter(b, d.Counts.OK, "Sucesso", b.Ok) + counter(b, d.Counts.Warning, "Com avisos", b.Accent) + counter(b, d.Counts.Failed, "Falhas", b.Danger) + counter(b, d.Counts.Missed, "Perdidos", b.Danger))
	sb.WriteString(fmt.Sprintf(`<td align="center" style="padding:10px 6px;border:1px solid %s;border-radius:10px;width:20%%"><div style="font-size:24px;font-weight:700;color:%s">%d</div><div style="font-size:11px;color:%s">Em execução</div></td>`, b.Border, b.Muted, d.Counts.Running, b.Muted))
	sb.WriteString(`</tr></table>`)

	if len(d.Attention) > 0 {
		sb.WriteString(h2(b, "Precisa de atenção"))
		sb.WriteString(fmt.Sprintf(`<ul style="margin:0;padding-left:20px;color:%s">`, b.Danger))
		for _, a := range d.Attention {
			sb.WriteString(fmt.Sprintf(`<li style="margin:4px 0">%s</li>`, esc(a)))
		}
		sb.WriteString(`</ul>`)
	}

	sb.WriteString(h2(b, "Jobs"))
	sb.WriteString(`<table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="border-collapse:collapse;font-size:13px"><tr>`)
	for _, h := range []string{"Job", "Última execução", "Estado", "Tamanho", "Nuvem"} {
		sb.WriteString(th(b, h))
	}
	sb.WriteString(`</tr>`)
	if len(d.Jobs) == 0 {
		sb.WriteString(fmt.Sprintf(`<tr><td colspan="5" style="padding:14px;color:%s;text-align:center">Nenhum job cadastrado.</td></tr>`, b.Muted))
	}
	for _, j := range d.Jobs {
		name := fmt.Sprintf(`<b>%s</b><div style="font-size:12px;color:%s">%s</div>`, esc(j.Name), b.Muted, esc(j.Agent))
		last, st, size, cl := "nunca executou", `<span style="color:`+b.Muted+`">—</span>`, "—", "—"
		if j.Last != nil {
			r := j.Last
			last = fmtTime(r.StartedAt, loc)
			if r.StartedAt == nil {
				last = "—"
			}
			st = badge(b, r.Status)
			size = FmtBytes(r.BytesOut)
			switch r.CloudStatus {
			case "ok":
				cl = fmt.Sprintf(`<span style="color:%s">✔ conferida</span>`, b.Ok)
			case "failed":
				cl = fmt.Sprintf(`<span style="color:%s">✖ falhou</span>`, b.Danger)
			}
		}
		if !j.Enabled {
			name += fmt.Sprintf(`<div style="font-size:11px;color:%s">pausado</div>`, b.Muted)
		}
		sb.WriteString("<tr>" + td(b, name) + td(b, esc(last)) + td(b, st) + td(b, esc(size)) + td(b, cl) + "</tr>")
	}
	sb.WriteString(`</table>`)

	if len(d.Clouds) > 0 {
		sb.WriteString(h2(b, "Nuvem"))
		for _, c := range d.Clouds {
			pct := 0
			if c.Total > 0 {
				pct = int(float64(c.Used) / float64(c.Total) * 100)
			}
			color := b.Primary
			if pct > 90 {
				color = b.Danger
			}
			line := fmt.Sprintf(`<b>%s</b> <span style="color:%s">%s</span>`, esc(c.Name), b.Muted, esc(strings.TrimSpace(c.Type+" "+c.Account)))
			if c.Status != "connected" {
				line += fmt.Sprintf(` <span style="color:%s">· %s</span>`, b.Danger, esc(firstNonEmpty(c.Err, "com erro")))
			}
			sb.WriteString(`<div style="margin:6px 0">` + line)
			if c.Total > 0 {
				sb.WriteString(fmt.Sprintf(`<div style="background:%s;border-radius:6px;height:8px;margin-top:4px"><div style="background:%s;width:%d%%;height:8px;border-radius:6px"></div></div><div style="font-size:12px;color:%s">%s de %s (%d%%)</div>`, b.Border, color, pct, b.Muted, FmtBytes(c.Used), FmtBytes(c.Total), pct))
			}
			sb.WriteString(`</div>`)
		}
	}

	sb.WriteString(h2(b, "Agentes"))
	sb.WriteString(`<table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="border-collapse:collapse;font-size:13px">`)
	for _, a := range d.Agents {
		st := fmt.Sprintf(`<span style="color:%s">● online</span>`, b.Ok)
		if a.Revoked {
			st = fmt.Sprintf(`<span style="color:%s">revogado</span>`, b.Muted)
		} else if !a.Online {
			st = fmt.Sprintf(`<span style="color:%s">○ offline (visto %s)</span>`, b.Danger, esc(fmtTime(a.LastSeen, loc)))
		}
		sb.WriteString("<tr>" + td(b, "<b>"+esc(a.Name)+"</b>") + td(b, st) + td(b, esc(a.Version)) + "</tr>")
	}
	sb.WriteString(`</table>`)
	if u := link(b.BaseURL, "/#/historico"); u != "" {
		sb.WriteString(fmt.Sprintf(`<p style="margin:20px 0 0"><a href="%s" style="color:%s;font-weight:600">Abrir o histórico completo →</a></p>`, esc(u), b.Primary))
	}
	return shell(b, d.Subject(), d.OverallLabel(), d.overallColor(), sb.String(), len(b.LogoPNG) > 0)
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

func (e RunEvent) HTML() string {
	b, loc, r := e.Brand, e.loc(), e.Run
	var sb strings.Builder
	sb.WriteString(`<table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="font-size:14px">`)
	sb.WriteString(kv(b, "Job", "<b>"+esc(r.Job)+"</b>"))
	sb.WriteString(kv(b, "Servidor", esc(r.Agent)))
	sb.WriteString(kv(b, "Estado", badge(b, r.Status)))
	sb.WriteString(kv(b, "Início", esc(fmtTime(r.StartedAt, loc))))
	sb.WriteString(kv(b, "Duração", esc(fmtDur(r.StartedAt, r.FinishedAt))))
	if r.BytesOut > 0 {
		sb.WriteString(kv(b, "Arquivo gerado", esc(FmtBytes(r.BytesOut))+fmt.Sprintf(" · %d itens", r.Files)))
	}
	if r.OutputName != "" {
		sb.WriteString(kv(b, "Arquivo", `<span style="font-family:monospace;font-size:12px">`+esc(r.OutputName)+`</span>`))
	}
	switch r.CloudStatus {
	case "ok":
		sb.WriteString(kv(b, "Nuvem", fmt.Sprintf(`<span style="color:%s">✔ enviada e conferida (%s)</span>`, b.Ok, esc(r.VerifiedBy))))
	case "failed":
		sb.WriteString(kv(b, "Nuvem", fmt.Sprintf(`<span style="color:%s">✖ o envio falhou</span>`, b.Danger)))
	}
	sb.WriteString(`</table>`)
	if r.Error != "" {
		sb.WriteString(fmt.Sprintf(`<div style="margin-top:14px;padding:12px 14px;border-left:4px solid %s;background:#fdf2f2;border-radius:6px;color:%s">%s</div>`, b.Danger, b.Text, esc(r.Error)))
	}
	if u := link(b.BaseURL, "/#/historico"); u != "" {
		sb.WriteString(fmt.Sprintf(`<p style="margin:18px 0 0"><a href="%s" style="color:%s;font-weight:600">Ver o log completo →</a></p>`, esc(u), b.Primary))
	}
	return shell(b, e.Subject(), StatusLabel(r.Status), b.statusColor(r.Status), sb.String(), len(b.LogoPNG) > 0)
}

func (e AgentEvent) HTML() string {
	b, loc := e.Brand, e.Loc
	if loc == nil {
		loc = timeUTC
	}
	body := fmt.Sprintf(`<table role="presentation" width="100%%" cellpadding="0" cellspacing="0" style="font-size:14px">%s%s%s</table><p style="color:%s">O servidor não dá sinal há mais de %d minutos. Se ele estiver desligado ou sem rede, os backups agendados não vão rodar.</p>`,
		kv(b, "Servidor", "<b>"+esc(e.Agent.Name)+"</b>"), kv(b, "Visto por último", esc(fmtTime(e.Agent.LastSeen, loc))), kv(b, "Versão do agente", esc(e.Agent.Version)), b.Muted, e.Minutes)
	return shell(b, e.Subject(), "Agente offline", b.Danger, body, len(b.LogoPNG) > 0)
}
