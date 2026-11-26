package httpapi

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/jpeg"
	"image/png"
	"math/rand/v2"
	"testing"

	"github.com/AprendendoLinux/corujabackup/internal/notify"
)

var tinyPNG, _ = base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg==")

func zipOf(files map[string][]byte) []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for n, b := range files {
		w, _ := zw.Create(n)
		_, _ = w.Write(b)
	}
	_ = zw.Close()
	return buf.Bytes()
}

func manifest(id, primary string) []byte {
	m := map[string]any{"schema": 1, "id": id, "name": "Tema " + id, "version": "1.0.0",
		"colors":   map[string]string{"bg": "#101010", "surface": "#1a1a1a", "text": "#eeeeee", "muted": "#999999", "border": "#333333", "primary": primary, "primary_contrast": "#001018", "accent": "#f59e0b", "ok": "#22c55e", "danger": "#ef4444"},
		"document": map[string]string{"primary": "#123456", "bg": "#fafafa"},
		"logo":     map[string]string{"ui": "logo.png"}}
	b, _ := json.Marshal(m)
	return b
}

func TestPacoteDeTemaImportaAplicaEPreserva(t *testing.T) {
	e := newEnv(t)
	adm := setupAdmin(e)
	pub := e.client()
	adm.mustOK("POST", "/api/users", map[string]string{"username": "operador2", "password": "senha-bem-forte-1", "role": "operator"})
	op := e.client()
	op.mustOK("POST", "/api/login", map[string]string{"username": "operador2", "password": "senha-bem-forte-1"})

	pkg := zipOf(map[string][]byte{"theme.json": manifest("acme", "#0ea5e9"), "logo.png": tinyPNG})
	if code, _, _ := op.raw("POST", "/api/branding/themes/import", pkg, "application/zip"); code != 403 {
		t.Fatalf("operador não importa: %d", code)
	}
	for name, body := range map[string][]byte{"lixo": []byte("xx"), "sem logo": zipOf(map[string][]byte{"theme.json": manifest("acme", "#0ea5e9")}),
		"script": zipOf(map[string][]byte{"theme.json": manifest("acme", "#0ea5e9"), "logo.png": []byte("<svg><script>1</script></svg>")})} {
		if code, _, _ := adm.raw("POST", "/api/branding/themes/import", body, "application/zip"); code != 400 {
			t.Errorf("%s: esperava 400, veio %d", name, code)
		}
	}
	code, body, _ := adm.raw("POST", "/api/branding/themes/import", pkg, "application/zip")
	if code != 200 {
		t.Fatalf("importação: %d %s", code, body)
	}
	_, m := pub.do("GET", "/api/branding", nil)
	ths := m["themes"].([]any)
	if len(ths) != 1 || ths[0].(map[string]any)["pkg"] == nil {
		t.Fatalf("tema não listado: %v", m)
	}
	if c, b, h := pub.raw("GET", "/api/branding/themes/acme/logo", nil, ""); c != 200 || !bytes.Equal(b, tinyPNG) || h.Get("Content-Type") != "image/png" || h.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("logo do tema: %d %s", c, h.Get("Content-Type"))
	}
	if c, _, _ := pub.raw("GET", "/api/branding/themes/..%2Fx/logo", nil, ""); c == 200 {
		t.Fatal("id malicioso não pode servir logo")
	}

	// Salvar a aparência pelo cliente (que não conhece "pkg") não pode perder o pacote nem o logo.
	themes := m["themes"]
	adm.mustOK("PUT", "/api/branding", map[string]any{"name": "Acme", "theme": "acme", "themes": themes})
	if c, _, _ := pub.raw("GET", "/api/branding/themes/acme/logo", nil, ""); c != 200 {
		t.Fatalf("logo perdido ao salvar: %d", c)
	}
	// Temas só entram por pacote: o cliente não cria tema nem altera cores, nome ou metadados de um instalado.
	forjado := []map[string]any{{"id": "meu", "label": "Meu", "vars": ths[0].(map[string]any)["vars"]}}
	if code, _ := adm.do("PUT", "/api/branding", map[string]any{"name": "Acme", "theme": "coruja", "themes": forjado}); code != 400 {
		t.Fatalf("tema criado pelo cliente deveria ser recusado: %d", code)
	}
	alterado := []map[string]any{{"id": "acme", "label": "Outro nome", "vars": map[string]string{"primary": "#ff0000"}, "pkg": map[string]any{"doc": map[string]string{"primary": "#ff0000"}}}}
	adm.mustOK("PUT", "/api/branding", map[string]any{"name": "Acme", "theme": "acme", "themes": alterado, "mode": "dark"})
	_, m2 := pub.do("GET", "/api/branding", nil)
	t0 := m2["themes"].([]any)[0].(map[string]any)
	if t0["label"] == "Outro nome" || t0["vars"].(map[string]any)["primary"] == "#ff0000" || t0["pkg"].(map[string]any)["doc"] != nil && t0["pkg"].(map[string]any)["doc"].(map[string]any)["primary"] == "#ff0000" {
		t.Fatalf("o cliente alterou o tema instalado: %v", t0)
	}
	if m2["mode"] != "dark" || t0["vars_dark"] == nil {
		t.Fatalf("modo escuro: %v %v", m2["mode"], t0["vars_dark"])
	}
	if code, _ := adm.do("PUT", "/api/branding", map[string]any{"name": "Acme", "theme": "acme", "themes": alterado, "mode": "roxo"}); code != 400 {
		t.Fatal("modo inválido deveria ser recusado")
	}
	// excluir o tema apaga o logo do pacote
	adm.mustOK("PUT", "/api/branding", map[string]any{"name": "Acme", "theme": "coruja", "themes": []any{}})
	if c, _, _ := pub.raw("GET", "/api/branding/themes/acme/logo", nil, ""); c != 404 {
		t.Fatalf("logo do tema removido deveria sumir: %d", c)
	}
	// o antigo tema "noturno" vira Coruja no modo escuro
	adm.mustOK("PUT", "/api/branding", map[string]any{"name": "Acme", "theme": "noturno", "themes": []any{}, "mode": "light"})
	if _, m2 = pub.do("GET", "/api/branding", nil); m2["theme"] != "coruja" || m2["mode"] != "dark" {
		t.Fatalf("noturno: %v %v", m2["theme"], m2["mode"])
	}

	// E-mail/PDF: cores do documento e logo do pacote.
	adm.mustOK("PUT", "/api/branding", map[string]any{"name": "Acme", "theme": "coruja", "themes": []any{}})
	if c, _, _ := adm.raw("POST", "/api/branding/themes/import", pkg, "application/zip"); c != 200 {
		t.Fatal("reimportação")
	}
	_, mm := pub.do("GET", "/api/branding", nil)
	adm.mustOK("PUT", "/api/branding", map[string]any{"name": "Acme", "theme": "acme", "themes": mm["themes"]})
	b := notify.LoadBrand(context.Background(), e.api.Store)
	if b.Primary != "#123456" || b.Bg != "#fafafa" || !bytes.Equal(b.LogoPNG, tinyPNG) || b.LogoMIME != "image/png" {
		t.Fatalf("marca do documento: %+v", b)
	}
	// Reimportar o mesmo id atualiza (não duplica).
	pkg2 := zipOf(map[string][]byte{"theme.json": manifest("acme", "#ff00ff"), "logo.png": tinyPNG})
	_, b2, _ := adm.raw("POST", "/api/branding/themes/import", pkg2, "application/zip")
	var r struct{ Replaced bool }
	_ = json.Unmarshal(b2, &r)
	if !r.Replaced {
		t.Fatalf("deveria atualizar: %s", b2)
	}
}

func TestCreditosDoRodapeConfiguraveis(t *testing.T) {
	e := newEnv(t)
	adm := setupAdmin(e)
	pub := e.client()
	_, m := pub.do("GET", "/api/branding", nil)
	c := m["credit"].(map[string]any)
	if c["label"] != "Desenvolvido por" || c["name"] != "Henrique Fagundes" || c["url"] != "https://henrique.tec.br" {
		t.Fatalf("padrão: %v", c)
	}
	adm.mustOK("PUT", "/api/branding", map[string]any{"name": "Acme", "theme": "coruja", "themes": []any{},
		"credit": map[string]string{"label": " Feito por ", "name": "TI da Acme", "url": "https://acme.example/ti"}})
	_, m = pub.do("GET", "/api/branding", nil)
	if c = m["credit"].(map[string]any); c["label"] != "Feito por" || c["name"] != "TI da Acme" || c["url"] != "https://acme.example/ti" {
		t.Fatalf("salvo: %v", c)
	}
	// sem o campo, os créditos são mantidos
	adm.mustOK("PUT", "/api/branding", map[string]any{"name": "Acme 2", "theme": "coruja", "themes": []any{}})
	if _, m = pub.do("GET", "/api/branding", nil); m["credit"].(map[string]any)["name"] != "TI da Acme" {
		t.Fatalf("perdeu os créditos: %v", m["credit"])
	}
	for _, bad := range []map[string]string{{"name": "x", "url": "javascript:alert(1)"}, {"name": "<b>x</b>"}, {"name": "x", "url": "https://a.b/\"onmouseover"}, {"name": "x", "url": "ftp://a.b"}} {
		if code, _ := adm.do("PUT", "/api/branding", map[string]any{"name": "Acme", "theme": "coruja", "themes": []any{}, "credit": bad}); code != 400 {
			t.Errorf("%v deveria ser recusado: %d", bad, code)
		}
	}
	// nome vazio desliga os créditos (e o link)
	adm.mustOK("PUT", "/api/branding", map[string]any{"name": "Acme", "theme": "coruja", "themes": []any{}, "credit": map[string]string{"label": "x", "name": "", "url": "https://a.b"}})
	if _, m = pub.do("GET", "/api/branding", nil); m["credit"].(map[string]any)["url"] != "" {
		t.Fatalf("%v", m["credit"])
	}
}

func TestAvatarDoUsuario(t *testing.T) {
	e := newEnv(t)
	adm := setupAdmin(e)
	me := adm.mustOK("GET", "/api/me", nil)["user"].(map[string]any)
	id := itoa(int(me["id"].(float64)))
	if me["avatar_version"] != "" {
		t.Fatalf("sem foto no início: %v", me)
	}
	if c, _, _ := adm.raw("GET", "/api/users/"+id+"/avatar", nil, ""); c != 404 {
		t.Fatalf("sem foto: %d", c)
	}
	for name, body := range map[string][]byte{
		"svg":        []byte(`<svg xmlns="http://www.w3.org/2000/svg"></svg>`),
		"html":       []byte("<html><script>x</script></html>"),
		"gigante":    append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{1}, 2<<20+10)...),
		"corrompida": append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{1}, 900<<10)...),
	} {
		if c, _, _ := adm.raw("POST", "/api/me/avatar", body, "image/png"); c != 400 {
			t.Errorf("%s: esperava 400, veio %d", name, c)
		}
	}
	// uma foto grande (1,6 MB, retangular) é aceita, recortada no centro e reduzida para um JPEG de 256×256
	big := image.NewRGBA(image.Rect(0, 0, 720, 560))
	rnd := rand.New(rand.NewPCG(1, 2))
	for i := range big.Pix {
		big.Pix[i] = byte(rnd.IntN(256))
	}
	var pngBuf bytes.Buffer
	if err := png.Encode(&pngBuf, big); err != nil {
		t.Fatal(err)
	}
	if pngBuf.Len() < 1<<20 || pngBuf.Len() > 2<<20 {
		t.Fatalf("a imagem de teste deveria ter entre 1 e 2 MB: %d", pngBuf.Len())
	}
	if c, b, _ := adm.raw("POST", "/api/me/avatar", pngBuf.Bytes(), "image/png"); c != 200 {
		t.Fatalf("envio: %d %s", c, b)
	}
	ver := adm.mustOK("GET", "/api/me", nil)["user"].(map[string]any)["avatar_version"]
	if ver == "" {
		t.Fatal("versão da foto não mudou")
	}
	// outro usuário autenticado vê; quem não entrou, não
	adm.mustOK("POST", "/api/users", map[string]string{"username": "leitor9", "password": pw, "role": "viewer"})
	v := e.client()
	v.mustOK("POST", "/api/login", map[string]string{"username": "leitor9", "password": pw})
	c, b, h := v.raw("GET", "/api/users/"+id+"/avatar", nil, "")
	if c != 200 || h.Get("Content-Type") != "image/jpeg" || h.Get("X-Content-Type-Options") != "nosniff" || len(b) > 200<<10 {
		t.Fatalf("ver a foto: %d %s %d bytes", c, h.Get("Content-Type"), len(b))
	}
	if cfg, err := jpeg.DecodeConfig(bytes.NewReader(b)); err != nil || cfg.Width != 256 || cfg.Height != 256 {
		t.Fatalf("a foto guardada deveria ser 256×256: %v %+v", err, cfg)
	}
	if c, _, _ := e.client().raw("GET", "/api/users/"+id+"/avatar", nil, ""); c != 401 {
		t.Fatalf("sem login: %d", c)
	}
	// cada um só troca a própria foto: o leitor envia e não altera a do administrador
	v.raw("POST", "/api/me/avatar", pngBuf.Bytes(), "image/png")
	adm.mustOK("DELETE", "/api/me/avatar", nil)
	if c, _, _ := v.raw("GET", "/api/users/"+id+"/avatar", nil, ""); c != 404 {
		t.Fatalf("foto removida: %d", c)
	}
	if u := v.mustOK("GET", "/api/me", nil)["user"].(map[string]any); u["avatar_version"] == "" {
		t.Fatal("a foto do leitor deveria continuar")
	}
}
