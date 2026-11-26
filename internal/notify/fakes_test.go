package notify

import (
	"bufio"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime/quotedprintable"
	"net"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"sync"
	"testing"

	"github.com/AprendendoLinux/corujabackup/internal/tlsutil"
)

type smtpMsg struct {
	From  string
	Rcpts []string
	Data  string
}

// fakeSMTP implementa o suficiente do SMTP para testar: EHLO, STARTTLS, AUTH PLAIN, MAIL/RCPT/DATA, QUIT.
type fakeSMTP struct {
	ln       net.Listener
	mode     string // plain | starttls | ssl
	user     string
	pass     string
	cert     tls.Certificate
	mu       sync.Mutex
	msgs     []smtpMsg
	authSeen []string
	rejectTo string
	noTLS    bool // starttls: não anuncia STARTTLS
}

func newFakeSMTP(t *testing.T, mode, user, pass string) *fakeSMTP {
	t.Helper()
	cert, err := tlsutil.LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeSMTP{mode: mode, user: user, pass: pass, cert: cert}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if mode == "ssl" {
		ln = tls.NewListener(ln, &tls.Config{Certificates: []tls.Certificate{cert}})
	}
	f.ln = ln
	go f.serve()
	t.Cleanup(func() { ln.Close() })
	return f
}

func (f *fakeSMTP) Port() int { return f.ln.Addr().(*net.TCPAddr).Port }

func (f *fakeSMTP) Messages() []smtpMsg {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]smtpMsg(nil), f.msgs...)
}

func (f *fakeSMTP) serve() {
	for {
		c, err := f.ln.Accept()
		if err != nil {
			return
		}
		go f.handle(c)
	}
}

func (f *fakeSMTP) handle(c net.Conn) {
	defer c.Close()
	var tc *textproto.Conn
	var raw net.Conn = c
	tc = textproto.NewConn(raw)
	tc.PrintfLine("220 fake ESMTP")
	secure := f.mode == "ssl"
	var cur smtpMsg
	authed := f.user == ""
	for {
		line, err := tc.ReadLine()
		if err != nil {
			return
		}
		up := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(up, "EHLO"), strings.HasPrefix(up, "HELO"):
			tc.PrintfLine("250-fake")
			if f.mode == "starttls" && !secure && !f.noTLS {
				tc.PrintfLine("250-STARTTLS")
			}
			if f.user != "" {
				tc.PrintfLine("250-AUTH PLAIN")
			}
			tc.PrintfLine("250 8BITMIME")
		case up == "STARTTLS":
			tc.PrintfLine("220 pronto")
			tl := tls.Server(raw, &tls.Config{Certificates: []tls.Certificate{f.cert}})
			if err := tl.Handshake(); err != nil {
				return
			}
			raw, secure = tl, true
			tc = textproto.NewConn(raw)
		case strings.HasPrefix(up, "AUTH PLAIN"):
			parts := strings.Fields(line)
			dec, _ := base64.StdEncoding.DecodeString(parts[len(parts)-1])
			f.mu.Lock()
			f.authSeen = append(f.authSeen, strings.ReplaceAll(string(dec), "\x00", "|"))
			f.mu.Unlock()
			if string(dec) == "\x00"+f.user+"\x00"+f.pass {
				authed = true
				tc.PrintfLine("235 ok")
			} else {
				tc.PrintfLine("535 5.7.8 credenciais inválidas")
			}
		case strings.HasPrefix(up, "MAIL FROM"):
			if !authed {
				tc.PrintfLine("530 autenticação necessária")
				continue
			}
			cur = smtpMsg{From: between(line)}
			tc.PrintfLine("250 ok")
		case strings.HasPrefix(up, "RCPT TO"):
			to := between(line)
			if f.rejectTo != "" && to == f.rejectTo {
				tc.PrintfLine("550 5.1.1 usuário desconhecido")
				continue
			}
			cur.Rcpts = append(cur.Rcpts, to)
			tc.PrintfLine("250 ok")
		case up == "DATA":
			tc.PrintfLine("354 envie")
			var b strings.Builder
			r := bufio.NewReader(tc.DotReader())
			io.Copy(&b, r)
			cur.Data = b.String()
			f.mu.Lock()
			f.msgs = append(f.msgs, cur)
			f.mu.Unlock()
			tc.PrintfLine("250 aceito")
		case up == "QUIT":
			tc.PrintfLine("221 tchau")
			return
		default:
			tc.PrintfLine("250 ok")
		}
	}
}

func between(s string) string {
	i, j := strings.Index(s, "<"), strings.LastIndex(s, ">")
	if i < 0 || j < i {
		return ""
	}
	return s[i+1 : j]
}

// fakeTG imita a API de bots do Telegram.
type fakeTG struct {
	*httptest.Server
	mu       sync.Mutex
	Messages []map[string]string
	Docs     []string
	Fail     map[string]string // chat -> descrição do erro (HTTP 403)
	ParseErr bool              // recusa HTML com 400 "can't parse entities"
}

func newFakeTG(t *testing.T, token string) *fakeTG {
	f := &fakeTG{Fail: map[string]string{}}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/bot"+token+"/") {
			w.WriteHeader(404)
			json.NewEncoder(w).Encode(map[string]any{"ok": false, "description": "Not Found"})
			return
		}
		method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		f.mu.Lock()
		defer f.mu.Unlock()
		reply := func(code int, ok bool, desc string) {
			w.WriteHeader(code)
			json.NewEncoder(w).Encode(map[string]any{"ok": ok, "description": desc})
		}
		switch method {
		case "sendMessage":
			r.ParseForm()
			chat := r.Form.Get("chat_id")
			if d, bad := f.Fail[chat]; bad {
				reply(403, false, d)
				return
			}
			if f.ParseErr && r.Form.Get("parse_mode") == "HTML" {
				reply(400, false, "Bad Request: can't parse entities: Unsupported start tag")
				return
			}
			m := map[string]string{"chat": chat, "text": r.Form.Get("text"), "mode": r.Form.Get("parse_mode")}
			f.Messages = append(f.Messages, m)
			reply(200, true, "")
		case "sendDocument":
			r.ParseMultipartForm(32 << 20)
			chat := r.FormValue("chat_id")
			if d, bad := f.Fail[chat]; bad {
				reply(403, false, d)
				return
			}
			file, hdr, err := r.FormFile("document")
			if err != nil {
				reply(400, false, "sem documento")
				return
			}
			b, _ := io.ReadAll(file)
			f.Docs = append(f.Docs, fmt.Sprintf("%s|%s|%d|%s", chat, hdr.Filename, len(b), string(b[:min(4, len(b))])))
			reply(200, true, "")
		default:
			reply(404, false, "método desconhecido")
		}
	}))
	t.Cleanup(f.Close)
	return f
}

func decodeQP(b []byte) []byte {
	out, _ := io.ReadAll(quotedprintable.NewReader(strings.NewReader(string(b))))
	return out
}

func decodeB64(b []byte) []byte {
	out, _ := base64.StdEncoding.DecodeString(strings.ReplaceAll(strings.ReplaceAll(string(b), "\r", ""), "\n", ""))
	return out
}
