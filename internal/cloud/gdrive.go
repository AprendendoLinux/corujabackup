package cloud

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	gdriveAPI    = "https://www.googleapis.com/drive/v3"
	gdriveUpload = "https://www.googleapis.com/upload/drive/v3"
	gdriveChunk  = 32 * 262144 // 8 MiB: múltiplo de 256 KiB
	// drive.file: o app só enxerga o que ele mesmo criou (suficiente para backups e mais seguro que acesso total).
	gdriveScope = "https://www.googleapis.com/auth/drive.file https://www.googleapis.com/auth/userinfo.email"
	folderMIME  = "application/vnd.google-apps.folder"
)

type GDrive struct {
	TS     Access
	HTTP   *http.Client
	API    string
	Upload string

	mu      sync.Mutex
	folders map[string]string // caminho -> id (cache)
}

func GDriveOAuth(clientID, clientSecret string) *OAuth {
	return &OAuth{ClientID: clientID, ClientSecret: clientSecret, Scope: gdriveScope,
		DeviceURL: "https://oauth2.googleapis.com/device/code", AuthURL: "https://accounts.google.com/o/oauth2/v2/auth", TokenURL: "https://oauth2.googleapis.com/token",
		AuthExtra: url.Values{"access_type": {"offline"}, "prompt": {"consent"}, "include_granted_scopes": {"false"}}}
}

func (g *GDrive) Type() string { return TypeGDrive }

func (g *GDrive) http() *http.Client {
	if g.HTTP != nil {
		return g.HTTP
	}
	return &http.Client{Timeout: 60 * time.Second}
}

func (g *GDrive) api() string {
	if g.API != "" {
		return strings.TrimRight(g.API, "/")
	}
	return gdriveAPI
}

func (g *GDrive) upload() string {
	if g.Upload != "" {
		return strings.TrimRight(g.Upload, "/")
	}
	return gdriveUpload
}

func (g *GDrive) do(ctx context.Context, method, rawURL string, body any, out any, op string, hdr http.Header) (*http.Response, error) {
	tok, err := g.TS.Access(ctx)
	if err != nil {
		return nil, err
	}
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, rd)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	if body != nil {
		req.Header.Set("Content-Type", "application/json; charset=UTF-8")
	}
	for k, v := range hdr {
		req.Header[k] = v
	}
	resp, err := g.http().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode >= 300 {
		return resp, &APIError{Status: resp.StatusCode, Body: string(b), Op: op}
	}
	if out != nil && len(b) > 0 {
		if err := json.Unmarshal(b, out); err != nil {
			return resp, fmt.Errorf("%s: resposta inválida: %w", op, err)
		}
	}
	return resp, nil
}

func (g *GDrive) Account(ctx context.Context) (string, error) {
	var d struct {
		User struct {
			Email string `json:"emailAddress"`
			Name  string `json:"displayName"`
		} `json:"user"`
	}
	if _, err := g.do(ctx, "GET", g.api()+"/about?fields=user(emailAddress,displayName)", nil, &d, "conta Google Drive", nil); err != nil {
		return "", err
	}
	if d.User.Email != "" {
		return d.User.Email, nil
	}
	return d.User.Name, nil
}

func (g *GDrive) Quota(ctx context.Context) (Quota, error) {
	var d struct {
		Q struct {
			Limit string `json:"limit"`
			Usage string `json:"usage"`
		} `json:"storageQuota"`
	}
	if _, err := g.do(ctx, "GET", g.api()+"/about?fields=storageQuota(limit,usage)", nil, &d, "cota Google Drive", nil); err != nil {
		return Quota{}, err
	}
	l, _ := strconv.ParseInt(d.Q.Limit, 10, 64)
	u, _ := strconv.ParseInt(d.Q.Usage, 10, 64)
	return Quota{Total: l, Used: u}, nil
}

func gq(s string) string { return strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), `'`, `\'`) }

type gfile struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Size    string `json:"size"`
	MD5     string `json:"md5Checksum"`
	Created string `json:"createdTime"`
}

func (f gfile) remote() RemoteFile {
	sz, _ := strconv.ParseInt(f.Size, 10, 64)
	t, _ := time.Parse(time.RFC3339, f.Created)
	return RemoteFile{ID: f.ID, Name: f.Name, Size: sz, MD5: strings.ToLower(f.MD5), Modified: t}
}

func (g *GDrive) listQ(ctx context.Context, q, fields string) ([]gfile, error) {
	var out []gfile
	tokenParam := ""
	for i := 0; i < maxListPagesSafe; i++ {
		u := g.api() + "/files?pageSize=1000&spaces=drive&q=" + url.QueryEscape(q) + "&fields=" + url.QueryEscape("nextPageToken,files("+fields+")") + tokenParam
		var page struct {
			Files []gfile `json:"files"`
			Next  string  `json:"nextPageToken"`
		}
		if _, err := g.do(ctx, "GET", u, nil, &page, "listar Google Drive", nil); err != nil {
			return nil, err
		}
		out = append(out, page.Files...)
		if page.Next == "" {
			break
		}
		tokenParam = "&pageToken=" + url.QueryEscape(page.Next)
	}
	return out, nil
}

// folderID resolve (e, se create, cria) a cadeia de pastas a partir da raiz do Drive.
func (g *GDrive) folderID(ctx context.Context, folder string, create bool) (string, error) {
	g.mu.Lock()
	if g.folders == nil {
		g.folders = map[string]string{}
	}
	if id, ok := g.folders[folder]; ok {
		g.mu.Unlock()
		return id, nil
	}
	g.mu.Unlock()
	parent, path := "root", ""
	for _, seg := range strings.Split(folder, "/") {
		if path != "" {
			path += "/"
		}
		path += seg
		g.mu.Lock()
		id, ok := g.folders[path]
		g.mu.Unlock()
		if !ok {
			fs, err := g.listQ(ctx, fmt.Sprintf("name='%s' and mimeType='%s' and '%s' in parents and trashed=false", gq(seg), folderMIME, gq(parent)), "id,name")
			if err != nil {
				return "", err
			}
			switch {
			case len(fs) > 0:
				id = fs[0].ID
			case create:
				var nf gfile
				if _, err := g.do(ctx, "POST", g.api()+"/files?fields=id", map[string]any{"name": seg, "mimeType": folderMIME, "parents": []string{parent}}, &nf, "criar pasta Google Drive", nil); err != nil {
					return "", err
				}
				id = nf.ID
			default:
				return "", ErrNotFound
			}
			g.mu.Lock()
			g.folders[path] = id
			g.mu.Unlock()
		}
		parent = id
	}
	return parent, nil
}

func (g *GDrive) forget(folder string) {
	g.mu.Lock()
	for k := range g.folders {
		if k == folder || strings.HasPrefix(k, folder+"/") || strings.HasPrefix(folder, k+"/") {
			delete(g.folders, k)
		}
	}
	g.mu.Unlock()
}

func (g *GDrive) NewUpload(ctx context.Context, folder, name string, size int64) (*UploadSession, error) {
	pid, err := g.folderID(ctx, folder, true)
	if err != nil {
		return nil, err
	}
	hdr := http.Header{"X-Upload-Content-Type": {"application/gzip"}, "X-Upload-Content-Length": {strconv.FormatInt(size, 10)}}
	resp, err := g.do(ctx, "POST", g.upload()+"/files?uploadType=resumable&fields=id,name,size,md5Checksum",
		map[string]any{"name": name, "parents": []string{pid}}, nil, "abrir sessão de upload Google Drive", hdr)
	if err != nil {
		g.forget(folder) // a pasta pode ter sido apagada: refaz a busca na próxima vez
		return nil, err
	}
	loc := resp.Header.Get("Location")
	if loc == "" {
		return nil, fmt.Errorf("o Google Drive não devolveu a URL de upload")
	}
	return &UploadSession{Protocol: "gdrive", URL: loc, ChunkSize: gdriveChunk, ExpiresAt: time.Now().Add(6 * 24 * time.Hour)}, nil
}

func (g *GDrive) Stat(ctx context.Context, folder, name string) (*RemoteFile, error) {
	pid, err := g.folderID(ctx, folder, false)
	if err != nil {
		return nil, err
	}
	fs, err := g.listQ(ctx, fmt.Sprintf("name='%s' and '%s' in parents and mimeType!='%s' and trashed=false", gq(name), gq(pid), folderMIME), "id,name,size,md5Checksum,createdTime")
	if err != nil {
		return nil, err
	}
	if len(fs) == 0 {
		return nil, ErrNotFound
	}
	rf := fs[len(fs)-1].remote()
	return &rf, nil
}

func (g *GDrive) List(ctx context.Context, folder string) ([]RemoteFile, error) {
	pid, err := g.folderID(ctx, folder, false)
	if err != nil {
		if err == ErrNotFound {
			return nil, nil
		}
		return nil, err
	}
	fs, err := g.listQ(ctx, fmt.Sprintf("'%s' in parents and mimeType!='%s' and trashed=false", gq(pid), folderMIME), "id,name,size,md5Checksum,createdTime")
	if err != nil {
		return nil, err
	}
	out := make([]RemoteFile, 0, len(fs))
	for _, f := range fs {
		out = append(out, f.remote())
	}
	return out, nil
}

// Folders lista subpastas. Com o escopo drive.file o aplicativo só enxerga as pastas que ele mesmo criou.
func (g *GDrive) Folders(ctx context.Context, parent string) ([]string, error) {
	pid := "root"
	if parent != "" {
		var err error
		if pid, err = g.folderID(ctx, parent, false); err != nil {
			if err == ErrNotFound {
				return nil, nil
			}
			return nil, err
		}
	}
	fs, err := g.listQ(ctx, fmt.Sprintf("'%s' in parents and mimeType='%s' and trashed=false", gq(pid), folderMIME), "id,name")
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(fs))
	for _, f := range fs {
		out = append(out, f.Name)
	}
	return out, nil
}

func (g *GDrive) Delete(ctx context.Context, folder string, f RemoteFile) error {
	// DELETE em /files/{id} apaga de vez (não vai para a lixeira)
	_, err := g.do(ctx, "DELETE", g.api()+"/files/"+url.PathEscape(f.ID), nil, nil, "apagar arquivo Google Drive", nil)
	if err != nil && !isNotFound(err) {
		return err
	}
	return nil
}

func (g *GDrive) Open(ctx context.Context, folder, name string) (io.ReadCloser, int64, error) {
	return g.OpenRange(ctx, folder, name, 0, 0)
}

func (g *GDrive) OpenRange(ctx context.Context, folder, name string, off, n int64) (io.ReadCloser, int64, error) {
	rf, err := g.Stat(ctx, folder, name)
	if err != nil {
		return nil, 0, err
	}
	tok, err := g.TS.Access(ctx)
	if err != nil {
		return nil, 0, err
	}
	req, err := http.NewRequestWithContext(ctx, "GET", g.api()+"/files/"+url.PathEscape(rf.ID)+"?alt=media", nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	setRange(req, off, n)
	c := *g.http()
	c.Timeout = 0
	resp, err := c.Do(req)
	if err != nil {
		return nil, 0, err
	}
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		return nil, 0, &APIError{Status: resp.StatusCode, Body: string(b), Op: "baixar arquivo Google Drive"}
	}
	return fitRange(resp, off, n)
}
