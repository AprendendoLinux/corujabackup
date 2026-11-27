package report

import (
	"bytes"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/go-pdf/fpdf"
)

// Cores fixas dos estados nos gráficos e selos do PDF (paleta de estados validada para daltonismo; não muda com o tema,
// e sempre aparece junto de um rótulo). A ordem de empilhamento sucesso › aviso › falha › perdido mantém vizinhas que
// se distinguem.
var (
	stGood     = "#0aac23"
	stWarn     = "#fbb315"
	stCritical = "#dc3c3d"
	stSerious  = "#f18a59"
	seriesBlue = "#257de0" // série única (volume)
	inkDark    = "#0b0b0b" // texto sobre o amarelo
)

func chartStatusColor(s string) string {
	switch s {
	case "ok":
		return stGood
	case "warning":
		return stWarn
	case "failed":
		return stCritical
	case "missed":
		return stSerious
	}
	return "#9c9c9c"
}

// inkOn devolve a cor do texto escrito dentro de um preenchimento de estado.
func inkOn(fill string) string {
	if fill == stWarn {
		return inkDark
	}
	return "#ffffff"
}

func rgb(hex string) (int, int, int) {
	hex = strings.TrimPrefix(hex, "#")
	if len(hex) != 6 {
		return 0, 0, 0
	}
	v, err := strconv.ParseUint(hex, 16, 32)
	if err != nil {
		return 0, 0, 0
	}
	return int(v >> 16 & 255), int(v >> 8 & 255), int(v & 255)
}

// mix mistura a com b (t=0 → a; t=1 → b).
func mix(a, b string, t float64) string {
	ar, ag, ab := rgb(a)
	br, bg, bb := rgb(b)
	f := func(x, y int) int { return int(math.Round(float64(x) + (float64(y)-float64(x))*t)) }
	return fmt.Sprintf("#%02x%02x%02x", f(ar, br), f(ag, bg), f(ab, bb))
}

const (
	left   = 15.0
	width  = 180.0
	bottom = 277.0
)

type doc struct {
	p     *fpdf.Fpdf
	b     Brand
	tr    func(string) string
	title string
	sub   string
	logo  bool
}

func newDoc(b Brand, title, sub string) *doc {
	p := fpdf.New("P", "mm", "A4", "")
	d := &doc{p: p, b: b, tr: p.UnicodeTranslatorFromDescriptor(""), title: title, sub: sub}
	p.SetTitle(title, true)
	p.SetAuthor(b.Name, true)
	p.SetCreator(b.Name, true)
	p.SetMargins(left, 30, left)
	p.SetAutoPageBreak(true, 20)
	p.AliasNbPages("{nb}")
	if len(b.LogoPNG) > 0 {
		t := "PNG"
		switch b.LogoMIME {
		case "image/jpeg":
			t = "JPG"
		case "image/gif":
			t = "GIF"
		}
		p.RegisterImageOptionsReader("logo", fpdf.ImageOptions{ImageType: t, ReadDpi: false}, bytes.NewReader(b.LogoPNG))
		d.logo = p.Ok()
		if !d.logo {
			p.ClearError()
		}
	}
	p.SetHeaderFunc(d.header)
	p.SetFooterFunc(d.footer)
	return d
}

func (d *doc) fill(c string) { r, g, b := rgb(c); d.p.SetFillColor(r, g, b) }
func (d *doc) draw(c string) { r, g, b := rgb(c); d.p.SetDrawColor(r, g, b) }

func (d *doc) text(color string, style string, size float64) {
	r, g, b := rgb(color)
	d.p.SetTextColor(r, g, b)
	d.p.SetFont("Helvetica", style, size)
}

// at escreve s em (x, y) numa caixa de largura w.
func (d *doc) at(x, y, w, h float64, s, align string) {
	d.p.SetXY(x, y)
	d.p.CellFormat(w, h, d.tr(s), "", 0, align, false, 0, "")
}

// fit corta o texto com "..." até caber em w mm (na fonte atual).
func (d *doc) fit(s string, w float64) string {
	t := d.tr(s)
	if d.p.GetStringWidth(t) <= w {
		return s
	}
	r := []rune(s)
	for len(r) > 1 && d.p.GetStringWidth(d.tr(string(r)+"...")) > w {
		r = r[:len(r)-1]
	}
	return string(r) + "..."
}

func (d *doc) header() {
	p := d.p
	d.fill(d.b.Primary)
	p.Rect(0, 0, 210, 20, "F")
	d.fill(d.b.Accent)
	p.Rect(0, 20, 210, 1, "F")
	if d.logo {
		p.ImageOptions("logo", left, 4, 0, 12, false, fpdf.ImageOptions{}, 0, "")
		if p.Error() != nil {
			p.ClearError()
		}
	} else {
		d.text("#ffffff", "B", 14)
		d.at(left, 6, 100, 8, d.b.Name, "L")
	}
	d.text("#ffffff", "B", 10.5)
	d.at(95, 5, 100, 5, d.title, "R")
	d.text(mix(d.b.Primary, "#ffffff", 0.75), "", 8)
	d.at(95, 10.5, 100, 4, d.sub, "R")
	p.SetY(30)
}

func (d *doc) footer() {
	p := d.p
	d.draw(d.b.Border)
	p.SetLineWidth(0.2)
	p.Line(left, 283, left+width, 283)
	d.text(d.b.Muted, "", 7.5)
	d.at(left, 284.5, 140, 4, fmt.Sprintf("%s - Versão %s%s", d.b.Name, d.b.Version, d.b.Credit()), "L")
	d.at(left+140, 284.5, 40, 4, fmt.Sprintf("Página %d de {nb}", p.PageNo()), "R")
}

// room garante h mm livres na página (senão começa outra).
func (d *doc) room(h float64) {
	if d.p.GetY()+h > bottom {
		d.p.AddPage()
	}
}

// section abre uma seção: título com um traço da cor primária à esquerda e um subtítulo opcional.
func (d *doc) section(title, sub string, need float64) {
	d.room(need + 12)
	p := d.p
	p.Ln(4)
	y := p.GetY()
	d.fill(d.b.Primary)
	p.RoundedRect(left, y+0.6, 1.2, 5, 0.6, "1234", "F")
	d.text(d.b.Text, "B", 11.5)
	d.at(left+3.5, y, 120, 6.2, title, "L")
	if sub != "" {
		d.text(d.b.Muted, "", 7.5)
		d.at(left+60, y+0.6, width-60, 5.4, sub, "R")
	}
	p.SetY(y + 9)
}

func (d *doc) card(x, y, w, h float64) {
	d.draw(d.b.Border)
	d.p.SetLineWidth(0.25)
	d.fill("#ffffff")
	d.p.RoundedRect(x, y, w, h, 2.5, "1234", "FD")
}

// pill é o selo de estado (texto dentro do preenchimento, no branco ou quase preto conforme o fundo).
func (d *doc) pill(x, y, w float64, status string) {
	c := chartStatusColor(status)
	d.fill(c)
	d.p.RoundedRect(x, y, w, 4.6, 2.3, "1234", "F")
	d.text(inkOn(c), "B", 6.8)
	d.at(x, y, w, 4.6, StatusLabel(status), "C")
}

// icon desenha o símbolo do estado geral num círculo: ✓, ! ou ✕.
func (d *doc) icon(cx, cy, r float64, kind, color string) {
	p := d.p
	d.fill(color)
	p.Circle(cx, cy, r, "F")
	d.draw("#ffffff")
	p.SetLineWidth(0.9)
	p.SetLineCapStyle("round")
	p.SetLineJoinStyle("round")
	switch kind {
	case "ok":
		p.Line(cx-r*0.42, cy+r*0.02, cx-r*0.1, cy+r*0.34)
		p.Line(cx-r*0.1, cy+r*0.34, cx+r*0.45, cy-r*0.32)
	case "warn":
		p.Line(cx, cy-r*0.45, cx, cy+r*0.1)
		d.fill("#ffffff")
		p.Circle(cx, cy+r*0.42, 0.45, "F")
	default:
		p.Line(cx-r*0.35, cy-r*0.35, cx+r*0.35, cy+r*0.35)
		p.Line(cx+r*0.35, cy-r*0.35, cx-r*0.35, cy+r*0.35)
	}
	p.SetLineWidth(0.2)
}

type col struct {
	head  string
	w     float64
	align string
}

// table desenha uma tabela listrada, com quebra de linha por célula, e repete o cabeçalho ao mudar de página. Uma
// célula "@status:<estado>" vira o selo do estado; "@dot:<cor>|<texto>" vira um ponto colorido com o texto.
func (d *doc) table(cols []col, rows [][]string) {
	p := d.p
	total := 0.0
	for _, c := range cols {
		total += c.w
	}
	head := func() {
		y := p.GetY()
		d.fill(mix(d.b.Primary, "#ffffff", 0.92))
		p.RoundedRect(left, y, total, 6.5, 1.5, "1234", "F")
		d.text(mix(d.b.Primary, d.b.Text, 0.35), "B", 7.5)
		x := left
		for _, c := range cols {
			d.at(x+1.5, y, c.w-3, 6.5, c.head, c.align)
			x += c.w
		}
		p.SetY(y + 6.5)
	}
	d.room(14)
	head()
	for n, row := range rows {
		lines := 1
		wrapped := make([][]string, len(row))
		for i, c := range cols {
			txt := row[i]
			if strings.HasPrefix(txt, "@status:") {
				continue
			}
			w := c.w - 3
			if strings.HasPrefix(txt, "@dot:") {
				_, txt, _ = strings.Cut(strings.TrimPrefix(txt, "@dot:"), "|")
				w -= 3.5
			}
			d.text(d.b.Text, "", 8)
			l := d.wrap(d.tr(txt), w)
			if len(l) == 0 {
				l = []string{""}
			}
			if len(l) > 3 {
				l = append(l[:2], l[2]+"...")
			}
			wrapped[i] = l
			if len(l) > lines {
				lines = len(l)
			}
		}
		h := float64(lines)*4 + 3
		if p.GetY()+h > bottom {
			p.AddPage()
			head()
		}
		y0 := p.GetY()
		if n%2 == 1 {
			d.fill(mix(d.b.Bg, "#ffffff", 0.45))
			p.Rect(left, y0, total, h, "F")
		}
		x := left
		for i, c := range cols {
			cell := row[i]
			switch {
			case strings.HasPrefix(cell, "@status:"):
				pw := math.Min(c.w-3, 22)
				d.pill(x+(c.w-pw)/2, y0+1.2, pw, strings.TrimPrefix(cell, "@status:"))
			default:
				tx := x + 1.5
				if strings.HasPrefix(cell, "@dot:") {
					color, _, _ := strings.Cut(strings.TrimPrefix(cell, "@dot:"), "|")
					d.fill(color)
					p.Circle(tx+1, y0+1.5+2, 1, "F")
					tx += 3.5
				}
				d.text(d.b.Text, "", 8)
				for k, ln := range wrapped[i] {
					p.SetXY(tx, y0+1.5+float64(k)*4)
					p.CellFormat(x+c.w-1.5-tx, 4, ln, "", 0, c.align, false, 0, "")
				}
			}
			x += c.w
		}
		d.draw(mix(d.b.Border, "#ffffff", 0.3))
		p.SetLineWidth(0.15)
		p.Line(left, y0+h, left+total, y0+h)
		p.SetY(y0 + h)
	}
}

func (d *doc) bytes() ([]byte, error) {
	var buf bytes.Buffer
	if err := d.p.Output(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// ---------------------------------------------------------------- blocos do resumo

func (dl Daily) overallKind() string {
	switch dl.Overall {
	case "fail":
		return "fail"
	case "warn":
		return "warn"
	}
	return "ok"
}

func plural(n int, um, varios string) string {
	if n == 1 {
		return "1 " + um
	}
	return strconv.Itoa(n) + " " + varios
}

func (d *doc) statusCard(dl Daily) {
	p := d.p
	kind := dl.overallKind()
	color := map[string]string{"ok": stGood, "warn": stWarn, "fail": stCritical}[kind]
	y := p.GetY()
	d.fill(mix(color, "#ffffff", 0.88))
	p.RoundedRect(left, y, width, 20, 3, "1234", "F")
	d.fill(color)
	p.RoundedRect(left, y, 2.2, 20, 1.1, "14", "F")
	d.icon(left+13, y+10, 5.2, kind, color)
	d.text(d.b.Text, "B", 14)
	d.at(left+22, y+3.5, 150, 7, dl.OverallLabel(), "L")
	c := dl.Counts
	fin := c.OK + c.Warning + c.Failed + c.Missed
	var parts []string
	if c.OK > 0 {
		parts = append(parts, plural(c.OK, "com sucesso", "com sucesso"))
	}
	if c.Warning > 0 {
		parts = append(parts, plural(c.Warning, "com avisos", "com avisos"))
	}
	if c.Failed > 0 {
		parts = append(parts, plural(c.Failed, "falha", "falhas"))
	}
	if c.Missed > 0 {
		parts = append(parts, plural(c.Missed, "perdido", "perdidos"))
	}
	sentence := "Nenhum backup terminou nas últimas 24 horas."
	if fin > 0 {
		sentence = plural(fin, "backup terminou", "backups terminaram") + " nas últimas 24 horas: " + strings.Join(parts, ", ") + "."
	}
	if c.Running > 0 {
		sentence += " " + plural(c.Running, "em execução agora", "em execução agora") + "."
	}
	d.text(mix(d.b.Text, "#ffffff", 0.2), "", 8.5)
	d.at(left+22, y+10.5, 155, 5, sentence, "L")
	p.SetY(y + 24)
}

type kpi struct{ label, value, sub, dot string }

func (d *doc) kpis(items []kpi) {
	p := d.p
	gap := 3.5
	w := (width - gap*float64(len(items)-1)) / float64(len(items))
	y := p.GetY()
	for i, k := range items {
		x := left + float64(i)*(w+gap)
		d.card(x, y, w, 21)
		d.text(d.b.Muted, "", 7.5)
		d.at(x+4, y+3, w-8, 4, k.label, "L")
		vx := x + 4
		if k.dot != "" {
			d.fill(k.dot)
			p.Circle(vx+1.3, y+11.6, 1.3, "F")
			vx += 4
		}
		d.text(d.b.Text, "B", 16)
		d.at(vx, y+8, w-8, 7.5, k.value, "L")
		d.text(d.b.Muted, "", 6.8)
		d.at(x+4, y+15.5, w-8, 3.5, d.fit(k.sub, w-8), "L")
	}
	p.SetY(y + 25)
}

func (d *doc) attention(items []string, kind string) {
	if len(items) == 0 {
		return
	}
	p := d.p
	color := stCritical
	if kind == "warn" {
		color = stWarn
	}
	d.text(d.b.Text, "", 8.3)
	var lines [][]string
	h := 10.0
	for _, a := range items {
		l := d.wrap(d.tr(a), width-16)
		lines = append(lines, l)
		h += float64(len(l))*4.2 + 1
	}
	d.room(h + 2)
	y := p.GetY()
	d.fill(mix(color, "#ffffff", 0.9))
	p.RoundedRect(left, y, width, h, 2.5, "1234", "F")
	d.fill(color)
	p.RoundedRect(left, y, 1.6, h, 0.8, "14", "F")
	d.text(d.b.Text, "B", 9.5)
	d.at(left+6, y+2.5, 100, 5, "Precisa de atenção", "L")
	cy := y + 9
	for _, l := range lines {
		d.fill(color)
		p.Circle(left+7.5, cy+2, 0.9, "F")
		d.text(d.b.Text, "", 8.3)
		for _, s := range l {
			p.SetXY(left+10, cy)
			p.CellFormat(width-16, 4.2, s, "", 0, "L", false, 0, "")
			cy += 4.2
		}
		cy++
	}
	p.SetY(y + h + 2)
}

func (d *doc) legend(x, y float64, items [][2]string) float64 {
	for _, it := range items {
		d.fill(it[1])
		d.p.RoundedRect(x, y+0.9, 2.8, 2.8, 0.7, "1234", "F")
		d.text(d.b.Muted, "", 7.3)
		w := d.p.GetStringWidth(d.tr(it[0]))
		d.at(x+4, y, w+1, 4.6, it[0], "L")
		x += 4 + w + 5
	}
	return x
}

// niceMax arredonda o topo do eixo para 1, 2 ou 5 × 10^n e devolve o passo das marcas.
func niceMax(v float64, ticks int) (float64, float64) {
	if v <= 0 {
		return float64(ticks), 1
	}
	raw := v / float64(ticks)
	mag := math.Pow(10, math.Floor(math.Log10(raw)))
	step := mag
	for _, m := range []float64{1, 2, 5, 10} {
		if m*mag >= raw {
			step = m * mag
			break
		}
	}
	if step < 1 {
		step = 1
	}
	return step * math.Ceil(v/step), step
}

// historyChart: colunas empilhadas por estado, uma por dia.
func (d *doc) historyChart(h []DayStat) {
	p := d.p
	const plotH = 42.0
	d.room(plotH + 22)
	y := p.GetY()
	d.legend(left, y, [][2]string{{"Sucesso", stGood}, {"Com avisos", stWarn}, {"Falhas", stCritical}, {"Perdidos", stSerious}})
	top := y + 8
	max := 0
	for _, x := range h {
		if n := x.Finished(); n > max {
			max = n
		}
	}
	if max == 0 {
		d.text(d.b.Muted, "", 8.5)
		d.at(left, top+plotH/2-3, width, 6, "Nenhuma execução no período.", "C")
		p.SetY(top + plotH + 8)
		return
	}
	ymax, step := niceMax(float64(max), 4)
	x0 := left + 9
	pw := width - 9
	base := top + plotH
	scale := plotH / ymax
	d.text(d.b.Muted, "", 6.8)
	p.SetLineWidth(0.15)
	for v := 0.0; v <= ymax+0.001; v += step {
		yy := base - v*scale
		d.draw(mix(d.b.Border, "#ffffff", 0.2))
		if v == 0 {
			d.draw(mix(d.b.Muted, "#ffffff", 0.45))
		}
		p.Line(x0, yy, x0+pw, yy)
		d.at(left, yy-2, 7.5, 4, strconv.Itoa(int(v)), "R")
	}
	band := pw / float64(len(h))
	bw := math.Min(6.3, band*0.62)
	maxIdx, lastIdx := 0, len(h)-1
	for i, x := range h {
		if x.Finished() > h[maxIdx].Finished() {
			maxIdx = i
		}
	}
	for i, x := range h {
		cx := x0 + band*float64(i) + band/2
		segs := []struct {
			n int
			c string
		}{{x.OK, stGood}, {x.Warning, stWarn}, {x.Failed, stCritical}, {x.Missed, stSerious}}
		yy := base
		topSeg := -1
		for k, s := range segs {
			if s.n > 0 {
				topSeg = k
			}
		}
		for k, s := range segs {
			if s.n == 0 {
				continue
			}
			sh := float64(s.n) * scale
			d.fill(s.c)
			if k == topSeg {
				r := math.Min(1, sh/2)
				p.RoundedRect(cx-bw/2, yy-sh, bw, sh, r, "12", "F")
			} else {
				p.Rect(cx-bw/2, yy-sh+0.5, bw, sh-0.5, "F") // 0,5 mm do fundo separa os segmentos
			}
			yy -= sh
		}
		// rótulo só no pico e no último dia (o eixo carrega o resto)
		if (i == maxIdx || i == lastIdx) && x.Finished() > 0 {
			d.text(d.b.Text, "B", 7)
			d.at(cx-6, yy-4.6, 12, 4, strconv.Itoa(x.Finished()), "C")
		}
		d.text(d.b.Muted, "", 6.5)
		lbl := x.Day.Format("02/01")
		if len(h) > 16 && i%2 == 1 && i != lastIdx {
			lbl = ""
		}
		d.at(cx-band/2, base+1.2, band, 4, lbl, "C")
	}
	p.SetY(base + 8)
}

// splitBar: barra segmentada alta (uma única distribuição), com a parcela escrita dentro quando cabe.
func (d *doc) splitBar(x, y, w float64, c Counts) float64 {
	p := d.p
	parts := []struct {
		n     int
		label string
		color string
	}{{c.OK, "Sucesso", stGood}, {c.Warning, "Com avisos", stWarn}, {c.Failed, "Falhas", stCritical}, {c.Missed, "Perdidos", stSerious}}
	total := 0
	for _, s := range parts {
		total += s.n
	}
	if total == 0 {
		d.fill(mix(d.b.Border, "#ffffff", 0.3))
		p.RoundedRect(x, y, w, 7, 1, "1234", "F")
		d.text(d.b.Muted, "", 7.5)
		d.at(x, y, w, 7, "Nenhuma execução terminada", "C")
		return y + 10
	}
	cx := x
	n := 0
	for _, s := range parts {
		if s.n > 0 {
			n++
		}
	}
	avail := w - 0.6*float64(n-1)
	for _, s := range parts {
		if s.n == 0 {
			continue
		}
		sw := avail * float64(s.n) / float64(total)
		d.fill(s.color)
		p.RoundedRect(cx, y, sw, 7, 1, "1234", "F")
		pct := fmt.Sprintf("%d%%", int(math.Round(float64(s.n)*100/float64(total))))
		d.text(inkOn(s.color), "B", 7)
		if p.GetStringWidth(pct)+3 < sw {
			d.at(cx+1.5, y, sw-2, 7, pct, "L")
		}
		cx += sw + 0.6
	}
	ly := y + 10
	for _, s := range parts {
		if s.n == 0 {
			continue
		}
		d.fill(s.color)
		p.RoundedRect(x, ly+0.9, 2.8, 2.8, 0.7, "1234", "F")
		d.text(d.b.Text, "", 7.8)
		d.at(x+4.5, ly, w-20, 4.6, s.label, "L")
		d.text(d.b.Text, "B", 7.8)
		d.at(x+w-16, ly, 16, 4.6, strconv.Itoa(s.n), "R")
		ly += 5.2
	}
	return ly
}

// volumeBars: barras horizontais ordenadas, uma só cor.
func (d *doc) volumeBars(x, y, w float64, vols []JobVolume) float64 {
	p := d.p
	if len(vols) == 0 {
		d.text(d.b.Muted, "", 7.5)
		d.at(x, y, w, 7, "Nenhum backup concluído no período", "L")
		return y + 10
	}
	if len(vols) > 5 {
		var rest int64
		for _, v := range vols[4:] {
			rest += v.Bytes
		}
		vols = append(vols[:4:4], JobVolume{fmt.Sprintf("Outros (%d)", len(vols)-4), rest})
	}
	max := vols[0].Bytes
	for _, v := range vols {
		if v.Bytes > max {
			max = v.Bytes
		}
	}
	nameW, valW := 30.0, 17.0
	barW := w - nameW - valW - 2
	for _, v := range vols {
		d.text(d.b.Text, "", 7.6)
		d.at(x, y, nameW, 5, d.fit(v.Name, nameW-1), "L")
		bw := 0.0
		if max > 0 {
			bw = barW * float64(v.Bytes) / float64(max)
		}
		if bw < 1 {
			bw = 1
		}
		d.fill(seriesBlue)
		p.RoundedRect(x+nameW, y+1, bw, 3, 0.8, "23", "F")
		d.text(d.b.Text, "", 7.3)
		d.at(x+nameW+bw+1.5, y, valW, 5, FmtBytes(v.Bytes), "L")
		y += 6
	}
	return y + 2
}

// meter: barra de uso (cheia na cor da severidade, trilho num tom mais claro da mesma cor).
func (d *doc) meter(x, y, w float64, frac float64) {
	color := seriesBlue
	switch {
	case frac >= 0.9:
		color = stCritical
	case frac >= 0.75:
		color = stWarn
	}
	d.fill(mix(color, "#ffffff", 0.82))
	d.p.RoundedRect(x, y, w, 2.6, 1.3, "1234", "F")
	fw := math.Max(2.6, w*math.Min(frac, 1))
	d.fill(color)
	d.p.RoundedRect(x, y, fw, 2.6, 1.3, "1234", "F")
}

// ---------------------------------------------------------------- relatório diário

// PDF gera o relatório diário em A4: resumo executivo com gráficos e, em seguida, os jobs, storages, agentes e as
// execuções do período.
func (dl Daily) PDF() ([]byte, error) {
	b, loc := dl.Brand, dl.loc()
	d := newDoc(b, "Relatório de backup", "gerado em "+dl.Generated.In(loc).Format("02/01/2006 às 15:04"))
	p := d.p
	p.AddPage()

	d.text(b.Text, "B", 19)
	d.at(left, p.GetY(), width, 9, "Relatório diário de backup", "L")
	p.Ln(9)
	d.text(b.Muted, "", 9)
	d.at(left, p.GetY(), width, 5, fmt.Sprintf("Últimas 24 horas: de %s a %s", dl.From.In(loc).Format("02/01/2006 15:04"), dl.To.In(loc).Format("02/01/2006 15:04")), "L")
	p.Ln(9)
	d.statusCard(dl)

	c := dl.Counts
	fin := c.OK + c.Warning + c.Failed + c.Missed
	rate, rateSub, rateDot := "-", "sem execuções terminadas", ""
	if fin > 0 {
		r := float64(c.OK+c.Warning) / float64(fin)
		rate = fmt.Sprintf("%d%%", int(math.Floor(r*100)))
		rateSub = fmt.Sprintf("%d de %d terminaram bem", c.OK+c.Warning, fin)
		rateDot = stGood
		if r < 1 {
			rateDot = stCritical
		}
	}
	sum := Sum(dl.Runs)
	online, active := 0, 0
	for _, a := range dl.Agents {
		if a.Revoked {
			continue
		}
		active++
		if a.Online {
			online++
		}
	}
	agDot := stGood
	if online < active {
		agDot = stCritical
	}
	if active == 0 {
		agDot = ""
	}
	enabled := 0
	for _, j := range dl.Jobs {
		if j.Enabled {
			enabled++
		}
	}
	d.kpis([]kpi{
		{"Taxa de sucesso (24 h)", rate, rateSub, rateDot},
		{"Execuções (24 h)", strconv.Itoa(fin + c.Running), fmt.Sprintf("%d de %d jobs ativos", enabled, len(dl.Jobs)), ""},
		{"Volume gerado (24 h)", FmtBytes(sum.Bytes), "soma dos backups concluídos", ""},
		{"Agentes online", fmt.Sprintf("%d de %d", online, active), "conectados ao Server agora", agDot},
	})
	d.attention(dl.Attention, dl.overallKind())

	if len(dl.History) > 0 {
		first, last := dl.History[0].Day, dl.History[len(dl.History)-1].Day
		d.section(fmt.Sprintf("Execuções nos últimos %d dias", len(dl.History)), first.Format("02/01")+" a "+last.Format("02/01/2006")+", por dia de início", 50)
		d.historyChart(dl.History)
	}

	// estados e volume das últimas 24 h, lado a lado
	d.room(48)
	half := (width - 8) / 2
	y := p.GetY() + 3
	d.text(b.Text, "B", 9.5)
	d.at(left, y, half, 5, "Estados nas últimas 24 h", "L")
	d.at(left+half+8, y, half, 5, "Maior volume por job (24 h)", "L")
	y1 := d.splitBar(left, y+7, half, c)
	y2 := d.volumeBars(left+half+8, y+7, half, VolumeByJob(dl.Runs))
	p.SetY(math.Max(y1, y2) + 2)

	d.section("Jobs", fmt.Sprintf("%d cadastrados", len(dl.Jobs)), 20)
	var rows [][]string
	for _, j := range dl.Jobs {
		name := j.Name + " (" + j.Agent + ")"
		if !j.Enabled {
			name += " - pausado"
		}
		next := "-"
		if j.NextRun != nil {
			next = j.NextRun.In(loc).Format("02/01 15:04")
		}
		if j.Last == nil {
			rows = append(rows, []string{name, "nunca executou", "-", "-", "-", next})
			continue
		}
		cl := "só local"
		switch j.Last.CloudStatus {
		case "ok":
			cl = "conferida (" + j.Last.VerifiedBy + ")"
		case "failed":
			cl = "FALHOU"
		}
		rows = append(rows, []string{name, fmtTime(j.Last.StartedAt, loc), "@status:" + j.Last.Status, FmtBytes(j.Last.BytesOut), cl, next})
	}
	if len(rows) == 0 {
		rows = [][]string{{"Nenhum job cadastrado.", "", "", "", "", ""}}
	}
	d.table([]col{{"Job", 54, "L"}, {"Última execução", 28, "L"}, {"Estado", 24, "C"}, {"Tamanho", 20, "R"}, {"Storage", 31, "L"}, {"Próxima", 23, "L"}}, rows)

	if len(dl.Clouds) > 0 {
		d.section("Storages", "", 16)
		for _, cl := range dl.Clouds {
			d.room(14)
			y := p.GetY()
			d.text(b.Text, "B", 8.8)
			d.at(left, y, 70, 5, d.fit(cl.Name, 68), "L")
			d.text(b.Muted, "", 7.8)
			state := strings.TrimSpace(cl.Type + "  " + cl.Account)
			if cl.Status != "connected" {
				state += "  ·  " + firstNonEmpty(cl.Err, "com erro")
			}
			d.at(left+70, y, 110, 5, d.fit(state, 108), "R")
			if cl.Total > 0 {
				frac := float64(cl.Used) / float64(cl.Total)
				d.meter(left, y+6, width-28, frac)
				d.text(b.Text, "", 7.5)
				d.at(left+width-26, y+4.6, 26, 5, fmt.Sprintf("%d%% em uso", int(frac*100)), "R")
				d.text(b.Muted, "", 7)
				d.at(left, y+9.3, width, 4, fmt.Sprintf("%s de %s", FmtBytes(cl.Used), FmtBytes(cl.Total)), "L")
				p.SetY(y + 15)
			} else {
				p.SetY(y + 7)
			}
		}
	}

	d.section("Agentes", "", 16)
	var ar [][]string
	for _, a := range dl.Agents {
		st := "@dot:" + stGood + "|online"
		if a.Revoked {
			st = "@dot:#9c9c9c|revogado"
		} else if !a.Online {
			st = "@dot:" + stCritical + "|offline (visto " + fmtTime(a.LastSeen, loc) + ")"
		}
		ar = append(ar, []string{a.Name, st, a.Version})
	}
	d.table([]col{{"Agente", 70, "L"}, {"Estado", 80, "L"}, {"Versão", 30, "L"}}, ar)

	if len(dl.Runs) > 0 {
		p.AddPage()
		d.section(fmt.Sprintf("Execuções das últimas 24 horas (%d)", len(dl.Runs)), "mais recentes primeiro", 20)
		var rr [][]string
		for _, r := range dl.Runs {
			info := r.Error
			if info == "" && r.OutputName != "" {
				info = r.OutputName
			}
			rr = append(rr, []string{fmtTime(r.StartedAt, loc), r.Job, "@status:" + r.Status, fmtDur(r.StartedAt, r.FinishedAt), FmtBytes(r.BytesOut), info})
		}
		d.table([]col{{"Quando", 27, "L"}, {"Job", 36, "L"}, {"Estado", 24, "C"}, {"Duração", 19, "R"}, {"Tamanho", 19, "R"}, {"Detalhe", 55, "L"}}, rr)
	}
	return d.bytes()
}

// wrap quebra o texto (já traduzido para a codificação da fonte, 1 byte por caractere) em linhas que cabem em w mm.
// (SplitText da biblioteca não aceita texto traduzido: ele itera por runas.)
func (d *doc) wrap(s string, w float64) []string {
	var lines []string
	for _, para := range strings.Split(s, "\n") {
		cur := ""
		flush := func() {
			if cur != "" {
				lines = append(lines, cur)
				cur = ""
			}
		}
		for _, word := range strings.Fields(para) {
			for d.p.GetStringWidth(word) > w && len(word) > 1 { // palavra maior que a coluna: corta por caracteres
				n := len(word)
				for n > 1 && d.p.GetStringWidth(word[:n]) > w {
					n--
				}
				flush()
				lines = append(lines, word[:n])
				word = word[n:]
			}
			try := word
			if cur != "" {
				try = cur + " " + word
			}
			if d.p.GetStringWidth(try) <= w {
				cur = try
			} else {
				flush()
				cur = word
			}
		}
		flush()
	}
	return lines
}
