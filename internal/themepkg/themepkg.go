// Package themepkg lê, valida e empacota pacotes de tema (.zip) do Coruja Backup.
//
// Um pacote é um .zip PLANO (sem pastas) com um theme.json e, opcionalmente, logos e uma pré-visualização.
// Veja docs/TEMAS.md para o formato completo.
package themepkg

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const (
	SchemaVersion = 1
	MaxZipBytes   = 3 << 20 // tamanho do .zip
	MaxFileBytes  = 1 << 20 // cada arquivo descompactado
	MaxFiles      = 12
)

// UIKeys são as 10 cores da interface (obrigatórias). DocKeys são as cores opcionais usadas em e-mails e PDFs.
var (
	UIKeys  = []string{"bg", "surface", "text", "muted", "border", "primary", "primary_contrast", "accent", "ok", "danger"}
	DocKeys = []string{"bg", "text", "muted", "border", "primary", "accent", "ok", "danger"}

	idRE    = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,29}$`)
	colorRE = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
	fileRE  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	verRE   = regexp.MustCompile(`^[0-9A-Za-z.+-]{1,20}$`)
)

type Logo struct {
	UI       string `json:"ui,omitempty"`       // arquivo usado na interface (PNG, JPEG, GIF, WebP ou SVG)
	Document string `json:"document,omitempty"` // arquivo usado em e-mail e PDF (PNG, JPEG ou GIF)
}

// Manifest é o conteúdo de theme.json.
type Manifest struct {
	Schema      int               `json:"schema"`
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Version     string            `json:"version,omitempty"`
	Author      string            `json:"author,omitempty"`
	Description string            `json:"description,omitempty"`
	Colors      map[string]string `json:"colors"`
	Document    map[string]string `json:"document,omitempty"`
	Logo        Logo              `json:"logo,omitempty"`
	Preview     string            `json:"preview,omitempty"`
}

// Package é um pacote já validado, com os arquivos carregados.
type Package struct {
	Manifest
	LogoUI, LogoDoc         []byte
	LogoUIType, LogoDocType string
}

// SniffImage identifica o formato pelo conteúdo (nunca pela extensão). SVG com script é recusado.
func SniffImage(b []byte) (string, bool) {
	switch {
	case bytes.HasPrefix(b, []byte("\x89PNG\r\n\x1a\n")):
		return "image/png", true
	case bytes.HasPrefix(b, []byte("\xff\xd8\xff")):
		return "image/jpeg", true
	case bytes.HasPrefix(b, []byte("GIF87a")) || bytes.HasPrefix(b, []byte("GIF89a")):
		return "image/gif", true
	case len(b) > 12 && string(b[0:4]) == "RIFF" && string(b[8:12]) == "WEBP":
		return "image/webp", true
	}
	low := strings.ToLower(string(b))
	if strings.Contains(low, "<svg") {
		for _, bad := range []string{"<script", "javascript:", "<foreignobject", "onload=", "onerror=", "<iframe", "<!entity", "<use", "xlink:href=\"http", "href=\"http"} {
			if strings.Contains(low, bad) {
				return "", false
			}
		}
		return "image/svg+xml", true
	}
	return "", false
}

func validColors(m map[string]string, keys []string, required bool, where string) error {
	for _, k := range keys {
		v, ok := m[k]
		if !ok {
			if required {
				return fmt.Errorf("%s: falta a cor %q", where, k)
			}
			continue
		}
		if !colorRE.MatchString(v) {
			return fmt.Errorf("%s: cor inválida em %q (use #RRGGBB)", where, k)
		}
	}
	allowed := map[string]bool{}
	for _, k := range keys {
		allowed[k] = true
	}
	for k := range m {
		if !allowed[k] {
			return fmt.Errorf("%s: chave desconhecida %q", where, k)
		}
	}
	return nil
}

// Read valida e carrega um pacote .zip.
func Read(data []byte) (*Package, error) {
	if len(data) == 0 || len(data) > MaxZipBytes {
		return nil, fmt.Errorf("o pacote deve ter até %d MiB", MaxZipBytes>>20)
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, errors.New("arquivo .zip inválido")
	}
	if len(zr.File) > MaxFiles {
		return nil, fmt.Errorf("arquivos demais no pacote (máximo %d)", MaxFiles)
	}
	files := map[string][]byte{}
	var total uint64
	for _, f := range zr.File {
		name := f.Name
		if f.FileInfo().IsDir() {
			return nil, errors.New("o pacote não pode ter pastas: coloque tudo na raiz do .zip")
		}
		if !fileRE.MatchString(name) || strings.Contains(name, "..") {
			return nil, fmt.Errorf("nome de arquivo não permitido: %q", name)
		}
		if f.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("links simbólicos não são permitidos: %q", name)
		}
		if f.UncompressedSize64 > MaxFileBytes {
			return nil, fmt.Errorf("%s: arquivo maior que %d MiB", name, MaxFileBytes>>20)
		}
		if _, dup := files[name]; dup {
			return nil, fmt.Errorf("arquivo repetido: %q", name)
		}
		rc, err := f.Open()
		if err != nil {
			return nil, fmt.Errorf("%s: não foi possível ler", name)
		}
		b, err := io.ReadAll(io.LimitReader(rc, MaxFileBytes+1))
		rc.Close()
		if err != nil || len(b) > MaxFileBytes {
			return nil, fmt.Errorf("%s: arquivo inválido ou grande demais", name)
		}
		total += uint64(len(b))
		if total > 4*MaxFileBytes {
			return nil, errors.New("o conteúdo descompactado é grande demais")
		}
		files[name] = b
	}
	raw, ok := files["theme.json"]
	if !ok {
		return nil, errors.New("falta o arquivo theme.json na raiz do pacote")
	}
	var m Manifest
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("theme.json inválido: %v", err)
	}
	if m.Schema != SchemaVersion {
		return nil, fmt.Errorf("theme.json: \"schema\" deve ser %d", SchemaVersion)
	}
	if !idRE.MatchString(m.ID) {
		return nil, errors.New("theme.json: \"id\" deve ter 2 a 30 caracteres: letras minúsculas, números e hífen")
	}
	switch m.ID {
	case "coruja", "noturno", "floresta":
		return nil, errors.New("theme.json: esse \"id\" é de um tema embutido")
	}
	m.Name = strings.TrimSpace(m.Name)
	if m.Name == "" || len([]rune(m.Name)) > 40 {
		return nil, errors.New("theme.json: \"name\" deve ter de 1 a 40 caracteres")
	}
	if m.Version != "" && !verRE.MatchString(m.Version) {
		return nil, errors.New("theme.json: \"version\" inválida")
	}
	if len([]rune(m.Author)) > 80 || len([]rune(m.Description)) > 300 {
		return nil, errors.New("theme.json: \"author\" (80) ou \"description\" (300) longos demais")
	}
	if err := validColors(m.Colors, UIKeys, true, "colors"); err != nil {
		return nil, err
	}
	if err := validColors(m.Document, DocKeys, false, "document"); err != nil {
		return nil, err
	}
	for k, v := range m.Colors {
		m.Colors[k] = strings.ToLower(v)
	}
	for k, v := range m.Document {
		m.Document[k] = strings.ToLower(v)
	}
	p := &Package{Manifest: m}
	used := map[string]bool{"theme.json": true}
	load := func(file, what string, docOnly bool) ([]byte, string, error) {
		if file == "" {
			return nil, "", nil
		}
		b, ok := files[file]
		if !ok {
			return nil, "", fmt.Errorf("%s: o arquivo %q não está no pacote", what, file)
		}
		used[file] = true
		typ, ok := SniffImage(b)
		if !ok {
			return nil, "", fmt.Errorf("%s: %q não é uma imagem aceita (PNG, JPEG, GIF, WebP ou SVG sem scripts)", what, file)
		}
		if docOnly && typ != "image/png" && typ != "image/jpeg" && typ != "image/gif" {
			return nil, "", fmt.Errorf("%s: e-mail e PDF aceitam só PNG, JPEG ou GIF", what)
		}
		return b, typ, nil
	}
	if p.LogoUI, p.LogoUIType, err = load(m.Logo.UI, "logo.ui", false); err != nil {
		return nil, err
	}
	if p.LogoDoc, p.LogoDocType, err = load(m.Logo.Document, "logo.document", true); err != nil {
		return nil, err
	}
	if _, _, err = load(m.Preview, "preview", false); err != nil {
		return nil, err
	}
	// Sem logo de documento, mas com logo de interface raster: serve para os dois.
	if p.LogoDoc == nil && p.LogoUI != nil && (p.LogoUIType == "image/png" || p.LogoUIType == "image/jpeg" || p.LogoUIType == "image/gif") {
		p.LogoDoc, p.LogoDocType = p.LogoUI, p.LogoUIType
	}
	var extra []string
	for n := range files {
		if !used[n] && !isDoc(n) {
			extra = append(extra, n)
		}
	}
	if len(extra) > 0 {
		sort.Strings(extra)
		return nil, fmt.Errorf("arquivos não declarados no theme.json: %s", strings.Join(extra, ", "))
	}
	return p, nil
}

func isDoc(n string) bool {
	switch strings.ToLower(n) {
	case "readme.md", "license", "license.txt", "license.md", "readme.txt":
		return true
	}
	return false
}

// Pack monta um .zip a partir de uma pasta (só arquivos da raiz) e valida o resultado.
func Pack(dir string) ([]byte, error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range ents {
		if e.IsDir() || e.Type()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("%s: só arquivos comuns na raiz da pasta", e.Name())
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		h := &zip.FileHeader{Name: e.Name(), Method: zip.Deflate}
		h.SetMode(0o644)
		w, err := zw.CreateHeader(h)
		if err != nil {
			return nil, err
		}
		if _, err = w.Write(b); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	if _, err := Read(buf.Bytes()); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
