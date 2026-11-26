package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
	"github.com/AprendendoLinux/corujabackup/internal/ratelimit"
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
	callOut []byte
	callErr error
}

func (f *fakeHub) Online() map[int64]time.Time     { return map[int64]time.Time{} }
func (f *fakeHub) Revoke(id int64, _ string)       { f.revoked = append(f.revoked, id) }
func (f *fakeHub) PushJobs(context.Context, int64) { f.pushes++ }
func (f *fakeHub) RunNow(_, jobID int64, uid string) error {
	if f.offline {
		return agentsrv.ErrOffline
	}
	f.runs = append(f.runs, uid)
	return nil
}
func (f *fakeHub) CancelRun(int64, string) error { return nil }
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
	e.api = &Server{DB: d, Store: store.New(d), Box: box, Pinger: d, Agents: e.hub, Started: time.Now(), Fingerprint: strings.Repeat("ab", 32), AgentPort: "8443",
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
	if code, _ := o.do("POST", "/api/agents/tokens", map[string]any{"name": "srv-1"}); code != 201 {
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
	if code, _ := c.do("POST", "/api/agents/tokens", map[string]any{"name": "a"}); code != 400 {
		t.Fatalf("nome curto: %d", code)
	}
	tk := c.mustOK("POST", "/api/agents/tokens", map[string]any{"name": "umami-server2", "ttl_hours": 9999})
	if !strings.HasPrefix(tk["token"].(string), "cb1."+strings.Repeat("ab", 16)+".") {
		t.Fatalf("token deveria carregar a impressão digital: %v", tk["token"])
	}
	list := c.mustOK("GET", "/api/agents", nil)
	if len(list["pending_tokens"].([]any)) != 1 || strings.Contains(jsonString(list), "cb1.") {
		t.Fatalf("o token em si nunca deve ser listado: %v", list)
	}
	// agente criado direto no banco para testar renomear/revogar/excluir
	a, err := e.api.Store.Enroll(context.Background(), enrollHashOf(t, c, e), "host", "linux", "amd64", "0.0.1", "10.0.0.1", auth.SHA256Hex("cred"))
	if err != nil {
		t.Fatal(err)
	}
	if code, _ := c.do("POST", "/api/agents/tokens", map[string]any{"name": "umami-server2"}); code != 409 {
		t.Fatalf("nome de agente existente: %d", code)
	}
	c.mustOK("PATCH", "/api/agents/"+itoa(int(a.ID)), map[string]string{"name": "umami-s2"})
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
		"nome vazio":         func(m map[string]any) { m["name"] = "" },
		"agente inexistente": func(m map[string]any) { m["agent_id"] = 9999 },
		"cron inválido":      func(m map[string]any) { m["cron"] = "99 * * * *" },
		"cron @daily":        func(m map[string]any) { m["cron"] = "@daily" },
		"fuso inválido":      func(m map[string]any) { m["timezone"] = "Marte/X" },
		"caminho relativo":   func(m map[string]any) { m["paths"] = []string{"etc"} },
		"sem caminhos":       func(m map[string]any) { m["paths"] = []string{} },
		"destino com ..":     func(m map[string]any) { m["dest_subdir"] = "../fora" },
		"destino absoluto":   func(m map[string]any) { m["dest_subdir"] = "/etc" },
		"keep zero":          func(m map[string]any) { m["keep"] = 0 },
		"campo desconhecido": func(m map[string]any) { m["foo"] = 1 },
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
