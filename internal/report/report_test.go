package report

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func tp(t time.Time) *time.Time { return &t }

func sample() Daily {
	loc, _ := time.LoadLocation("America/Sao_Paulo")
	now := time.Date(2026, 11, 26, 7, 0, 0, 0, loc)
	mk := func(job, agent, status string, hoursAgo int, bytes int64, cloud, errTxt string) RunRow {
		st := now.Add(-time.Duration(hoursAgo) * time.Hour)
		en := st.Add(7*time.Minute + 12*time.Second)
		return RunRow{ID: int64(hoursAgo), Job: job, Agent: agent, Kind: "files", Trigger: "schedule", Status: status, StartedAt: tp(st), FinishedAt: tp(en), BytesOut: bytes, Files: 1200,
			CloudStatus: cloud, VerifiedBy: "quickxor", Error: errTxt, OutputName: job + "-20261126-001000.tar.gz"}
	}
	r1 := mk("Servidor web", "umami-server2", "ok", 7, 48<<30, "ok", "")
	r2 := mk("Bancos de dados", "umami-db", "ok", 7, 207<<20, "ok", "")
	r3 := mk("Arquivos do server3", "umami-server3", "failed", 5, 3<<30, "failed", "o backup local foi concluído e conferido, mas o envio para a nuvem falhou: sessão expirada (ç ã é ü)")
	r4 := mk("Home dos usuários", "umami-server3", "warning", 6, 900<<20, "ok", "")
	d := Daily{Brand: DefaultBrand(), Loc: loc, Generated: now, From: now.Add(-24 * time.Hour), To: now,
		Counts: Counts{OK: 2, Warning: 1, Failed: 1}, Overall: "fail",
		Jobs: []JobRow{{Name: "Servidor web", Agent: "umami-server2", Enabled: true, Last: &r1}, {Name: "Bancos de dados", Agent: "umami-db", Enabled: true, Last: &r2},
			{Name: "Arquivos do server3", Agent: "umami-server3", Enabled: true, Last: &r3}, {Name: "Home dos usuários", Agent: "umami-server3", Enabled: true, Last: &r4}, {Name: "Novo job", Agent: "umami-db", Enabled: false}},
		Runs:      []RunRow{r1, r2, r3, r4},
		Agents:    []AgentRow{{Name: "umami-db", Online: true, Version: "0.0.7"}, {Name: "umami-server2", Online: true, Version: "0.0.7"}, {Name: "umami-server3", Online: false, LastSeen: tp(now.Add(-3 * time.Hour)), Version: "0.0.7"}},
		Clouds:    []CloudRow{{Name: "OneDrive Umami", Type: "OneDrive", Account: "backup@umami.com.br", Status: "connected", Total: 1 << 40, Used: 900 << 30}, {Name: "Drive extra", Type: "Google Drive", Status: "error", Err: "reconecte a conta"}},
		Attention: []string{"Arquivos do server3: o envio para a nuvem falhou", "Agente umami-server3 está offline há 3 h", "OneDrive Umami está com 88% da cota usada"}}
	d.Version = "0.0.7"
	var hist []RunRow // 14 dias variados para o gráfico
	for i := 0; i < 14; i++ {
		for k := 0; k < 3+i%3; k++ {
			st := now.AddDate(0, 0, -i).Add(-time.Duration(k) * time.Hour)
			status := "ok"
			switch {
			case i == 3 && k == 0, i == 9 && k == 1:
				status = "failed"
			case i == 5 && k == 2:
				status = "missed"
			case k == 3:
				status = "warning"
			}
			hist = append(hist, RunRow{Job: "j", Status: status, StartedAt: tp(st), BytesOut: 2 << 30})
		}
	}
	d.History = History(hist, now, 14, loc)
	return d
}

func TestRelatorioDiarioHTMLEscapaEContem(t *testing.T) {
	d := sample()
	d.Jobs[0].Name = `<script>alert("x")</script> & cia`
	h := d.HTML()
	for _, want := range []string{"Relatório de backup", "Tudo certo", "&lt;script&gt;", "umami-server3", "88%", "Desenvolvido por", "Henrique Fagundes", "Precisa de atenção", "cid:" + "logo"} {
		if want == "Tudo certo" || want == "cid:logo" {
			continue
		}
		if !strings.Contains(h, want) {
			t.Errorf("HTML sem %q", want)
		}
	}
	if strings.Contains(h, "<script>") {
		t.Fatal("nome de job não foi escapado")
	}
	d.LogoPNG = []byte("png")
	if !strings.Contains(d.HTML(), `src="cid:logo"`) {
		t.Fatal("com logo o HTML deve referenciar o CID")
	}
	if !strings.Contains(sample().Subject(), "🔴") || !strings.Contains(sample().Subject(), "26/11/2026") {
		t.Fatal(sample().Subject())
	}
}

func TestTelegramRespeitaOLimiteEEscapa(t *testing.T) {
	d := sample()
	for i := 0; i < 300; i++ {
		r := d.Runs[0]
		d.Jobs = append(d.Jobs, JobRow{Name: fmt.Sprintf("Job <%d> & mais um nome bem comprido para encher a mensagem", i), Agent: "srv", Enabled: true, Last: &r})
	}
	tg := d.Telegram()
	if len(tg) > 4096 {
		t.Fatalf("a mensagem do Telegram passou do limite: %d", len(tg))
	}
	if !strings.Contains(tg, "e mais") || strings.Contains(tg, "<0>") || !strings.Contains(tg, "&lt;0&gt;") {
		t.Fatalf("truncamento/escape:\n%s", tg[:300])
	}
	ev := RunEvent{Brand: DefaultBrand(), Run: d.Runs[2]}
	if s := ev.Telegram(); !strings.Contains(s, "Falhou") || !strings.Contains(s, "<code>") || len(s) > 4096 {
		t.Fatal(s)
	}
	long := ev
	long.Run.Error = strings.Repeat("x", 5000)
	if len(long.Telegram()) > 4096 {
		t.Fatal("erro longo deve ser truncado")
	}
}

func TestPDFA4ComVariasPaginasESemPaginaEmBranco(t *testing.T) {
	d := sample()
	pdf, err := d.PDF()
	if err != nil || !bytes.HasPrefix(pdf, []byte("%PDF")) {
		t.Fatalf("%v", err)
	}
	if !bytes.Contains(pdf, []byte("/MediaBox [0 0 595.28 841.89]")) {
		t.Fatal("o PDF deve ser A4")
	}
	// muitas execuções => várias páginas, e o relatório continua válido
	for i := 0; i < 80; i++ {
		d.Runs = append(d.Runs, d.Runs[i%4])
	}
	big, err := d.PDF()
	if err != nil {
		t.Fatal(err)
	}
	if n := bytes.Count(big, []byte("/Type /Page\n")); n < 3 {
		t.Fatalf("esperava várias páginas, vieram %d", n)
	}
	if out := os.Getenv("REPORT_OUT"); out != "" {
		os.WriteFile(filepath.Join(out, "relatorio.pdf"), pdf, 0o644)
		os.WriteFile(filepath.Join(out, "relatorio-grande.pdf"), big, 0o644)
		os.WriteFile(filepath.Join(out, "relatorio.html"), []byte(d.HTML()), 0o644)
		ev := RunEvent{Brand: DefaultBrand(), Run: sample().Runs[2]}
		os.WriteFile(filepath.Join(out, "evento.html"), []byte(ev.HTML()), 0o644)
	}
}

func TestEventoDeAgenteOfflineEExecucao(t *testing.T) {
	a := AgentEvent{Brand: DefaultBrand(), Agent: AgentRow{Name: "srv<1>", LastSeen: tp(time.Now())}, Minutes: 15}
	if !strings.Contains(a.HTML(), "srv&lt;1&gt;") || !strings.Contains(a.Telegram(), "15 min") || !strings.Contains(a.Text(), "srv<1>") {
		t.Fatal("evento de agente offline")
	}
	e := RunEvent{Brand: DefaultBrand(), Run: sample().Runs[0]}
	for _, s := range []string{e.HTML(), e.Text(), e.Telegram()} {
		if !strings.Contains(s, "Servidor web") || !strings.Contains(s, "quickxor") {
			t.Fatalf("evento sem os dados:\n%s", s)
		}
	}
}

func TestFormatacao(t *testing.T) {
	if FmtBytes(0) != "—" || FmtBytes(512) != "512 B" || FmtBytes(1536) != "1.5 KiB" || FmtBytes(3<<30) != "3.0 GiB" {
		t.Fatal("FmtBytes")
	}
	a, b := time.Unix(0, 0), time.Unix(3725, 0)
	if fmtDur(&a, &b) != "1h 2min" || fmtDur(nil, &b) != "—" {
		t.Fatal(fmtDur(&a, &b))
	}
}

func TestCreditosConfiguraveisNosRelatorios(t *testing.T) {
	b := DefaultBrand()
	b.CreditLabel, b.CreditName, b.CreditURL = "Feito por", "TI <Acme>", "https://acme.example"
	ev := RunEvent{Brand: b, Run: sample().Runs[2]}
	if h := ev.HTML(); !strings.Contains(h, `Feito por <a href="https://acme.example"`) || !strings.Contains(h, "TI &lt;Acme&gt;") || strings.Contains(h, "Henrique") {
		t.Fatal("e-mail sem os créditos configurados")
	}
	if txt := ev.Text(); !strings.Contains(txt, "Feito por TI <Acme>") || strings.Contains(txt, "Henrique") {
		t.Fatalf("%s", txt)
	}
	b.CreditName = ""
	if h := (RunEvent{Brand: b, Run: sample().Runs[2]}).HTML(); strings.Contains(h, "Feito por") || strings.Contains(h, "acme.example") {
		t.Fatal("créditos desligados ainda aparecem")
	}
}

func TestHistoryAgrupaPorDiaNoFuso(t *testing.T) {
	loc, _ := time.LoadLocation("America/Sao_Paulo")
	now := time.Date(2026, 11, 27, 10, 0, 0, 0, loc)
	at := func(d, h int) *time.Time { x := time.Date(2026, 11, d, h, 0, 0, 0, loc); return &x }
	runs := []RunRow{
		{Status: "ok", StartedAt: at(27, 1), BytesOut: 10},
		{Status: "warning", StartedAt: at(27, 2), BytesOut: 5},
		{Status: "failed", StartedAt: at(26, 23), BytesOut: 99}, // 02:00 UTC do dia 27, mas dia 26 no fuso
		{Status: "missed", Scheduled: at(25, 3)},                // perdido: só tem o horário agendado
		{Status: "canceled", StartedAt: at(25, 4)},
		{Status: "running", StartedAt: at(27, 9)},
		{Status: "ok", StartedAt: at(10, 1)}, // fora da janela
	}
	h := History(runs, now, 3, loc)
	if len(h) != 3 || h[0].Date != "2026-11-25" || h[2].Date != "2026-11-27" {
		t.Fatalf("dias: %+v", h)
	}
	if h[2].OK != 1 || h[2].Warning != 1 || h[2].Bytes != 15 || h[1].Failed != 1 || h[1].Bytes != 0 || h[0].Missed != 1 || h[0].Canceled != 1 || h[0].Finished() != 1 {
		t.Fatalf("contagem: %+v", h)
	}
	s := Sum(runs)
	if s.Runs != 6 || s.SuccessRate() != 0.6 { // Sum não recorta por data: soma tudo o que recebe
		t.Fatalf("totais: %+v %v", s, s.SuccessRate())
	}
	if v := VolumeByJob([]RunRow{{Job: "a", Status: "ok", BytesOut: 1}, {Job: "b", Status: "ok", BytesOut: 5}, {Job: "a", Status: "failed", BytesOut: 50}}); v[0].Name != "b" || v[1].Bytes != 1 {
		t.Fatalf("volume: %+v", v)
	}
}
