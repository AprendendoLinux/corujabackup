package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"regexp"
	"strings"
	"time"
)

type TelegramConfig struct {
	Token   string   `json:"token,omitempty"`
	ChatIDs []string `json:"chat_ids"`
}

const telegramAPI = "https://api.telegram.org"

var tagRE = regexp.MustCompile(`<[^>]+>`)

type Telegram struct {
	Base string // padrão https://api.telegram.org (testes apontam para um servidor falso)
	HTTP *http.Client
}

func (t Telegram) client() *http.Client {
	if t.HTTP != nil {
		return t.HTTP
	}
	return &http.Client{Timeout: 60 * time.Second}
}

func (t Telegram) url(token, method string) string {
	b := t.Base
	if b == "" {
		b = telegramAPI
	}
	return strings.TrimRight(b, "/") + "/bot" + token + "/" + method
}

// redact remove o token de qualquer texto de erro (URLs de requisição o contêm).
func redact(s, token string) string {
	if token != "" {
		s = strings.ReplaceAll(s, token, "***")
	}
	return s
}

func (t Telegram) do(ctx context.Context, token, method string, body io.Reader, ctype string) (int, string, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", t.url(token, method), body)
	if err != nil {
		return 0, "", errors.New(redact(err.Error(), token))
	}
	req.Header.Set("Content-Type", ctype)
	resp, err := t.client().Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			return 0, "", fmt.Errorf("não foi possível falar com o Telegram: %s", redact(ue.Err.Error(), token))
		}
		return 0, "", errors.New(redact(err.Error(), token))
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var r struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
	}
	_ = json.Unmarshal(b, &r)
	if resp.StatusCode == 200 && r.OK {
		return 200, "", nil
	}
	return resp.StatusCode, r.Description, nil
}

// SendMessage envia o texto (HTML do Telegram). Se o Telegram recusar o HTML, reenvia como texto simples.
func (t Telegram) SendMessage(ctx context.Context, cfg TelegramConfig, chat, htmlText string) error {
	send := func(text, mode string) (int, string, error) {
		v := url.Values{"chat_id": {chat}, "text": {text}, "disable_web_page_preview": {"true"}}
		if mode != "" {
			v.Set("parse_mode", mode)
		}
		return t.do(ctx, cfg.Token, "sendMessage", strings.NewReader(v.Encode()), "application/x-www-form-urlencoded")
	}
	code, desc, err := send(htmlText, "HTML")
	if err != nil {
		return err
	}
	if code == 200 {
		return nil
	}
	if code == 400 && strings.Contains(strings.ToLower(desc), "parse") {
		plain := strings.NewReplacer("&lt;", "<", "&gt;", ">", "&amp;", "&").Replace(tagRE.ReplaceAllString(htmlText, ""))
		if code, desc, err = send(plain, ""); err != nil {
			return err
		} else if code == 200 {
			return nil
		}
	}
	return fmt.Errorf("o Telegram recusou (HTTP %d): %s", code, redact(desc, cfg.Token))
}

func (t Telegram) SendDocument(ctx context.Context, cfg TelegramConfig, chat, filename, mimeType string, data []byte, caption string) error {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	_ = w.WriteField("chat_id", chat)
	if caption != "" {
		_ = w.WriteField("caption", caption)
	}
	part, err := w.CreatePart(textproto.MIMEHeader{"Content-Disposition": {fmt.Sprintf(`form-data; name="document"; filename="%s"`, filename)}, "Content-Type": {mimeType}})
	if err != nil {
		return err
	}
	_, _ = part.Write(data)
	_ = w.Close()
	code, desc, err := t.do(ctx, cfg.Token, "sendDocument", &buf, w.FormDataContentType())
	if err != nil {
		return err
	}
	if code != 200 {
		return fmt.Errorf("o Telegram recusou o arquivo (HTTP %d): %s", code, redact(desc, cfg.Token))
	}
	return nil
}
