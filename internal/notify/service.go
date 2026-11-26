package notify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/AprendendoLinux/corujabackup/internal/report"
	"github.com/AprendendoLinux/corujabackup/internal/secrets"
	"github.com/AprendendoLinux/corujabackup/internal/store"
)

const (
	DefaultTZ         = "America/Sao_Paulo"
	maxAttempts       = 5
	dailyLateWindow   = 6 * time.Hour
	defaultOfflineMin = 15
)

var retryDelays = []time.Duration{time.Minute, 5 * time.Minute, 15 * time.Minute, 30 * time.Minute}

type Service struct {
	Store    *store.Store
	Box      *secrets.Box
	Telegram Telegram
	Now      func() time.Time
	Log      *slog.Logger

	lastSched time.Time
}

func New(st *store.Store, box *secrets.Box) *Service {
	return &Service{Store: st, Box: box, Now: time.Now, Log: slog.Default()}
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// ---------------------------------------------------------------- eventos

func eventFor(status string) string {
	switch status {
	case "failed", "missed":
		return "run_failed"
	case "warning":
		return "run_warning"
	case "ok":
		return "run_ok"
	}
	return ""
}

func contains(ids []int64, id int64) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}

// OnRunFinished enfileira as notificações de uma execução que acabou de terminar.
func (s *Service) OnRunFinished(ctx context.Context, runID int64) {
	run, err := s.Store.GetRun(ctx, runID)
	if err != nil {
		return
	}
	ev := eventFor(run.Status)
	if ev == "" {
		return
	}
	rules, err := s.Store.ListRules(ctx)
	if err != nil {
		return
	}
	now := s.now()
	for _, r := range rules {
		if !r.Enabled || r.Event != ev || (len(r.JobIDs) > 0 && !contains(r.JobIDs, run.JobID)) {
			continue
		}
		for _, cid := range r.ChannelIDs {
			if _, err := s.Store.Enqueue(ctx, r.ID, cid, "run", strconv.FormatInt(run.ID, 10), now); err != nil {
				s.Log.Warn("não foi possível enfileirar a notificação", "erro", err.Error())
			}
		}
	}
}

func (s *Service) locOf(name string) *time.Location {
	if l, err := time.LoadLocation(name); err == nil && name != "" {
		return l
	}
	l, _ := time.LoadLocation(DefaultTZ)
	if l == nil {
		return time.UTC
	}
	return l
}

// scheduleChecks enfileira os relatórios diários cujo horário chegou e os alertas de agente offline.
func (s *Service) scheduleChecks(ctx context.Context, now time.Time) {
	rules, err := s.Store.ListRules(ctx)
	if err != nil {
		return
	}
	var agents []store.Agent
	for _, r := range rules {
		if !r.Enabled || len(r.ChannelIDs) == 0 {
			continue
		}
		switch r.Event {
		case "daily_report":
			loc := s.locOf(r.Params.Timezone)
			ln := now.In(loc)
			hh, mm := parseHM(r.Params.Time)
			at := time.Date(ln.Year(), ln.Month(), ln.Day(), hh, mm, 0, 0, loc)
			if ln.Before(at) || ln.Sub(at) > dailyLateWindow {
				continue
			}
			for _, cid := range r.ChannelIDs {
				_, _ = s.Store.Enqueue(ctx, r.ID, cid, "daily", ln.Format("2006-01-02"), now)
			}
		case "agent_offline":
			if agents == nil {
				agents, _ = s.Store.ListAgents(ctx)
			}
			limit := r.Params.OfflineMin
			if limit <= 0 {
				limit = defaultOfflineMin
			}
			for _, a := range agents {
				if a.RevokedAt != nil || a.LastSeenAt == nil || now.Sub(*a.LastSeenAt) < time.Duration(limit)*time.Minute {
					continue
				}
				for _, cid := range r.ChannelIDs {
					_, _ = s.Store.Enqueue(ctx, r.ID, cid, "offline", fmt.Sprintf("%d:%d", a.ID, a.LastSeenAt.Unix()), now)
				}
			}
		}
	}
}

func parseHM(s string) (int, int) {
	var h, m int
	if _, err := fmt.Sscanf(s, "%d:%d", &h, &m); err != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		return 7, 0
	}
	return h, m
}

// ---------------------------------------------------------------- laço principal

// Run processa a fila a cada 15 s e confere os agendamentos a cada minuto, até ctx terminar.
func (s *Service) Run(ctx context.Context) {
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()
	for {
		s.Tick(ctx)
		select {
		case <-t.C:
		case <-ctx.Done():
			return
		}
	}
}

func (s *Service) Tick(ctx context.Context) {
	now := s.now()
	if now.Sub(s.lastSched) >= 55*time.Second {
		s.lastSched = now
		s.scheduleChecks(ctx, now)
	}
	due, err := s.Store.DuePending(ctx, now, 20)
	if err != nil {
		return
	}
	for _, n := range due {
		s.deliver(ctx, n)
	}
}

func (s *Service) deliver(ctx context.Context, n store.Notification) {
	sctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	subject, note, retry, err := s.send(sctx, n)
	switch {
	case err == nil:
		if note != "" {
			subject += " (" + note + ")"
		}
		_ = s.Store.MarkSent(ctx, n.ID, subject)
	case !retry || n.Attempts+1 >= maxAttempts:
		s.Log.Warn("notificação não enviada", "canal", n.ChannelName, "erro", err.Error())
		_ = s.Store.MarkAttemptFailed(ctx, n.ID, err.Error(), time.Time{})
	default:
		d := retryDelays[min(n.Attempts, len(retryDelays)-1)]
		_ = s.Store.MarkAttemptFailed(ctx, n.ID, err.Error(), s.now().Add(d))
	}
}

// ---------------------------------------------------------------- envio

type message struct {
	subject, text, html, telegram string
	pdf                           []byte
	pdfName                       string
	skip                          string // não enviar (motivo)
}

// send devolve (assunto, observação, vale tentar de novo?, erro).
func (s *Service) send(ctx context.Context, n store.Notification) (string, string, bool, error) {
	if n.ChannelID == nil || n.RuleID == nil {
		return "", "", false, errors.New("canal ou regra removidos")
	}
	ch, err := s.Store.GetChannel(ctx, *n.ChannelID)
	if err != nil {
		return "", "", false, errors.New("canal removido")
	}
	if !ch.Enabled {
		return "", "", false, errors.New("canal desativado")
	}
	rule, err := s.Store.GetRule(ctx, *n.RuleID)
	if err != nil {
		return "", "", false, errors.New("regra removida")
	}
	msg, err := s.compose(ctx, n.Kind, n.Ref, rule)
	if err != nil {
		return "", "", true, err
	}
	if msg.skip != "" {
		return "sem envio", msg.skip, false, nil
	}
	note, retry, err := s.dispatch(ctx, ch, msg)
	return msg.subject, note, retry, err
}

func (s *Service) compose(ctx context.Context, kind, ref string, rule *store.Rule) (*message, error) {
	brand := LoadBrand(ctx, s.Store)
	switch kind {
	case "run":
		id, _ := strconv.ParseInt(ref, 10, 64)
		run, err := s.Store.GetRun(ctx, id)
		if err != nil {
			return nil, errors.New("execução não encontrada")
		}
		job, _ := s.Store.GetJob(ctx, run.JobID)
		jk := ""
		if job != nil {
			jk = job.Kind
		}
		loc := s.locOf(rule.Params.Timezone)
		ev := report.RunEvent{Brand: brand, Loc: loc, Run: rowFrom(*run, jk)}
		return &message{subject: ev.Subject(), text: ev.Text(), html: ev.HTML(), telegram: ev.Telegram()}, nil
	case "offline":
		idStr, _, _ := strings.Cut(ref, ":")
		id, _ := strconv.ParseInt(idStr, 10, 64)
		a, err := s.Store.AgentByID(ctx, id)
		if err != nil {
			return nil, errors.New("agente não encontrado")
		}
		min := rule.Params.OfflineMin
		if min <= 0 {
			min = defaultOfflineMin
		}
		ev := report.AgentEvent{Brand: brand, Loc: s.locOf(rule.Params.Timezone), Agent: report.AgentRow{Name: a.Name, LastSeen: a.LastSeenAt, Version: a.AgentVersion}, Minutes: min}
		return &message{subject: ev.Subject(), text: ev.Text(), html: ev.HTML(), telegram: ev.Telegram()}, nil
	case "daily", "test", "manual":
		loc := s.locOf(rule.Params.Timezone)
		d, err := Collect(ctx, s.Store, brand, s.now(), loc)
		if err != nil {
			return nil, err
		}
		if kind == "daily" && rule.Params.OnlyProblems && d.Overall == "ok" {
			return &message{skip: "tudo certo, relatório não enviado (só em caso de problemas)"}, nil
		}
		m := &message{subject: d.Subject(), text: d.Text(), html: d.HTML(), telegram: d.Telegram()}
		if kind == "test" {
			m.subject = "[TESTE] " + m.subject
			m.telegram = "<b>[TESTE]</b> " + m.telegram
		}
		if rule.Params.AttachPDF {
			pdf, err := d.PDF()
			if err != nil {
				return nil, fmt.Errorf("não foi possível gerar o PDF: %w", err)
			}
			m.pdf, m.pdfName = pdf, "Relatorio-Backup-"+d.Generated.In(loc).Format("2006-01-02")+".pdf"
		}
		return m, nil
	}
	return nil, fmt.Errorf("tipo de notificação desconhecido: %s", kind)
}

func (s *Service) dispatch(ctx context.Context, ch *store.Channel, m *message) (note string, retry bool, err error) {
	plain, err := s.Box.Open(ch.Config)
	if err != nil {
		return "", false, fmt.Errorf("não foi possível ler a configuração do canal: %w", err)
	}
	switch ch.Type {
	case "email":
		var cfg EmailConfig
		if err := json.Unmarshal([]byte(plain), &cfg); err != nil {
			return "", false, err
		}
		var atts []Attachment
		if m.pdf != nil {
			atts = append(atts, Attachment{Name: m.pdfName, MIME: "application/pdf", Data: m.pdf})
		}
		brand := LoadBrand(ctx, s.Store)
		var inline *Inline
		if len(brand.LogoPNG) > 0 {
			inline = &Inline{CID: "logo", MIME: brand.LogoMIME, Data: brand.LogoPNG}
		}
		raw, err := BuildMessage(cfg, cfg.To, m.subject, m.text, m.html, inline, atts, s.now())
		if err != nil {
			return "", false, err
		}
		if err := SendMail(ctx, cfg, cfg.To, raw); err != nil {
			return "", true, err
		}
		return "", false, nil
	case "telegram":
		var cfg TelegramConfig
		if err := json.Unmarshal([]byte(plain), &cfg); err != nil {
			return "", false, err
		}
		var failed []string
		ok := 0
		var first error
		for _, chat := range cfg.ChatIDs {
			e := s.Telegram.SendMessage(ctx, cfg, chat, m.telegram)
			if e == nil && m.pdf != nil {
				e = s.Telegram.SendDocument(ctx, cfg, chat, m.pdfName, "application/pdf", m.pdf, "")
			}
			if e != nil {
				failed = append(failed, chat)
				if first == nil {
					first = e
				}
				continue
			}
			ok++
		}
		switch {
		case len(failed) == 0:
			return "", false, nil
		case ok == 0:
			return "", true, first // nenhuma entrega: pode repetir sem duplicar
		}
		// entrega parcial: não repete (duplicaria nos chats que já receberam); fica registrado no histórico
		return fmt.Sprintf("falhou em %d de %d chats: %s", len(failed), len(cfg.ChatIDs), first.Error()), false, nil
	}
	return "", false, fmt.Errorf("tipo de canal desconhecido: %s", ch.Type)
}

// ---------------------------------------------------------------- uso pela API

// SendTest envia agora, de forma síncrona, um relatório de exemplo (com PDF) para o canal — a "prova" de que está configurado.
func (s *Service) SendTest(ctx context.Context, channelID int64) error {
	ch, err := s.Store.GetChannel(ctx, channelID)
	if err != nil {
		return err
	}
	rule := &store.Rule{Event: "daily_report", Params: store.RuleParams{AttachPDF: true, Timezone: DefaultTZ}}
	msg, err := s.compose(ctx, "test", "", rule)
	if err != nil {
		return err
	}
	note, _, err := s.dispatch(ctx, ch, msg)
	if err != nil {
		return err
	}
	if note != "" {
		return errors.New(note)
	}
	return nil
}

// Preview devolve o relatório diário (agora) e seu PDF.
func (s *Service) Preview(ctx context.Context) (*report.Daily, []byte, error) {
	d, err := Collect(ctx, s.Store, LoadBrand(ctx, s.Store), s.now(), s.locOf(DefaultTZ))
	if err != nil {
		return nil, nil, err
	}
	pdf, err := d.PDF()
	return d, pdf, err
}

// QueueRuleNow enfileira a regra agora (relatório diário "enviar agora").
func (s *Service) QueueRuleNow(ctx context.Context, ruleID int64) error {
	r, err := s.Store.GetRule(ctx, ruleID)
	if err != nil {
		return err
	}
	if r.Event != "daily_report" {
		return errors.New("só regras de relatório diário podem ser enviadas manualmente")
	}
	ref := "manual-" + strconv.FormatInt(s.now().UnixNano(), 36)
	for _, cid := range r.ChannelIDs {
		if _, err := s.Store.Enqueue(ctx, r.ID, cid, "manual", ref, s.now()); err != nil {
			return err
		}
	}
	return nil
}
