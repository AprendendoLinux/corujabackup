package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/AprendendoLinux/corujabackup/internal/agentsrv"
	"github.com/AprendendoLinux/corujabackup/internal/auth"
	"github.com/AprendendoLinux/corujabackup/internal/cloud"
	"github.com/AprendendoLinux/corujabackup/internal/cloudmgr"
	"github.com/AprendendoLinux/corujabackup/internal/netdest"
	"github.com/AprendendoLinux/corujabackup/internal/pb"
	"github.com/AprendendoLinux/corujabackup/internal/ratelimit"
	"github.com/AprendendoLinux/corujabackup/internal/report"
	"github.com/AprendendoLinux/corujabackup/internal/secrets"
	"github.com/AprendendoLinux/corujabackup/internal/store"
	"github.com/AprendendoLinux/corujabackup/internal/testutil"
)

type fakePing struct{ err error }

func (f fakePing) PingContext(context.Context) error { return f.err }

type fakeHub struct {
	revoked []int64
	pushes  int
	runs    []string
	offline bool
	calls   []string
	actions []*pb.AgentAction
	fulls   []bool
	callOut []byte
	callErr error
	wakes   int
}

func (f *fakeHub) Online() map[int64]time.Time     { return map[int64]time.Time{} }
func (f *fakeHub) Revoke(id int64, _ string)       { f.revoked = append(f.revoked, id) }
func (f *fakeHub) PushJobs(context.Context, int64) { f.pushes++ }
func (f *fakeHub) RunNow(_, jobID int64, uid string, full bool) error {
	if f.offline {
		return agentsrv.ErrOffline
	}
	f.fulls = append(f.fulls, full)
	f.runs = append(f.runs, uid)
	return nil
}
func (f *fakeHub) CancelRun(int64, string) error { return nil }
func (f *fakeHub) Links() map[string]agentsrv.LinkState {
	return map[string]agentsrv.LinkState{"10.0.0.9:8443": {Address: "10.0.0.9:8443", State: "error", Error: "conexão recusada"}}
}
func (f *fakeHub) Wake() { f.wakes++ }
func (f *fakeHub) FetchDownload(_ context.Context, _ int64, a *pb.AgentAction, _ time.Duration) (io.ReadCloser, string, error) {
	if f.offline {
		return nil, "", agentsrv.ErrOffline
	}
	if strings.Contains(strings.Join(a.Paths, ","), "/nao-existe") {
		return nil, "", errors.New("nenhum dos itens escolhidos está neste backup")
	}
	name := "arquivo.txt"
	if a.Zip {
		name = "backup ção.zip"
	}
	f.actions = append(f.actions, a)
	return io.NopCloser(strings.NewReader("conteudo:" + strings.Join(a.Paths, ","))), name, nil
}

func (f *fakeHub) Action(_ int64, a *pb.AgentAction) error {
	if f.offline {
		return agentsrv.ErrOffline
	}
	f.actions = append(f.actions, a)
	return nil
}
func (f *fakeHub) Call(_ context.Context, _ int64, kind string, payload []byte, _ time.Duration) ([]byte, error) {
	f.calls = append(f.calls, kind+":"+string(payload))
	if f.callErr != nil {
		return nil, f.callErr
	}
	return f.callOut, nil
}

// ---------------------------------------------------------------- sem banco

func TestHealthEVersaoSemBanco(t *testing.T) {
	s := &Server{Pinger: fakePing{}, Started: time.Now()}
	rec := httptest.NewRecorder()
	s.Routes().ServeHTTP(rec, httptest.NewRequest("GET", "/api/health", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"status":"ok"`) {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" || rec.Header().Get("Content-Security-Policy") == "" {
		t.Fatal("faltam cabeçalhos de segurança")
	}
	s = &Server{Pinger: fakePing{errors.New("x")}, Started: time.Now()}
	rec = httptest.NewRecorder()
	s.Routes().ServeHTTP(rec, httptest.NewRequest("GET", "/api/health", nil))
	if rec.Code != 503 {
		t.Fatalf("banco caído deveria dar 503, deu %d", rec.Code)
	}
}

func TestCSRFeOrigem(t *testing.T) {
	s := &Server{Started: time.Now()}
	h := s.Routes()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/api/login", strings.NewReader(`{}`)))
	if rec.Code != 403 {
		t.Fatalf("POST sem cabeçalho CSRF deveria dar 403, deu %d", rec.Code)
	}
	req := httptest.NewRequest("POST", "/api/login", strings.NewReader(`{}`))
	req.Header.Set("X-Coruja-CSRF", "1")
	req.Header.Set("Origin", "https://evil.example")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 403 {
		t.Fatalf("origem diferente deveria dar 403, deu %d", rec.Code)
	}
}

// ---------------------------------------------------------------- com banco real

type env struct {
	t   *testing.T
	srv *httptest.Server
	api *Server
	hub *fakeHub
	now time.Time
}

func newEnv(t *testing.T) *env {
	t.Helper()
	d := testutil.DB(t)
	e := &env{t: t, hub: &fakeHub{}, now: time.Now().UTC().Truncate(time.Second)}
	box, _ := secrets.Open(t.TempDir())
	e.api = &Server{DB: d, Store: store.New(d), Box: box, Pinger: d, Agents: e.hub, Started: time.Now(), Fingerprint: strings.Repeat("ab", 32),
		LoginLimiter: ratelimit.New(5, 15*time.Minute), now: func() time.Time { return e.now }}
	e.srv = httptest.NewServer(e.api.Routes())
	t.Cleanup(e.srv.Close)
	return e
}

type client struct {
	e *env
	c *http.Client
}

func (e *env) client() *client {
	jar, _ := cookiejar.New(nil)
	return &client{e: e, c: &http.Client{Jar: jar}}
}

func (c *client) do(method, path string, body any) (int, map[string]any) {
	c.e.t.Helper()
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	req, _ := http.NewRequest(method, c.e.srv.URL+path, rd)
	req.Header.Set("X-Coruja-CSRF", "1")
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.c.Do(req)
	if err != nil {
		c.e.t.Fatal(err)
	}
	defer resp.Body.Close()
	var m map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&m)
	return resp.StatusCode, m
}

func (c *client) mustOK(method, path string, body any) map[string]any {
	c.e.t.Helper()
	code, m := c.do(method, path, body)
	if code >= 300 {
		c.e.t.Fatalf("%s %s => %d %v", method, path, code, m)
	}
	return m
}

const pw = "senha-bem-forte-1"

func setupAdmin(e *env) *client {
	c := e.client()
	c.mustOK("POST", "/api/setup", map[string]string{"username": "admin", "password": pw})
	return c
}

func TestSetupSoUmaVez(t *testing.T) {
	e := newEnv(t)
	c := e.client()
	if _, m := c.do("GET", "/api/setup/status", nil); m["needs_setup"] != true {
		t.Fatalf("deveria precisar de setup: %v", m)
	}
	if code, _ := c.do("POST", "/api/setup", map[string]string{"username": "Admin!", "password": pw}); code != 400 {
		t.Fatalf("usuário inválido deveria dar 400, deu %d", code)
	}
	if code, _ := c.do("POST", "/api/setup", map[string]string{"username": "admin", "password": "curta"}); code != 400 {
		t.Fatalf("senha fraca deveria dar 400, deu %d", code)
	}
	c.mustOK("POST", "/api/setup", map[string]string{"username": "admin", "password": pw})
	if _, m := c.do("GET", "/api/me", nil); m["user"] == nil {
		t.Fatalf("setup deveria deixar logado: %v", m)
	}
	if code, _ := e.client().do("POST", "/api/setup", map[string]string{"username": "outro", "password": pw}); code != 409 {
		t.Fatalf("segundo setup deveria dar 409, deu %d", code)
	}
	if _, m := c.do("GET", "/api/setup/status", nil); m["needs_setup"] != false {
		t.Fatalf("%v", m)
	}
}

func TestLoginErroGenericoELimite(t *testing.T) {
	e := newEnv(t)
	setupAdmin(e)
	c := e.client()
	code1, m1 := c.do("POST", "/api/login", map[string]string{"username": "admin", "password": "errada-errada"})
	code2, m2 := c.do("POST", "/api/login", map[string]string{"username": "naoexiste", "password": "errada-errada"})
	if code1 != 401 || code2 != 401 || m1["error"] != m2["error"] {
		t.Fatalf("mensagem deve ser igual para usuário existente ou não: %v %v", m1, m2)
	}
	for i := 0; i < 5; i++ {
		c.do("POST", "/api/login", map[string]string{"username": "admin", "password": "errada-errada"})
	}
	if code, _ := c.do("POST", "/api/login", map[string]string{"username": "admin", "password": pw}); code != 429 {
		t.Fatalf("após várias falhas deveria bloquear (429), deu %d", code)
	}
}

func TestSessaoLogoutEPermissoes(t *testing.T) {
	e := newEnv(t)
	adm := setupAdmin(e)
	adm.mustOK("POST", "/api/users", map[string]string{"username": "visitante", "password": pw, "role": "viewer", "display_name": "V"})
	adm.mustOK("POST", "/api/users", map[string]string{"username": "operador", "password": pw, "role": "operator"})

	v := e.client()
	v.mustOK("POST", "/api/login", map[string]string{"username": "visitante", "password": pw})
	if code, _ := v.do("GET", "/api/agents", nil); code != 200 {
		t.Fatalf("viewer deveria listar agentes, deu %d", code)
	}
	for _, rq := range []struct{ m, p string }{{"GET", "/api/users"}, {"GET", "/api/audit"}, {"POST", "/api/agents/tokens"}} {
		if code, _ := v.do(rq.m, rq.p, map[string]any{"name": "x1"}); code != 403 {
			t.Fatalf("viewer em %s %s deveria dar 403, deu %d", rq.m, rq.p, code)
		}
	}
	o := e.client()
	o.mustOK("POST", "/api/login", map[string]string{"username": "operador", "password": pw})
	if code, _ := o.do("GET", "/api/users", nil); code != 403 {
		t.Fatalf("operador não administra usuários: %d", code)
	}
	if code, _ := o.do("POST", "/api/agents/tokens", map[string]any{"name": "srv-1", "address": "10.0.0.5:8443"}); code != 201 {
		t.Fatalf("operador deveria criar token: %d", code)
	}
	adm.mustOK("POST", "/api/logout", nil)
	if code, _ := adm.do("GET", "/api/me", nil); code != 401 {
		t.Fatalf("após logout deveria dar 401, deu %d", code)
	}
	if code, _ := e.client().do("GET", "/api/agents", nil); code != 401 {
		t.Fatal("sem sessão deveria dar 401")
	}
}

func TestDoisFatoresEndToEnd(t *testing.T) {
	e := newEnv(t)
	c := setupAdmin(e)
	begin := c.mustOK("POST", "/api/me/2fa/begin", nil)
	secret := begin["secret"].(string)
	if !strings.HasPrefix(begin["uri"].(string), "otpauth://totp/") {
		t.Fatalf("uri: %v", begin["uri"])
	}
	if code, _ := c.do("POST", "/api/me/2fa/enable", map[string]string{"code": "000000"}); code != 400 {
		t.Fatalf("código errado deveria dar 400, deu %d", code)
	}
	code1, _ := auth.TOTPCode(secret, e.now)
	en := c.mustOK("POST", "/api/me/2fa/enable", map[string]string{"code": code1})
	rec := en["recovery_codes"].([]any)
	if len(rec) != 8 {
		t.Fatalf("esperava 8 códigos de recuperação, vieram %d", len(rec))
	}

	// novo login passa a exigir o segundo fator
	e.now = e.now.Add(2 * time.Minute)
	c2 := e.client()
	_, m := c2.do("POST", "/api/login", map[string]string{"username": "admin", "password": pw})
	if m["needs_2fa"] != true {
		t.Fatalf("deveria pedir 2FA: %v", m)
	}
	if code, _ := c2.do("GET", "/api/me", nil); code != 401 {
		t.Fatalf("sessão pendente não pode acessar a API: %d", code)
	}
	if code, _ := c2.do("POST", "/api/login/2fa", map[string]string{"code": "123456"}); code != 401 {
		t.Fatalf("código errado deveria dar 401, deu %d", code)
	}
	good, _ := auth.TOTPCode(secret, e.now)
	c2.mustOK("POST", "/api/login/2fa", map[string]string{"code": good})
	if code, _ := c2.do("GET", "/api/me", nil); code != 200 {
		t.Fatalf("após o 2FA deveria acessar: %d", code)
	}

	// o mesmo código não vale duas vezes (anti-replay)
	c3 := e.client()
	c3.do("POST", "/api/login", map[string]string{"username": "admin", "password": pw})
	if code, _ := c3.do("POST", "/api/login/2fa", map[string]string{"code": good}); code != 401 {
		t.Fatalf("reuso do código deveria falhar: %d", code)
	}
	// código de recuperação vale uma vez só
	rc := rec[0].(string)
	if code, _ := c3.do("POST", "/api/login/2fa", map[string]string{"code": rc}); code != 200 {
		t.Fatalf("código de recuperação deveria funcionar: %d", code)
	}
	c4 := e.client()
	c4.do("POST", "/api/login", map[string]string{"username": "admin", "password": pw})
	if code, _ := c4.do("POST", "/api/login/2fa", map[string]string{"code": rc}); code != 401 {
		t.Fatalf("código de recuperação reutilizado deveria falhar: %d", code)
	}
	// desativar exige senha + código
	e.now = e.now.Add(2 * time.Minute)
	fresh, _ := auth.TOTPCode(secret, e.now)
	if code, _ := c3.do("POST", "/api/me/2fa/disable", map[string]string{"password": "errada-errada", "code": fresh}); code != 400 {
		t.Fatalf("senha errada: %d", code)
	}
	c3.mustOK("POST", "/api/me/2fa/disable", map[string]string{"password": pw, "code": fresh})
	c5 := e.client()
	if _, m := c5.do("POST", "/api/login", map[string]string{"username": "admin", "password": pw}); m["needs_2fa"] != false {
		t.Fatalf("2FA deveria estar desligado: %v", m)
	}
}

func TestUltimoAdminProtegidoESenha(t *testing.T) {
	e := newEnv(t)
	c := setupAdmin(e)
	_, me := c.do("GET", "/api/me", nil)
	id := int(me["user"].(map[string]any)["id"].(float64))
	path := "/api/users/" + itoa(id)
	if code, _ := c.do("DELETE", path, nil); code != 409 {
		t.Fatalf("não pode excluir a si mesmo: %d", code)
	}
	if code, _ := c.do("PATCH", path, map[string]any{"role": "viewer"}); code != 409 {
		t.Fatalf("não pode rebaixar o último admin: %d", code)
	}
	if code, _ := c.do("PATCH", path, map[string]any{"disabled": true}); code != 409 {
		t.Fatalf("não pode desativar o último admin: %d", code)
	}
	if code, _ := c.do("POST", "/api/me/password", map[string]string{"current": "x", "new": "novasenha-123"}); code != 400 {
		t.Fatalf("senha atual errada: %d", code)
	}
	c.mustOK("POST", "/api/me/password", map[string]string{"current": pw, "new": "novasenha-123"})
	if code, _ := e.client().do("POST", "/api/login", map[string]string{"username": "admin", "password": pw}); code != 401 {
		t.Fatal("a senha antiga não deveria mais valer")
	}
	e.client().mustOK("POST", "/api/login", map[string]string{"username": "admin", "password": "novasenha-123"})
}

func TestTokensDeMatriculaEAgentes(t *testing.T) {
	e := newEnv(t)
	c := setupAdmin(e)
	if code, _ := c.do("POST", "/api/agents/tokens", map[string]any{"name": "a", "address": "10.0.0.9:8443"}); code != 400 {
		t.Fatalf("nome curto: %d", code)
	}
	// o endereço onde o Server conecta no agente é obrigatório e validado
	for _, bad := range []string{"", "semporta", "http://x:8443", "x:0", "x:70000", "x y:1", `x";rm -rf /:1`, "$(id):8443", "x:abc"} {
		if code, _ := c.do("POST", "/api/agents/tokens", map[string]any{"name": "umami-server2", "address": bad}); code != 400 {
			t.Errorf("endereço %q deveria ser recusado: %d", bad, code)
		}
	}
	tk := c.mustOK("POST", "/api/agents/tokens", map[string]any{"name": "umami-server2", "ttl_hours": 9999, "address": " 10.0.0.9:8443 "})
	if tk["address"] != "10.0.0.9:8443" || e.hub.wakes != 1 {
		t.Fatalf("endereço normalizado e conexão imediata: %v %d", tk["address"], e.hub.wakes)
	}
	if !strings.HasPrefix(tk["token"].(string), "cb1."+strings.Repeat("ab", 16)+".") {
		t.Fatalf("token deveria carregar a impressão digital: %v", tk["token"])
	}
	list := c.mustOK("GET", "/api/agents", nil)
	if len(list["pending_tokens"].([]any)) != 1 || strings.Contains(jsonString(list), "cb1.") {
		t.Fatalf("o token em si nunca deve ser listado: %v", list)
	}
	if p := list["pending_tokens"].([]any)[0].(map[string]any); p["address"] != "10.0.0.9:8443" || p["link"].(map[string]any)["error"] != "conexão recusada" {
		t.Fatalf("o token pendente deve mostrar o endereço e a situação da conexão: %v", p)
	}
	// agente criado direto no banco para testar renomear/revogar/excluir
	a, err := e.api.Store.Enroll(context.Background(), enrollHashOf(t, c, e), "host", "linux", "amd64", "0.0.1", "10.0.0.1", auth.SHA256Hex("cred"))
	if err != nil {
		t.Fatal(err)
	}
	if a.Address != "10.0.0.9:8443" {
		t.Fatalf("o agente herda o endereço do token: %q", a.Address)
	}
	if code, _ := c.do("POST", "/api/agents/tokens", map[string]any{"name": "umami-server2", "address": "10.0.0.8:8443"}); code != 409 {
		t.Fatalf("nome de agente existente: %d", code)
	}
	c.mustOK("PATCH", "/api/agents/"+itoa(int(a.ID)), map[string]string{"name": "umami-s2"})
	if code, _ := c.do("PATCH", "/api/agents/"+itoa(int(a.ID)), map[string]string{"address": "sem-porta"}); code != 400 {
		t.Fatalf("endereço inválido: %d", code)
	}
	c.mustOK("PATCH", "/api/agents/"+itoa(int(a.ID)), map[string]string{"address": "[2001:db8::1]:9443"})
	if g, _ := e.api.Store.AgentByID(context.Background(), a.ID); g.Address != "[2001:db8::1]:9443" || g.Name != "umami-s2" {
		t.Fatalf("endereço: %+v", g)
	}
	c.mustOK("POST", "/api/agents/"+itoa(int(a.ID))+"/revoke", nil)
	if len(e.hub.revoked) != 1 || e.hub.revoked[0] != a.ID {
		t.Fatalf("revogar deveria derrubar o canal: %v", e.hub.revoked)
	}
	l := c.mustOK("GET", "/api/agents", nil)["agents"].([]any)
	if len(l) != 1 || l[0].(map[string]any)["revoked_at"] == nil || l[0].(map[string]any)["name"] != "umami-s2" {
		t.Fatalf("%v", l)
	}
	c.mustOK("DELETE", "/api/agents/"+itoa(int(a.ID)), nil)
	if l := c.mustOK("GET", "/api/agents", nil)["agents"].([]any); len(l) != 0 {
		t.Fatalf("deveria ter sido excluído: %v", l)
	}
	aud := c.mustOK("GET", "/api/audit", nil)["entries"].([]any)
	if len(aud) < 3 {
		t.Fatalf("auditoria deveria registrar as ações: %d", len(aud))
	}
}

// enrollHashOf recupera o hash do token pendente mais recente (testes não conhecem o segredo, só o hash no banco).
func enrollHashOf(t *testing.T, c *client, e *env) string {
	t.Helper()
	var h string
	if err := e.api.DB.QueryRow("SELECT token_hash FROM enrollment_tokens ORDER BY id DESC LIMIT 1").Scan(&h); err != nil {
		t.Fatal(err)
	}
	return h
}

func jsonString(v any) string { b, _ := json.Marshal(v); return string(b) }
func itoa(i int) string       { b, _ := json.Marshal(i); return string(b) }

// ---------------------------------------------------------------- jobs e execuções

func newAgent(t *testing.T, e *env, name string) *store.Agent {
	t.Helper()
	ctx := context.Background()
	h := auth.SHA256Hex("tok-" + name)
	if err := e.api.Store.CreateEnrollToken(ctx, h, name, 0, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	a, err := e.api.Store.Enroll(ctx, h, "host", "linux", "amd64", "0.0.2", "10.0.0.1", auth.SHA256Hex("c"))
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestJobsCRUDValidacoesEPush(t *testing.T) {
	e := newEnv(t)
	c := setupAdmin(e)
	a := newAgent(t, e, "srv-1")
	good := map[string]any{"name": "Backup do etc", "agent_id": a.ID, "paths": []string{"/etc", "/srv/"}, "excludes": []string{"*.tmp"}, "cron": "10 0 * * *", "keep": 5}

	bad := func(mod func(m map[string]any)) int {
		m := map[string]any{}
		for k, v := range good {
			m[k] = v
		}
		mod(m)
		code, _ := c.do("POST", "/api/jobs", m)
		return code
	}
	for name, mod := range map[string]func(map[string]any){
		"nome vazio":          func(m map[string]any) { m["name"] = "" },
		"agente inexistente":  func(m map[string]any) { m["agent_id"] = 9999 },
		"cron inválido":       func(m map[string]any) { m["cron"] = "99 * * * *" },
		"cron @daily":         func(m map[string]any) { m["cron"] = "@daily" },
		"fuso inválido":       func(m map[string]any) { m["timezone"] = "Marte/X" },
		"caminho relativo":    func(m map[string]any) { m["paths"] = []string{"etc"} },
		"sem caminhos":        func(m map[string]any) { m["paths"] = []string{} },
		"destino com ..":      func(m map[string]any) { m["dest_subdir"] = "../fora" },
		"destino na raiz":     func(m map[string]any) { m["dest_subdir"] = "/" },
		"destino virtual":     func(m map[string]any) { m["dest_subdir"] = "/proc/x" },
		"destino sai da base": func(m map[string]any) { m["dest_subdir"] = "/srv/../etc" },
		"keep zero":           func(m map[string]any) { m["keep"] = 0 },
		"campo desconhecido":  func(m map[string]any) { m["foo"] = 1 },
	} {
		if code := bad(mod); code != 400 {
			t.Errorf("%s: esperava 400, veio %d", name, code)
		}
	}
	r := c.mustOK("POST", "/api/jobs", good)
	id := int(r["id"].(float64))
	if e.hub.pushes != 1 {
		t.Fatalf("criar job deveria enviar a agenda ao agente: %d", e.hub.pushes)
	}
	if code, _ := c.do("POST", "/api/jobs", good); code != 409 {
		t.Fatalf("nome repetido no mesmo agente: %d", code)
	}
	jobs := c.mustOK("GET", "/api/jobs", nil)["jobs"].([]any)
	j := jobs[0].(map[string]any)
	if j["dest_subdir"] != "backup-do-etc" || j["agent_name"] != "srv-1" || j["next_run_at"] == nil || j["timezone"] != "America/Sao_Paulo" {
		t.Fatalf("padrões/campos calculados inesperados: %v", j)
	}
	if ps := j["paths"].([]any); ps[1] != "/srv" {
		t.Fatalf("caminhos devem ser normalizados: %v", ps)
	}
	c.mustOK("PATCH", "/api/jobs/"+itoa(id), map[string]any{"enabled": false, "keep": 9})
	jobs = c.mustOK("GET", "/api/jobs", nil)["jobs"].([]any)
	j = jobs[0].(map[string]any)
	if j["enabled"] != false || j["keep"] != float64(9) || j["version"] != float64(2) || j["next_run_at"] != nil {
		t.Fatalf("edição não aplicada: %v", j)
	}
	if e.hub.pushes != 2 {
		t.Fatalf("editar deveria reenviar a agenda: %d", e.hub.pushes)
	}
	c.mustOK("DELETE", "/api/jobs/"+itoa(id), nil)
	if len(c.mustOK("GET", "/api/jobs", nil)["jobs"].([]any)) != 0 || e.hub.pushes != 3 {
		t.Fatal("exclusão não aplicada")
	}
}

func TestExecucaoManualRelatoriosEPermissoes(t *testing.T) {
	e := newEnv(t)
	c := setupAdmin(e)
	a := newAgent(t, e, "srv-2")
	id := int(c.mustOK("POST", "/api/jobs", map[string]any{"name": "j1", "agent_id": a.ID, "paths": []string{"/etc"}})["id"].(float64))

	c.mustOK("POST", "/api/users", map[string]string{"username": "leitor", "password": pw, "role": "viewer"})
	v := e.client()
	v.mustOK("POST", "/api/login", map[string]string{"username": "leitor", "password": pw})
	if code, _ := v.do("POST", "/api/jobs/"+itoa(id)+"/run", nil); code != 403 {
		t.Fatalf("viewer não executa backup: %d", code)
	}
	if code, _ := v.do("GET", "/api/runs", nil); code != 200 {
		t.Fatalf("viewer consulta histórico: %d", code)
	}

	e.hub.offline = true
	if code, _ := c.do("POST", "/api/jobs/"+itoa(id)+"/run", nil); code != 409 {
		t.Fatalf("agente offline deveria dar 409: %d", code)
	}
	if n := len(c.mustOK("GET", "/api/runs", nil)["runs"].([]any)); n != 0 {
		t.Fatalf("pedido que não chegou ao agente não pode ficar no histórico: %d", n)
	}
	e.hub.offline = false
	uid := c.mustOK("POST", "/api/jobs/"+itoa(id)+"/run", nil)["run_uid"].(string)
	if len(e.hub.runs) != 1 || e.hub.runs[0] != uid {
		t.Fatalf("o agente deveria receber a ordem: %v", e.hub.runs)
	}
	// o agente relata início e fim
	ctx := context.Background()
	now := time.Now().UTC()
	if err := e.api.Store.ApplyReport(ctx, store.Report{UID: uid, JobID: int64(id), AgentID: a.ID, Trigger: "manual", Started: true, StartedAt: now}); err != nil {
		t.Fatal(err)
	}
	runs := c.mustOK("GET", "/api/runs?status=running", nil)["runs"].([]any)
	if len(runs) != 1 {
		t.Fatalf("deveria haver 1 em execução: %v", runs)
	}
	if err := e.api.Store.ApplyReport(ctx, store.Report{UID: uid, JobID: int64(id), AgentID: a.ID, Final: true, Status: "ok", StartedAt: now, FinishedAt: now.Add(time.Minute),
		BytesOutput: 1234, Files: 10, SHA256: strings.Repeat("a", 64), OutputName: "j1-x.tar.gz",
		Logs: []store.RunLog{{Seq: 1, At: now, Level: "info", Text: "início"}, {Seq: 2, At: now, Level: "warn", Text: "aviso"}}}); err != nil {
		t.Fatal(err)
	}
	// relatório repetido/atrasado não reabre nem duplica
	_ = e.api.Store.ApplyReport(ctx, store.Report{UID: uid, JobID: int64(id), AgentID: a.ID, Started: true, StartedAt: now})
	_ = e.api.Store.ApplyReport(ctx, store.Report{UID: uid, JobID: int64(id), AgentID: a.ID, Final: true, Status: "failed", Logs: []store.RunLog{{Seq: 1, At: now, Text: "dup"}}})
	all := c.mustOK("GET", "/api/runs", nil)["runs"].([]any)
	if len(all) != 1 || all[0].(map[string]any)["status"] != "ok" || all[0].(map[string]any)["output_name"] != "j1-x.tar.gz" {
		t.Fatalf("execução final não pode mudar: %v", all)
	}
	d := c.mustOK("GET", "/api/runs/"+itoa(int(all[0].(map[string]any)["id"].(float64))), nil)
	if lg := d["logs"].([]any); len(lg) != 2 || lg[0].(map[string]any)["text"] != "início" {
		t.Fatalf("logs: %v", lg)
	}
	jobs := c.mustOK("GET", "/api/jobs", nil)["jobs"].([]any)
	if lr := jobs[0].(map[string]any)["last_run"].(map[string]any); lr["status"] != "ok" {
		t.Fatalf("última execução deveria aparecer no job: %v", lr)
	}
	st := c.mustOK("GET", "/api/stats", nil)["last_24h"].(map[string]any)
	if st["ok"] != float64(1) {
		t.Fatalf("estatísticas: %v", st)
	}
	// outro agente não consegue relatar jobs alheios
	b := newAgent(t, e, "srv-3")
	if err := e.api.Store.ApplyReport(ctx, store.Report{UID: "x", JobID: int64(id), AgentID: b.ID, Final: true, Status: "ok"}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("agente só relata os próprios jobs: %v", err)
	}
	if code, _ := c.do("GET", "/api/runs?status=bobagem", nil); code != 400 {
		t.Fatalf("filtro inválido: %d", code)
	}
}

// ---------------------------------------------------------------- nuvem

type fakeClouds struct{ created int }

func (f *fakeClouds) Create(_ context.Context, name, typ string, cfg cloudmgr.AccountConfig) (int64, *cloud.Device, error) {
	f.created++
	return 0, nil, errors.New("não usado")
}
func (f *fakeClouds) Reauthorize(context.Context, int64) (*cloud.Device, error) { return nil, nil }
func (f *fakeClouds) PollDevice(context.Context, int64) (string, *cloud.Device, error) {
	return "pending", &cloud.Device{UserCode: "ABCD-1234", VerificationURI: "https://x", Interval: 5}, nil
}
func (f *fakeClouds) Test(context.Context, int64) (string, cloud.Quota, error) {
	return "fulano@x.com", cloud.Quota{Total: 100, Used: 10}, nil
}
func (f *fakeClouds) Invalidate(int64) {}
func (f *fakeClouds) Folders(_ context.Context, id int64, parent string) ([]string, error) {
	return []string{"zeta", "Alfa", "com/barra", "..", "Alfa"}, nil
}
func (f *fakeClouds) BeginWeb(_ context.Context, name, typ, origin string) (string, string, error) {
	return "https://login.test/?o=" + origin, "nonce." + name, nil
}
func (f *fakeClouds) BeginWebReauth(_ context.Context, id int64, origin string) (string, string, error) {
	return "https://login.test/?re=1", "nonce.re", nil
}
func (f *fakeClouds) CreateDestination(context.Context, string, netdest.Config) (int64, error) {
	return 77, nil
}
func (f *fakeClouds) DestConfig(context.Context, int64) (*netdest.Config, error) { return nil, nil }
func (f *fakeClouds) UsesPopup(context.Context, int64) bool                      { return false }
func (f *fakeClouds) FinishWeb(_ context.Context, state, code string) (int64, error) {
	if code != "ok" {
		return 0, errors.New("código inválido")
	}
	return 42, nil
}
func (f *fakeClouds) Open(context.Context, int64, string, string) (io.ReadCloser, int64, error) {
	return io.NopCloser(strings.NewReader("conteudo-do-backup")), 18, nil
}

func addCloud(t *testing.T, e *env, name, status string) int64 {
	t.Helper()
	id, err := e.api.Store.CreateCloud(context.Background(), name, "onedrive", "v1:segredo-cifrado", "")
	if err != nil {
		t.Fatal(err)
	}
	e.api.Store.SetCloudStatus(context.Background(), id, status, "")
	return id
}

func TestNuvemPermissoesSegredosEJobs(t *testing.T) {
	e := newEnv(t)
	fc := &fakeClouds{}
	e.api.Clouds = fc
	adm := setupAdmin(e)
	a := newAgent(t, e, "srv-n")
	cid := addCloud(t, e, "OneDrive Umami", "connected")
	pend := addCloud(t, e, "Pendente", "pending")

	// a lista nunca devolve configuração nem tokens
	l := adm.mustOK("GET", "/api/clouds", nil)
	if s := jsonString(l); strings.Contains(s, "segredo-cifrado") || strings.Contains(s, "config") || strings.Contains(s, "tokens") {
		t.Fatalf("segredos não podem sair pela API: %s", s)
	}
	// operador não cria/exclui contas, mas pode testar
	adm.mustOK("POST", "/api/users", map[string]string{"username": "operador1", "password": pw, "role": "operator"})
	op := e.client()
	op.mustOK("POST", "/api/login", map[string]string{"username": "operador1", "password": pw})
	if code, _ := op.do("POST", "/api/clouds", map[string]any{"name": "x1", "type": "onedrive", "client_id": "a"}); code != 403 {
		t.Fatalf("operador não cria conta: %d", code)
	}
	if code, _ := op.do("DELETE", "/api/clouds/"+itoa(int(cid)), nil); code != 403 {
		t.Fatalf("operador não exclui conta: %d", code)
	}
	if code, _ := op.do("POST", "/api/clouds/"+itoa(int(cid))+"/test", nil); code != 200 {
		t.Fatalf("operador pode testar: %d", code)
	}
	for name, body := range map[string]map[string]any{
		"nome inválido": {"name": "", "type": "onedrive", "client_id": "a"},
		"tipo inválido": {"name": "ok1", "type": "dropbox", "client_id": "a"},
	} {
		if code, _ := adm.do("POST", "/api/clouds", body); code != 400 {
			t.Errorf("%s: esperava 400, veio %d", name, code)
		}
	}
	if code, m := adm.do("POST", "/api/clouds/"+itoa(int(pend))+"/poll", nil); code != 200 || m["status"] != "pending" || m["device"].(map[string]any)["user_code"] != "ABCD-1234" {
		t.Fatalf("poll: %d %v", code, m)
	}

	base := map[string]any{"name": "Nuvem job", "agent_id": a.ID, "paths": []string{"/etc"}}
	with := func(extra map[string]any) map[string]any {
		m := map[string]any{}
		for k, v := range base {
			m[k] = v
		}
		for k, v := range extra {
			m[k] = v
		}
		return m
	}
	for name, body := range map[string]map[string]any{
		"conta pendente":    with(map[string]any{"cloud_account_id": pend, "cloud_keep": 3}),
		"conta inexistente": with(map[string]any{"cloud_account_id": 9999, "cloud_keep": 3}),
		"sem retenção":      with(map[string]any{"cloud_account_id": cid}),
		"pasta com ..":      with(map[string]any{"cloud_account_id": cid, "cloud_keep": 3, "cloud_folder": "a/../b"}),
		"keep 0 sem nuvem":  with(map[string]any{"keep": 0}),
	} {
		if code, _ := adm.do("POST", "/api/jobs", body); code != 400 {
			t.Errorf("%s: esperava 400, veio %d", name, code)
		}
	}
	id := int(adm.mustOK("POST", "/api/jobs", with(map[string]any{"cloud_account_id": cid, "cloud_keep": 3, "cloud_keep_days": 30, "keep": 0}))["id"].(float64))
	jobs := adm.mustOK("GET", "/api/jobs", nil)["jobs"].([]any)
	j := jobs[0].(map[string]any)
	if j["cloud_folder"] != "Coruja Backup/srv-n/nuvem-job" || j["keep"] != float64(0) || j["cloud_keep"] != float64(3) || j["cloud_account_id"] != float64(cid) {
		t.Fatalf("job com nuvem: %v", j)
	}
	if code, _ := adm.do("DELETE", "/api/clouds/"+itoa(int(cid)), nil); code != 409 {
		t.Fatalf("conta em uso não pode ser excluída: %d", code)
	}
	// remover a nuvem do job (volta a exigir cópia local >= 1)
	if code, _ := adm.do("PATCH", "/api/jobs/"+itoa(id), map[string]any{"clear_cloud": true}); code != 400 {
		t.Fatalf("sem nuvem, keep=0 é inválido: %d", code)
	}
	adm.mustOK("PATCH", "/api/jobs/"+itoa(id), map[string]any{"clear_cloud": true, "keep": 3})
	adm.mustOK("DELETE", "/api/clouds/"+itoa(int(cid)), nil)
}

// ---------------------------------------------------------------- bancos de dados

func TestJobDeBancoValidacaoSenhaEEdicao(t *testing.T) {
	e := newEnv(t)
	adm := setupAdmin(e)
	a := newAgent(t, e, "srv-db")
	dbOK := map[string]any{"engine": "mysql", "mode": "docker", "container": "mysql-8", "user": "root", "password": "segredo-super", "restore_test": "full", "exclude_databases": []string{"mysql"}}
	base := func(extra map[string]any) map[string]any {
		m := map[string]any{"name": "Bancos", "agent_id": a.ID, "kind": "database", "keep": 3, "db": dbOK}
		for k, v := range extra {
			m[k] = v
		}
		return m
	}
	with := func(mod func(db map[string]any)) map[string]any {
		db := map[string]any{}
		for k, v := range dbOK {
			db[k] = v
		}
		mod(db)
		return base(map[string]any{"db": db})
	}
	for name, body := range map[string]map[string]any{
		"sem configuração":     {"name": "x1", "agent_id": a.ID, "kind": "database"},
		"motor inválido":       with(func(d map[string]any) { d["engine"] = "oracle" }),
		"modo inválido":        with(func(d map[string]any) { d["mode"] = "ssh" }),
		"sem contêiner":        with(func(d map[string]any) { d["container"] = "" }),
		"contêiner malicioso":  with(func(d map[string]any) { d["container"] = "x; rm -rf /" }),
		"rede sem host":        with(func(d map[string]any) { d["mode"] = "network"; d["host"] = "" }),
		"porta inválida":       with(func(d map[string]any) { d["mode"] = "network"; d["host"] = "10.0.0.1"; d["port"] = 70000 }),
		"banco malicioso":      with(func(d map[string]any) { d["databases"] = []string{"ok", "-rf"} }),
		"restauração inválida": with(func(d map[string]any) { d["restore_test"] = "magica" }),
		"tipo inválido":        base(map[string]any{"kind": "planilha"}),
	} {
		if code, _ := adm.do("POST", "/api/jobs", body); code != 400 {
			t.Errorf("%s: esperava 400, veio %d", name, code)
		}
	}
	id := int(adm.mustOK("POST", "/api/jobs", base(nil))["id"].(float64))

	// a senha nunca sai pela API e fica cifrada no banco
	jobs := adm.mustOK("GET", "/api/jobs", nil)["jobs"].([]any)
	raw := jsonString(jobs)
	if strings.Contains(raw, "segredo-super") {
		t.Fatalf("a senha vazou pela API: %s", raw)
	}
	j := jobs[0].(map[string]any)
	dv := j["db"].(map[string]any)
	if j["kind"] != "database" || dv["engine"] != "mysql" || dv["password_set"] != true || dv["restore_test"] != "full" || len(j["paths"].([]any)) != 0 {
		t.Fatalf("job de banco: %v", j)
	}
	var sealed string
	e.api.DB.QueryRow("SELECT db_config FROM jobs WHERE id=?", id).Scan(&sealed)
	if !strings.HasPrefix(sealed, "v1:") || strings.Contains(sealed, "segredo-super") || strings.Contains(sealed, "mysql-8") {
		t.Fatalf("a configuração deve estar cifrada: %.30s", sealed)
	}
	// a edição sem senha mantém a anterior; o tipo não muda
	adm.mustOK("PATCH", "/api/jobs/"+itoa(id), map[string]any{"db": map[string]any{"engine": "mysql", "mode": "docker", "container": "mysql-9", "user": "root", "restore_test": "full"}})
	job, _ := e.api.Store.GetJob(context.Background(), int64(id))
	cfg, err := job.OpenDB(e.api.Box)
	if err != nil || cfg.Password != "segredo-super" || cfg.Container != "mysql-9" {
		t.Fatalf("a senha deveria ser mantida: %+v %v", cfg, err)
	}
	if code, _ := adm.do("PATCH", "/api/jobs/"+itoa(id), map[string]any{"kind": "files"}); code != 400 {
		t.Fatalf("o tipo do job é imutável: %d", code)
	}
	// o agente recebe a senha (por canal TLS) — o spec é montado a partir do banco
	if cfg.Engine != "mysql" || cfg.RestoreTest != "full" || len(cfg.ExcludeDatabases) != 1 {
		t.Fatalf("%+v", cfg)
	}
}

func TestConsultasAoAgenteContainersETesteDeBanco(t *testing.T) {
	e := newEnv(t)
	adm := setupAdmin(e)
	a := newAgent(t, e, "srv-q")
	e.hub.callOut = []byte(`{"containers":[{"name":"db","image":"mysql:8","engine":"mysql"}]}`)
	r := adm.mustOK("GET", "/api/agents/"+itoa(int(a.ID))+"/containers", nil)
	if len(r["containers"].([]any)) != 1 || e.hub.calls[0] != "containers:" {
		t.Fatalf("%v %v", r, e.hub.calls)
	}
	// db-test: valida a configuração e repassa ao agente (com a senha), sem a devolver
	e.hub.callOut = []byte(`{"databases":["app"],"version":"8.0","user":"root","where":"contêiner db"}`)
	r = adm.mustOK("POST", "/api/agents/"+itoa(int(a.ID))+"/db-test", map[string]any{"db": map[string]any{"engine": "mysql", "mode": "docker", "container": "db", "password": "p4ss"}})
	if r["databases"].([]any)[0] != "app" || !strings.Contains(e.hub.calls[1], `"password":"p4ss"`) || !strings.HasPrefix(e.hub.calls[1], "db_test:") {
		t.Fatalf("%v %v", r, e.hub.calls)
	}
	if code, _ := adm.do("POST", "/api/agents/"+itoa(int(a.ID))+"/db-test", map[string]any{"db": map[string]any{"engine": "mysql", "mode": "docker", "container": ""}}); code != 400 {
		t.Fatalf("config inválida: %d", code)
	}
	e.hub.callErr = agentsrv.ErrOffline
	if code, _ := adm.do("GET", "/api/agents/"+itoa(int(a.ID))+"/containers", nil); code != 409 {
		t.Fatalf("agente offline: %d", code)
	}
	e.hub.callErr = errors.New("o Docker não está acessível")
	if code, m := adm.do("GET", "/api/agents/"+itoa(int(a.ID))+"/containers", nil); code != 422 || !strings.Contains(m["error"].(string), "Docker") {
		t.Fatalf("erro do agente deve chegar à interface: %d %v", code, m)
	}
	adm.mustOK("POST", "/api/users", map[string]string{"username": "leitor2", "password": pw, "role": "viewer"})
	v := e.client()
	v.mustOK("POST", "/api/login", map[string]string{"username": "leitor2", "password": pw})
	if code, _ := v.do("GET", "/api/agents/"+itoa(int(a.ID))+"/containers", nil); code != 403 {
		t.Fatalf("somente leitura não consulta contêineres: %d", code)
	}
}

func TestSeletorDePastasEEnderecoDosAgentes(t *testing.T) {
	e := newEnv(t)
	adm := setupAdmin(e)
	a := newAgent(t, e, "srv-b")
	browse := "/api/agents/" + itoa(int(a.ID)) + "/browse"
	e.hub.callOut = []byte(`{"path":"/etc","entries":[{"name":"nginx","dir":true}]}`)
	r := adm.mustOK("GET", browse+"?area=host&path=%2Fetc", nil)
	if len(r["entries"].([]any)) != 1 || e.hub.calls[0] != `fs_list:{"area":"host","path":"/etc"}` {
		t.Fatalf("%v %v", r, e.hub.calls)
	}
	// pasta do backup local: caminho completo do servidor, com espaços e acentos
	job := adm.mustOK("POST", "/api/jobs", map[string]any{"name": "local escolhido", "agent_id": a.ID, "cron": "0 3 * * *", "timezone": "UTC", "paths": []string{"/etc"}, "keep": 2, "dest_subdir": "/mnt/hd 2/Cópias//diárias/"})
	got := ""
	for _, x := range adm.mustOK("GET", "/api/jobs", nil)["jobs"].([]any) {
		if m := x.(map[string]any); m["id"] == job["id"] {
			got, _ = m["dest_subdir"].(string)
		}
	}
	if got != "/mnt/hd 2/Cópias/diárias" {
		t.Fatalf("pasta gravada: %q", got)
	}
	e.hub.calls = nil
	e.hub.callOut = []byte(`{"path":"/mnt","entries":[],"free":10,"total":20,"exists":true}`)
	if r = adm.mustOK("GET", browse+"?area=disk&path=%2Fmnt", nil); r["free"].(float64) != 10 || e.hub.calls[0] != `fs_list:{"area":"disk","path":"/mnt"}` {
		t.Fatalf("%v %v", r, e.hub.calls)
	}
	e.hub.callErr = errors.New("área de navegação inválida")
	if code, m := adm.do("GET", browse+"?area=disk", nil); code != 422 || !strings.Contains(m["error"].(string), "atualize o agente") {
		t.Fatalf("agente 0.0.13 precisa de mensagem clara: %d %v", code, m)
	}
	e.hub.callErr = nil
	if code, _ := adm.do("GET", browse+"?area=qualquer", nil); code != 400 {
		t.Fatalf("área inválida: %d", code)
	}
	e.hub.callErr = errors.New("chamada desconhecida: fs_list")
	if code, m := adm.do("GET", browse+"?area=host", nil); code != 422 || !strings.Contains(m["error"].(string), "atualize o agente") {
		t.Fatalf("agente antigo precisa de mensagem clara: %d %v", code, m)
	}
	e.hub.callErr = nil

	// nuvem: o Server lista e só devolve nomes utilizáveis, ordenados
	e.api.Clouds = &fakeClouds{}
	r = adm.mustOK("GET", "/api/clouds/1/folders?path=Coruja%20Backup", nil)
	if got := fmt.Sprint(r["folders"]); got != "[Alfa zeta]" {
		t.Fatalf("%s", got)
	}
	if code, _ := adm.do("GET", "/api/clouds/1/folders?path=a%2F..%2Fb", nil); code != 400 {
		t.Fatalf("'..' deveria ser recusado: %d", code)
	}

	// permissões: só leitura não navega; operador navega
	for name, role := range map[string]string{"leitor3": "viewer", "oper3": "operator"} {
		adm.mustOK("POST", "/api/users", map[string]string{"username": name, "password": pw, "role": role})
		u := e.client()
		u.mustOK("POST", "/api/login", map[string]string{"username": name, "password": pw})
		want := 200
		if role == "viewer" {
			want = 403
		}
		if code, _ := u.do("GET", browse+"?area=host", nil); code != want {
			t.Errorf("%s navegando: %d", role, code)
		}
		if code, _ := u.do("GET", "/api/clouds/1/folders", nil); code != want {
			t.Errorf("%s listando destino: %d", role, code)
		}
	}
	if code, _ := e.client().do("GET", browse+"?area=host", nil); code != 401 {
		t.Errorf("sem login: %d", code)
	}
}

// ---------------------------------------------------------------- identidade visual, transmissão ao vivo e restauração

func (c *client) raw(method, path string, body []byte, ctype string) (int, []byte, http.Header) {
	c.e.t.Helper()
	req, _ := http.NewRequest(method, c.e.srv.URL+path, bytes.NewReader(body))
	req.Header.Set("X-Coruja-CSRF", "1")
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	resp, err := c.c.Do(req)
	if err != nil {
		c.e.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b, resp.Header
}

func TestIdentidadeVisualTemasELogo(t *testing.T) {
	e := newEnv(t)
	adm := setupAdmin(e)
	pub := e.client()
	if _, m := pub.do("GET", "/api/branding", nil); m["name"] != "Coruja Backup" || m["has_logo"] != false {
		t.Fatalf("padrão: %v", m)
	}
	cores := map[string]string{"bg": "#101010", "surface": "#1a1a1a", "text": "#eeeeee", "muted": "#999999", "border": "#333333", "primary": "#0ea5e9", "primary_contrast": "#001018", "accent": "#f59e0b", "ok": "#22c55e", "danger": "#ef4444"}
	tema := func(id string, mod func(m map[string]string)) map[string]any {
		v := map[string]string{}
		for k, c := range cores {
			v[k] = c
		}
		if mod != nil {
			mod(v)
		}
		return map[string]any{"id": id, "label": "Meu tema", "vars": v}
	}
	save := func(themes []any, theme string) int {
		code, _ := adm.do("PUT", "/api/branding", map[string]any{"name": "Backup Acme", "theme": theme, "themes": themes})
		return code
	}
	// temas só entram por pacote: qualquer tema que o cliente tente criar é recusado
	for name, c := range map[string]int{
		"tema criado na tela":  save([]any{tema("acme", nil)}, "coruja"),
		"id de tema embutido":  save([]any{tema("coruja", nil)}, "coruja"),
		"id inválido":          save([]any{tema("A cme!", nil)}, "coruja"),
		"tema padrão inexiste": save([]any{}, "outro"),
	} {
		if c != 400 {
			t.Errorf("%s: esperava 400, veio %d", name, c)
		}
	}
	if code, _ := adm.do("PUT", "/api/branding", map[string]any{"name": "", "theme": "coruja", "themes": []any{}}); code != 400 {
		t.Fatalf("nome vazio: %d", code)
	}
	if save([]any{}, "floresta") != 200 {
		t.Fatal("configuração válida recusada")
	}
	_, m := pub.do("GET", "/api/branding", nil)
	if m["name"] != "Backup Acme" || m["theme"] != "floresta" || len(m["themes"].([]any)) != 0 {
		t.Fatalf("branding: %v", m)
	}
	// somente administrador altera
	adm.mustOK("POST", "/api/users", map[string]string{"username": "operador2", "password": pw, "role": "operator"})
	op := e.client()
	op.mustOK("POST", "/api/login", map[string]string{"username": "operador2", "password": pw})
	if code, _ := op.do("PUT", "/api/branding", map[string]any{"name": "x", "theme": "coruja", "themes": []any{}}); code != 403 {
		t.Fatalf("operador não altera a identidade: %d", code)
	}
	png := append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{0}, 64)...)
	if code, _, _ := op.raw("POST", "/api/branding/logo", png, "image/png"); code != 403 {
		t.Fatalf("operador não envia logo: %d", code)
	}
	// logos inválidas/inseguras
	for name, body := range map[string][]byte{
		"html disfarçado": []byte("<html><script>alert(1)</script></html>"),
		"svg com script":  []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`),
		"svg com onload":  []byte(`<svg xmlns="http://www.w3.org/2000/svg" onload="x()"></svg>`),
		"gigante":         append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{1}, 1<<20+10)...),
		"vazio":           {},
	} {
		if code, _, _ := adm.raw("POST", "/api/branding/logo", body, "image/png"); code != 400 {
			t.Errorf("%s: esperava 400, veio %d", name, code)
		}
	}
	if code, _, _ := adm.raw("POST", "/api/branding/logo", png, "text/html"); code != 200 { // o tipo enviado é ignorado: vale o conteúdo
		t.Fatalf("PNG válido recusado: %d", code)
	}
	if _, m := pub.do("GET", "/api/branding", nil); m["has_logo"] != true || m["logo_version"] == "" {
		t.Fatalf("logo deveria constar: %v", m)
	}
	code, body, h := pub.raw("GET", "/api/branding/logo", nil, "")
	if code != 200 || !bytes.Equal(body, png) || h.Get("Content-Type") != "image/png" || !strings.Contains(h.Get("Content-Security-Policy"), "sandbox") || h.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("logo servida: %d %s %v", code, h.Get("Content-Type"), h)
	}
	svg := []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 10 10"><rect width="10" height="10"/></svg>`)
	if code, _, _ := adm.raw("POST", "/api/branding/logo", svg, ""); code != 200 {
		t.Fatalf("SVG limpo recusado: %d", code)
	}
	if _, _, h := pub.raw("GET", "/api/branding/logo", nil, ""); h.Get("Content-Type") != "image/svg+xml" {
		t.Fatalf("tipo do SVG: %s", h.Get("Content-Type"))
	}
	adm.mustOK("DELETE", "/api/branding/logo", nil)
	if code, _, _ := pub.raw("GET", "/api/branding/logo", nil, ""); code != 404 {
		t.Fatalf("logo removida deveria dar 404: %d", code)
	}
}

func TestTransmissaoAoVivoDoLog(t *testing.T) {
	e := newEnv(t)
	c := setupAdmin(e)
	a := newAgent(t, e, "srv-sse")
	jid := int(c.mustOK("POST", "/api/jobs", map[string]any{"name": "j-sse", "agent_id": a.ID, "paths": []string{"/etc"}})["id"].(float64))
	ctx := context.Background()
	now := time.Now().UTC()
	st := e.api.Store
	if err := st.ApplyReport(ctx, store.Report{UID: "u-sse", JobID: int64(jid), AgentID: a.ID, Started: true, StartedAt: now, Logs: []store.RunLog{{Seq: 1, At: now, Level: "info", Text: "primeira"}}}); err != nil {
		t.Fatal(err)
	}
	run, _ := st.RunByUID(ctx, "u-sse")
	// o backup termina enquanto a transmissão está aberta
	go func() {
		time.Sleep(1500 * time.Millisecond)
		st.ApplyReport(ctx, store.Report{UID: "u-sse", JobID: int64(jid), AgentID: a.ID, Logs: []store.RunLog{{Seq: 2, At: now, Level: "warn", Text: "segunda"}}})
		time.Sleep(700 * time.Millisecond)
		st.ApplyReport(ctx, store.Report{UID: "u-sse", JobID: int64(jid), AgentID: a.ID, Final: true, Status: "ok", FinishedAt: now, Logs: []store.RunLog{{Seq: 1, At: now, Text: "primeira"}, {Seq: 2, At: now, Text: "segunda"}, {Seq: 3, At: now, Text: "fim"}}})
	}()
	req, _ := http.NewRequest("GET", e.srv.URL+"/api/runs/"+itoa(int(run.ID))+"/stream", nil)
	resp, err := c.c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("%d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	done := make(chan string, 1)
	go func() { b, _ := io.ReadAll(resp.Body); done <- string(b) }()
	select {
	case body := <-done:
		for _, want := range []string{"event: log", `"text":"primeira"`, `"text":"segunda"`, `"text":"fim"`, "event: run", `"status":"running"`, `"status":"ok"`, "event: end"} {
			if !strings.Contains(body, want) {
				t.Errorf("transmissão sem %q:\n%s", want, body)
			}
		}
		if strings.Count(body, `"text":"primeira"`) != 1 {
			t.Errorf("cada linha deve ser entregue uma vez:\n%s", body)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("a transmissão não terminou depois do fim da execução")
	}
	if code, _ := e.client().do("GET", "/api/runs/1/stream", nil); code != 401 {
		t.Fatalf("sem sessão: %d", code)
	}
}

func TestRestauracaoDownloadReenvioEExtracao(t *testing.T) {
	e := newEnv(t)
	e.api.Clouds = &fakeClouds{}
	adm := setupAdmin(e)
	a := newAgent(t, e, "srv-rs")
	cid := addCloud(t, e, "Nuvem R", "connected")
	jid := int(adm.mustOK("POST", "/api/jobs", map[string]any{"name": "j-rs", "agent_id": a.ID, "paths": []string{"/etc"}, "cloud_account_id": cid, "cloud_keep": 3, "keep": 1})["id"].(float64))
	ctx := context.Background()
	now := time.Now().UTC()
	st := e.api.Store
	// execução 1: concluída e na nuvem; execução 2: o envio falhou
	st.ApplyReport(ctx, store.Report{UID: "ok1", JobID: int64(jid), AgentID: a.ID, Final: true, Status: "ok", StartedAt: now, FinishedAt: now, OutputName: "j-rs-20261126-001000.tar.gz",
		CloudStatus: "ok", CloudPath: "Coruja Backup/srv-rs/j-rs/j-rs-20261126-001000.tar.gz", CloudVerifiedBy: "quickxor", CloudHash: "x"})
	st.ApplyReport(ctx, store.Report{UID: "falhou2", JobID: int64(jid), AgentID: a.ID, Final: true, Status: "failed", StartedAt: now, FinishedAt: now, OutputName: "j-rs-20261126-001100.tar.gz",
		CloudStatus: "failed", Error: "o backup local foi concluído e conferido, mas o envio para a nuvem falhou: x"})
	r1, _ := st.RunByUID(ctx, "ok1")
	r2, _ := st.RunByUID(ctx, "falhou2")

	// download da nuvem (repasse do fluxo) e auditoria
	code, body, h := adm.raw("GET", "/api/runs/"+itoa(int(r1.ID))+"/download", nil, "")
	if code != 200 || string(body) != "conteudo-do-backup" || !strings.Contains(h.Get("Content-Disposition"), "j-rs-20261126-001000.tar.gz") {
		t.Fatalf("download: %d %q %v", code, body, h)
	}
	if code, _, _ := adm.raw("GET", "/api/runs/"+itoa(int(r2.ID))+"/download", nil, ""); code != 409 {
		t.Fatalf("sem cópia na nuvem: %d", code)
	}
	// reenvio: só para quem não está na nuvem
	if code, _ := adm.do("POST", "/api/runs/"+itoa(int(r1.ID))+"/reupload", nil); code != 409 {
		t.Fatalf("já está na nuvem: %d", code)
	}
	adm.mustOK("POST", "/api/runs/"+itoa(int(r2.ID))+"/reupload", nil)
	if len(e.hub.actions) != 1 || e.hub.actions[0].Kind != "reupload" || e.hub.actions[0].FileName != "j-rs-20261126-001100.tar.gz" || e.hub.actions[0].RunUid != "falhou2" {
		t.Fatalf("ação enviada ao agente: %v", e.hub.actions)
	}
	// o agente informa o resultado: o log recebe linhas numeradas a partir de 10000 e a execução é corrigida
	err := st.ApplyActionLog(ctx, a.ID, store.ActionResult{RunUID: "falhou2", Kind: "reupload", State: "ok", CloudStatus: "ok", CloudPath: "A/B/j-rs-20261126-001100.tar.gz", CloudHash: "h", CloudVerifiedBy: "md5",
		Lines: []store.RunLog{{At: now, Level: "info", Text: "reenviando"}, {At: now, Level: "info", Text: "ok"}}})
	if err != nil {
		t.Fatal(err)
	}
	fixed, _ := st.RunByUID(ctx, "falhou2")
	logs, _ := st.RunLogs(ctx, fixed.ID)
	if fixed.Status != "ok" || fixed.CloudStatus != "ok" || fixed.CloudVerifiedBy != "md5" || fixed.Error != "" || len(logs) != 2 || logs[0].Seq != 10000 || logs[1].Seq != 10001 {
		t.Fatalf("reenvio aplicado: %+v %v", fixed, logs)
	}
	b := newAgent(t, e, "srv-outro")
	if err := st.ApplyActionLog(ctx, b.ID, store.ActionResult{RunUID: "falhou2", Kind: "reupload", State: "ok"}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("um agente só atua nas próprias execuções: %v", err)
	}
	// extração: valida o nome da pasta
	for _, bad := range []string{"../x", "a/b", "-x", strings.Repeat("a", 70)} {
		if code, _ := adm.do("POST", "/api/runs/"+itoa(int(r1.ID))+"/extract", map[string]any{"target": bad}); code != 400 {
			t.Errorf("pasta %q: esperava 400, veio %d", bad, code)
		}
	}
	m := adm.mustOK("POST", "/api/runs/"+itoa(int(r1.ID))+"/extract", map[string]any{"target": "restaurar-1"})
	if m["target"] != "restaurar-1" || e.hub.actions[1].Kind != "extract" || e.hub.actions[1].Target != "restaurar-1" {
		t.Fatalf("%v %v", m, e.hub.actions)
	}
	e.hub.offline = true
	if code, _ := adm.do("POST", "/api/runs/"+itoa(int(r1.ID))+"/extract", map[string]any{}); code != 409 {
		t.Fatalf("agente offline: %d", code)
	}
	// somente leitura não baixa nem restaura
	adm.mustOK("POST", "/api/users", map[string]string{"username": "leitor3", "password": pw, "role": "viewer"})
	v := e.client()
	v.mustOK("POST", "/api/login", map[string]string{"username": "leitor3", "password": pw})
	if code, _, _ := v.raw("GET", "/api/runs/"+itoa(int(r1.ID))+"/download", nil, ""); code != 403 {
		t.Fatalf("viewer não baixa: %d", code)
	}
}

// ---------------------------------------------------------------- notificações

type fakeNotifier struct {
	tested []int64
	queued []int64
	fail   error
}

func (f *fakeNotifier) SendTest(_ context.Context, id int64) error {
	f.tested = append(f.tested, id)
	return f.fail
}
func (f *fakeNotifier) QueueRuleNow(_ context.Context, id int64) error {
	f.queued = append(f.queued, id)
	return nil
}
func (f *fakeNotifier) Preview(context.Context) (*report.Daily, []byte, error) {
	return &report.Daily{Generated: time.Date(2026, 11, 26, 7, 0, 0, 0, time.UTC)}, []byte("%PDF-1.4 falso"), nil
}

func TestCanaisDeNotificacaoValidacaoSegredosETeste(t *testing.T) {
	e := newEnv(t)
	fn := &fakeNotifier{}
	e.api.Notifier = fn
	adm := setupAdmin(e)
	email := map[string]any{"host": "smtp.exemplo.com", "port": 587, "security": "starttls", "user": "u", "password": "senha-smtp-secreta", "from": "backup@exemplo.com", "from_name": "Backup", "to": []string{"a@x.com", "Fulana <b@x.com>"}}
	ok := func(extra map[string]any) map[string]any {
		m := map[string]any{"name": "SMTP principal", "type": "email", "email": email}
		for k, v := range extra {
			m[k] = v
		}
		return m
	}
	mut := func(f func(m map[string]any)) map[string]any {
		cp := map[string]any{}
		for k, v := range email {
			cp[k] = v
		}
		f(cp)
		return ok(map[string]any{"email": cp})
	}
	for name, body := range map[string]map[string]any{
		"host com espaço":     mut(func(m map[string]any) { m["host"] = "a b" }),
		"porta inválida":      mut(func(m map[string]any) { m["port"] = 70000 }),
		"segurança inválida":  mut(func(m map[string]any) { m["security"] = "ssl3" }),
		"remetente inválido":  mut(func(m map[string]any) { m["from"] = "sem-arroba" }),
		"sem destinatários":   mut(func(m map[string]any) { m["to"] = []string{} }),
		"destinatário ruim":   mut(func(m map[string]any) { m["to"] = []string{"nao-e-email"} }),
		"tipo inválido":       ok(map[string]any{"type": "sms"}),
		"nome vazio":          ok(map[string]any{"name": ""}),
		"telegram token ruim": {"name": "TG1", "type": "telegram", "telegram": map[string]any{"token": "abc", "chat_ids": []string{"123"}}},
		"telegram chat ruim":  {"name": "TG2", "type": "telegram", "telegram": map[string]any{"token": "123456:ABCDEFGHIJKLMNOPQRSTUVWX", "chat_ids": []string{"abc def"}}},
	} {
		if code, _ := adm.do("POST", "/api/notifications/channels", body); code != 400 {
			t.Errorf("%s: esperava 400, veio %d", name, code)
		}
	}
	id := int(adm.mustOK("POST", "/api/notifications/channels", ok(nil))["id"].(float64))
	tgID := int(adm.mustOK("POST", "/api/notifications/channels", map[string]any{"name": "Telegram", "type": "telegram", "telegram": map[string]any{"token": "123456:ABCDEFGHIJKLMNOPQRSTUVWX", "chat_ids": []string{"87646403", "-1003931742209", "@meucanal"}}})["id"].(float64))
	if code, _ := adm.do("POST", "/api/notifications/channels", ok(nil)); code != 409 {
		t.Fatalf("nome repetido: %d", code)
	}
	// nenhum segredo sai pela API; ficam cifrados no banco
	raw := jsonString(adm.mustOK("GET", "/api/notifications/channels", nil))
	if strings.Contains(raw, "senha-smtp-secreta") || strings.Contains(raw, "ABCDEFGHIJKLMNOPQRSTUVWX") || !strings.Contains(raw, `"password_set":true`) || !strings.Contains(raw, `"token_set":true`) || !strings.Contains(raw, "b@x.com") {
		t.Fatalf("segredos/forma da lista: %s", raw)
	}
	var cfg string
	e.api.DB.QueryRow("SELECT config FROM notification_channels WHERE id=?", id).Scan(&cfg)
	if !strings.HasPrefix(cfg, "v1:") || strings.Contains(cfg, "smtp.exemplo.com") {
		t.Fatalf("configuração deve estar cifrada: %.20s", cfg)
	}
	// editar sem a senha mantém a atual
	upd := map[string]any{}
	for k, v := range email {
		upd[k] = v
	}
	delete(upd, "password")
	upd["host"] = "smtp2.exemplo.com"
	adm.mustOK("PATCH", "/api/notifications/channels/"+itoa(id), map[string]any{"email": upd})
	ch, _ := e.api.Store.GetChannel(context.Background(), int64(id))
	plain, _ := e.api.Box.Open(ch.Config)
	if !strings.Contains(plain, "senha-smtp-secreta") || !strings.Contains(plain, "smtp2.exemplo.com") {
		t.Fatalf("a senha deveria ser mantida: %s", plain)
	}
	// teste de envio e permissões
	if r := adm.mustOK("POST", "/api/notifications/channels/"+itoa(id)+"/test", nil); r["ok"] != true || len(fn.tested) != 1 {
		t.Fatalf("%v %v", r, fn.tested)
	}
	fn.fail = errors.New("autenticação SMTP recusada")
	if r := adm.mustOK("POST", "/api/notifications/channels/"+itoa(id)+"/test", nil); r["ok"] != false || !strings.Contains(r["error"].(string), "autenticação") {
		t.Fatalf("erro do teste deve chegar à interface: %v", r)
	}
	adm.mustOK("POST", "/api/users", map[string]string{"username": "operador3", "password": pw, "role": "operator"})
	op := e.client()
	op.mustOK("POST", "/api/login", map[string]string{"username": "operador3", "password": pw})
	for _, rq := range []struct{ m, p string }{{"GET", "/api/notifications/channels"}, {"POST", "/api/notifications/channels/1/test"}, {"GET", "/api/notifications/log"}} {
		if code, _ := op.do(rq.m, rq.p, nil); code != 403 {
			t.Errorf("operador em %s %s deveria dar 403, deu %d", rq.m, rq.p, code)
		}
	}
	adm.mustOK("DELETE", "/api/notifications/channels/"+itoa(tgID), nil)
}

func TestRegrasDeNotificacaoRelatorioEConfiguracao(t *testing.T) {
	e := newEnv(t)
	fn := &fakeNotifier{}
	e.api.Notifier = fn
	adm := setupAdmin(e)
	a := newAgent(t, e, "srv-rn")
	jid := int(adm.mustOK("POST", "/api/jobs", map[string]any{"name": "j-rn", "agent_id": a.ID, "paths": []string{"/etc"}})["id"].(float64))
	chID := int(adm.mustOK("POST", "/api/notifications/channels", map[string]any{"name": "TG", "type": "telegram", "telegram": map[string]any{"token": "123456:ABCDEFGHIJKLMNOPQRSTUVWX", "chat_ids": []string{"87646403"}}})["id"].(float64))
	rule := func(extra map[string]any) map[string]any {
		m := map[string]any{"name": "regra", "event": "run_failed", "channel_ids": []int{chID}}
		for k, v := range extra {
			m[k] = v
		}
		return m
	}
	for name, body := range map[string]map[string]any{
		"evento inválido":   rule(map[string]any{"event": "chuva"}),
		"sem canal":         rule(map[string]any{"channel_ids": []int{}}),
		"canal inexistente": rule(map[string]any{"channel_ids": []int{9999}}),
		"job inexistente":   rule(map[string]any{"job_ids": []int{9999}}),
		"hora inválida":     rule(map[string]any{"event": "daily_report", "params": map[string]any{"time": "25:00"}}),
		"fuso inválido":     rule(map[string]any{"event": "daily_report", "params": map[string]any{"time": "07:00", "timezone": "Marte/X"}}),
		"offline inválido":  rule(map[string]any{"event": "agent_offline", "params": map[string]any{"offline_minutes": 5000}}),
		"nome vazio":        rule(map[string]any{"name": ""}),
	} {
		if code, _ := adm.do("POST", "/api/notifications/rules", body); code != 400 {
			t.Errorf("%s: esperava 400, veio %d", name, code)
		}
	}
	adm.mustOK("POST", "/api/notifications/rules", rule(map[string]any{"name": "falha do job", "job_ids": []int{jid}}))
	did := int(adm.mustOK("POST", "/api/notifications/rules", rule(map[string]any{"name": "diário", "event": "daily_report", "job_ids": []int{jid}, "params": map[string]any{"attach_pdf": true}}))["id"].(float64))
	adm.mustOK("POST", "/api/notifications/rules", rule(map[string]any{"name": "offline", "event": "agent_offline"}))
	rs := adm.mustOK("GET", "/api/notifications/rules", nil)["rules"].([]any)
	byName := map[string]map[string]any{}
	for _, x := range rs {
		m := x.(map[string]any)
		byName[m["name"].(string)] = m
	}
	d := byName["diário"]["params"].(map[string]any)
	if d["time"] != "07:00" || d["timezone"] != "America/Sao_Paulo" || d["attach_pdf"] != true || len(byName["diário"]["job_ids"].([]any)) != 0 {
		t.Fatalf("padrões do relatório diário: %v", byName["diário"])
	}
	if byName["offline"]["params"].(map[string]any)["offline_minutes"] != float64(15) || len(byName["falha do job"]["job_ids"].([]any)) != 1 {
		t.Fatalf("regras: %v", byName)
	}
	adm.mustOK("PATCH", "/api/notifications/rules/"+itoa(did), rule(map[string]any{"name": "diário", "event": "daily_report", "params": map[string]any{"time": "06:30", "only_problems": true}}))
	adm.mustOK("POST", "/api/notifications/rules/"+itoa(did)+"/send-now", nil)
	if len(fn.queued) != 1 || fn.queued[0] != int64(did) {
		t.Fatalf("enviar agora: %v", fn.queued)
	}
	// PDF do relatório: operador baixa, somente leitura não
	code, body, h := adm.raw("GET", "/api/reports/daily.pdf", nil, "")
	if code != 200 || !strings.HasPrefix(string(body), "%PDF") || h.Get("Content-Type") != "application/pdf" || !strings.Contains(h.Get("Content-Disposition"), "2026-11-26") {
		t.Fatalf("pdf: %d %q %v", code, body, h)
	}
	adm.mustOK("POST", "/api/users", map[string]string{"username": "leitor4", "password": pw, "role": "viewer"})
	v := e.client()
	v.mustOK("POST", "/api/login", map[string]string{"username": "leitor4", "password": pw})
	if code, _, _ := v.raw("GET", "/api/reports/daily.pdf", nil, ""); code != 403 {
		t.Fatalf("viewer não gera relatório: %d", code)
	}
	// histórico e endereço público
	if l := adm.mustOK("GET", "/api/notifications/log", nil); l["notifications"] == nil {
		t.Fatalf("%v", l)
	}
	for _, bad := range []string{"ftp://x.com", "javascript:alert(1)", "sem-esquema.com", "http://"} {
		if code, _ := adm.do("PUT", "/api/notifications/settings", map[string]string{"base_url": bad}); code != 400 {
			t.Errorf("%q: esperava 400, veio %d", bad, code)
		}
	}
	adm.mustOK("PUT", "/api/notifications/settings", map[string]string{"base_url": "https://backup.exemplo.com/"})
	if m := adm.mustOK("GET", "/api/notifications/settings", nil); m["base_url"] != "https://backup.exemplo.com" {
		t.Fatalf("%v", m)
	}
	adm.mustOK("DELETE", "/api/notifications/rules/"+itoa(did), nil)
}

// ---------------------------------------------------------------- endurecimento (Fase 7)

func TestSetupExigeOCodigoDaExecucao(t *testing.T) {
	e := newEnv(t)
	e.api.SetupCode = "codigo-do-log-123"
	c := e.client()
	if _, m := c.do("GET", "/api/setup/status", nil); m["needs_setup"] != true || m["needs_code"] != true {
		t.Fatalf("%v", m)
	}
	body := func(code string) map[string]string {
		return map[string]string{"username": "admin", "password": pw, "setup_code": code}
	}
	if code, _ := c.do("POST", "/api/setup", body("")); code != 403 {
		t.Fatalf("sem código: %d", code)
	}
	if code, _ := c.do("POST", "/api/setup", body("errado")); code != 403 {
		t.Fatalf("código errado: %d", code)
	}
	// tentativas demais bloqueiam (impede adivinhar o código)
	for i := 0; i < 6; i++ {
		c.do("POST", "/api/setup", body("chute"+itoa(i)))
	}
	if code, _ := c.do("POST", "/api/setup", body("codigo-do-log-123")); code != 429 {
		t.Fatalf("após várias falhas deveria bloquear: %d", code)
	}
	e.api.LoginLimiter.Reset("setup:127.0.0.1")
	if code, _ := c.do("POST", "/api/setup", body("codigo-do-log-123")); code != 200 {
		t.Fatalf("código certo: %d", code)
	}
}

func TestCabecalhosDeSegurancaEIPDoProxy(t *testing.T) {
	e := newEnv(t)
	_, _, h := e.client().raw("GET", "/api/version", nil, "")
	for _, k := range []string{"Content-Security-Policy", "Permissions-Policy", "Cross-Origin-Opener-Policy", "X-Frame-Options", "X-Content-Type-Options"} {
		if h.Get(k) == "" {
			t.Errorf("falta o cabeçalho %s", k)
		}
	}
	if h.Get("Strict-Transport-Security") != "" {
		t.Error("HSTS só em HTTPS")
	}
	// atrás de proxy confiável vale o ÚLTIMO endereço de X-Forwarded-For (o acrescentado pelo proxy), não o forjado pelo cliente
	e.api.TrustProxy = true
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "10.0.0.2:1234"
	req.Header.Set("X-Forwarded-For", "6.6.6.6, 203.0.113.7")
	req.Header.Set("X-Forwarded-Proto", "https")
	if ip := e.api.clientIP(req); ip != "203.0.113.7" {
		t.Fatalf("IP do cliente: %s", ip)
	}
	rec := httptest.NewRecorder()
	e.api.Routes().ServeHTTP(rec, httptest.NewRequest("GET", "/api/version", nil))
	req2 := httptest.NewRequest("GET", "/api/version", nil)
	req2.Header.Set("X-Forwarded-Proto", "https")
	rec2 := httptest.NewRecorder()
	e.api.Routes().ServeHTTP(rec2, req2)
	if rec2.Header().Get("Strict-Transport-Security") == "" {
		t.Error("atrás de proxy HTTPS, deve enviar HSTS")
	}
	req.Header.Set("X-Forwarded-For", "lixo, não-é-ip")
	if ip := e.api.clientIP(req); ip != "10.0.0.2" {
		t.Fatalf("valor inválido cai para o endereço da conexão: %s", ip)
	}
	e.api.TrustProxy = false
	req.Header.Set("X-Forwarded-For", "6.6.6.6")
	if ip := e.api.clientIP(req); ip != "10.0.0.2" {
		t.Fatalf("sem proxy confiável o cabeçalho é ignorado: %s", ip)
	}
}

func TestPoliticaDe2FASessoesETempoDaSessao(t *testing.T) {
	e := newEnv(t)
	adm := setupAdmin(e)
	adm.mustOK("POST", "/api/users", map[string]string{"username": "operador9", "password": pw, "role": "operator"})
	op := e.client()
	op.mustOK("POST", "/api/login", map[string]string{"username": "operador9", "password": pw})

	// não dá para exigir 2FA sem ter o próprio (trancaria o administrador para fora)
	if code, _ := adm.do("PUT", "/api/security", map[string]any{"require_2fa": "admins", "session_hours": 12}); code != 409 {
		t.Fatalf("exigir 2FA sem ter o próprio: %d", code)
	}
	for name, body := range map[string]map[string]any{
		"política inválida": {"require_2fa": "talvez", "session_hours": 12},
		"sessão curta":      {"require_2fa": "none", "session_hours": 0},
		"sessão longa":      {"require_2fa": "none", "session_hours": 1000},
	} {
		if code, _ := adm.do("PUT", "/api/security", body); code != 400 {
			t.Errorf("%s: %d", name, code)
		}
	}
	// o administrador ativa o 2FA e então exige para todos
	sec := adm.mustOK("POST", "/api/me/2fa/begin", nil)["secret"].(string)
	code, _ := auth.TOTPCode(sec, e.now)
	adm.mustOK("POST", "/api/me/2fa/enable", map[string]string{"code": code})
	adm.mustOK("PUT", "/api/security", map[string]any{"require_2fa": "all", "session_hours": 2})
	e.api.invalidatePolicy()

	// operador sem 2FA: só acessa a própria conta até ativar
	if c, m := op.do("GET", "/api/agents", nil); c != 403 || m["code"] != "2fa_required" {
		t.Fatalf("o operador deveria ser barrado: %d %v", c, m)
	}
	if c, _ := op.do("GET", "/api/me", nil); c != 200 {
		t.Fatalf("a própria conta continua acessível: %d", c)
	}
	osec := op.mustOK("POST", "/api/me/2fa/begin", nil)["secret"].(string)
	ocode, _ := auth.TOTPCode(osec, e.now)
	op.mustOK("POST", "/api/me/2fa/enable", map[string]string{"code": ocode})
	if c, _ := op.do("GET", "/api/agents", nil); c != 200 {
		t.Fatalf("depois de ativar o 2FA o acesso volta: %d", c)
	}
	// a duração da sessão vem da política
	var exp time.Time
	e.api.DB.QueryRow("SELECT MAX(s.expires_at) FROM sessions s JOIN users u ON u.id=s.user_id WHERE u.username='operador9' AND s.pending_2fa=0").Scan(&exp)
	if d := time.Until(exp); d > 2*time.Hour+time.Minute {
		t.Fatalf("a sessão deveria durar no máximo 2 h: %v", d)
	}

	// sessões: listar, encerrar outra, não encerrar a atual
	second := e.client()
	second.mustOK("POST", "/api/login", map[string]string{"username": "operador9", "password": pw}) // pede 2FA; a pendente não é listada
	list := op.mustOK("GET", "/api/me/sessions", nil)["sessions"].([]any)
	if len(list) != 1 || list[0].(map[string]any)["current"] != true {
		t.Fatalf("só a sessão completa aparece: %v", list)
	}
	cur := list[0].(map[string]any)["id"].(string)
	if c, _ := op.do("DELETE", "/api/me/sessions/"+cur, nil); c != 409 {
		t.Fatalf("a sessão atual não se encerra por aqui: %d", c)
	}
	if c, _ := op.do("DELETE", "/api/me/sessions/inexistente", nil); c != 404 {
		t.Fatalf("%d", c)
	}
	// outro usuário não encerra a sessão alheia
	if c, _ := adm.do("DELETE", "/api/me/sessions/"+cur, nil); c != 404 {
		t.Fatalf("sessão alheia: %d", c)
	}
	adm.mustOK("POST", "/api/me/sessions/revoke-others", nil)
	if c, _ := op.do("GET", "/api/me", nil); c != 200 {
		t.Fatal("o operador não foi afetado pelo admin")
	}
}

func TestMetricasPrometheusComToken(t *testing.T) {
	e := newEnv(t)
	adm := setupAdmin(e)
	pub := e.client()
	if c, _, _ := pub.raw("GET", "/metrics", nil, ""); c != 404 {
		t.Fatalf("sem token configurado o endereço não deve existir: %d", c)
	}
	a := newAgent(t, e, "srv-m")
	jid := int(adm.mustOK("POST", "/api/jobs", map[string]any{"name": "Job (a&b)", "agent_id": a.ID, "paths": []string{"/etc"}})["id"].(float64))
	now := time.Now().UTC()
	e.api.Store.ApplyReport(context.Background(), store.Report{UID: "m1", JobID: int64(jid), AgentID: a.ID, Final: true, Status: "ok", StartedAt: now, FinishedAt: now, BytesOutput: 1234, OutputName: "x"})
	cid := addCloud(t, e, "Nuvem M", "connected")
	e.api.Store.SetCloudQuota(context.Background(), cid, 1000, 250)

	tok := adm.mustOK("POST", "/api/security/metrics-token", nil)["token"].(string)
	if !strings.HasPrefix(tok, "cbm_") {
		t.Fatal(tok)
	}
	var stored string
	e.api.DB.QueryRow("SELECT v FROM settings WHERE k='security.metrics_hash'").Scan(&stored)
	if stored == "" || strings.Contains(stored, tok) {
		t.Fatal("o Server guarda só o hash do token")
	}
	e.api.invalidatePolicy()
	if c, _, _ := pub.raw("GET", "/metrics", nil, ""); c != 401 {
		t.Fatalf("sem token: %d", c)
	}
	req, _ := http.NewRequest("GET", e.srv.URL+"/metrics", nil)
	req.Header.Set("Authorization", "Bearer errado")
	if resp, _ := http.DefaultClient.Do(req); resp.StatusCode != 401 {
		t.Fatalf("token errado: %d", resp.StatusCode)
	}
	req, _ = http.NewRequest("GET", e.srv.URL+"/metrics", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("%v", err)
	}
	b, _ := io.ReadAll(resp.Body)
	body := string(b)
	for _, want := range []string{`coruja_info{version=`, `coruja_agents{state="offline"} 1`, `coruja_job_last_run_ok{job="Job (a&b)",agent="srv-m",status="ok"} 1`,
		`coruja_job_last_run_bytes{job="Job (a&b)",agent="srv-m"} 1234`, `coruja_cloud_quota_bytes{account="Nuvem M",kind="used"} 250`, `coruja_cloud_up{account="Nuvem M",type="onedrive"} 1`, `coruja_runs_24h{status="ok"} 1`} {
		if !strings.Contains(body, want) {
			t.Errorf("métricas sem %q", want)
		}
	}
	adm.mustOK("DELETE", "/api/security/metrics-token", nil)
	e.api.invalidatePolicy()
	req.Header.Set("Authorization", "Bearer "+tok)
	if resp, _ := http.DefaultClient.Do(req); resp.StatusCode != 404 {
		t.Fatalf("token revogado: %d", resp.StatusCode)
	}
}

func TestAuditoriaCSVNeutralizaFormulas(t *testing.T) {
	e := newEnv(t)
	adm := setupAdmin(e)
	e.api.Store.Audit(context.Background(), 0, "=cmd|calc", "teste", "+1+1", "1.2.3.4")
	code, body, h := adm.raw("GET", "/api/audit.csv", nil, "")
	if code != 200 || !strings.HasPrefix(h.Get("Content-Type"), "text/csv") {
		t.Fatalf("%d %v", code, h)
	}
	csv := string(body)
	if !strings.Contains(csv, "quando_utc,usuario,acao,detalhe,ip") || !strings.Contains(csv, "'=cmd|calc") || !strings.Contains(csv, "'+1+1") || strings.Contains(csv, ",=cmd") {
		t.Fatalf("fórmulas devem ser neutralizadas:\n%s", csv)
	}
	op := e.client()
	adm.mustOK("POST", "/api/users", map[string]string{"username": "operador8", "password": pw, "role": "operator"})
	op.mustOK("POST", "/api/login", map[string]string{"username": "operador8", "password": pw})
	if c, _, _ := op.raw("GET", "/api/audit.csv", nil, ""); c != 403 {
		t.Fatalf("operador não exporta a auditoria: %d", c)
	}
}

func TestEscapeDeRotulosDasMetricas(t *testing.T) {
	if got := esc("a\"b\\c\nd"); got != `a\"b\\c\nd` {
		t.Fatalf("%q", got)
	}
}

func TestNomeESobrenome(t *testing.T) {
	e := newEnv(t)
	adm := setupAdmin(e)
	adm.mustOK("POST", "/api/users", map[string]any{"username": "maria", "password": pw, "role": "viewer", "first_name": "  Maria ", "last_name": "da   Silva"})
	adm.mustOK("POST", "/api/users", map[string]any{"username": "antigo", "password": pw, "role": "viewer", "display_name": "João Pedro Souza"})
	users := adm.mustOK("GET", "/api/users", nil)["users"].([]any)
	got := map[string][3]string{}
	for _, x := range users {
		u := x.(map[string]any)
		got[u["username"].(string)] = [3]string{u["first_name"].(string), u["last_name"].(string), u["display_name"].(string)}
	}
	if got["maria"] != [3]string{"Maria", "da Silva", "Maria da Silva"} || got["antigo"] != [3]string{"João", "Pedro Souza", "João Pedro Souza"} {
		t.Fatalf("nomes: %v", got)
	}

	m := e.client()
	m.mustOK("POST", "/api/login", map[string]string{"username": "maria", "password": pw})
	m.mustOK("PATCH", "/api/me", map[string]any{"first_name": "Maria Clara", "last_name": "Souza"})
	me := m.mustOK("GET", "/api/me", nil)["user"].(map[string]any)
	if me["display_name"] != "Maria Clara Souza" || me["first_name"] != "Maria Clara" || me["last_name"] != "Souza" {
		t.Fatalf("depois de trocar: %v", me)
	}
	if code, _ := m.do("PATCH", "/api/me", map[string]any{"first_name": " "}); code != 400 {
		t.Fatalf("nome vazio deveria ser recusado: %d", code)
	}
	if code, _ := m.do("PATCH", "/api/me", map[string]any{"first_name": strings.Repeat("a", 61)}); code != 400 {
		t.Fatalf("nome longo demais deveria ser recusado: %d", code)
	}
	if code, _ := m.do("PATCH", "/api/me", map[string]any{"role": "admin"}); code != 400 {
		t.Fatalf("PATCH /api/me não muda perfil: %d", code)
	}
}

func TestPainel(t *testing.T) {
	e := newEnv(t)
	adm := setupAdmin(e)
	for _, days := range []string{"7", "30", "x"} {
		m := adm.mustOK("GET", "/api/dashboard?days="+days+"&tz=America/Sao_Paulo", nil)
		want := map[string]int{"7": 7, "30": 30, "x": 14}[days]
		if h, _ := m["history"].([]any); len(h) != want || m["totals"] == nil || m["previous"] == nil {
			t.Fatalf("days=%s: %v", days, m)
		}
	}
	if code, _ := adm.do("GET", "/api/dashboard?tz=Lugar/Nenhum", nil); code != 200 {
		t.Fatalf("fuso inválido deveria cair no padrão: %d", code)
	}
}

func TestPeriodoEscolhido(t *testing.T) {
	e := newEnv(t)
	adm := setupAdmin(e)
	m := adm.mustOK("GET", "/api/dashboard?from=2026-11-01&to=2026-11-10&tz=America/Sao_Paulo", nil)
	if h, _ := m["history"].([]any); len(h) != 10 || m["from"] != "2026-11-01" || m["to"] != "2026-11-10" || m["days"].(float64) != 10 {
		t.Fatalf("período: %v", m)
	}
	if h := adm.mustOK("GET", "/api/dashboard?from=2026-11-05&tz=UTC", nil)["history"].([]any); len(h) != 1 {
		t.Fatalf("só a data inicial = um dia: %v", h)
	}
	for _, q := range []string{"from=2026-11-10&to=2026-11-01", "from=ontem", "from=2025-01-01&to=2026-11-01"} {
		if code, _ := adm.do("GET", "/api/dashboard?"+q, nil); code != 400 {
			t.Fatalf("%s deveria ser recusado: %d", q, code)
		}
		if code, _ := adm.do("GET", "/api/runs?"+q, nil); code != 400 {
			t.Fatalf("runs %s deveria ser recusado: %d", q, code)
		}
	}
	// três execuções em dias diferentes (horário de Brasília): o filtro pega só as do período, no fuso pedido
	ag := newAgent(t, e, "srv-periodo")
	jid := int64(adm.mustOK("POST", "/api/jobs", map[string]any{"name": "j-periodo", "agent_id": ag.ID, "paths": []string{"/etc"}, "keep": 1})["id"].(float64))
	sp, _ := time.LoadLocation("America/Sao_Paulo")
	for i, at := range []time.Time{
		time.Date(2026, 11, 3, 23, 30, 0, 0, sp), // 02:30 UTC do dia 4: no fuso, ainda é dia 3
		time.Date(2026, 11, 4, 10, 0, 0, 0, sp),
		time.Date(2026, 11, 6, 0, 10, 0, 0, sp),
	} {
		e.api.Store.ApplyReport(context.Background(), store.Report{UID: "p" + itoa(i), JobID: jid, AgentID: ag.ID, Final: true, Status: "ok", StartedAt: at.UTC(), FinishedAt: at.UTC().Add(time.Minute), BytesOutput: 100})
	}
	count := func(q string) int {
		return len(adm.mustOK("GET", "/api/runs?job_id="+itoa(int(jid))+"&tz=America/Sao_Paulo&"+q, nil)["runs"].([]any))
	}
	if n := count("from=2026-11-04&to=2026-11-05"); n != 1 {
		t.Fatalf("dias 4 e 5: esperava 1, veio %d", n)
	}
	if n := count("from=2026-11-03&to=2026-11-06"); n != 3 {
		t.Fatalf("dias 3 a 6: esperava 3, veio %d", n)
	}
	if n := count(""); n != 3 {
		t.Fatalf("sem período: %d", n)
	}
	d := adm.mustOK("GET", "/api/dashboard?from=2026-11-03&to=2026-11-04&tz=America/Sao_Paulo", nil)
	if tot := d["totals"].(map[string]any); tot["ok"].(float64) != 2 || tot["bytes"].(float64) != 200 {
		t.Fatalf("totais do painel no período: %v", tot)
	}
}

func TestJobIncremental(t *testing.T) {
	e := newEnv(t)
	adm := setupAdmin(e)
	ag := newAgent(t, e, "srv-inc")
	base := map[string]any{"agent_id": ag.ID, "paths": []string{"/srv"}, "keep": 3}
	with := func(m map[string]any) map[string]any {
		out := map[string]any{}
		for k, v := range base {
			out[k] = v
		}
		for k, v := range m {
			out[k] = v
		}
		return out
	}
	if code, m := adm.do("POST", "/api/jobs", with(map[string]any{"name": "velho", "backup_mode": "incremental"})); code != 400 || !strings.Contains(m["error"].(string), "0.0.21") {
		t.Fatalf("agente antigo não pode ter job incremental: %d %v", code, m)
	}
	e.api.Store.DB.Exec("UPDATE agents SET agent_version='0.0.21' WHERE id=?", ag.ID)
	jid := int64(adm.mustOK("POST", "/api/jobs", with(map[string]any{"name": "inc", "backup_mode": "incremental", "full_weekday": 3}))["id"].(float64))
	j, _ := e.api.Store.GetJob(context.Background(), jid)
	if j.BackupMode != "incremental" || j.FullWeekday != 3 {
		t.Fatalf("%+v", j)
	}
	if sp := agentsrv.SpecFromJob(*j); sp.BackupMode != "incremental" || sp.FullWeekday != 3 {
		t.Fatalf("spec: %+v", sp)
	}
	for _, bad := range []map[string]any{{"name": "x1", "backup_mode": "diferencial"}, {"name": "x2", "backup_mode": "incremental", "full_weekday": 7}} {
		if code, _ := adm.do("POST", "/api/jobs", with(bad)); code != 400 {
			t.Fatalf("%v deveria ser recusado: %d", bad, code)
		}
	}
	if j0 := adm.mustOK("POST", "/api/jobs", with(map[string]any{"name": "padrao"})); j0 != nil {
		jj, _ := e.api.Store.GetJob(context.Background(), int64(j0["id"].(float64)))
		if jj.BackupMode != "full" {
			t.Fatalf("o padrão é full: %q", jj.BackupMode)
		}
	}
	adm.mustOK("POST", "/api/jobs/"+itoa(int(jid))+"/run?full=1", nil)
	adm.mustOK("POST", "/api/jobs/"+itoa(int(jid))+"/run", nil)
	if f := e.hub.fulls; len(f) != 2 || !f[0] || f[1] {
		t.Fatalf("full agora: %v", f)
	}

	// extrair um incremental manda a cadeia (full + incrementais até ele, em ordem) com os SHA-256
	st := e.api.Store
	ctx := context.Background()
	now := time.Now().UTC()
	for i, n := range []string{"inc-20261122-030000.tar.gz", "inc-20261123-030000-inc.tar.gz", "inc-20261124-030000-inc.tar.gz", "inc-20261125-030000-inc.tar.gz"} {
		status := "ok"
		if i == 3 {
			status = "failed" // não conta para a cadeia
		}
		kind := "incremental"
		if i == 0 {
			kind = "full"
		}
		st.ApplyReport(ctx, store.Report{UID: "c" + itoa(i), JobID: jid, AgentID: ag.ID, Final: true, Status: status, StartedAt: now, FinishedAt: now, OutputName: n, SHA256: "sha" + itoa(i), BackupKind: kind})
	}
	r3, _ := st.RunByUID(ctx, "c2")
	if r3.BackupKind != "incremental" {
		t.Fatalf("tipo da execução: %q", r3.BackupKind)
	}
	adm.mustOK("POST", "/api/runs/"+itoa(int(r3.ID))+"/extract", map[string]string{"target": "rest-inc"})
	act := e.hub.actions[len(e.hub.actions)-1]
	if strings.Join(act.Chain, ",") != "inc-20261122-030000.tar.gz,inc-20261123-030000-inc.tar.gz,inc-20261124-030000-inc.tar.gz" || strings.Join(act.ChainSha256, ",") != "sha0,sha1,sha2" {
		t.Fatalf("cadeia: %v %v", act.Chain, act.ChainSha256)
	}
	r0, _ := st.RunByUID(ctx, "c0")
	adm.mustOK("POST", "/api/runs/"+itoa(int(r0.ID))+"/extract", map[string]string{"target": "rest-full"})
	if act := e.hub.actions[len(e.hub.actions)-1]; len(act.Chain) != 0 {
		t.Fatalf("um full não precisa de cadeia: %v", act.Chain)
	}
}

func TestJobMikrotik(t *testing.T) {
	e := newEnv(t)
	adm := setupAdmin(e)
	ag := newAgent(t, e, "srv-mt")
	rt := map[string]any{"host": "10.0.0.1", "user": "coruja", "password": "s3nha", "export": true, "binary": true, "backup_password": "senha-backup-1", "host_key": "SHA256:abc"}
	job := func(name string, m map[string]any) map[string]any {
		return map[string]any{"name": name, "agent_id": ag.ID, "kind": "mikrotik", "mikrotik": m}
	}
	if code, m := adm.do("POST", "/api/jobs", job("velho", rt)); code != 400 || !strings.Contains(m["error"].(string), "0.0.22") {
		t.Fatalf("agente antigo: %d %v", code, m)
	}
	e.api.Store.DB.Exec("UPDATE agents SET agent_version='0.0.22' WHERE id=?", ag.ID)
	semChave := map[string]any{}
	for k, v := range rt {
		semChave[k] = v
	}
	delete(semChave, "host_key")
	if code, m := adm.do("POST", "/api/jobs", job("sem-chave", semChave)); code != 400 || !strings.Contains(m["error"].(string), "impressão digital") {
		t.Fatalf("sem impressão digital: %d %v", code, m)
	}
	id := int64(adm.mustOK("POST", "/api/jobs", job("roteador", rt))["id"].(float64))
	var v map[string]any
	for _, x := range adm.mustOK("GET", "/api/jobs", nil)["jobs"].([]any) {
		if x.(map[string]any)["name"] == "roteador" {
			v = x.(map[string]any)
		}
	}
	mt := v["mikrotik"].(map[string]any)
	if mt["password_set"] != true || mt["backup_password_set"] != true || mt["host"] != "10.0.0.1" || mt["password"] != nil || mt["backup_password"] != nil {
		t.Fatalf("a visão não pode expor senhas: %v", mt)
	}
	// editar sem reenviar as senhas mantém as salvas
	edit := map[string]any{"host": "10.0.0.1", "user": "coruja", "export": true, "export_sensitive": true, "binary": true, "host_key": "SHA256:abc"}
	adm.mustOK("PATCH", "/api/jobs/"+itoa(int(id)), map[string]any{"mikrotik": edit})
	j, _ := e.api.Store.GetJob(context.Background(), id)
	c, err := j.OpenMikrotik(e.api.Box)
	if err != nil || c.Password != "s3nha" || c.BackupPassword != "senha-backup-1" || !c.ExportSensitive || j.BackupMode != "full" {
		t.Fatalf("edição: %v %+v %s", err, c, j.BackupMode)
	}
	// trocar o roteador exige a senha de novo
	edit["host"] = "10.0.0.2"
	if code, _ := adm.do("PATCH", "/api/jobs/"+itoa(int(id)), map[string]any{"mikrotik": edit}); code != 400 {
		t.Fatalf("outro roteador sem senha deveria ser recusado: %d", code)
	}
	if db, _ := j.OpenDB(e.api.Box); db != nil {
		t.Fatal("job de MikroTik não tem configuração de banco")
	}
}

func TestRestauracaoPorSelecao(t *testing.T) {
	e := newEnv(t)
	adm := setupAdmin(e)
	ag := newAgent(t, e, "srv-rest")
	e.api.Store.DB.Exec("UPDATE agents SET agent_version='0.0.23' WHERE id=?", ag.ID)
	jid := int64(adm.mustOK("POST", "/api/jobs", map[string]any{"name": "rest", "agent_id": ag.ID, "paths": []string{"/srv"}, "keep": 3, "backup_mode": "incremental"})["id"].(float64))
	ctx := context.Background()
	now := time.Now().UTC()
	st := e.api.Store
	for i, n := range []string{"rest-20261122-030000.tar.gz", "rest-20261123-030000-inc.tar.gz"} {
		st.ApplyReport(ctx, store.Report{UID: "r" + itoa(i), JobID: jid, AgentID: ag.ID, Final: true, Status: "ok", StartedAt: now, FinishedAt: now, OutputName: n, SHA256: "s" + itoa(i)})
	}
	r1, _ := st.RunByUID(ctx, "r1")
	base := "/api/runs/" + itoa(int(r1.ID))
	adm.mustOK("POST", base+"/restore", map[string]any{"paths": []string{"/srv/a.txt", "/srv/dir"}, "target": "sel-1"})
	act := e.hub.actions[len(e.hub.actions)-1]
	if act.Kind != "restore_files" || act.Original || act.Target != "sel-1" || strings.Join(act.Paths, ",") != "/srv/a.txt,/srv/dir" ||
		strings.Join(act.Chain, ",") != "rest-20261122-030000.tar.gz,rest-20261123-030000-inc.tar.gz" || act.Sha256 != "s1" {
		t.Fatalf("ação: %+v", act)
	}
	adm.mustOK("POST", base+"/restore", map[string]any{"paths": []string{"/srv/a.txt"}, "original": true})
	if a := e.hub.actions[len(e.hub.actions)-1]; !a.Original || a.Target != "" {
		t.Fatalf("original: %+v", a)
	}
	for _, bad := range []map[string]any{{"paths": []string{}}, {"paths": []string{"relativo"}}, {"paths": []string{"/x"}, "target": "../fora"}} {
		if code, _ := adm.do("POST", base+"/restore", bad); code != 400 {
			t.Fatalf("%v deveria dar 400: %d", bad, code)
		}
	}
	// numa pasta escolhida do servidor: exige agente 0.0.27; caminho normalizado; não junto com o original
	if code, m := adm.do("POST", base+"/restore", map[string]any{"paths": []string{"/srv/a.txt"}, "dest": "/mnt/rest"}); code != 422 || !strings.Contains(m["error"].(string), "0.0.27") {
		t.Fatalf("pasta escolhida com agente antigo: %d %v", code, m)
	}
	e.api.Store.DB.Exec("UPDATE agents SET agent_version='0.0.27' WHERE id=?", ag.ID)
	adm.mustOK("POST", base+"/restore", map[string]any{"paths": []string{"/srv/a.txt"}, "dest": "/mnt//rest/", "target": "ignorado"})
	if a := e.hub.actions[len(e.hub.actions)-1]; a.DestDir != "/mnt/rest" || a.Original || a.Target != "" {
		t.Fatalf("pasta escolhida: %+v", a)
	}
	for _, bad := range []map[string]any{{"paths": []string{"/x"}, "dest": "/proc/1"}, {"paths": []string{"/x"}, "dest": "/"}, {"paths": []string{"/x"}, "dest": "rel"},
		{"paths": []string{"/x"}, "dest": "/a/../b"}, {"paths": []string{"/x"}, "dest": "/mnt", "original": true}} {
		if code, _ := adm.do("POST", base+"/restore", bad); code != 400 {
			t.Fatalf("%v deveria dar 400: %d", bad, code)
		}
	}
	// operador restaura numa pasta, mas não no local original; leitor não restaura
	adm.mustOK("POST", "/api/users", map[string]string{"username": "oper", "password": pw, "role": "operator"})
	adm.mustOK("POST", "/api/users", map[string]string{"username": "leit", "password": pw, "role": "viewer"})
	o := e.client()
	o.mustOK("POST", "/api/login", map[string]string{"username": "oper", "password": pw})
	o.mustOK("POST", base+"/restore", map[string]any{"paths": []string{"/srv"}})
	if code, _ := o.do("POST", base+"/restore", map[string]any{"paths": []string{"/srv"}, "original": true}); code != 403 {
		t.Fatalf("operador no local original: %d", code)
	}
	if code, _ := o.do("POST", base+"/restore", map[string]any{"paths": []string{"/srv"}, "dest": "/mnt/rest"}); code != 403 {
		t.Fatalf("operador numa pasta do servidor: %d", code)
	}
	v := e.client()
	v.mustOK("POST", "/api/login", map[string]string{"username": "leit", "password": pw})
	for _, rq := range []struct{ m, p string }{{"POST", base + "/restore"}, {"GET", base + "/browse"}, {"GET", "/api/jobs/" + itoa(int(jid)) + "/versions?path=/srv/a"}} {
		if code, _ := v.do(rq.m, rq.p, map[string]any{"paths": []string{"/srv"}}); code != 403 {
			t.Fatalf("leitor em %s: %d", rq.p, code)
		}
	}
	// auditoria registra o que foi restaurado
	found := false
	for _, x := range adm.mustOK("GET", "/api/audit", nil)["entries"].([]any) {
		if m := x.(map[string]any); m["action"] == "run.restore_files" && strings.Contains(m["detail"].(string), "LOCAL ORIGINAL") {
			found = true
		}
	}
	if !found {
		t.Fatal("restauração no local original deveria ir para a auditoria")
	}
	// agente antigo
	e.api.Store.DB.Exec("UPDATE agents SET agent_version='0.0.22' WHERE id=?", ag.ID)
	if code, m := adm.do("POST", base+"/restore", map[string]any{"paths": []string{"/srv"}}); code != 422 || !strings.Contains(m["error"].(string), "0.0.23") {
		t.Fatalf("agente antigo: %d %v", code, m)
	}
}

func TestDownloadPeloNavegador(t *testing.T) {
	e := newEnv(t)
	adm := setupAdmin(e)
	ag := newAgent(t, e, "srv-dl")
	e.api.Store.DB.Exec("UPDATE agents SET agent_version='0.0.24' WHERE id=?", ag.ID)
	jid := int64(adm.mustOK("POST", "/api/jobs", map[string]any{"name": "dl", "agent_id": ag.ID, "paths": []string{"/srv"}, "keep": 3})["id"].(float64))
	ctx := context.Background()
	now := time.Now().UTC()
	e.api.Store.ApplyReport(ctx, store.Report{UID: "d0", JobID: jid, AgentID: ag.ID, Final: true, Status: "ok", StartedAt: now, FinishedAt: now, OutputName: "dl-20261127-030000.tar.gz", SHA256: "x"})
	r0, _ := e.api.Store.RunByUID(ctx, "d0")
	base := "/api/runs/" + itoa(int(r0.ID)) + "/download"
	m := adm.mustOK("POST", base, map[string]any{"paths": []string{"/srv/a", "/srv/b"}, "zip": true})
	url := m["url"].(string)
	if a := e.hub.actions[len(e.hub.actions)-1]; !a.Zip || a.FileName != "dl-20261127-030000.tar.gz" || len(a.Chain) != 1 {
		t.Fatalf("pedido ao agente: %+v", a)
	}
	// outro usuário não usa o endereço de quem pediu
	adm.mustOK("POST", "/api/users", map[string]string{"username": "outro", "password": pw, "role": "admin"})
	o := e.client()
	o.mustOK("POST", "/api/login", map[string]string{"username": "outro", "password": pw})
	if code, _, _ := o.raw("GET", url, nil, ""); code != 404 {
		t.Fatalf("outro usuário: %d", code)
	}
	code, body, h := adm.raw("GET", url, nil, "")
	if code != 200 || string(body) != "conteudo:/srv/a,/srv/b" || h.Get("Content-Type") != "application/zip" || !strings.Contains(h.Get("Content-Disposition"), "attachment") {
		t.Fatalf("download: %d %q %v", code, body, h)
	}
	if code, _, _ := adm.raw("GET", url, nil, ""); code != 404 {
		t.Fatalf("o endereço é de uso único: %d", code)
	}
	if code, m := adm.do("POST", base, map[string]any{"paths": []string{"/nao-existe"}}); code != 422 || !strings.Contains(m["error"].(string), "nenhum") {
		t.Fatalf("erro do agente deve voltar como mensagem: %d %v", code, m)
	}
	e.api.Store.DB.Exec("UPDATE agents SET agent_version='0.0.23' WHERE id=?", ag.ID)
	if code, m := adm.do("POST", base, map[string]any{"paths": []string{"/srv/a"}}); code != 422 || !strings.Contains(m["error"].(string), "0.0.24") {
		t.Fatalf("agente antigo: %d %v", code, m)
	}
}

func TestRestaurarBancoPelaInterface(t *testing.T) {
	e := newEnv(t)
	adm := setupAdmin(e)
	ag := newAgent(t, e, "srv-db")
	e.api.Store.DB.Exec("UPDATE agents SET agent_version='0.0.24' WHERE id=?", ag.ID)
	jid := int64(adm.mustOK("POST", "/api/jobs", map[string]any{"name": "bancos", "agent_id": ag.ID, "kind": "database", "keep": 3,
		"db": map[string]any{"engine": "mysql", "mode": "docker", "container": "db1", "user": "root", "password": "x"}})["id"].(float64))
	ctx := context.Background()
	now := time.Now().UTC()
	e.api.Store.ApplyReport(ctx, store.Report{UID: "b0", JobID: jid, AgentID: ag.ID, Final: true, Status: "ok", StartedAt: now, FinishedAt: now, OutputName: "bancos-20261127-030000.tar.gz", SHA256: "x"})
	r0, _ := e.api.Store.RunByUID(ctx, "b0")
	base := "/api/runs/" + itoa(int(r0.ID)) + "/restore-db"
	adm.mustOK("POST", base, map[string]any{"databases": []map[string]string{{"source": "app", "target": "app_restaurado"}, {"source": "loja", "target": "loja_r"}}})
	a := e.hub.actions[len(e.hub.actions)-1]
	if a.Kind != "restore_db" || a.Original || strings.Join(a.Paths, ",") != "app,loja" || strings.Join(a.Targets, ",") != "app_restaurado,loja_r" {
		t.Fatalf("ação: %+v", a)
	}
	for _, bad := range []map[string]any{
		{"databases": []map[string]string{{"source": "app", "target": "app"}}},                                      // mesmo nome sem sobrescrever
		{"databases": []map[string]string{{"source": "app", "target": "x; drop"}}},                                  // nome inválido
		{"databases": []map[string]string{{"source": "app", "target": "dup"}, {"source": "loja", "target": "dup"}}}, // destino repetido
		{"databases": []map[string]string{}},
	} {
		if code, _ := adm.do("POST", base, bad); code != 400 {
			t.Fatalf("%v deveria dar 400: %d", bad, code)
		}
	}
	adm.mustOK("POST", base, map[string]any{"databases": []map[string]string{{"source": "app", "target": "app"}}, "overwrite": true})
	if a := e.hub.actions[len(e.hub.actions)-1]; !a.Original {
		t.Fatal("sobrescrever")
	}
	adm.mustOK("POST", "/api/users", map[string]string{"username": "op2", "password": pw, "role": "operator"})
	o := e.client()
	o.mustOK("POST", "/api/login", map[string]string{"username": "op2", "password": pw})
	if code, _ := o.do("POST", base, map[string]any{"databases": []map[string]string{{"source": "app", "target": "app"}}, "overwrite": true}); code != 403 {
		t.Fatalf("operador sobrescrevendo: %d", code)
	}
	o.mustOK("POST", base, map[string]any{"databases": []map[string]string{{"source": "app", "target": "app_copia"}}})
	// job de arquivos não restaura banco
	fj := int64(adm.mustOK("POST", "/api/jobs", map[string]any{"name": "arqs", "agent_id": ag.ID, "paths": []string{"/srv"}, "keep": 3})["id"].(float64))
	e.api.Store.ApplyReport(ctx, store.Report{UID: "f0", JobID: fj, AgentID: ag.ID, Final: true, Status: "ok", StartedAt: now, FinishedAt: now, OutputName: "arqs-20261127-030000.tar.gz", SHA256: "x"})
	f0, _ := e.api.Store.RunByUID(ctx, "f0")
	if code, _ := adm.do("POST", "/api/runs/"+itoa(int(f0.ID))+"/restore-db", map[string]any{"databases": []map[string]string{{"source": "a", "target": "b"}}}); code != 409 {
		t.Fatalf("job de arquivos: %d", code)
	}
}

func TestCancelandoFicaRegistrado(t *testing.T) {
	e := newEnv(t)
	adm := setupAdmin(e)
	ag := newAgent(t, e, "srv-c")
	jid := int64(adm.mustOK("POST", "/api/jobs", map[string]any{"name": "cancelar", "agent_id": ag.ID, "paths": []string{"/srv"}, "keep": 3})["id"].(float64))
	ctx := context.Background()
	now := time.Now().UTC()
	e.api.Store.ApplyReport(ctx, store.Report{UID: "c0", JobID: jid, AgentID: ag.ID, Status: "running", StartedAt: now})
	r0, _ := e.api.Store.RunByUID(ctx, "c0")
	if r0.CancelRequested {
		t.Fatal("ainda não pediram cancelamento")
	}
	adm.mustOK("POST", "/api/runs/"+itoa(int(r0.ID))+"/cancel", nil)
	d := adm.mustOK("GET", "/api/runs/"+itoa(int(r0.ID)), nil)
	if run := d["run"].(map[string]any); run["cancel_requested"] != true || store.IsFinal(run["status"].(string)) {
		t.Fatalf("deveria aparecer como cancelando: %v", run)
	}
	jobs := adm.mustOK("GET", "/api/jobs", nil)["jobs"].([]any)
	if lr := jobs[0].(map[string]any)["last_run"].(map[string]any); lr["cancel_requested"] != true {
		t.Fatalf("lista de jobs: %v", lr)
	}
}

func TestPadraoDePastaExigeAgenteNovo(t *testing.T) {
	e := newEnv(t)
	adm := setupAdmin(e)
	ag := newAgent(t, e, "srv-p")
	e.api.Store.DB.Exec("UPDATE agents SET agent_version='0.0.25' WHERE id=?", ag.ID)
	body := map[string]any{"name": "padroes", "agent_id": ag.ID, "paths": []string{"/srv"}, "excludes": []string{"*.log", "cache/"}, "keep": 3}
	if code, _ := adm.do("POST", "/api/jobs", body); code != 400 && code != 422 {
		t.Fatalf("agente 0.0.25 com padrão de pasta: %d", code)
	}
	body["excludes"] = []string{"*.log", "/srv/tmp"}
	adm.mustOK("POST", "/api/jobs", body)
	e.api.Store.DB.Exec("UPDATE agents SET agent_version='0.0.26' WHERE id=?", ag.ID)
	body["name"], body["excludes"] = "padroes2", []string{"cache/", "*/www/tmp/"}
	adm.mustOK("POST", "/api/jobs", body)
}
