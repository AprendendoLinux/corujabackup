package httpapi

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strings"

	"github.com/AprendendoLinux/corujabackup/internal/version"
)

const (
	maxLogoBytes = 1 << 20
	maxThemes    = 10
)

var (
	builtinThemes = map[string]bool{"coruja": true, "noturno": true, "floresta": true}
	themeIDRE     = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,29}$`)
	colorRE       = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
	themeKeys     = []string{"bg", "surface", "text", "muted", "border", "primary", "primary_contrast", "accent", "ok", "danger"}
)

type customTheme struct {
	ID    string            `json:"id"`
	Label string            `json:"label"`
	Vars  map[string]string `json:"vars"`
}

func (s *Server) loadBrand(r *http.Request) (name, theme string, themes []customTheme, logoType, logoHash string) {
	name, theme, themes = version.Name, "coruja", []customTheme{}
	if s.Store == nil {
		return
	}
	m, err := s.Store.GetSettings(r.Context(), "brand.name", "brand.theme", "brand.themes", "brand.logo_type", "brand.logo")
	if err != nil {
		return
	}
	if v := m["brand.name"]; v != "" {
		name = v
	}
	if v := m["brand.theme"]; v != "" {
		theme = v
	}
	if v := m["brand.themes"]; v != "" {
		_ = json.Unmarshal([]byte(v), &themes)
	}
	if lg := m["brand.logo"]; lg != "" {
		h := sha256.Sum256([]byte(lg))
		logoType, logoHash = m["brand.logo_type"], hex.EncodeToString(h[:6])
	}
	return
}

// branding é público (a tela de login precisa do nome, tema e logo antes de autenticar).
func (s *Server) branding(w http.ResponseWriter, r *http.Request) {
	name, theme, themes, lt, lh := s.loadBrand(r)
	out := map[string]any{"name": name, "theme": theme, "themes": themes, "has_logo": lt != ""}
	if lt != "" {
		out["logo_version"] = lh
	}
	writeJSON(w, 200, out)
}

func (s *Server) logo(w http.ResponseWriter, r *http.Request) {
	m, err := s.Store.GetSettings(r.Context(), "brand.logo", "brand.logo_type")
	if err != nil || m["brand.logo"] == "" {
		writeErr(w, 404, "sem logo personalizada")
		return
	}
	raw, err := decodeB64(m["brand.logo"])
	if err != nil {
		writeErr(w, 500, "logo corrompida")
		return
	}
	h := w.Header()
	h.Set("Content-Type", m["brand.logo_type"])
	h.Set("Cache-Control", "public, max-age=300")
	h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
	h.Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(raw)
}

func (s *Server) saveBranding(w http.ResponseWriter, r *http.Request, cu *ctxUser) {
	var in struct {
		Name   string        `json:"name"`
		Theme  string        `json:"theme"`
		Themes []customTheme `json:"themes"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len([]rune(in.Name)) > 60 {
		writeErr(w, 400, "o nome deve ter de 1 a 60 caracteres")
		return
	}
	if len(in.Themes) > maxThemes {
		writeErr(w, 400, "no máximo 10 temas personalizados")
		return
	}
	seen := map[string]bool{}
	custom := map[string]bool{}
	for i := range in.Themes {
		t := &in.Themes[i]
		t.Label = strings.TrimSpace(t.Label)
		if !themeIDRE.MatchString(t.ID) || builtinThemes[t.ID] || seen[t.ID] {
			writeErr(w, 400, "identificador de tema inválido ou repetido: "+t.ID)
			return
		}
		seen[t.ID] = true
		custom[t.ID] = true
		if t.Label == "" || len([]rune(t.Label)) > 40 {
			writeErr(w, 400, "o nome do tema deve ter de 1 a 40 caracteres")
			return
		}
		clean := map[string]string{}
		for _, k := range themeKeys {
			v := t.Vars[k]
			if !colorRE.MatchString(v) {
				writeErr(w, 400, "cor inválida em "+t.Label+" ("+k+"): use o formato #RRGGBB")
				return
			}
			clean[k] = strings.ToLower(v)
		}
		t.Vars = clean
	}
	if in.Theme == "" {
		in.Theme = "coruja"
	}
	if !builtinThemes[in.Theme] && !custom[in.Theme] {
		writeErr(w, 400, "tema padrão inexistente")
		return
	}
	tj, _ := json.Marshal(in.Themes)
	for k, v := range map[string]string{"brand.name": in.Name, "brand.theme": in.Theme, "brand.themes": string(tj)} {
		if err := s.Store.SetSetting(r.Context(), k, v); err != nil {
			writeErr(w, 500, "erro interno")
			return
		}
	}
	s.Store.Audit(r.Context(), cu.User.ID, cu.User.Username, "brand.updated", in.Name+" / "+in.Theme, s.clientIP(r))
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// sniffLogo identifica o formato pelo conteúdo (não confia no Content-Type enviado).
func sniffLogo(b []byte) (string, bool) {
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
		for _, bad := range []string{"<script", "javascript:", "<foreignobject", "onload=", "onerror=", "<iframe", "<!entity"} {
			if strings.Contains(low, bad) {
				return "", false
			}
		}
		return "image/svg+xml", true
	}
	return "", false
}

func (s *Server) uploadLogo(w http.ResponseWriter, r *http.Request, cu *ctxUser) {
	r.Body = http.MaxBytesReader(w, r.Body, maxLogoBytes+1024)
	b, err := io.ReadAll(r.Body)
	if err != nil || len(b) == 0 || len(b) > maxLogoBytes {
		writeErr(w, 400, "a logo deve ter até 1 MiB")
		return
	}
	typ, ok := sniffLogo(b)
	if !ok {
		writeErr(w, 400, "formato de imagem não suportado ou inseguro (use PNG, JPEG, GIF, WebP ou SVG sem scripts)")
		return
	}
	if err := s.Store.SetSetting(r.Context(), "brand.logo", encodeB64(b)); err != nil {
		writeErr(w, 500, "erro interno")
		return
	}
	_ = s.Store.SetSetting(r.Context(), "brand.logo_type", typ)
	s.Store.Audit(r.Context(), cu.User.ID, cu.User.Username, "brand.logo_set", typ, s.clientIP(r))
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) deleteLogo(w http.ResponseWriter, r *http.Request, cu *ctxUser) {
	_ = s.Store.DeleteSetting(r.Context(), "brand.logo")
	_ = s.Store.DeleteSetting(r.Context(), "brand.logo_type")
	s.Store.Audit(r.Context(), cu.User.ID, cu.User.Username, "brand.logo_removed", "", s.clientIP(r))
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func encodeB64(b []byte) string          { return base64.StdEncoding.EncodeToString(b) }
func decodeB64(s string) ([]byte, error) { return base64.StdEncoding.DecodeString(s) }
