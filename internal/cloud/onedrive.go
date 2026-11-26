package cloud

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	graphBase        = "https://graph.microsoft.com/v1.0"
	oneDriveChunk    = 32 * 327680 // 10 MiB: múltiplo de 320 KiB, como exige a Graph API
	oneDriveScope    = "Files.ReadWrite offline_access User.Read"
	msLoginBase      = "https://login.microsoftonline.com"
	defaultMSTenant  = "common"
	maxListPagesSafe = 200
)

type Access interface {
	Access(ctx context.Context) (string, error)
}

type OneDrive struct {
	TS         Access
	HTTP       *http.Client
	Graph      string // padrão https://graph.microsoft.com/v1.0 (testes apontam para um servidor falso)
	DriveID    string // vazio = /me/drive
	HardDelete bool   // tenta permanentDelete (OneDrive for Business) antes de mandar para a lixeira
}

// OneDriveOAuth monta o fluxo de autorização (device code) do OneDrive.
func OneDriveOAuth(clientID, clientSecret, tenant string) *OAuth {
	if tenant == "" {
		tenant = defaultMSTenant
	}
	return &OAuth{ClientID: clientID, ClientSecret: clientSecret, Scope: oneDriveScope,
		DeviceURL: fmt.Sprintf("%s/%s/oauth2/v2.0/devicecode", msLoginBase, tenant),
		AuthURL:   fmt.Sprintf("%s/%s/oauth2/v2.0/authorize", msLoginBase, tenant),
		TokenURL:  fmt.Sprintf("%s/%s/oauth2/v2.0/token", msLoginBase, tenant),
		AuthExtra: url.Values{"prompt": {"select_account"}, "response_mode": {"query"}}}
}

func (o *OneDrive) Type() string { return TypeOneDrive }

func (o *OneDrive) http() *http.Client {
	if o.HTTP != nil {
		return o.HTTP
	}
	return &http.Client{Timeout: 60 * time.Second}
}

func (o *OneDrive) base() string {
	if o.Graph != "" {
		return strings.TrimRight(o.Graph, "/")
	}
	return graphBase
}

func (o *OneDrive) drive() string {
	if o.DriveID != "" {
		return "/drives/" + url.PathEscape(o.DriveID)
	}
	return "/me/drive"
}

func (o *OneDrive) do(ctx context.Context, method, rawURL string, body any, out any, op string) (int, error) {
	tok, err := o.TS.Access(ctx)
	if err != nil {
		return 0, err
	}
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, rd)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := o.http().Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode >= 300 {
		return resp.StatusCode, &APIError{Status: resp.StatusCode, Body: string(b), Op: op}
	}
	if out != nil && len(b) > 0 {
		if err := json.Unmarshal(b, out); err != nil {
			return resp.StatusCode, fmt.Errorf("%s: resposta inválida: %w", op, err)
		}
	}
	return resp.StatusCode, nil
}

func (o *OneDrive) Account(ctx context.Context) (string, error) {
	var me struct {
		DisplayName       string `json:"displayName"`
		UserPrincipalName string `json:"userPrincipalName"`
		Mail              string `json:"mail"`
	}
	if _, err := o.do(ctx, "GET", o.base()+"/me?$select=displayName,userPrincipalName,mail", nil, &me, "conta OneDrive"); err != nil {
		return "", err
	}
	switch {
	case me.UserPrincipalName != "":
		return me.UserPrincipalName, nil
	case me.Mail != "":
		return me.Mail, nil
	}
	return me.DisplayName, nil
}

func (o *OneDrive) Quota(ctx context.Context) (Quota, error) {
	var d struct {
		Quota struct {
			Total int64 `json:"total"`
			Used  int64 `json:"used"`
		} `json:"quota"`
	}
	if _, err := o.do(ctx, "GET", o.base()+o.drive()+"?$select=quota", nil, &d, "cota OneDrive"); err != nil {
		return Quota{}, err
	}
	return Quota{Total: d.Quota.Total, Used: d.Quota.Used}, nil
}

func (o *OneDrive) itemURL(folder, name string) string {
	p := folder
	if name != "" {
		p += "/" + name
	}
	return o.base() + o.drive() + "/root:/" + escPath(p) + ":"
}

// ensureFolder cria a cadeia de pastas que faltar (idempotente).
func (o *OneDrive) ensureFolder(ctx context.Context, folder string) error {
	if _, err := o.do(ctx, "GET", o.itemURL(folder, "")+"?$select=id", nil, nil, "pasta OneDrive"); err == nil {
		return nil
	} else if !isNotFound(err) {
		return err
	}
	segs := strings.Split(folder, "/")
	parent := ""
	for _, s := range segs {
		cur := s
		if parent != "" {
			cur = parent + "/" + s
		}
		if _, err := o.do(ctx, "GET", o.itemURL(cur, "")+"?$select=id", nil, nil, "pasta OneDrive"); err != nil {
			if !isNotFound(err) {
				return err
			}
			var u string
			if parent == "" {
				u = o.base() + o.drive() + "/root/children"
			} else {
				u = o.itemURL(parent, "") + "/children"
			}
			_, err := o.do(ctx, "POST", u, map[string]any{"name": s, "folder": map[string]any{}, "@microsoft.graph.conflictBehavior": "fail"}, nil, "criar pasta OneDrive")
			var ae *APIError
			if err != nil && !(asAPI(err, &ae) && ae.Status == http.StatusConflict) { // 409 = alguém criou antes
				return err
			}
		}
		parent = cur
	}
	return nil
}

func isNotFound(err error) bool {
	var ae *APIError
	return asAPI(err, &ae) && ae.Status == http.StatusNotFound
}

func asAPI(err error, t **APIError) bool {
	for err != nil {
		if a, ok := err.(*APIError); ok {
			*t = a
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

func (o *OneDrive) NewUpload(ctx context.Context, folder, name string, size int64) (*UploadSession, error) {
	if err := o.ensureFolder(ctx, folder); err != nil {
		return nil, err
	}
	var r struct {
		UploadURL  string `json:"uploadUrl"`
		Expiration string `json:"expirationDateTime"`
	}
	_, err := o.do(ctx, "POST", o.itemURL(folder, name)+"/createUploadSession",
		map[string]any{"item": map[string]any{"@microsoft.graph.conflictBehavior": "replace"}}, &r, "abrir sessão de upload OneDrive")
	if err != nil {
		return nil, err
	}
	if r.UploadURL == "" {
		return nil, fmt.Errorf("o OneDrive não devolveu a URL de upload")
	}
	exp, _ := time.Parse(time.RFC3339, r.Expiration)
	return &UploadSession{Protocol: "graph", URL: r.UploadURL, ChunkSize: oneDriveChunk, ExpiresAt: exp}, nil
}

type driveItem struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Size int64  `json:"size"`
	File *struct {
		Hashes struct {
			QuickXor string `json:"quickXorHash"`
			SHA1     string `json:"sha1Hash"`
		} `json:"hashes"`
	} `json:"file"`
	Modified string `json:"lastModifiedDateTime"`
}

func (d driveItem) remote() RemoteFile {
	rf := RemoteFile{ID: d.ID, Name: d.Name, Size: d.Size}
	if d.File != nil {
		rf.QuickXor = d.File.Hashes.QuickXor
		rf.SHA1 = strings.ToLower(d.File.Hashes.SHA1)
	}
	rf.Modified, _ = time.Parse(time.RFC3339, d.Modified)
	return rf
}

func (o *OneDrive) Stat(ctx context.Context, folder, name string) (*RemoteFile, error) {
	var it driveItem
	if _, err := o.do(ctx, "GET", o.itemURL(folder, name)+"?$select=id,name,size,file", nil, &it, "consultar arquivo OneDrive"); err != nil {
		if isNotFound(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	rf := it.remote()
	return &rf, nil
}

func (o *OneDrive) List(ctx context.Context, folder string) ([]RemoteFile, error) {
	var out []RemoteFile
	next := o.itemURL(folder, "") + "/children?$select=id,name,size,file,lastModifiedDateTime&$top=200"
	for i := 0; next != "" && i < maxListPagesSafe; i++ {
		var page struct {
			Value []driveItem `json:"value"`
			Next  string      `json:"@odata.nextLink"`
		}
		if _, err := o.do(ctx, "GET", next, nil, &page, "listar pasta OneDrive"); err != nil {
			if isNotFound(err) {
				return nil, nil
			}
			return nil, err
		}
		for _, it := range page.Value {
			if it.File != nil {
				out = append(out, it.remote())
			}
		}
		next = page.Next
	}
	return out, nil
}

func (o *OneDrive) Delete(ctx context.Context, folder string, f RemoteFile) error {
	item := o.base() + o.drive() + "/items/" + url.PathEscape(f.ID)
	if o.HardDelete {
		// OneDrive for Business: apaga de vez (sem lixeira, que continuaria ocupando a cota)
		if _, err := o.do(ctx, "POST", item+"/permanentDelete", nil, nil, "apagar definitivamente (OneDrive)"); err == nil {
			return nil
		} else if isNotFound(err) {
			return nil
		}
		// contas pessoais não têm permanentDelete: cai para a exclusão comum
	}
	if _, err := o.do(ctx, "DELETE", item, nil, nil, "apagar arquivo OneDrive"); err != nil && !isNotFound(err) {
		return err
	}
	return nil
}

func (o *OneDrive) Open(ctx context.Context, folder, name string) (io.ReadCloser, int64, error) {
	tok, err := o.TS.Access(ctx)
	if err != nil {
		return nil, 0, err
	}
	// /content responde 302 para uma URL pré-autenticada. O redirecionamento é seguido À MÃO, sem o token: ele nunca é
	// reenviado ao endereço de destino (mesmo que seja do mesmo domínio).
	c := *o.http()
	c.Timeout = 0 // downloads grandes: quem controla é o contexto
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	req, err := http.NewRequestWithContext(ctx, "GET", o.itemURL(folder, name)+"/content", nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := c.Do(req)
	if err != nil {
		return nil, 0, err
	}
	for hops := 0; resp.StatusCode >= 300 && resp.StatusCode < 400 && hops < 5; hops++ {
		loc := resp.Header.Get("Location")
		resp.Body.Close()
		if loc == "" {
			return nil, 0, errors.New("redirecionamento sem destino")
		}
		next, err := http.NewRequestWithContext(ctx, "GET", loc, nil)
		if err != nil {
			return nil, 0, err
		}
		if resp, err = c.Do(next); err != nil {
			return nil, 0, err
		}
	}
	if resp.StatusCode == http.StatusNotFound {
		resp.Body.Close()
		return nil, 0, ErrNotFound
	}
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		return nil, 0, &APIError{Status: resp.StatusCode, Body: string(b), Op: "baixar arquivo OneDrive"}
	}
	return resp.Body, resp.ContentLength, nil
}
