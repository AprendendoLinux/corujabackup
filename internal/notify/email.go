// Package notify envia notificações (e-mail e Telegram) e decide quando enviá-las (regras, fila com novas tentativas,
// relatório diário e alerta de agente offline).
package notify

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"strconv"
	"strings"
	"time"
)

type EmailConfig struct {
	Host       string   `json:"host"`
	Port       int      `json:"port"`
	Security   string   `json:"security"` // starttls | ssl | none
	User       string   `json:"user"`
	Password   string   `json:"password,omitempty"`
	From       string   `json:"from"`
	FromName   string   `json:"from_name"`
	To         []string `json:"to"`
	SkipVerify bool     `json:"skip_verify"` // não validar o certificado (autoassinado)
}

type Attachment struct {
	Name string
	MIME string
	Data []byte
}

type Inline struct {
	CID  string
	MIME string
	Data []byte
}

func randID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%x", b)
}

func wrap76(b []byte) []byte {
	enc := base64.StdEncoding.EncodeToString(b)
	var out bytes.Buffer
	for len(enc) > 76 {
		out.WriteString(enc[:76] + "\r\n")
		enc = enc[76:]
	}
	out.WriteString(enc + "\r\n")
	return out.Bytes()
}

// BuildMessage monta o e-mail MIME: texto + HTML (com imagem embutida opcional) + anexos.
func BuildMessage(cfg EmailConfig, to []string, subject, text, html string, inline *Inline, atts []Attachment, now time.Time) ([]byte, error) {
	if len(to) == 0 {
		return nil, errors.New("sem destinatários")
	}
	from := mail.Address{Name: cfg.FromName, Address: cfg.From}
	var buf bytes.Buffer
	hdr := func(k, v string) { buf.WriteString(k + ": " + v + "\r\n") }
	domain := "localhost"
	if i := strings.LastIndex(cfg.From, "@"); i >= 0 {
		domain = cfg.From[i+1:]
	}
	hdr("From", from.String())
	hdr("To", strings.Join(to, ", "))
	hdr("Subject", mime.QEncoding.Encode("utf-8", subject))
	hdr("Date", now.Format(time.RFC1123Z))
	hdr("Message-ID", "<"+randID()+"@"+domain+">")
	hdr("MIME-Version", "1.0")
	hdr("X-Mailer", "Coruja Backup")

	mixed := multipart.NewWriter(&buf)
	hdr("Content-Type", fmt.Sprintf("multipart/mixed; boundary=%q", mixed.Boundary()))
	buf.WriteString("\r\n")

	// related (html + imagem inline) dentro de mixed
	var relBuf, altBuf bytes.Buffer
	related := multipart.NewWriter(&relBuf)
	alt := multipart.NewWriter(&altBuf)
	writeQP := func(w *multipart.Writer, ctype, body string) error {
		p, err := w.CreatePart(textproto.MIMEHeader{"Content-Type": {ctype + "; charset=utf-8"}, "Content-Transfer-Encoding": {"quoted-printable"}})
		if err != nil {
			return err
		}
		q := quotedprintable.NewWriter(p)
		if _, err := q.Write([]byte(strings.ReplaceAll(body, "\r\n", "\n"))); err != nil {
			return err
		}
		return q.Close()
	}
	if err := writeQP(alt, "text/plain", text); err != nil {
		return nil, err
	}
	if err := writeQP(alt, "text/html", html); err != nil {
		return nil, err
	}
	if err := alt.Close(); err != nil {
		return nil, err
	}
	mp, err := mixed.CreatePart(textproto.MIMEHeader{"Content-Type": {fmt.Sprintf("multipart/related; boundary=%q", related.Boundary())}})
	if err != nil {
		return nil, err
	}
	ap, err := related.CreatePart(textproto.MIMEHeader{"Content-Type": {fmt.Sprintf("multipart/alternative; boundary=%q", alt.Boundary())}})
	if err != nil {
		return nil, err
	}
	_, _ = ap.Write(altBuf.Bytes())
	if inline != nil {
		ip, err := related.CreatePart(textproto.MIMEHeader{"Content-Type": {inline.MIME}, "Content-Transfer-Encoding": {"base64"},
			"Content-ID": {"<" + inline.CID + ">"}, "Content-Disposition": {`inline; filename="` + inline.CID + `"`}})
		if err != nil {
			return nil, err
		}
		_, _ = ip.Write(wrap76(inline.Data))
	}
	if err := related.Close(); err != nil {
		return nil, err
	}
	_, _ = mp.Write(relBuf.Bytes())
	for _, a := range atts {
		p, err := mixed.CreatePart(textproto.MIMEHeader{"Content-Type": {a.MIME + `; name="` + a.Name + `"`}, "Content-Transfer-Encoding": {"base64"},
			"Content-Disposition": {`attachment; filename="` + a.Name + `"`}})
		if err != nil {
			return nil, err
		}
		_, _ = p.Write(wrap76(a.Data))
	}
	if err := mixed.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// plainAuth autentica sem exigir TLS (só usado quando o administrador escolheu explicitamente "sem criptografia").
type plainAuth struct{ user, pass string }

func (a plainAuth) Start(*smtp.ServerInfo) (string, []byte, error) {
	return "PLAIN", []byte("\x00" + a.user + "\x00" + a.pass), nil
}
func (a plainAuth) Next(_ []byte, more bool) ([]byte, error) {
	if more {
		return nil, errors.New("resposta inesperada do servidor SMTP")
	}
	return nil, nil
}

// SendMail entrega a mensagem. Nunca inclui a senha em mensagens de erro.
func SendMail(ctx context.Context, cfg EmailConfig, rcpts []string, msg []byte) error {
	if cfg.Host == "" {
		return errors.New("servidor SMTP não configurado")
	}
	port := cfg.Port
	if port == 0 {
		switch cfg.Security {
		case "ssl":
			port = 465
		case "none":
			port = 25
		default:
			port = 587
		}
	}
	addr := net.JoinHostPort(cfg.Host, strconv.Itoa(port))
	d := net.Dialer{Timeout: 20 * time.Second}
	tlsCfg := &tls.Config{ServerName: cfg.Host, InsecureSkipVerify: cfg.SkipVerify, MinVersion: tls.VersionTLS12} //nolint:gosec // opção explícita do administrador
	var conn net.Conn
	var err error
	if cfg.Security == "ssl" {
		conn, err = (&tls.Dialer{NetDialer: &d, Config: tlsCfg}).DialContext(ctx, "tcp", addr)
	} else {
		conn, err = d.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("não foi possível conectar ao SMTP %s: %w", addr, err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Minute))
	c, err := smtp.NewClient(conn, cfg.Host)
	if err != nil {
		return fmt.Errorf("o servidor SMTP não respondeu como esperado: %w", err)
	}
	defer c.Close()
	if cfg.Security == "starttls" || cfg.Security == "" {
		if ok, _ := c.Extension("STARTTLS"); !ok {
			return errors.New("o servidor SMTP não oferece STARTTLS (escolha 'sem criptografia' só se tiver certeza)")
		}
		if err := c.StartTLS(tlsCfg); err != nil {
			return fmt.Errorf("falha no STARTTLS: %w", err)
		}
	}
	if cfg.User != "" {
		if err := c.Auth(plainAuth{cfg.User, cfg.Password}); err != nil {
			return fmt.Errorf("autenticação SMTP recusada: %s", cleanErr(err, cfg.Password))
		}
	}
	if err := c.Mail(cfg.From); err != nil {
		return fmt.Errorf("remetente recusado: %s", cleanErr(err, cfg.Password))
	}
	for _, r := range rcpts {
		if err := c.Rcpt(r); err != nil {
			return fmt.Errorf("destinatário %s recusado: %s", r, cleanErr(err, cfg.Password))
		}
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(msg); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("o servidor SMTP não aceitou a mensagem: %s", cleanErr(err, cfg.Password))
	}
	return c.Quit()
}

func cleanErr(err error, secret string) string {
	s := err.Error()
	if secret != "" {
		s = strings.ReplaceAll(s, secret, "***")
	}
	return s
}
