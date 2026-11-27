package cloudtest

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/AprendendoLinux/corujabackup/internal/cloud"
)

// Dropbox é um Dropbox falso: OAuth (PKCE e renovação com escopo), conta, arquivos e sessões de upload em partes.
type Dropbox struct {
	*httptest.Server
	Served   atomic.Int64 // bytes de conteúdo entregues nos downloads
	mu       sync.Mutex
	Key      string
	Files    map[string][]byte // caminho completo ("/pasta/nome")
	sessions map[string][]byte
	scopes   map[string]string // access token -> escopo
	seq      int

	LastCode, LastVerifier, LastRedirect, LastSecret string
	ScopesIssued                                     []string
	FailAppends                                      int  // próximos N append respondem 503
	LoseAppendReply                                  bool // grava o próximo append mas responde 503 (força correct_offset)
	CorruptUpload                                    bool // grava conteúdo diferente do recebido
}

func NewDropbox(key string) *Dropbox {
	d := &Dropbox{Key: key, Files: map[string][]byte{}, sessions: map[string][]byte{}, scopes: map[string]string{}}
	d.Server = httptest.NewServer(http.HandlerFunc(d.serve))
	return d
}

// Names lista os arquivos de uma pasta.
func (d *Dropbox) Names(folder string) []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []string
	for p := range d.Files {
		if n, ok := strings.CutPrefix(p, "/"+folder+"/"); ok && !strings.Contains(n, "/") {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}

func (d *Dropbox) meta(path string) map[string]any {
	h := cloud.NewDropboxHash()
	_, _ = h.Write(d.Files[path])
	return map[string]any{".tag": "file", "id": "id:" + path, "name": path[strings.LastIndex(path, "/")+1:], "path_lower": strings.ToLower(path),
		"size": len(d.Files[path]), "content_hash": h.Hex(), "server_modified": "2026-01-02T03:04:05Z"}
}

func conflict(w http.ResponseWriter, summary string, extra map[string]any) {
	writeJSON(w, 409, map[string]any{"error_summary": summary, "error": extra})
}

func (d *Dropbox) token(scope string) string {
	d.seq++
	t := fmt.Sprintf("tok%d", d.seq)
	d.scopes[t] = scope
	return t
}

func (d *Dropbox) serve(w http.ResponseWriter, r *http.Request) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if r.URL.Path == "/oauth2/token" {
		_ = r.ParseForm()
		if r.Form.Get("client_id") != d.Key {
			writeJSON(w, 400, map[string]any{"error": "invalid_client"})
			return
		}
		switch r.Form.Get("grant_type") {
		case "authorization_code":
			d.LastCode, d.LastVerifier, d.LastRedirect, d.LastSecret = r.Form.Get("code"), r.Form.Get("code_verifier"), r.Form.Get("redirect_uri"), r.Form.Get("client_secret")
			if d.LastCode != "codigo-ok" {
				writeJSON(w, 400, map[string]any{"error": "invalid_grant"})
				return
			}
			writeJSON(w, 200, map[string]any{"access_token": d.token("full"), "refresh_token": "dbx-refresh", "expires_in": 14400})
		case "refresh_token":
			if r.Form.Get("refresh_token") != "dbx-refresh" {
				writeJSON(w, 400, map[string]any{"error": "invalid_grant"})
				return
			}
			sc := r.Form.Get("scope")
			if sc == "" {
				sc = "full"
			} else {
				d.ScopesIssued = append(d.ScopesIssued, sc)
			}
			writeJSON(w, 200, map[string]any{"access_token": d.token(sc), "expires_in": 14400}) // sem novo refresh token, como o Dropbox
		default:
			writeJSON(w, 400, map[string]any{"error": "unsupported_grant_type"})
		}
		return
	}
	scope, ok := d.scopes[strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")]
	if !ok {
		writeJSON(w, 401, map[string]any{"error_summary": "expired_access_token/"})
		return
	}
	upload := strings.Contains(r.URL.Path, "/upload_session/")
	if scope != "full" && !upload { // o token do agente só serve para enviar
		writeJSON(w, 401, map[string]any{"error_summary": "missing_scope/"})
		return
	}
	body, _ := io.ReadAll(r.Body)
	var in struct {
		Path   string `json:"path"`
		Cursor struct {
			ID     string `json:"session_id"`
			Offset int64  `json:"offset"`
		} `json:"cursor"`
		Commit struct {
			Path string `json:"path"`
		} `json:"commit"`
	}
	if upload || strings.HasSuffix(r.URL.Path, "/files/download") {
		arg := r.Header.Get("Dropbox-API-Arg")
		for _, c := range arg {
			if c > 0x7e {
				writeJSON(w, 400, map[string]any{"error_summary": "cabeçalho não ASCII"})
				return
			}
		}
		_ = json.Unmarshal([]byte(arg), &in)
	} else {
		_ = json.Unmarshal(body, &in)
	}
	switch strings.TrimPrefix(r.URL.Path, "/2") {
	case "/users/get_current_account":
		writeJSON(w, 200, map[string]any{"email": "dono@dropbox.test"})
	case "/users/get_space_usage":
		writeJSON(w, 200, map[string]any{"used": 1000, "allocation": map[string]any{".tag": "individual", "allocated": 1 << 31}})
	case "/files/upload_session/start":
		d.seq++
		id := fmt.Sprintf("sess%d", d.seq)
		d.sessions[id] = append([]byte(nil), body...)
		writeJSON(w, 200, map[string]any{"session_id": id})
	case "/files/upload_session/append_v2", "/files/upload_session/finish":
		cur, found := d.sessions[in.Cursor.ID]
		if !found {
			conflict(w, "lookup_failed/not_found/", map[string]any{".tag": "lookup_failed"})
			return
		}
		finishing := strings.HasSuffix(r.URL.Path, "/finish")
		if !finishing && d.FailAppends > 0 {
			d.FailAppends--
			w.WriteHeader(503)
			return
		}
		if in.Cursor.Offset != int64(len(cur)) {
			extra := map[string]any{".tag": "incorrect_offset", "correct_offset": len(cur)}
			if finishing {
				extra = map[string]any{".tag": "lookup_failed", "lookup_failed": map[string]any{".tag": "incorrect_offset", "correct_offset": len(cur)}}
			}
			conflict(w, "incorrect_offset/", extra)
			return
		}
		cur = append(cur, body...)
		d.sessions[in.Cursor.ID] = cur
		if !finishing && d.LoseAppendReply {
			d.LoseAppendReply = false
			w.WriteHeader(503)
			return
		}
		if !finishing {
			writeJSON(w, 200, nil)
			return
		}
		if d.CorruptUpload {
			cur = append([]byte(nil), cur...)
			cur[0] ^= 0xff
		}
		d.Files[in.Commit.Path] = cur
		delete(d.sessions, in.Cursor.ID)
		writeJSON(w, 200, d.meta(in.Commit.Path))
	case "/files/get_metadata":
		if _, ok := d.Files[in.Path]; !ok {
			conflict(w, "path/not_found/", nil)
			return
		}
		writeJSON(w, 200, d.meta(in.Path))
	case "/files/list_folder":
		var es []map[string]any
		folders := map[string]bool{}
		for p := range d.Files {
			n, ok := strings.CutPrefix(p, in.Path+"/")
			if !ok {
				continue
			}
			if sub, _, nested := strings.Cut(n, "/"); nested {
				if !folders[sub] {
					folders[sub] = true
					es = append(es, map[string]any{".tag": "folder", "name": sub})
				}
			} else {
				es = append(es, d.meta(p))
			}
		}
		if es == nil {
			conflict(w, "path/not_found/", nil)
			return
		}
		writeJSON(w, 200, map[string]any{"entries": es, "has_more": false, "cursor": "c"})
	case "/files/delete_v2":
		if _, ok := d.Files[in.Path]; !ok {
			conflict(w, "path_lookup/not_found/", nil)
			return
		}
		delete(d.Files, in.Path)
		writeJSON(w, 200, map[string]any{})
	case "/files/download":
		c, ok := d.Files[in.Path]
		if !ok {
			conflict(w, "path/not_found/", nil)
			return
		}
		serveRange(w, r, c, &d.Served)
	default:
		writeJSON(w, 404, map[string]any{"error_summary": "rota desconhecida " + r.URL.Path})
	}
}
