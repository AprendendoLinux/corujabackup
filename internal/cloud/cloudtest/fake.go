// Package cloudtest traz servidores HTTP falsos que imitam, com fidelidade, os trechos das APIs do OneDrive (Microsoft
// Graph) e do Google Drive usados pelo Coruja Backup: OAuth device code, sessões de upload retomáveis (com as regras
// de tamanho de bloco e Content-Range), hashes, listagem e exclusão. Só para testes.
package cloudtest

import (
	"crypto/md5" //nolint:gosec
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"

	"github.com/AprendendoLinux/corujabackup/internal/cloud"
)

type File struct {
	ID      string
	Path    string // pasta/nome
	Name    string
	Content []byte
}

type session struct {
	path     string
	name     string
	total    int64
	received []byte
}

// Common reúne o estado e os ganchos de falha compartilhados pelos dois falsos.
type Common struct {
	*httptest.Server
	mu       sync.Mutex
	Files    map[string]*File // chave: caminho completo
	Folders  map[string]string
	sessions map[string]*session
	seq      int

	// OAuth
	ClientID     string
	PollsUntilOK int
	polls        int
	RefreshCount int
	valid        map[string]bool
	RefreshToken string
	IssuedTokens int

	// Falhas injetáveis
	FailPuts          int  // próximos N PUTs de bloco respondem 503
	ExpireSessions    bool // sessões respondem 404
	NoHashes          bool
	Deleted           []string
	PermanentlyDelete []string
}

func (c *Common) nextID(p string) string { c.seq++; return fmt.Sprintf("%s%d", p, c.seq) }

func (c *Common) init() {
	c.Files, c.Folders, c.sessions, c.valid = map[string]*File{}, map[string]string{}, map[string]*session{}, map[string]bool{}
	c.RefreshToken = "refresh-0"
}

// Put grava um arquivo diretamente (para preparar cenários de retenção).
func (c *Common) Put(folder, name string, content []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	id := c.nextID("f")
	c.Files[folder+"/"+name] = &File{ID: id, Path: folder + "/" + name, Name: name, Content: content}
	c.ensure(folder)
}

func (c *Common) ensure(folder string) {
	p := ""
	for _, s := range strings.Split(folder, "/") {
		if p != "" {
			p += "/"
		}
		p += s
		if _, ok := c.Folders[p]; !ok {
			c.Folders[p] = c.nextID("d")
		}
	}
}

func (c *Common) Names(folder string) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []string
	for _, f := range c.Files {
		if strings.TrimSuffix(f.Path, "/"+f.Name) == folder {
			out = append(out, f.Name)
		}
	}
	return out
}

func (c *Common) Content(folder, name string) []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	if f := c.Files[folder+"/"+name]; f != nil {
		return f.Content
	}
	return nil
}

func (c *Common) newToken() (string, string) {
	c.IssuedTokens++
	acc := fmt.Sprintf("access-%d", c.IssuedTokens)
	c.valid = map[string]bool{acc: true} // o token anterior deixa de valer
	return acc, c.RefreshToken
}

func (c *Common) authed(r *http.Request) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.valid[strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")]
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// handleOAuth cobre /devicecode e /token (mesma forma para Microsoft e Google).
func (c *Common) handleOAuth(w http.ResponseWriter, r *http.Request) bool {
	switch r.URL.Path {
	case "/devicecode", "/device/code":
		_ = r.ParseForm()
		if r.Form.Get("client_id") != c.ClientID {
			writeJSON(w, 400, map[string]any{"error": "invalid_client"})
			return true
		}
		writeJSON(w, 200, map[string]any{"device_code": "DEVCODE", "user_code": "ABCD-1234", "verification_uri": "https://example.test/device", "verification_url": "https://example.test/device", "expires_in": 900, "interval": 1})
		return true
	case "/token":
		_ = r.ParseForm()
		c.mu.Lock()
		defer c.mu.Unlock()
		if r.Form.Get("client_id") != c.ClientID {
			writeJSON(w, 400, map[string]any{"error": "invalid_client"})
			return true
		}
		switch r.Form.Get("grant_type") {
		case "urn:ietf:params:oauth:grant-type:device_code":
			if r.Form.Get("device_code") != "DEVCODE" {
				writeJSON(w, 400, map[string]any{"error": "bad_verification_code"})
				return true
			}
			if c.polls++; c.polls <= c.PollsUntilOK {
				writeJSON(w, 400, map[string]any{"error": "authorization_pending"})
				return true
			}
			acc, ref := c.newToken()
			writeJSON(w, 200, map[string]any{"access_token": acc, "refresh_token": ref, "expires_in": 3600, "token_type": "Bearer"})
		case "refresh_token":
			if r.Form.Get("refresh_token") != c.RefreshToken {
				writeJSON(w, 400, map[string]any{"error": "invalid_grant", "error_description": "refresh token inválido"})
				return true
			}
			c.RefreshCount++
			c.RefreshToken = fmt.Sprintf("refresh-%d", c.RefreshCount) // a Microsoft rotaciona o refresh token
			acc, ref := c.newToken()
			writeJSON(w, 200, map[string]any{"access_token": acc, "refresh_token": ref, "expires_in": 3600})
		default:
			writeJSON(w, 400, map[string]any{"error": "unsupported_grant_type"})
		}
		return true
	}
	return false
}

// chunkPut implementa o PUT de blocos (regras comuns). align = múltiplo exigido para blocos que não são o último.
func (c *Common) chunkPut(w http.ResponseWriter, r *http.Request, sid string, align int64, google bool, finish func(s *session) any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := c.sessions[sid]
	if s == nil || c.ExpireSessions {
		writeJSON(w, 404, map[string]any{"error": "sessão inexistente ou expirada"})
		return
	}
	cr := r.Header.Get("Content-Range")
	if google && strings.HasPrefix(cr, "bytes */") { // consulta de status
		var qt int64
		fmt.Sscanf(cr, "bytes */%d", &qt)
		if s.total <= 0 {
			s.total = qt
		}
		if int64(len(s.received)) == s.total && s.total > 0 {
			writeJSON(w, 200, finish(s))
			return
		}
		if len(s.received) > 0 {
			w.Header().Set("Range", fmt.Sprintf("bytes=0-%d", len(s.received)-1))
		}
		w.WriteHeader(308)
		return
	}
	var a, b, total int64
	if _, err := fmt.Sscanf(cr, "bytes %d-%d/%d", &a, &b, &total); err != nil {
		writeJSON(w, 400, map[string]any{"error": "Content-Range inválido"})
		return
	}
	if c.FailPuts > 0 {
		c.FailPuts--
		w.WriteHeader(503)
		return
	}
	if s.total <= 0 {
		s.total = total
	}
	body, _ := io.ReadAll(r.Body)
	if total != s.total || int64(len(body)) != b-a+1 {
		writeJSON(w, 400, map[string]any{"error": "Content-Range não confere com o corpo ou com o tamanho total"})
		return
	}
	if a != int64(len(s.received)) {
		writeJSON(w, 416, map[string]any{"error": "o bloco deve começar em " + strconv.Itoa(len(s.received))})
		return
	}
	last := b+1 == total
	if !last && int64(len(body))%align != 0 {
		writeJSON(w, 400, map[string]any{"error": fmt.Sprintf("blocos intermediários precisam ser múltiplos de %d bytes", align)})
		return
	}
	s.received = append(s.received, body...)
	if last {
		writeJSON(w, 201, finish(s))
		return
	}
	if google {
		w.Header().Set("Range", fmt.Sprintf("bytes=0-%d", len(s.received)-1))
		w.WriteHeader(308)
		return
	}
	writeJSON(w, 202, map[string]any{"nextExpectedRanges": []string{fmt.Sprintf("%d-", len(s.received))}})
}

func md5hex(b []byte) string { h := md5.Sum(b); return hex.EncodeToString(h[:]) } //nolint:gosec

// ------------------------------------------------------------------------ Microsoft Graph (OneDrive)

type Graph struct{ *Common }

func NewGraph(clientID string) *Graph {
	g := &Graph{&Common{ClientID: clientID}}
	g.init()
	g.Server = httptest.NewServer(http.HandlerFunc(g.serve))
	return g
}

func (g *Graph) GraphURL() string { return g.URL + "/v1.0" }

func (g *Graph) hashes(content []byte) map[string]any {
	if g.NoHashes {
		return map[string]any{}
	}
	s := cloud.NewSums()
	s.Write(content)
	d := s.Digest()
	return map[string]any{"quickXorHash": d.QuickXor}
}

func (g *Graph) item(f *File) map[string]any {
	return map[string]any{"id": f.ID, "name": f.Name, "size": len(f.Content), "file": map[string]any{"hashes": g.hashes(f.Content)}, "lastModifiedDateTime": "2026-11-25T00:00:00Z"}
}

func (g *Graph) serve(w http.ResponseWriter, r *http.Request) {
	if g.handleOAuth(w, r) {
		return
	}
	if strings.HasPrefix(r.URL.Path, "/upload/") {
		sid := strings.TrimPrefix(r.URL.Path, "/upload/")
		if r.Method == "GET" {
			g.mu.Lock()
			s := g.sessions[sid]
			g.mu.Unlock()
			if s == nil {
				writeJSON(w, 404, map[string]any{})
				return
			}
			writeJSON(w, 200, map[string]any{"nextExpectedRanges": []string{fmt.Sprintf("%d-", len(s.received))}})
			return
		}
		g.chunkPut(w, r, sid, 327680, false, func(s *session) any {
			id := g.nextID("f")
			f := &File{ID: id, Path: s.path + "/" + s.name, Name: s.name, Content: s.received}
			g.Files[f.Path] = f
			return g.item(f)
		})
		return
	}
	if !g.authed(r) {
		writeJSON(w, 401, map[string]any{"error": map[string]any{"code": "InvalidAuthenticationToken"}})
		return
	}
	p := strings.TrimPrefix(r.URL.Path, "/v1.0")
	g.mu.Lock()
	defer g.mu.Unlock()
	switch {
	case p == "/me":
		writeJSON(w, 200, map[string]any{"displayName": "Fulano", "userPrincipalName": "fulano@exemplo.onmicrosoft.com"})
	case p == "/me/drive":
		writeJSON(w, 200, map[string]any{"quota": map[string]any{"total": 1 << 40, "used": 5 << 30}})
	case strings.HasPrefix(p, "/me/drive/items/"):
		rest := strings.TrimPrefix(p, "/me/drive/items/")
		id, perm := strings.TrimSuffix(rest, "/permanentDelete"), strings.HasSuffix(rest, "/permanentDelete")
		for k, f := range g.Files {
			if f.ID == id {
				if perm {
					if g.permDeleteUnsupported() {
						writeJSON(w, 405, map[string]any{"error": "permanentDelete indisponível"})
						return
					}
					g.PermanentlyDelete = append(g.PermanentlyDelete, f.Name)
				} else {
					g.Deleted = append(g.Deleted, f.Name)
				}
				delete(g.Files, k)
				w.WriteHeader(204)
				return
			}
		}
		writeJSON(w, 404, map[string]any{"error": "item não encontrado"})
	case strings.HasPrefix(p, "/me/drive/root:/"):
		rest := strings.TrimPrefix(p, "/me/drive/root:/")
		i := strings.Index(rest, ":")
		if i < 0 {
			writeJSON(w, 400, map[string]any{})
			return
		}
		path, tail := rest[:i], rest[i+1:]
		switch {
		case tail == "/createUploadSession" && r.Method == "POST":
			folder, name := path[:strings.LastIndex(path, "/")], path[strings.LastIndex(path, "/")+1:]
			sid := g.nextID("s")
			var body struct{ Item map[string]any }
			_ = json.NewDecoder(r.Body).Decode(&body)
			g.sessions[sid] = &session{path: folder, name: name, total: -1} // o total chega no primeiro bloco
			writeJSON(w, 200, map[string]any{"uploadUrl": g.URL + "/upload/" + sid, "expirationDateTime": "2099-01-01T00:00:00Z"})
		case tail == "/children" && r.Method == "GET":
			if _, ok := g.Folders[path]; !ok {
				writeJSON(w, 404, map[string]any{"error": "pasta inexistente"})
				return
			}
			var vals []any
			for _, f := range g.Files {
				if strings.TrimSuffix(f.Path, "/"+f.Name) == path {
					vals = append(vals, g.item(f))
				}
			}
			writeJSON(w, 200, map[string]any{"value": vals})
		case tail == "/children" && r.Method == "POST":
			var b struct{ Name string }
			_ = json.NewDecoder(r.Body).Decode(&b)
			g.ensure(path + "/" + b.Name)
			writeJSON(w, 201, map[string]any{"name": b.Name})
		case tail == "" && r.Method == "GET":
			if f := g.Files[path]; f != nil {
				writeJSON(w, 200, g.item(f))
			} else if id, ok := g.Folders[path]; ok {
				writeJSON(w, 200, map[string]any{"id": id, "name": path, "folder": map[string]any{}})
			} else {
				writeJSON(w, 404, map[string]any{"error": "não encontrado"})
			}
		default:
			writeJSON(w, 400, map[string]any{"error": "rota não suportada pelo falso: " + p})
		}
	case p == "/me/drive/root/children" && r.Method == "POST":
		var b struct{ Name string }
		_ = json.NewDecoder(r.Body).Decode(&b)
		g.ensure(b.Name)
		writeJSON(w, 201, map[string]any{"name": b.Name})
	default:
		writeJSON(w, 400, map[string]any{"error": "rota não suportada pelo falso: " + r.Method + " " + p})
	}
}

// NoPermanentDelete simula conta pessoal (sem permanentDelete).
var noPermanent sync.Map

func (g *Graph) SetNoPermanentDelete(v bool) { noPermanent.Store(g, v) }
func (g *Graph) permDeleteUnsupported() bool { v, _ := noPermanent.Load(g); b, _ := v.(bool); return b }

// ------------------------------------------------------------------------ Google Drive

type Drive struct{ *Common }

func NewDrive(clientID string) *Drive {
	d := &Drive{&Common{ClientID: clientID}}
	d.init()
	d.Server = httptest.NewServer(http.HandlerFunc(d.serve))
	return d
}

func (d *Drive) APIURL() string    { return d.URL + "/drive/v3" }
func (d *Drive) UploadURL() string { return d.URL + "/upload/drive/v3" }

func (d *Drive) fileJSON(f *File) map[string]any {
	m := map[string]any{"id": f.ID, "name": f.Name, "size": strconv.Itoa(len(f.Content)), "createdTime": "2026-11-25T00:00:00Z"}
	if !d.NoHashes {
		m["md5Checksum"] = md5hex(f.Content)
	}
	return m
}

// parseQ extrai o que o cliente usa na consulta q=.
func parseQ(q string) (name, parent string, wantFolder, notFolder bool) {
	get := func(prefix string) string {
		i := strings.Index(q, prefix)
		if i < 0 {
			return ""
		}
		rest := q[i+len(prefix):]
		j := strings.Index(rest, "'")
		if j < 0 {
			return ""
		}
		return strings.ReplaceAll(rest[:j], `\'`, `'`)
	}
	name = get("name='")
	if i := strings.Index(q, "' in parents"); i > 0 {
		k := strings.LastIndex(q[:i], "'")
		parent = q[k+1 : i]
	}
	wantFolder = strings.Contains(q, "mimeType='application/vnd.google-apps.folder'")
	notFolder = strings.Contains(q, "mimeType!='application/vnd.google-apps.folder'")
	return
}

func (d *Drive) serve(w http.ResponseWriter, r *http.Request) {
	if d.handleOAuth(w, r) {
		return
	}
	if strings.HasPrefix(r.URL.Path, "/session/") {
		sid := strings.TrimPrefix(r.URL.Path, "/session/")
		d.chunkPut(w, r, sid, 262144, true, func(s *session) any {
			id := d.nextID("f")
			f := &File{ID: id, Path: s.path + "/" + s.name, Name: s.name, Content: s.received}
			d.Files[f.Path] = f
			return d.fileJSON(f)
		})
		return
	}
	if !d.authed(r) {
		writeJSON(w, 401, map[string]any{"error": map[string]any{"code": 401}})
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	pth := r.URL.Path
	switch {
	case pth == "/drive/v3/about":
		writeJSON(w, 200, map[string]any{"user": map[string]any{"emailAddress": "fulano@gmail.com", "displayName": "Fulano"}, "storageQuota": map[string]any{"limit": strconv.Itoa(15 << 30), "usage": strconv.Itoa(2 << 30)}})
	case pth == "/drive/v3/files" && r.Method == "GET":
		name, parent, wantFolder, notFolder := parseQ(r.URL.Query().Get("q"))
		var out []any
		if wantFolder {
			for path, id := range d.Folders {
				seg := path[strings.LastIndex(path, "/")+1:]
				par := "root"
				if i := strings.LastIndex(path, "/"); i > 0 {
					par = d.Folders[path[:i]]
				}
				if seg == name && par == parent {
					out = append(out, map[string]any{"id": id, "name": seg})
				}
			}
		} else if notFolder {
			for _, f := range d.Files {
				dir := strings.TrimSuffix(f.Path, "/"+f.Name)
				if d.Folders[dir] == parent && (name == "" || f.Name == name) {
					out = append(out, d.fileJSON(f))
				}
			}
		}
		writeJSON(w, 200, map[string]any{"files": out})
	case pth == "/drive/v3/files" && r.Method == "POST":
		var b struct {
			Name    string
			Parents []string
		}
		_ = json.NewDecoder(r.Body).Decode(&b)
		parentPath := ""
		for p, id := range d.Folders {
			if id == b.Parents[0] {
				parentPath = p
			}
		}
		full := b.Name
		if parentPath != "" {
			full = parentPath + "/" + b.Name
		}
		d.ensure(full)
		writeJSON(w, 200, map[string]any{"id": d.Folders[full]})
	case strings.HasPrefix(pth, "/drive/v3/files/") && r.Method == "DELETE":
		id := strings.TrimPrefix(pth, "/drive/v3/files/")
		for k, f := range d.Files {
			if f.ID == id {
				d.PermanentlyDelete = append(d.PermanentlyDelete, f.Name)
				delete(d.Files, k)
				w.WriteHeader(204)
				return
			}
		}
		writeJSON(w, 404, map[string]any{"error": "não encontrado"})
	case pth == "/upload/drive/v3/files" && r.Method == "POST":
		var b struct {
			Name    string
			Parents []string
		}
		_ = json.NewDecoder(r.Body).Decode(&b)
		folder := ""
		for p, id := range d.Folders {
			if id == b.Parents[0] {
				folder = p
			}
		}
		sid := d.nextID("s")
		d.sessions[sid] = &session{path: folder, name: b.Name, total: -1}
		w.Header().Set("Location", d.URL+"/session/"+sid)
		w.WriteHeader(200)
	default:
		writeJSON(w, 400, map[string]any{"error": "rota não suportada pelo falso: " + r.Method + " " + pth})
	}
}
