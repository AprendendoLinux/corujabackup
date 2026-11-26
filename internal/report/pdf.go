package report

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"

	"github.com/go-pdf/fpdf"
)

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

type doc struct {
	p     *fpdf.Fpdf
	b     Brand
	tr    func(string) string
	title string
	logo  bool
}

func newDoc(b Brand, title string) *doc {
	p := fpdf.New("P", "mm", "A4", "")
	d := &doc{p: p, b: b, tr: p.UnicodeTranslatorFromDescriptor(""), title: title}
	p.SetTitle(title, true)
	p.SetAuthor(b.Name, true)
	p.SetCreator(b.Name, true)
	p.SetMargins(15, 24, 15)
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

func (d *doc) header() {
	p := d.p
	r, g, bl := rgb(d.b.Primary)
	p.SetFillColor(r, g, bl)
	p.Rect(0, 0, 210, 16, "F")
	ar, ag, ab := rgb(d.b.Accent)
	p.SetFillColor(ar, ag, ab)
	p.Rect(0, 16, 210, 1.2, "F")
	if d.logo {
		p.ImageOptions("logo", 15, 3, 0, 10, false, fpdf.ImageOptions{}, 0, "")
		if p.Error() != nil {
			p.ClearError()
		}
	} else {
		p.SetTextColor(255, 255, 255)
		p.SetFont("Helvetica", "B", 13)
		p.SetXY(15, 4)
		p.CellFormat(100, 8, d.tr(d.b.Name), "", 0, "L", false, 0, "")
	}
	p.SetTextColor(255, 255, 255)
	p.SetFont("Helvetica", "", 9)
	p.SetXY(95, 4.5)
	p.CellFormat(100, 7, d.tr(d.title), "", 0, "R", false, 0, "")
	p.SetY(24)
}

func (d *doc) footer() {
	p := d.p
	p.SetY(-13)
	mr, mg, mb := rgb(d.b.Muted)
	p.SetTextColor(mr, mg, mb)
	p.SetFont("Helvetica", "", 8)
	p.CellFormat(150, 5, d.tr(fmt.Sprintf("%s - Versão %s - Desenvolvido por Henrique Fagundes", d.b.Name, d.b.Version)), "", 0, "L", false, 0, "")
	p.CellFormat(30, 5, d.tr(fmt.Sprintf("Página %d/{nb}", p.PageNo())), "", 0, "R", false, 0, "")
}

func (d *doc) text(color string, style string, size float64) {
	r, g, b := rgb(color)
	d.p.SetTextColor(r, g, b)
	d.p.SetFont("Helvetica", style, size)
}

func (d *doc) h2(s string) {
	if d.p.GetY() > 255 {
		d.p.AddPage()
	}
	d.p.Ln(4)
	d.text(d.b.Primary, "B", 12)
	d.p.CellFormat(0, 7, d.tr(s), "", 1, "L", false, 0, "")
	r, g, b := rgb(d.b.Border)
	d.p.SetDrawColor(r, g, b)
	d.p.Line(15, d.p.GetY(), 195, d.p.GetY())
	d.p.Ln(2)
}

func (d *doc) pill(x, y float64, label, color string, w float64) {
	r, g, b := rgb(color)
	d.p.SetFillColor(r, g, b)
	d.p.RoundedRect(x, y, w, 4.6, 2.2, "1234", "F")
	d.p.SetTextColor(255, 255, 255)
	d.p.SetFont("Helvetica", "B", 7)
	d.p.SetXY(x, y)
	d.p.CellFormat(w, 4.6, d.tr(label), "", 0, "C", false, 0, "")
}

type col struct {
	head  string
	w     float64
	align string
}

// table desenha uma tabela com quebra de linha por célula e repete o cabeçalho ao mudar de página. cell devolve o texto de
// cada coluna (ou "@status:<estado>" para desenhar o selo de estado).
func (d *doc) table(cols []col, rows [][]string) {
	p := d.p
	head := func() {
		r, g, b := rgb(d.b.Bg)
		p.SetFillColor(r, g, b)
		d.text(d.b.Muted, "B", 8)
		x := p.GetX()
		for _, c := range cols {
			p.CellFormat(c.w, 6.5, d.tr(c.head), "B", 0, c.align, true, 0, "")
		}
		p.Ln(6.5)
		p.SetX(x)
	}
	head()
	for _, row := range rows {
		// altura = maior número de linhas entre as células (máx. 3)
		lines := 1
		wrapped := make([][]string, len(row))
		for i, c := range cols {
			txt := row[i]
			if strings.HasPrefix(txt, "@status:") {
				wrapped[i] = []string{txt}
				continue
			}
			d.text(d.b.Text, "", 8)
			l := d.wrap(d.tr(txt), c.w-2)
			if len(l) == 0 {
				l = []string{""}
			}
			if len(l) > 3 {
				l = append(l[:2], l[2]+"...")
				l = l[:3]
			}
			wrapped[i] = l
			if len(l) > lines {
				lines = len(l)
			}
		}
		h := float64(lines)*4.2 + 2.4
		if p.GetY()+h > 277 {
			p.AddPage()
			head()
		}
		y0, x0 := p.GetY(), 15.0
		x := x0
		for i, c := range cols {
			if strings.HasPrefix(row[i], "@status:") {
				st := strings.TrimPrefix(row[i], "@status:")
				d.pill(x+1, y0+1.4, StatusLabel(st), d.b.statusColor(st), c.w-2)
			} else {
				d.text(d.b.Text, "", 8)
				for k, ln := range wrapped[i] {
					p.SetXY(x+1, y0+1.2+float64(k)*4.2)
					p.CellFormat(c.w-2, 4.2, ln, "", 0, c.align, false, 0, "")
				}
			}
			x += c.w
		}
		br, bg, bb := rgb(d.b.Border)
		p.SetDrawColor(br, bg, bb)
		p.Line(x0, y0+h, x0+sumW(cols), y0+h)
		p.SetXY(x0, y0+h)
	}
}

func sumW(c []col) float64 {
	t := 0.0
	for _, x := range c {
		t += x.w
	}
	return t
}

func (d *doc) banner(label, color string) {
	r, g, b := rgb(color)
	d.p.SetFillColor(r, g, b)
	d.p.RoundedRect(15, d.p.GetY(), 180, 10, 3, "1234", "F")
	d.p.SetTextColor(255, 255, 255)
	d.p.SetFont("Helvetica", "B", 12)
	d.p.SetXY(15, d.p.GetY())
	d.p.CellFormat(180, 10, d.tr(label), "", 1, "C", false, 0, "")
	d.p.Ln(3)
}

func (d *doc) counters(c Counts) {
	items := []struct {
		n     int
		label string
		color string
	}{{c.OK, "Sucesso", d.b.Ok}, {c.Warning, "Com avisos", d.b.Accent}, {c.Failed, "Falhas", d.b.Danger}, {c.Missed, "Perdidos", d.b.Danger}, {c.Running, "Em execução", d.b.Muted}}
	w, y := 34.0, d.p.GetY()
	for i, it := range items {
		x := 15 + float64(i)*(w+2.5)
		br, bg, bb := rgb(d.b.Border)
		d.p.SetDrawColor(br, bg, bb)
		d.p.RoundedRect(x, y, w, 17, 2.5, "1234", "D")
		d.text(it.color, "B", 17)
		d.p.SetXY(x, y+2)
		d.p.CellFormat(w, 8, strconv.Itoa(it.n), "", 0, "C", false, 0, "")
		d.text(d.b.Muted, "", 7.5)
		d.p.SetXY(x, y+10.5)
		d.p.CellFormat(w, 4.5, d.tr(it.label), "", 0, "C", false, 0, "")
	}
	d.p.SetY(y + 21)
}

func (d *doc) bytes() ([]byte, error) {
	var buf bytes.Buffer
	if err := d.p.Output(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// PDF gera o relatório diário em A4 (a primeira página é o resumo executivo; as seguintes detalham as execuções).
func (dl Daily) PDF() ([]byte, error) {
	b, loc := dl.Brand, dl.loc()
	d := newDoc(b, "Relatório de backup")
	p := d.p
	p.AddPage()
	d.text(b.Text, "B", 17)
	p.CellFormat(0, 9, d.tr("Relatório de backup"), "", 1, "L", false, 0, "")
	d.text(b.Muted, "", 9)
	p.CellFormat(0, 5, d.tr(fmt.Sprintf("Período: %s a %s  ·  gerado em %s", dl.From.In(loc).Format("02/01/2006 15:04"), dl.To.In(loc).Format("02/01/2006 15:04"), dl.Generated.In(loc).Format("02/01/2006 15:04"))), "", 1, "L", false, 0, "")
	p.Ln(3)
	d.banner(dl.OverallLabel(), dl.overallColor())
	d.counters(dl.Counts)

	if len(dl.Attention) > 0 {
		d.h2("Precisa de atenção")
		d.text(b.Danger, "", 9)
		for _, a := range dl.Attention {
			p.SetX(17)
			p.MultiCell(176, 4.6, d.tr("- "+a), "", "L", false)
		}
	}

	d.h2("Jobs")
	var rows [][]string
	for _, j := range dl.Jobs {
		name := j.Name + "  (" + j.Agent + ")"
		if !j.Enabled {
			name += "  [pausado]"
		}
		if j.Last == nil {
			rows = append(rows, []string{name, "nunca executou", "-", "-", "-"})
			continue
		}
		cl := "-"
		switch j.Last.CloudStatus {
		case "ok":
			cl = "conferida (" + j.Last.VerifiedBy + ")"
		case "failed":
			cl = "FALHOU"
		}
		rows = append(rows, []string{name, fmtTime(j.Last.StartedAt, loc), "@status:" + j.Last.Status, FmtBytes(j.Last.BytesOut), cl})
	}
	if len(rows) == 0 {
		rows = [][]string{{"Nenhum job cadastrado.", "", "", "", ""}}
	}
	d.table([]col{{"Job", 66, "L"}, {"Última execução", 30, "L"}, {"Estado", 26, "C"}, {"Tamanho", 22, "R"}, {"Nuvem", 36, "L"}}, rows)

	if len(dl.Clouds) > 0 {
		d.h2("Nuvem")
		for _, c := range dl.Clouds {
			d.text(b.Text, "B", 9)
			p.CellFormat(70, 5, d.tr(c.Name), "", 0, "L", false, 0, "")
			d.text(b.Muted, "", 8.5)
			state := strings.TrimSpace(c.Type + " " + c.Account)
			if c.Status != "connected" {
				state += "  ·  " + firstNonEmpty(c.Err, "com erro")
			}
			p.CellFormat(110, 5, d.tr(state), "", 1, "L", false, 0, "")
			if c.Total > 0 {
				pct := float64(c.Used) / float64(c.Total)
				br, bg, bb := rgb(b.Border)
				p.SetFillColor(br, bg, bb)
				y := p.GetY()
				p.RoundedRect(15, y, 180, 2.6, 1.3, "1234", "F")
				col := b.Primary
				if pct > 0.9 {
					col = b.Danger
				}
				cr, cg, cb := rgb(col)
				p.SetFillColor(cr, cg, cb)
				w := 180 * pct
				if w < 2.6 {
					w = 2.6
				}
				p.RoundedRect(15, y, w, 2.6, 1.3, "1234", "F")
				p.SetY(y + 3.5)
				d.text(b.Muted, "", 7.5)
				p.CellFormat(0, 4, d.tr(fmt.Sprintf("%s de %s (%d%%)", FmtBytes(c.Used), FmtBytes(c.Total), int(pct*100))), "", 1, "L", false, 0, "")
			}
			p.Ln(1.5)
		}
	}

	d.h2("Agentes")
	var ar [][]string
	for _, a := range dl.Agents {
		st := "online"
		if a.Revoked {
			st = "revogado"
		} else if !a.Online {
			st = "OFFLINE (visto " + fmtTime(a.LastSeen, loc) + ")"
		}
		ar = append(ar, []string{a.Name, st, a.Version})
	}
	d.table([]col{{"Servidor", 70, "L"}, {"Estado", 80, "L"}, {"Versão", 30, "L"}}, ar)

	if len(dl.Runs) > 0 {
		p.AddPage()
		d.h2(fmt.Sprintf("Execuções do período (%d)", len(dl.Runs)))
		var rr [][]string
		for _, r := range dl.Runs {
			info := r.Error
			if info == "" && r.OutputName != "" {
				info = r.OutputName
			}
			rr = append(rr, []string{fmtTime(r.StartedAt, loc), r.Job, "@status:" + r.Status, fmtDur(r.StartedAt, r.FinishedAt), FmtBytes(r.BytesOut), info})
		}
		d.table([]col{{"Quando", 28, "L"}, {"Job", 38, "L"}, {"Estado", 24, "C"}, {"Duração", 20, "R"}, {"Tamanho", 20, "R"}, {"Detalhe", 50, "L"}}, rr)
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
