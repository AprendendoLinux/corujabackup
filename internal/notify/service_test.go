package notify

import (
	"context"
	"io"
	"log/slog"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/AprendendoLinux/corujabackup/internal/auth"
	"github.com/AprendendoLinux/corujabackup/internal/secrets"
	"github.com/AprendendoLinux/corujabackup/internal/store"
	"github.com/AprendendoLinux/corujabackup/internal/testutil"
)

const tgToken = "123456:ABCDEFGHIJKLMNOPQRSTUVWX"

type rig struct {
	t     *testing.T
	svc   *Service
	st    *store.Store
	box   *secrets.Box
	now   time.Time
	agent *store.Agent
	jobID int64
}

func newRig(t *testing.T) *rig {
	t.Helper()
	d := testutil.DB(t)
	st := store.New(d)
	box, _ := secrets.Open(t.TempDir())
	r := &rig{t: t, st: st, box: box, now: time.Date(2026, 11, 26, 10, 0, 0, 0, time.UTC)} // 07:00 em São Paulo
	r.svc = New(st, box)
	r.svc.Now = func() time.Time { return r.now }
	r.svc.Log = slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := context.Background()
	h := auth.SHA256Hex("tok")
	st.CreateEnrollToken(ctx, h, "srv-n", 0, time.Now().UTC().Add(time.Hour)) // o banco compara com o relógio real
	a, err := st.Enroll(ctx, h, "host", "linux", "amd64", "0.0.7", "10.0.0.1", auth.SHA256Hex("c"))
	if err != nil {
		t.Fatal(err)
	}
	r.agent = a
	st.DB.Exec("UPDATE agents SET last_seen_at=? WHERE id=?", r.now, a.ID)
	jid, err := st.CreateJob(ctx, &store.Job{Name: "Backup web", AgentID: a.ID, Kind: "files", Enabled: true, Cron: "10 0 * * *", Timezone: "America/Sao_Paulo", Paths: []string{"/etc"}, DestSubdir: "w", Keep: 3})
	if err != nil {
		t.Fatal(err)
	}
	r.jobID = jid
	return r
}

func (r *rig) emailChannel(name string, port int) int64 {
	b := `{"host":"127.0.0.1","port":` + itoa(port) + `,"security":"starttls","user":"relay@x.com","password":"segredo-smtp","from":"backup@x.com","from_name":"Backup","to":["dest@x.com"],"skip_verify":true}`
	s, _ := r.box.Seal(b)
	id, err := r.st.CreateChannel(context.Background(), name, "email", s, true)
	if err != nil {
		r.t.Fatal(err)
	}
	return id
}

func (r *rig) tgChannel(name string, chats ...string) int64 {
	cs := `"` + strings.Join(chats, `","`) + `"`
	s, _ := r.box.Seal(`{"token":"` + tgToken + `","chat_ids":[` + cs + `]}`)
	id, err := r.st.CreateChannel(context.Background(), name, "telegram", s, true)
	if err != nil {
		r.t.Fatal(err)
	}
	return id
}

func (r *rig) rule(name, event string, chans []int64, params store.RuleParams, jobs ...int64) int64 {
	id, err := r.st.CreateRule(context.Background(), &store.Rule{Name: name, Event: event, Enabled: true, JobIDs: jobs, ChannelIDs: chans, Params: params})
	if err != nil {
		r.t.Fatal(err)
	}
	return id
}

func (r *rig) finishRun(uid, status, errTxt string) int64 {
	ctx := context.Background()
	fin, id, err := r.st.ApplyReportX(ctx, store.Report{UID: uid, JobID: r.jobID, AgentID: r.agent.ID, Final: true, Status: status, Error: errTxt,
		StartedAt: r.now.Add(-8 * time.Minute), FinishedAt: r.now, BytesOutput: 5 << 20, Files: 10, OutputName: "backup-web-20261126-001000.tar.gz", CloudStatus: "failed"})
	if err != nil || !fin {
		r.t.Fatalf("finalizar: %v %v", fin, err)
	}
	return id
}

func itoa(i int) string { return strconv.Itoa(i) }

func (r *rig) log() []store.Notification {
	l, _ := r.st.ListNotifications(context.Background(), 50)
	return l
}

func TestRegrasPorEventoEscopoEDeduplicacao(t *testing.T) {
	r := newRig(t)
	srv := newFakeSMTP(t, "starttls", "relay@x.com", "segredo-smtp")
	ch := r.emailChannel("mail", srv.Port())
	other, _ := r.st.CreateJob(context.Background(), &store.Job{Name: "Outro", AgentID: r.agent.ID, Kind: "files", Enabled: true, Cron: "0 1 * * *", Timezone: "UTC", Paths: []string{"/x"}, DestSubdir: "o", Keep: 1})
	r.rule("falhas", "run_failed", []int64{ch}, store.RuleParams{})
	r.rule("so-o-outro", "run_failed", []int64{ch}, store.RuleParams{}, other) // outro escopo
	r.rule("sucessos", "run_ok", []int64{ch}, store.RuleParams{})
	desligada, _ := r.st.CreateRule(context.Background(), &store.Rule{Name: "desligada", Event: "run_failed", Enabled: false, ChannelIDs: []int64{ch}})
	_ = desligada

	id := r.finishRun("u1", "failed", "disco cheio")
	r.svc.OnRunFinished(context.Background(), id)
	r.svc.OnRunFinished(context.Background(), id) // duplicada: não enfileira de novo
	if l := r.log(); len(l) != 1 || l[0].RuleName != "falhas" || l[0].Status != "pending" {
		t.Fatalf("só a regra de falhas do job certo deveria disparar (1 vez): %+v", l)
	}
	// cancelada não notifica
	fin, id2, _ := r.st.ApplyReportX(context.Background(), store.Report{UID: "u2", JobID: r.jobID, AgentID: r.agent.ID, Final: true, Status: "canceled"})
	if fin {
		r.svc.OnRunFinished(context.Background(), id2)
	}
	if len(r.log()) != 1 {
		t.Fatal("cancelamento não gera notificação")
	}
}

func TestEntregaDeFalhaPorEmailETelegramEHistorico(t *testing.T) {
	r := newRig(t)
	srv := newFakeSMTP(t, "starttls", "relay@x.com", "segredo-smtp")
	tg := newFakeTG(t, tgToken)
	r.svc.Telegram = Telegram{Base: tg.URL}
	mail := r.emailChannel("mail", srv.Port())
	tgc := r.tgChannel("telegram", "111", "222")
	r.rule("falhas", "run_failed", []int64{mail, tgc}, store.RuleParams{})
	id := r.finishRun("u1", "failed", "o backup local foi concluído, mas o envio para a nuvem falhou: sessão expirada")
	r.svc.OnRunFinished(context.Background(), id)
	r.svc.Tick(context.Background())

	ms := srv.Messages()
	if len(ms) != 1 || ms[0].Rcpts[0] != "dest@x.com" {
		t.Fatalf("e-mail: %+v", ms)
	}
	_, parts := parseMail(t, ms[0].Data)
	if !strings.Contains(parts["text/plain"], "Backup web") || !strings.Contains(parts["text/html"], "sessão expirada") || !strings.Contains(parts["text/html"], "Desenvolvido por") {
		t.Fatalf("conteúdo: %v", parts)
	}
	if len(tg.Messages) != 2 || !strings.Contains(tg.Messages[0]["text"], "Falhou") || !strings.Contains(tg.Messages[0]["text"], "Backup web") {
		t.Fatalf("telegram: %v", tg.Messages)
	}
	for _, n := range r.log() {
		if n.Status != "sent" || n.Attempts != 1 || !strings.Contains(n.Subject, "Falhou") || n.SentAt == nil {
			t.Fatalf("histórico: %+v", n)
		}
	}
	r.svc.Tick(context.Background()) // nada novo
	if len(srv.Messages()) != 1 || len(tg.Messages) != 2 {
		t.Fatal("não pode reenviar o que já foi entregue")
	}
}

func TestNovasTentativasComEsperaCrescenteEDesistencia(t *testing.T) {
	r := newRig(t)
	ch := r.emailChannel("mail", 1) // ninguém escutando
	r.rule("falhas", "run_failed", []int64{ch}, store.RuleParams{})
	r.svc.OnRunFinished(context.Background(), r.finishRun("u1", "failed", "x"))
	ctx := context.Background()
	delays := []time.Duration{time.Minute, 5 * time.Minute, 15 * time.Minute, 30 * time.Minute}
	for i, d := range delays {
		r.svc.Tick(ctx)
		n := r.log()[0]
		if n.Status != "pending" || n.Attempts != i+1 || n.Error == "" {
			t.Fatalf("tentativa %d: %+v", i+1, n)
		}
		r.svc.Tick(ctx) // ainda dentro da espera: não tenta de novo
		if r.log()[0].Attempts != i+1 {
			t.Fatalf("não deveria tentar antes da espera")
		}
		r.now = r.now.Add(d + time.Second)
	}
	r.svc.Tick(ctx)
	if n := r.log()[0]; n.Status != "failed" || n.Attempts != 5 {
		t.Fatalf("após 5 tentativas deve desistir: %+v", n)
	}
	// recuperação no meio do caminho: servidor volta e a notificação sai
	r2 := newRig(t)
	srv := newFakeSMTP(t, "starttls", "relay@x.com", "segredo-smtp")
	ch2 := r2.emailChannel("mail", 1)
	r2.rule("falhas", "run_failed", []int64{ch2}, store.RuleParams{})
	r2.svc.OnRunFinished(ctx, r2.finishRun("u1", "failed", "x"))
	r2.svc.Tick(ctx)
	enc, _ := r2.box.Seal(`{"host":"127.0.0.1","port":` + itoa(srv.Port()) + `,"security":"starttls","user":"relay@x.com","password":"segredo-smtp","from":"backup@x.com","to":["dest@x.com"],"skip_verify":true}`)
	r2.st.UpdateChannel(ctx, ch2, "mail", enc, true)
	r2.now = r2.now.Add(2 * time.Minute)
	r2.svc.Tick(ctx)
	if n := r2.log()[0]; n.Status != "sent" || n.Attempts != 2 || len(srv.Messages()) != 1 {
		t.Fatalf("recuperação: %+v", n)
	}
}

func TestRelatorioDiarioNoHorarioUmaVezPorDiaComPDF(t *testing.T) {
	r := newRig(t)
	srv := newFakeSMTP(t, "starttls", "relay@x.com", "segredo-smtp")
	tg := newFakeTG(t, tgToken)
	r.svc.Telegram = Telegram{Base: tg.URL}
	r.rule("diário", "daily_report", []int64{r.emailChannel("mail", srv.Port()), r.tgChannel("tg", "111")}, store.RuleParams{Time: "07:00", Timezone: "America/Sao_Paulo", AttachPDF: true})
	ctx := context.Background()
	r.finishRun("u1", "failed", "falha de teste")

	r.now = time.Date(2026, 11, 26, 9, 59, 0, 0, time.UTC) // 06:59 local
	r.svc.Tick(ctx)
	if len(r.log()) != 0 {
		t.Fatal("ainda não é a hora")
	}
	r.now = time.Date(2026, 11, 26, 10, 0, 5, 0, time.UTC) // 07:00:05
	r.svc.lastSched = time.Time{}
	r.svc.Tick(ctx)
	ms := srv.Messages()
	if len(ms) != 1 {
		t.Fatalf("o relatório deveria sair às 07:00: %d", len(ms))
	}
	_, parts := parseMail(t, ms[0].Data)
	pdf := parts["application/pdf:Relatorio-Backup-2026-11-26.pdf"]
	if !strings.HasPrefix(pdf, "%PDF") || !strings.Contains(parts["text/html"], "Precisa de atenção") || !strings.Contains(parts["text/html"], "falha de teste") {
		t.Fatalf("anexo/conteúdo: pdf=%q", pdf[:min(8, len(pdf))])
	}
	if len(tg.Messages) != 1 || len(tg.Docs) != 1 || !strings.HasPrefix(tg.Docs[0], "111|Relatorio-Backup-2026-11-26.pdf|") {
		t.Fatalf("telegram: %v %v", tg.Messages, tg.Docs)
	}
	// o mesmo dia não repete
	r.now = r.now.Add(2 * time.Hour)
	r.svc.lastSched = time.Time{}
	r.svc.Tick(ctx)
	if len(srv.Messages()) != 1 {
		t.Fatal("um relatório por dia")
	}
	// dia seguinte, mas o Server só voltou 8 h depois do horário: não manda relatório velho
	r.now = time.Date(2026, 11, 27, 18, 0, 0, 0, time.UTC)
	r.svc.lastSched = time.Time{}
	r.svc.Tick(ctx)
	if len(srv.Messages()) != 1 {
		t.Fatal("fora da janela de 6 h não envia")
	}
	r.now = time.Date(2026, 11, 28, 10, 30, 0, 0, time.UTC)
	r.svc.lastSched = time.Time{}
	r.svc.Tick(ctx)
	if len(srv.Messages()) != 2 {
		t.Fatal("no dia seguinte, no horário, envia de novo")
	}
}

func TestRelatorioSoComProblemasESemProblemas(t *testing.T) {
	r := newRig(t)
	srv := newFakeSMTP(t, "starttls", "relay@x.com", "segredo-smtp")
	r.rule("diário", "daily_report", []int64{r.emailChannel("mail", srv.Port())}, store.RuleParams{Time: "07:00", Timezone: "America/Sao_Paulo", OnlyProblems: true})
	ctx := context.Background()
	fin, _, _ := r.st.ApplyReportX(ctx, store.Report{UID: "ok1", JobID: r.jobID, AgentID: r.agent.ID, Final: true, Status: "ok", StartedAt: r.now.Add(-time.Hour), FinishedAt: r.now.Add(-50 * time.Minute)})
	_ = fin
	r.svc.Tick(ctx)
	if len(srv.Messages()) != 0 {
		t.Fatal("tudo certo + só problemas: não envia")
	}
	if l := r.log(); len(l) != 1 || l[0].Status != "sent" || !strings.Contains(l[0].Subject, "sem envio") {
		t.Fatalf("o histórico deve explicar por que não enviou: %+v", l)
	}
}

func TestAgenteOfflineAlertaUmaVezPorQueda(t *testing.T) {
	r := newRig(t)
	srv := newFakeSMTP(t, "starttls", "relay@x.com", "segredo-smtp")
	r.rule("offline", "agent_offline", []int64{r.emailChannel("mail", srv.Port())}, store.RuleParams{OfflineMin: 10})
	ctx := context.Background()
	r.svc.Tick(ctx)
	if len(srv.Messages()) != 0 {
		t.Fatal("agente online")
	}
	r.now = r.now.Add(12 * time.Minute) // sem batimento há 12 min
	r.svc.lastSched = time.Time{}
	r.svc.Tick(ctx)
	r.svc.lastSched = time.Time{}
	r.svc.Tick(ctx)
	ms := srv.Messages()
	if len(ms) != 1 || !strings.Contains(ms[0].Data, "srv-n") {
		t.Fatalf("um alerta por queda: %d", len(ms))
	}
	// volta, cai de novo: novo alerta
	r.st.DB.Exec("UPDATE agents SET last_seen_at=? WHERE id=?", r.now, r.agent.ID)
	r.now = r.now.Add(20 * time.Minute)
	r.svc.lastSched = time.Time{}
	r.svc.Tick(ctx)
	if len(srv.Messages()) != 2 {
		t.Fatalf("segunda queda deveria alertar de novo: %d", len(srv.Messages()))
	}
}

func TestTelegramEntregaParcialNaoRepete(t *testing.T) {
	r := newRig(t)
	tg := newFakeTG(t, tgToken)
	tg.Fail["222"] = "Forbidden: bot was blocked by the user"
	r.svc.Telegram = Telegram{Base: tg.URL}
	r.rule("falhas", "run_failed", []int64{r.tgChannel("tg", "111", "222")}, store.RuleParams{})
	r.svc.OnRunFinished(context.Background(), r.finishRun("u1", "failed", "x"))
	r.svc.Tick(context.Background())
	n := r.log()[0]
	if n.Status != "sent" || !strings.Contains(n.Subject, "falhou em 1 de 2") || n.Attempts != 1 {
		t.Fatalf("entrega parcial: %+v", n)
	}
	r.now = r.now.Add(time.Hour)
	r.svc.Tick(context.Background())
	if len(tg.Messages) != 1 {
		t.Fatalf("o chat que já recebeu não pode receber de novo: %d", len(tg.Messages))
	}
	// nenhum chat recebe: tenta de novo depois
	r2 := newRig(t)
	tg2 := newFakeTG(t, tgToken)
	tg2.Fail["111"] = "Forbidden"
	r2.svc.Telegram = Telegram{Base: tg2.URL}
	r2.rule("falhas", "run_failed", []int64{r2.tgChannel("tg", "111")}, store.RuleParams{})
	r2.svc.OnRunFinished(context.Background(), r2.finishRun("u1", "failed", "x"))
	r2.svc.Tick(context.Background())
	if n := r2.log()[0]; n.Status != "pending" || n.Attempts != 1 {
		t.Fatalf("sem nenhuma entrega: %+v", n)
	}
}

func TestEnvioDeTesteEColetaDoRelatorio(t *testing.T) {
	r := newRig(t)
	srv := newFakeSMTP(t, "starttls", "relay@x.com", "segredo-smtp")
	tg := newFakeTG(t, tgToken)
	r.svc.Telegram = Telegram{Base: tg.URL}
	mail, tgc := r.emailChannel("mail", srv.Port()), r.tgChannel("tg", "111")
	ctx := context.Background()
	// cenário: falha na janela, nuvem com erro e quase cheia, agente sem sinal
	r.finishRun("f1", "failed", "disco cheio")
	cid, _ := r.st.CreateCloud(ctx, "OneDrive X", "onedrive", "x", "")
	r.st.SetCloudStatus(ctx, cid, "error", "reconecte a conta")
	r.st.SetCloudQuota(ctx, cid, 1000, 900)
	r.st.DB.Exec("UPDATE agents SET last_seen_at=? WHERE id=?", r.now.Add(-3*time.Hour), r.agent.ID)

	if err := r.svc.SendTest(ctx, mail); err != nil {
		t.Fatal(err)
	}
	if err := r.svc.SendTest(ctx, tgc); err != nil {
		t.Fatal(err)
	}
	ms := srv.Messages()
	if len(ms) != 1 || !strings.Contains(ms[0].Data, "TESTE") {
		t.Fatalf("e-mail de teste: %d", len(ms))
	}
	_, parts := parseMail(t, ms[0].Data)
	html := parts["text/html"]
	for _, want := range []string{"disco cheio", "OneDrive X", "reconecte a conta", "90%", "offline desde", "Atenção: há falhas"} {
		if !strings.Contains(html, want) {
			t.Errorf("relatório sem %q", want)
		}
	}
	if !strings.HasPrefix(parts["application/pdf:Relatorio-Backup-2026-11-26.pdf"], "%PDF") {
		t.Fatal("o teste deve levar o PDF")
	}
	if len(tg.Messages) != 1 || !strings.Contains(tg.Messages[0]["text"], "TESTE") || len(tg.Docs) != 1 {
		t.Fatalf("telegram de teste: %v %v", tg.Messages, tg.Docs)
	}
	bad := r.emailChannel("quebrado", 1)
	if err := r.svc.SendTest(ctx, bad); err == nil {
		t.Fatal("canal com servidor inacessível deveria falhar no teste")
	}
}

func TestAtencaoJuntaLinhasIguais(t *testing.T) {
	got := dedupe([]string{"a falhou", "b falhou", "a falhou", "a falhou"})
	if len(got) != 2 || got[0] != "a falhou (×3)" || got[1] != "b falhou" {
		t.Fatalf("%v", got)
	}
}
