package httpapi

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/AprendendoLinux/corujabackup/internal/report"
	"github.com/AprendendoLinux/corujabackup/internal/themepkg"
	"github.com/AprendendoLinux/corujabackup/internal/version"
)

const (
	maxLogoBytes = 1 << 20
	maxThemes    = 10
)

var (
	// Temas embutidos (cada um com modo claro e escuro). "noturno" é o antigo tema escuro da Coruja: vira coruja + modo escuro.
	builtinThemes = map[string]bool{"coruja": true, "floresta": true}
	themeModes    = map[string]bool{"auto": true, "light": true, "dark": true}
	themeIDRE     = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,29}$`)
	colorRE       = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
	themeKeys     = []string{"bg", "surface", "text", "muted", "border", "primary", "primary_contrast", "accent", "ok", "danger"}
)

// themeMeta existe só em temas vindos de um pacote .zip; o cliente nunca a define (o servidor preserva a armazenada).
type themeMeta struct {
	Version     string            `json:"version,omitempty"`
	Author      string            `json:"author,omitempty"`
	Description string            `json:"description,omitempty"`
	Doc         map[string]string `json:"doc,omitempty"` // cores de e-mail/PDF
	LogoUI      string            `json:"logo_ui,omitempty"`
	LogoDoc     string            `json:"logo_doc,omitempty"`
	AssetVer    string            `json:"asset_ver,omitempty"`
}

type customTheme struct {
	ID       string            `json:"id"`
	Label    string            `json:"label"`
	Vars     map[string]string `json:"vars"`
	VarsDark map[string]string `json:"vars_dark,omitempty"`
	Pkg      *themeMeta        `json:"pkg,omitempty"`
}

// withDark garante o modo escuro (temas importados antes dele existir ganham um gerado a partir das cores claras).
func withDark(list []customTheme) []customTheme {
	for i := range list {
		if len(list[i].VarsDark) == 0 && len(list[i].Vars) > 0 {
			list[i].VarsDark = themepkg.DeriveDark(list[i].Vars)
		}
	}
	return list
}

func assetKey(id, kind string) string { return "brand.asset." + id + "." + kind }

func (s *Server) storedThemes(r *http.Request) []customTheme {
	var out []customTheme
	if m, err := s.Store.GetSettings(r.Context(), "brand.themes"); err == nil && m["brand.themes"] != "" {
		_ = json.Unmarshal([]byte(m["brand.themes"]), &out)
	}
	return out
}

func (s *Server) dropAssets(r *http.Request, id string) {
	for _, k := range []string{"ui", "ui_type", "doc", "doc_type"} {
		_ = s.Store.DeleteSetting(r.Context(), assetKey(id, k))
	}
}

// themeLogo serve o logo de um tema de pacote (?kind=doc para a versão de e-mail/PDF). Público, como o logo geral.
func (s *Server) themeLogo(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	kind := "ui"
	if r.URL.Query().Get("kind") == "doc" {
		kind = "doc"
	}
	if !themeIDRE.MatchString(id) {
		writeErr(w, 404, "sem logo")
		return
	}
	m, err := s.Store.GetSettings(r.Context(), assetKey(id, kind), assetKey(id, kind+"_type"))
	if err != nil || m[assetKey(id, kind)] == "" {
		writeErr(w, 404, "sem logo")
		return
	}
	raw, err := decodeB64(m[assetKey(id, kind)])
	if err != nil {
		writeErr(w, 500, "logo corrompida")
		return
	}
	h := w.Header()
	h.Set("Content-Type", m[assetKey(id, kind+"_type")])
	h.Set("Cache-Control", "public, max-age=300")
	h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
	h.Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(raw)
}

// importTheme recebe o .zip cru no corpo, valida e instala (ou atualiza, se o id já existir) o tema.
func (s *Server) importTheme(w http.ResponseWriter, r *http.Request, cu *ctxUser) {
	r.Body = http.MaxBytesReader(w, r.Body, themepkg.MaxZipBytes+1024)
	b, err := io.ReadAll(r.Body)
	if err != nil {
		writeErr(w, 400, "o pacote deve ter até 3 MiB")
		return
	}
	pk, err := themepkg.Read(b)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	list := s.storedThemes(r)
	idx := -1
	for i, t := range list {
		if t.ID == pk.ID {
			idx = i
		}
	}
	if idx < 0 && len(list) >= maxThemes {
		writeErr(w, 400, "no máximo 10 temas personalizados: exclua um antes de importar")
		return
	}
	sum := sha256.Sum256(b)
	meta := &themeMeta{Version: pk.Version, Author: pk.Author, Description: pk.Description, Doc: pk.Document, AssetVer: hex.EncodeToString(sum[:5])}
	s.dropAssets(r, pk.ID)
	put := func(kind string, data []byte, typ string) bool {
		if data == nil {
			return true
		}
		if s.Store.SetSetting(r.Context(), assetKey(pk.ID, kind), encodeB64(data)) != nil || s.Store.SetSetting(r.Context(), assetKey(pk.ID, kind+"_type"), typ) != nil {
			return false
		}
		return true
	}
	if !put("ui", pk.LogoUI, pk.LogoUIType) || !put("doc", pk.LogoDoc, pk.LogoDocType) {
		writeErr(w, 500, "erro interno")
		return
	}
	meta.LogoUI, meta.LogoDoc = pk.LogoUIType, pk.LogoDocType
	dark := pk.ColorsDark
	if dark == nil {
		dark = themepkg.DeriveDark(pk.Colors)
	}
	t := customTheme{ID: pk.ID, Label: pk.Name, Vars: pk.Colors, VarsDark: dark, Pkg: meta}
	if idx >= 0 {
		list[idx] = t
	} else {
		list = append(list, t)
	}
	tj, _ := json.Marshal(list)
	if s.Store.SetSetting(r.Context(), "brand.themes", string(tj)) != nil {
		writeErr(w, 500, "erro interno")
		return
	}
	s.Store.Audit(r.Context(), cu.User.ID, cu.User.Username, "brand.theme_imported", pk.ID+" "+pk.Version, s.clientIP(r))
	writeJSON(w, 200, map[string]any{"ok": true, "theme": t, "replaced": idx >= 0})
}

func (s *Server) loadBrand(r *http.Request) (name, theme, mode string, themes []customTheme, logoType, logoHash string) {
	name, theme, mode, themes = version.Name, "coruja", "auto", []customTheme{}
	if s.Store == nil {
		return
	}
	m, err := s.Store.GetSettings(r.Context(), "brand.name", "brand.theme", "brand.mode", "brand.themes", "brand.logo_type", "brand.logo")
	if err != nil {
		return
	}
	if v := m["brand.name"]; v != "" {
		name = v
	}
	if v := m["brand.theme"]; v != "" {
		theme = v
	}
	if themeModes[m["brand.mode"]] {
		mode = m["brand.mode"]
	}
	if theme == "noturno" {
		theme = "coruja"
		if m["brand.mode"] == "" {
			mode = "dark"
		}
	}
	if v := m["brand.themes"]; v != "" {
		_ = json.Unmarshal([]byte(v), &themes)
	}
	themes = withDark(themes)
	if lg := m["brand.logo"]; lg != "" {
		h := sha256.Sum256([]byte(lg))
		logoType, logoHash = m["brand.logo_type"], hex.EncodeToString(h[:6])
	}
	return
}

// credit são os créditos do rodapé (interface, e-mails e PDF). Name vazio = sem créditos; URL vazia = sem link.
type credit struct {
	Label string `json:"label"`
	Name  string `json:"name"`
	URL   string `json:"url"`
}

func defaultCredit() credit {
	return credit{Label: report.DefaultCreditLabel, Name: report.DefaultCreditName, URL: report.DefaultCreditURL}
}

func (s *Server) loadCredit(r *http.Request) credit {
	c := defaultCredit()
	if s.Store == nil {
		return c
	}
	if m, err := s.Store.GetSettings(r.Context(), "brand.credit"); err == nil && m["brand.credit"] != "" {
		_ = json.Unmarshal([]byte(m["brand.credit"]), &c)
	}
	return c
}

// cleanCredit valida os créditos; devolve a mensagem de erro ou "".
func cleanCredit(c *credit) string {
	c.Label, c.Name, c.URL = strings.TrimSpace(c.Label), strings.TrimSpace(c.Name), strings.TrimSpace(c.URL)
	if len([]rune(c.Label)) > 40 || len([]rune(c.Name)) > 80 || strings.ContainsAny(c.Label+c.Name+c.URL, "\x00\r\n<>") {
		return "créditos inválidos: rótulo até 40 caracteres e nome até 80, sem < e >"
	}
	if c.URL != "" {
		u, err := url.Parse(c.URL)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || len(c.URL) > 200 || strings.ContainsAny(c.URL, "\"' ") {
			return "o link dos créditos deve começar com https:// ou http://"
		}
	}
	if c.Name == "" {
		c.URL = ""
	}
	return ""
}

// branding é público (a tela de login precisa do nome, tema e logo antes de autenticar).
func (s *Server) branding(w http.ResponseWriter, r *http.Request) {
	name, theme, mode, themes, lt, lh := s.loadBrand(r)
	out := map[string]any{"name": name, "theme": theme, "mode": mode, "themes": themes, "has_logo": lt != "", "credit": s.loadCredit(r)}
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
		Credit *credit       `json:"credit"`
		Mode   string        `json:"mode"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len([]rune(in.Name)) > 60 {
		writeErr(w, 400, "o nome deve ter de 1 a 60 caracteres")
		return
	}
	cred := s.loadCredit(r)
	if in.Credit != nil {
		cred = *in.Credit
	}
	if msg := cleanCredit(&cred); msg != "" {
		writeErr(w, 400, msg)
		return
	}
	cj, _ := json.Marshal(cred)
	if len(in.Themes) > maxThemes {
		writeErr(w, 400, "no máximo 10 temas personalizados")
		return
	}
	// Temas só entram por pacote (.zip): aqui o cliente apenas mantém ou exclui os já instalados. Cores, nomes e metadados
	// vêm sempre do que está guardado, nunca do cliente.
	storedList := s.storedThemes(r)
	byID := map[string]customTheme{}
	stored := map[string]*themeMeta{}
	for _, t := range storedList {
		byID[t.ID] = t
		stored[t.ID] = t.Pkg
	}
	custom := map[string]bool{}
	kept := make([]customTheme, 0, len(in.Themes))
	for _, t := range in.Themes {
		st, ok := byID[t.ID]
		if !ok {
			writeErr(w, 400, "tema inexistente: "+t.ID+" (temas novos entram importando um pacote .zip)")
			return
		}
		if custom[t.ID] {
			continue
		}
		custom[t.ID] = true
		kept = append(kept, st)
	}
	in.Themes = kept
	if in.Mode == "" {
		_, _, in.Mode, _, _, _ = s.loadBrand(r)
	}
	if !themeModes[in.Mode] {
		writeErr(w, 400, "modo inválido (auto, light ou dark)")
		return
	}
	if in.Theme == "noturno" {
		in.Theme, in.Mode = "coruja", "dark"
	}
	if in.Theme == "" {
		in.Theme = "coruja"
	}
	if !builtinThemes[in.Theme] && !custom[in.Theme] {
		writeErr(w, 400, "tema padrão inexistente")
		return
	}
	for id, pk := range stored {
		if pk != nil && !custom[id] {
			s.dropAssets(r, id)
		}
	}
	tj, _ := json.Marshal(in.Themes)
	for k, v := range map[string]string{"brand.name": in.Name, "brand.theme": in.Theme, "brand.themes": string(tj), "brand.credit": string(cj), "brand.mode": in.Mode} {
		if err := s.Store.SetSetting(r.Context(), k, v); err != nil {
			writeErr(w, 500, "erro interno")
			return
		}
	}
	s.Store.Audit(r.Context(), cu.User.ID, cu.User.Username, "brand.updated", in.Name+" / "+in.Theme, s.clientIP(r))
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// sniffLogo identifica o formato pelo conteúdo (não confia no Content-Type enviado).
func sniffLogo(b []byte) (string, bool) { return themepkg.SniffImage(b) }

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
