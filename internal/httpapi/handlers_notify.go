package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/mail"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/AprendendoLinux/corujabackup/internal/notify"
	"github.com/AprendendoLinux/corujabackup/internal/report"
	"github.com/AprendendoLinux/corujabackup/internal/store"
)

// NotifyAdmin é o que a API precisa do serviço de notificações.
type NotifyAdmin interface {
	SendTest(ctx context.Context, channelID int64) error
	QueueRuleNow(ctx context.Context, ruleID int64) error
	Preview(ctx context.Context) (*report.Daily, []byte, error)
}

var (
	tokenRE = regexp.MustCompile(`^\d{5,}:[A-Za-z0-9_-]{20,}$`)
	chatRE  = regexp.MustCompile(`^(-?\d{3,20}|@[A-Za-z0-9_]{4,64})$`)
	timeRE  = regexp.MustCompile(`^([01]\d|2[0-3]):[0-5]\d$`)
)

var ruleEvents = map[string]bool{"run_failed": true, "run_warning": true, "run_ok": true, "daily_report": true, "agent_offline": true}

type channelIn struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	Enabled *bool  `json:"enabled"`
	Email   *struct {
		Host       string   `json:"host"`
		Port       int      `json:"port"`
		Security   string   `json:"security"`
		User       string   `json:"user"`
		Password   *string  `json:"password"`
		From       string   `json:"from"`
		FromName   string   `json:"from_name"`
		To         []string `json:"to"`
		SkipVerify bool     `json:"skip_verify"`
	} `json:"email"`
	Telegram *struct {
		Token   *string  `json:"token"`
		ChatIDs []string `json:"chat_ids"`
	} `json:"telegram"`
}

type channelView struct {
	store.Channel
	Email    map[string]any `json:"email,omitempty"`
	Telegram map[string]any `json:"telegram,omitempty"`
}

func (s *Server) channelView(c store.Channel) channelView {
	v := channelView{Channel: c}
	plain, err := s.Box.Open(c.Config)
	if err != nil {
		return v
	}
	switch c.Type {
	case "email":
		var e notify.EmailConfig
		if json.Unmarshal([]byte(plain), &e) == nil {
			v.Email = map[string]any{"host": e.Host, "port": e.Port, "security": e.Security, "user": e.User, "password_set": e.Password != "", "from": e.From, "from_name": e.FromName, "to": e.To, "skip_verify": e.SkipVerify}
		}
	case "telegram":
		var t notify.TelegramConfig
		if json.Unmarshal([]byte(plain), &t) == nil {
			v.Telegram = map[string]any{"token_set": t.Token != "", "chat_ids": t.ChatIDs}
		}
	}
	return v
}

func (s *Server) listChannels(w http.ResponseWriter, r *http.Request, _ *ctxUser) {
	cs, err := s.Store.ListChannels(r.Context())
	if err != nil {
		writeErr(w, 500, "erro interno")
		return
	}
	out := make([]channelView, 0, len(cs))
	for _, c := range cs {
		out = append(out, s.channelView(c))
	}
	writeJSON(w, 200, map[string]any{"channels": out})
}

// buildChannel valida e monta a configuração (mantendo os segredos atuais quando o campo vier vazio).
func (s *Server) buildChannel(in channelIn, old *store.Channel) (typ, sealed, msg string) {
	typ = in.Type
	if old != nil {
		typ = old.Type
	}
	var oldPlain string
	if old != nil {
		oldPlain, _ = s.Box.Open(old.Config)
	}
	switch typ {
	case "email":
		if in.Email == nil {
			return "", "", "informe a configuração de e-mail"
		}
		e := notify.EmailConfig{Host: strings.TrimSpace(in.Email.Host), Port: in.Email.Port, Security: in.Email.Security, User: strings.TrimSpace(in.Email.User),
			From: strings.TrimSpace(in.Email.From), FromName: strings.TrimSpace(in.Email.FromName), SkipVerify: in.Email.SkipVerify}
		if old != nil {
			var oc notify.EmailConfig
			_ = json.Unmarshal([]byte(oldPlain), &oc)
			e.Password = oc.Password
		}
		if in.Email.Password != nil && *in.Email.Password != "" {
			e.Password = *in.Email.Password
		}
		if e.Host == "" || strings.ContainsAny(e.Host, " /\\") || len(e.Host) > 255 {
			return "", "", "servidor SMTP inválido"
		}
		if e.Port < 0 || e.Port > 65535 {
			return "", "", "porta inválida"
		}
		switch e.Security {
		case "starttls", "ssl", "none":
		case "":
			e.Security = "starttls"
		default:
			return "", "", "segurança inválida (starttls, ssl ou none)"
		}
		if _, err := mail.ParseAddress(e.From); err != nil {
			return "", "", "remetente inválido"
		}
		if len(in.Email.To) == 0 || len(in.Email.To) > 20 {
			return "", "", "informe de 1 a 20 destinatários"
		}
		for _, t := range in.Email.To {
			a, err := mail.ParseAddress(strings.TrimSpace(t))
			if err != nil {
				return "", "", "destinatário inválido: " + t
			}
			e.To = append(e.To, a.Address)
		}
		if len(e.User) > 255 || len(e.Password) > 512 || len(e.FromName) > 100 || strings.ContainsAny(e.FromName, "\r\n") {
			return "", "", "usuário, senha ou nome do remetente inválido"
		}
		b, _ := json.Marshal(e)
		sealed, _ = s.Box.Seal(string(b))
	case "telegram":
		if in.Telegram == nil {
			return "", "", "informe a configuração do Telegram"
		}
		t := notify.TelegramConfig{}
		if old != nil {
			var ot notify.TelegramConfig
			_ = json.Unmarshal([]byte(oldPlain), &ot)
			t.Token = ot.Token
		}
		if in.Telegram.Token != nil && *in.Telegram.Token != "" {
			t.Token = strings.TrimSpace(*in.Telegram.Token)
		}
		if !tokenRE.MatchString(t.Token) {
			return "", "", "token do bot inválido (formato 123456:ABC…)"
		}
		if len(in.Telegram.ChatIDs) == 0 || len(in.Telegram.ChatIDs) > 20 {
			return "", "", "informe de 1 a 20 IDs de chat"
		}
		for _, c := range in.Telegram.ChatIDs {
			c = strings.TrimSpace(c)
			if !chatRE.MatchString(c) {
				return "", "", "ID de chat inválido: " + c
			}
			t.ChatIDs = append(t.ChatIDs, c)
		}
		b, _ := json.Marshal(t)
		sealed, _ = s.Box.Seal(string(b))
	default:
		return "", "", "tipo de canal inválido (email ou telegram)"
	}
	return typ, sealed, ""
}

func (s *Server) createChannel(w http.ResponseWriter, r *http.Request, cu *ctxUser) {
	var in channelIn
	if !decode(w, r, &in) {
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if !cloudNameRE.MatchString(in.Name) {
		writeErr(w, 400, "nome inválido: use 2 a 100 caracteres")
		return
	}
	typ, sealed, msg := s.buildChannel(in, nil)
	if msg != "" {
		writeErr(w, 400, msg)
		return
	}
	enabled := in.Enabled == nil || *in.Enabled
	id, err := s.Store.CreateChannel(r.Context(), in.Name, typ, sealed, enabled)
	if errors.Is(err, store.ErrDuplicate) {
		writeErr(w, 409, "já existe um canal com esse nome")
		return
	}
	if err != nil {
		writeErr(w, 500, "erro interno")
		return
	}
	s.Store.Audit(r.Context(), cu.User.ID, cu.User.Username, "notify.channel_created", in.Name+" ("+typ+")", s.clientIP(r))
	writeJSON(w, 201, map[string]int64{"id": id})
}

func (s *Server) updateChannel(w http.ResponseWriter, r *http.Request, cu *ctxUser) {
	id, ok := pathID(r)
	var in channelIn
	if !ok || !decode(w, r, &in) {
		return
	}
	old, err := s.Store.GetChannel(r.Context(), id)
	if err != nil {
		writeErr(w, 404, "canal não encontrado")
		return
	}
	name := old.Name
	if in.Name != "" {
		name = strings.TrimSpace(in.Name)
		if !cloudNameRE.MatchString(name) {
			writeErr(w, 400, "nome inválido")
			return
		}
	}
	sealed := old.Config
	if in.Email != nil || in.Telegram != nil {
		_, sl, msg := s.buildChannel(in, old)
		if msg != "" {
			writeErr(w, 400, msg)
			return
		}
		sealed = sl
	}
	enabled := old.Enabled
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	if err := s.Store.UpdateChannel(r.Context(), id, name, sealed, enabled); err != nil {
		if errors.Is(err, store.ErrDuplicate) {
			writeErr(w, 409, "já existe um canal com esse nome")
			return
		}
		writeErr(w, 500, "erro interno")
		return
	}
	s.Store.Audit(r.Context(), cu.User.ID, cu.User.Username, "notify.channel_updated", name, s.clientIP(r))
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) deleteChannel(w http.ResponseWriter, r *http.Request, cu *ctxUser) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, 400, "id inválido")
		return
	}
	c, err := s.Store.GetChannel(r.Context(), id)
	if err != nil {
		writeErr(w, 404, "canal não encontrado")
		return
	}
	_ = s.Store.DeleteChannel(r.Context(), id)
	s.Store.Audit(r.Context(), cu.User.ID, cu.User.Username, "notify.channel_deleted", c.Name, s.clientIP(r))
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) testChannel(w http.ResponseWriter, r *http.Request, cu *ctxUser) {
	id, ok := pathID(r)
	if !ok || s.Notifier == nil {
		writeErr(w, 400, "id inválido")
		return
	}
	if _, err := s.Store.GetChannel(r.Context(), id); err != nil {
		writeErr(w, 404, "canal não encontrado")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	if err := s.Notifier.SendTest(ctx, id); err != nil {
		writeJSON(w, 200, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	s.Store.Audit(r.Context(), cu.User.ID, cu.User.Username, "notify.channel_tested", strconv.FormatInt(id, 10), s.clientIP(r))
	writeJSON(w, 200, map[string]any{"ok": true})
}

// ---------------------------------------------------------------- regras

type ruleIn struct {
	Name       string           `json:"name"`
	Event      string           `json:"event"`
	Enabled    *bool            `json:"enabled"`
	JobIDs     []int64          `json:"job_ids"`
	ChannelIDs []int64          `json:"channel_ids"`
	Params     store.RuleParams `json:"params"`
}

func (s *Server) buildRule(ctx context.Context, in ruleIn, id int64) (*store.Rule, string) {
	in.Name = strings.TrimSpace(in.Name)
	if !cloudNameRE.MatchString(in.Name) {
		return nil, "nome inválido: use 2 a 100 caracteres"
	}
	if !ruleEvents[in.Event] {
		return nil, "tipo de evento inválido"
	}
	if len(in.ChannelIDs) == 0 {
		return nil, "escolha pelo menos um canal de envio"
	}
	for _, cid := range in.ChannelIDs {
		if _, err := s.Store.GetChannel(ctx, cid); err != nil {
			return nil, "canal inexistente"
		}
	}
	for _, jid := range in.JobIDs {
		if _, err := s.Store.GetJob(ctx, jid); err != nil {
			return nil, "job inexistente"
		}
	}
	p := in.Params
	switch in.Event {
	case "daily_report":
		if p.Time == "" {
			p.Time = "07:00"
		}
		if !timeRE.MatchString(p.Time) {
			return nil, "horário inválido (use HH:MM)"
		}
		if p.Timezone == "" {
			p.Timezone = notify.DefaultTZ
		}
		if _, err := time.LoadLocation(p.Timezone); err != nil {
			return nil, "fuso horário inválido"
		}
		in.JobIDs = nil
	case "agent_offline":
		if p.OfflineMin == 0 {
			p.OfflineMin = 15
		}
		if p.OfflineMin < 1 || p.OfflineMin > 1440 {
			return nil, "minutos de silêncio inválidos (1 a 1440)"
		}
		in.JobIDs, p.AttachPDF = nil, false
		p.Time, p.Timezone, p.OnlyProblems = "", notify.DefaultTZ, false
	default:
		p.Time, p.OnlyProblems, p.OfflineMin, p.AttachPDF = "", false, 0, false
		if p.Timezone == "" {
			p.Timezone = notify.DefaultTZ
		}
		if _, err := time.LoadLocation(p.Timezone); err != nil {
			return nil, "fuso horário inválido"
		}
	}
	enabled := in.Enabled == nil || *in.Enabled
	return &store.Rule{ID: id, Name: in.Name, Event: in.Event, Enabled: enabled, JobIDs: in.JobIDs, ChannelIDs: in.ChannelIDs, Params: p}, ""
}

func (s *Server) listRules(w http.ResponseWriter, r *http.Request, _ *ctxUser) {
	rs, err := s.Store.ListRules(r.Context())
	if err != nil {
		writeErr(w, 500, "erro interno")
		return
	}
	writeJSON(w, 200, map[string]any{"rules": rs})
}

func (s *Server) createRule(w http.ResponseWriter, r *http.Request, cu *ctxUser) {
	var in ruleIn
	if !decode(w, r, &in) {
		return
	}
	rule, msg := s.buildRule(r.Context(), in, 0)
	if msg != "" {
		writeErr(w, 400, msg)
		return
	}
	id, err := s.Store.CreateRule(r.Context(), rule)
	if errors.Is(err, store.ErrDuplicate) {
		writeErr(w, 409, "já existe uma regra com esse nome")
		return
	}
	if err != nil {
		writeErr(w, 500, "erro interno")
		return
	}
	s.Store.Audit(r.Context(), cu.User.ID, cu.User.Username, "notify.rule_created", rule.Name, s.clientIP(r))
	writeJSON(w, 201, map[string]int64{"id": id})
}

func (s *Server) updateRule(w http.ResponseWriter, r *http.Request, cu *ctxUser) {
	id, ok := pathID(r)
	var in ruleIn
	if !ok || !decode(w, r, &in) {
		return
	}
	if _, err := s.Store.GetRule(r.Context(), id); err != nil {
		writeErr(w, 404, "regra não encontrada")
		return
	}
	rule, msg := s.buildRule(r.Context(), in, id)
	if msg != "" {
		writeErr(w, 400, msg)
		return
	}
	if err := s.Store.UpdateRule(r.Context(), rule); err != nil {
		if errors.Is(err, store.ErrDuplicate) {
			writeErr(w, 409, "já existe uma regra com esse nome")
			return
		}
		writeErr(w, 500, "erro interno")
		return
	}
	s.Store.Audit(r.Context(), cu.User.ID, cu.User.Username, "notify.rule_updated", rule.Name, s.clientIP(r))
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) deleteRule(w http.ResponseWriter, r *http.Request, cu *ctxUser) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, 400, "id inválido")
		return
	}
	rule, err := s.Store.GetRule(r.Context(), id)
	if err != nil {
		writeErr(w, 404, "regra não encontrada")
		return
	}
	_ = s.Store.DeleteRule(r.Context(), id)
	s.Store.Audit(r.Context(), cu.User.ID, cu.User.Username, "notify.rule_deleted", rule.Name, s.clientIP(r))
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) sendRuleNow(w http.ResponseWriter, r *http.Request, cu *ctxUser) {
	id, ok := pathID(r)
	if !ok || s.Notifier == nil {
		writeErr(w, 400, "id inválido")
		return
	}
	if err := s.Notifier.QueueRuleNow(r.Context(), id); err != nil {
		writeErr(w, 409, err.Error())
		return
	}
	s.Store.Audit(r.Context(), cu.User.ID, cu.User.Username, "notify.send_now", strconv.FormatInt(id, 10), s.clientIP(r))
	writeJSON(w, 202, map[string]bool{"ok": true})
}

func (s *Server) notifyLog(w http.ResponseWriter, r *http.Request, _ *ctxUser) {
	n, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if n <= 0 || n > 500 {
		n = 100
	}
	ns, err := s.Store.ListNotifications(r.Context(), n)
	if err != nil {
		writeErr(w, 500, "erro interno")
		return
	}
	writeJSON(w, 200, map[string]any{"notifications": ns})
}

// dailyPDF baixa o relatório diário (agora) em PDF A4.
func (s *Server) dailyPDF(w http.ResponseWriter, r *http.Request, _ *ctxUser) {
	if s.Notifier == nil {
		writeErr(w, 503, "relatórios indisponíveis")
		return
	}
	d, pdf, err := s.Notifier.Preview(r.Context())
	if err != nil {
		writeErr(w, 500, "não foi possível gerar o relatório")
		return
	}
	h := w.Header()
	h.Set("Content-Type", "application/pdf")
	h.Set("Content-Disposition", fmt.Sprintf(`inline; filename="Relatorio-Backup-%s.pdf"`, d.Generated.Format("2006-01-02")))
	h.Set("Cache-Control", "no-store")
	_, _ = w.Write(pdf)
}

func (s *Server) getNotifySettings(w http.ResponseWriter, r *http.Request, _ *ctxUser) {
	m, _ := s.Store.GetSettings(r.Context(), "app.base_url")
	writeJSON(w, 200, map[string]string{"base_url": m["app.base_url"]})
}

func (s *Server) saveNotifySettings(w http.ResponseWriter, r *http.Request, cu *ctxUser) {
	var in struct {
		BaseURL string `json:"base_url"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.BaseURL = strings.TrimRight(strings.TrimSpace(in.BaseURL), "/")
	if in.BaseURL != "" {
		u, err := url.Parse(in.BaseURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || len(in.BaseURL) > 255 {
			writeErr(w, 400, "endereço inválido: use http:// ou https://")
			return
		}
	}
	if err := s.Store.SetSetting(r.Context(), "app.base_url", in.BaseURL); err != nil {
		writeErr(w, 500, "erro interno")
		return
	}
	s.Store.Audit(r.Context(), cu.User.ID, cu.User.Username, "notify.settings", in.BaseURL, s.clientIP(r))
	writeJSON(w, 200, map[string]bool{"ok": true})
}
