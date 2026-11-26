package notify

import (
	"context"
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"strings"
	"testing"
	"time"
)

func parseMail(t *testing.T, raw string) (*mail.Message, map[string]string) {
	t.Helper()
	m, err := mail.ReadMessage(strings.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	parts := map[string]string{}
	var walk func(h map[string][]string, body io.Reader)
	walk = func(h map[string][]string, body io.Reader) {
		ct := ""
		if v := h["Content-Type"]; len(v) > 0 {
			ct = v[0]
		}
		mt, params, _ := mime.ParseMediaType(ct)
		if strings.HasPrefix(mt, "multipart/") {
			mr := multipart.NewReader(body, params["boundary"])
			for {
				p, err := mr.NextPart()
				if err != nil {
					return
				}
				walk(p.Header, p)
			}
		}
		b, _ := io.ReadAll(body)
		key := mt
		if cd := h["Content-Disposition"]; len(cd) > 0 {
			_, dp, _ := mime.ParseMediaType(cd[0])
			if dp["filename"] != "" {
				key = mt + ":" + dp["filename"]
			}
		}
		if cte := h["Content-Transfer-Encoding"]; len(cte) > 0 && strings.EqualFold(cte[0], "quoted-printable") {
			b = decodeQP(b)
		}
		if cte := h["Content-Transfer-Encoding"]; len(cte) > 0 && strings.EqualFold(cte[0], "base64") {
			b = decodeB64(b)
		}
		parts[key] = string(b)
	}
	walk(m.Header, m.Body)
	return m, parts
}

func cfg(port int, sec string) EmailConfig {
	return EmailConfig{Host: "127.0.0.1", Port: port, Security: sec, User: "relay@x.com", Password: "segredo-smtp", From: "backup@x.com", FromName: "Backup Acme ✔", To: []string{"a@x.com", "b@x.com"}, SkipVerify: true}
}

func TestMensagemMIMEComLogoAnexoEAcentos(t *testing.T) {
	c := cfg(25, "none")
	now := time.Date(2026, 11, 26, 7, 0, 0, 0, time.UTC)
	raw, err := BuildMessage(c, c.To, "🟢 Relatório · ação concluída", "texto simples ç", "<p>html ç</p><img src=\"cid:logo\">", &Inline{CID: "logo", MIME: "image/png", Data: []byte("\x89PNGdados")},
		[]Attachment{{Name: "Relatorio.pdf", MIME: "application/pdf", Data: []byte("%PDF-1.4 conteudo")}}, now)
	if err != nil {
		t.Fatal(err)
	}
	m, parts := parseMail(t, string(raw))
	dec := new(mime.WordDecoder)
	subj, _ := dec.DecodeHeader(m.Header.Get("Subject"))
	from, _ := dec.DecodeHeader(m.Header.Get("From"))
	if subj != "🟢 Relatório · ação concluída" || !strings.Contains(from, "Backup Acme ✔") || m.Header.Get("To") != "a@x.com, b@x.com" {
		t.Fatalf("cabeçalhos: %q %q %q", subj, from, m.Header.Get("To"))
	}
	if !strings.Contains(parts["text/plain"], "texto simples ç") || !strings.Contains(parts["text/html"], "html ç") || !strings.Contains(parts["text/html"], "cid:logo") {
		t.Fatalf("partes de texto: %v", parts)
	}
	if parts["application/pdf:Relatorio.pdf"] != "%PDF-1.4 conteudo" || !strings.HasPrefix(parts["image/png:logo"], "\x89PNG") {
		t.Fatalf("anexos: %v", parts)
	}
	if _, err := BuildMessage(c, nil, "x", "x", "x", nil, nil, now); err == nil {
		t.Fatal("sem destinatários deveria falhar")
	}
}

func TestSMTPEntregaEmTresModos(t *testing.T) {
	for _, mode := range []string{"plain", "starttls", "ssl"} {
		t.Run(mode, func(t *testing.T) {
			srv := newFakeSMTP(t, mode, "relay@x.com", "segredo-smtp")
			sec := map[string]string{"plain": "none", "starttls": "starttls", "ssl": "ssl"}[mode]
			c := cfg(srv.Port(), sec)
			raw, _ := BuildMessage(c, c.To, "assunto", "t", "<b>h</b>", nil, nil, time.Now())
			if err := SendMail(context.Background(), c, c.To, raw); err != nil {
				t.Fatal(err)
			}
			ms := srv.Messages()
			if len(ms) != 1 || ms[0].From != "backup@x.com" || strings.Join(ms[0].Rcpts, ",") != "a@x.com,b@x.com" || !strings.Contains(ms[0].Data, "assunto") {
				t.Fatalf("%+v", ms)
			}
			if len(srv.authSeen) != 1 || srv.authSeen[0] != "|relay@x.com|segredo-smtp" {
				t.Fatalf("auth: %v", srv.authSeen)
			}
		})
	}
}

func TestSMTPErrosNaoVazamSenha(t *testing.T) {
	srv := newFakeSMTP(t, "starttls", "relay@x.com", "senha-certa")
	c := cfg(srv.Port(), "starttls")
	c.Password = "senha-errada-123"
	raw, _ := BuildMessage(c, c.To, "a", "t", "h", nil, nil, time.Now())
	err := SendMail(context.Background(), c, c.To, raw)
	if err == nil || !strings.Contains(err.Error(), "autenticação") || strings.Contains(err.Error(), "senha-errada-123") {
		t.Fatalf("erro de autenticação (sem vazar a senha): %v", err)
	}
	// STARTTLS não oferecido: recusa em vez de mandar a senha em claro
	srv2 := newFakeSMTP(t, "starttls", "relay@x.com", "segredo-smtp")
	srv2.noTLS = true
	c2 := cfg(srv2.Port(), "starttls")
	if err := SendMail(context.Background(), c2, c2.To, raw); err == nil || !strings.Contains(err.Error(), "STARTTLS") || len(srv2.authSeen) != 0 {
		t.Fatalf("não pode autenticar sem criptografia: %v %v", err, srv2.authSeen)
	}
	// certificado autoassinado sem "não validar": recusa
	c3 := cfg(srv.Port(), "starttls")
	c3.SkipVerify = false
	if err := SendMail(context.Background(), c3, c3.To, raw); err == nil {
		t.Fatal("certificado inválido deveria ser recusado")
	}
	// destinatário recusado e servidor fora do ar
	srv.rejectTo = "b@x.com"
	c4 := cfg(srv.Port(), "starttls")
	c4.Password = "senha-certa"
	if err := SendMail(context.Background(), c4, c4.To, raw); err == nil || !strings.Contains(err.Error(), "b@x.com") {
		t.Fatalf("destinatário recusado: %v", err)
	}
	if err := SendMail(context.Background(), cfg(1, "none"), []string{"a@x.com"}, raw); err == nil {
		t.Fatal("servidor inexistente")
	}
}

func TestTelegramMensagemDocumentoEFallback(t *testing.T) {
	const token = "123456:ABCDEFGHIJKLMNOPQRSTUVWX"
	fk := newFakeTG(t, token)
	tg := Telegram{Base: fk.URL}
	tc := TelegramConfig{Token: token, ChatIDs: []string{"111"}}
	ctx := context.Background()
	if err := tg.SendMessage(ctx, tc, "111", "<b>olá</b> &amp; ç"); err != nil {
		t.Fatal(err)
	}
	if m := fk.Messages[0]; m["mode"] != "HTML" || m["text"] != "<b>olá</b> &amp; ç" {
		t.Fatalf("%v", m)
	}
	// o Telegram recusa o HTML: reenvia como texto simples
	fk.ParseErr = true
	if err := tg.SendMessage(ctx, tc, "111", "<b>negrito</b> a &lt; b"); err != nil {
		t.Fatal(err)
	}
	if m := fk.Messages[1]; m["mode"] != "" || m["text"] != "negrito a < b" {
		t.Fatalf("fallback: %v", m)
	}
	if err := tg.SendDocument(ctx, tc, "111", "Relatorio.pdf", "application/pdf", []byte("%PDF-1.4"), "legenda"); err != nil || fk.Docs[0] != "111|Relatorio.pdf|8|%PDF" {
		t.Fatalf("%v %v", err, fk.Docs)
	}
	// erros: chat bloqueado e token errado (o token nunca aparece no erro)
	fk.Fail["222"] = "Forbidden: bot was blocked by the user"
	if err := tg.SendMessage(ctx, tc, "222", "x"); err == nil || !strings.Contains(err.Error(), "blocked") {
		t.Fatalf("%v", err)
	}
	bad := TelegramConfig{Token: "999999:TOKEN-ERRADO-DO-USUARIO-XYZ", ChatIDs: []string{"1"}}
	err := tg.SendMessage(ctx, bad, "1", "x")
	if err == nil || strings.Contains(err.Error(), "TOKEN-ERRADO") {
		t.Fatalf("o token não pode vazar: %v", err)
	}
	down := Telegram{Base: "http://127.0.0.1:1"}
	if err := down.SendMessage(ctx, tc, "1", "x"); err == nil || strings.Contains(err.Error(), token) {
		t.Fatalf("falha de rede sem vazar o token: %v", err)
	}
}
