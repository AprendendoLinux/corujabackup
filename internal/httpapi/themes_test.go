package httpapi

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
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
	// O cliente não pode forjar metadados de pacote em tema comum.
	forjado := []map[string]any{{"id": "meu", "label": "Meu", "vars": ths[0].(map[string]any)["vars"], "pkg": map[string]any{"logo_doc": "image/png", "doc": map[string]string{"primary": "#ff0000"}}}}
	adm.mustOK("PUT", "/api/branding", map[string]any{"name": "Acme", "theme": "coruja", "themes": forjado})
	if c, _, _ := pub.raw("GET", "/api/branding/themes/acme/logo", nil, ""); c != 404 {
		t.Fatalf("logo do tema removido deveria sumir: %d", c)
	}
	_, m2 := pub.do("GET", "/api/branding", nil)
	if p := m2["themes"].([]any)[0].(map[string]any)["pkg"]; p != nil {
		t.Fatalf("pkg forjado foi aceito: %v", p)
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
