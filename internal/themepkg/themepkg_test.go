package themepkg

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var png1x1, _ = base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg==")

const okJSON = `{"schema":1,"id":"acme","name":"Acme","version":"1.2.0","colors":{"bg":"#ffffff","surface":"#ffffff","text":"#000000","muted":"#666666","border":"#dddddd","primary":"#112233","primary_contrast":"#ffffff","accent":"#ffaa00","ok":"#00aa00","danger":"#aa0000"},"document":{"primary":"#223344"},"logo":{"ui":"logo.png"}}`

func mk(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for n, b := range files {
		w, err := zw.Create(n)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write(b)
	}
	_ = zw.Close()
	return buf.Bytes()
}

func TestValido(t *testing.T) {
	p, err := Read(mk(t, map[string][]byte{"theme.json": []byte(okJSON), "logo.png": png1x1}))
	if err != nil {
		t.Fatal(err)
	}
	if p.ID != "acme" || p.LogoUIType != "image/png" || p.LogoDocType != "image/png" || p.Document["primary"] != "#223344" {
		t.Fatalf("lido errado: %+v", p)
	}
}

func TestInvalidos(t *testing.T) {
	cases := map[string]struct {
		files map[string][]byte
		want  string
	}{
		"sem theme.json":     {map[string][]byte{"logo.png": png1x1}, "theme.json"},
		"json lixo":          {map[string][]byte{"theme.json": []byte("{")}, "inválido"},
		"campo desconhecido": {map[string][]byte{"theme.json": []byte(strings.Replace(okJSON, `"schema":1`, `"schema":1,"css":"x"`, 1)), "logo.png": png1x1}, "unknown field"},
		"id embutido":        {map[string][]byte{"theme.json": []byte(strings.Replace(okJSON, `"acme"`, `"coruja"`, 1)), "logo.png": png1x1}, "embutido"},
		"cor ruim":           {map[string][]byte{"theme.json": []byte(strings.Replace(okJSON, `#112233`, `red`, 1)), "logo.png": png1x1}, "cor inválida"},
		"falta cor":          {map[string][]byte{"theme.json": []byte(strings.Replace(okJSON, `"danger":"#aa0000"`, `"x":"#aa0000"`, 1)), "logo.png": png1x1}, "falta a cor"},
		"logo ausente":       {map[string][]byte{"theme.json": []byte(okJSON)}, "não está no pacote"},
		"logo nao imagem":    {map[string][]byte{"theme.json": []byte(okJSON), "logo.png": []byte("MZ....")}, "não é uma imagem"},
		"svg com script":     {map[string][]byte{"theme.json": []byte(strings.Replace(okJSON, "logo.png", "logo.svg", 1)), "logo.svg": []byte(`<svg><script>alert(1)</script></svg>`)}, "não é uma imagem"},
		"caminho":            {map[string][]byte{"theme.json": []byte(okJSON), "../x.png": png1x1}, "não permitido"},
		"pasta":              {map[string][]byte{"theme.json": []byte(okJSON), "a/b.png": png1x1}, "não permitido"},
		"arquivo extra":      {map[string][]byte{"theme.json": []byte(okJSON), "logo.png": png1x1, "x.exe": []byte("MZ")}, "não declarados"},
		"doc exige raster":   {map[string][]byte{"theme.json": []byte(strings.Replace(okJSON, `"logo":{"ui":"logo.png"}`, `"logo":{"document":"l.svg"}`, 1)), "l.svg": []byte(`<svg xmlns="x"></svg>`)}, "só PNG"},
	}
	for name, c := range cases {
		_, err := Read(mk(t, c.files))
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: erro = %v, quer conter %q", name, err, c.want)
		}
	}
	if _, err := Read([]byte("não é zip")); err == nil {
		t.Error("lixo deveria falhar")
	}
	big := bytes.Repeat([]byte("a"), MaxFileBytes+10)
	if _, err := Read(mk(t, map[string][]byte{"theme.json": []byte(okJSON), "logo.png": append(append([]byte{}, png1x1...), big...)})); err == nil {
		t.Error("arquivo grande deveria falhar")
	}
}

// Os pacotes entregues no repositório têm de ser sempre válidos.
func TestPacotesDoRepositorio(t *testing.T) {
	dirs, _ := filepath.Glob("../../themes/*")
	if len(dirs) == 0 {
		t.Skip("pasta themes/ não encontrada")
	}
	for _, d := range dirs {
		if fi, err := os.Stat(d); err != nil || !fi.IsDir() {
			continue
		}
		if _, err := Pack(d); err != nil {
			t.Errorf("%s: %v", d, err)
		}
	}
}

func TestModoEscuroGeradoELegivel(t *testing.T) {
	light := map[string]string{"bg": "#f5f6f8", "surface": "#ffffff", "text": "#262a33", "muted": "#5f6673", "border": "#dfe3ea",
		"primary": "#aa3335", "primary_contrast": "#ffffff", "accent": "#be904e", "ok": "#1f7a4d", "danger": "#c2410c"}
	d := DeriveDark(light)
	for _, k := range UIKeys {
		if !colorRE.MatchString(d[k]) {
			t.Fatalf("%s: %q", k, d[k])
		}
	}
	ratio := func(a, b string) float64 {
		la, lb := luminance(a)+0.05, luminance(b)+0.05
		if la < lb {
			la, lb = lb, la
		}
		return la / lb
	}
	if luminance(d["bg"]) > 0.05 || ratio(d["text"], d["bg"]) < 7 || ratio(d["primary"], d["bg"]) < 3 || ratio(d["primary_contrast"], d["primary"]) < 3 {
		t.Fatalf("modo escuro pouco legível: %v", d)
	}
}
