package cloud

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf16"
)

const (
	TypeDropbox  = "dropbox"
	dropboxScope = "account_info.read files.metadata.read files.content.read files.content.write"
	// DropboxAgentScope é o único escopo do token de curta duração entregue ao agente para o envio.
	DropboxAgentScope = "files.content.write"
	dropboxAPI        = "https://api.dropboxapi.com/2"
	dropboxContent    = "https://content.dropboxapi.com/2"
	dropboxBlock      = 4 << 20
	dropboxChunk      = 8 << 20
)

// DropboxHash implementa o "content_hash" do Dropbox: SHA-256 de cada bloco de 4 MiB e, no fim, o SHA-256 da
// concatenação desses resumos.
type DropboxHash struct {
	block hash.Hash
	n     int // bytes no bloco atual
	all   []byte
}

func NewDropboxHash() *DropboxHash { return &DropboxHash{block: sha256.New()} }

func (d *DropboxHash) Write(p []byte) (int, error) {
	total := len(p)
	for len(p) > 0 {
		k := min(dropboxBlock-d.n, len(p))
		d.block.Write(p[:k])
		d.n, p = d.n+k, p[k:]
		if d.n == dropboxBlock {
			d.all = d.block.Sum(d.all)
			d.block.Reset()
			d.n = 0
		}
	}
	return total, nil
}

// Hex devolve o content_hash dos bytes escritos até aqui (sem alterar o estado).
func (d *DropboxHash) Hex() string {
	all := append([]byte(nil), d.all...)
	if d.n > 0 {
		all = d.block.Sum(all)
	}
	s := sha256.Sum256(all)
	return hex.EncodeToString(s[:])
}

// DropboxOAuth: aplicativo público (PKCE, sem segredo) com acesso só à pasta do aplicativo.
func DropboxOAuth(clientID, clientSecret string) *OAuth {
	return &OAuth{ClientID: clientID, ClientSecret: clientSecret, Scope: dropboxScope,
		AuthURL: "https://www.dropbox.com/oauth2/authorize", TokenURL: "https://api.dropboxapi.com/oauth2/token",
		AuthExtra: url.Values{"token_access_type": {"offline"}}}
}

// Dropbox é o provedor. Os caminhos são relativos à pasta do aplicativo (App folder).
type Dropbox struct {
	TS      Access
	HTTP    *http.Client
	API     string
	Content string
	// UploadToken emite um token de curta duração, só de escrita de conteúdo, para o agente fazer o envio.
	UploadToken func(ctx context.Context) (token string, expires time.Time, err error)
}

func (d *Dropbox) Type() string { return TypeDropbox }

func (d *Dropbox) http() *http.Client {
	if d.HTTP != nil {
		return d.HTTP
	}
	return &http.Client{Timeout: 60 * time.Second}
}

func (d *Dropbox) api() string {
	if d.API != "" {
		return strings.TrimRight(d.API, "/")
	}
	return dropboxAPI
}

func (d *Dropbox) content() string {
	if d.Content != "" {
		return strings.TrimRight(d.Content, "/")
	}
	return dropboxContent
}

// ASCIIJSON serializa v em JSON só com ASCII (exigência do cabeçalho Dropbox-API-Arg).
func ASCIIJSON(v any) string {
	b, _ := json.Marshal(v)
	var out strings.Builder
	for _, r := range string(b) {
		switch {
		case r < 0x7f:
			out.WriteRune(r)
		case r > 0xffff:
			a, c := utf16.EncodeRune(r)
			fmt.Fprintf(&out, "\\u%04x\\u%04x", a, c)
		default:
			fmt.Fprintf(&out, "\\u%04x", r)
		}
	}
	return out.String()
}

type dbxError struct {
	Summary string `json:"error_summary"`
}

func errorsIsNotFound(err error) bool { return errors.Is(err, ErrNotFound) }

func dbxFail(op string, status int, body []byte) error {
	var e dbxError
	_ = json.Unmarshal(body, &e)
	if status == http.StatusConflict && strings.Contains(e.Summary, "not_found") {
		return fmt.Errorf("%s: %w", op, ErrNotFound)
	}
	return &APIError{Status: status, Body: string(body), Op: op}
}

// rpc chama um endpoint JSON da API.
func (d *Dropbox) rpc(ctx context.Context, op, path string, in, out any) error {
	tok, err := d.TS.Access(ctx)
	if err != nil {
		return err
	}
	body, _ := json.Marshal(in) // nil vira "null", que é o que o Dropbox espera em chamadas sem argumentos
	req, err := http.NewRequestWithContext(ctx, "POST", d.api()+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	resp, err := d.http().Do(req)
	if err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode != 200 {
		return dbxFail(op, resp.StatusCode, b)
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(b, out)
}

func (d *Dropbox) Account(ctx context.Context) (string, error) {
	var r struct {
		Email string `json:"email"`
	}
	if err := d.rpc(ctx, "conta", "/users/get_current_account", nil, &r); err != nil {
		return "", err
	}
	return r.Email, nil
}

func (d *Dropbox) Quota(ctx context.Context) (Quota, error) {
	var r struct {
		Used       int64 `json:"used"`
		Allocation struct {
			Allocated int64 `json:"allocated"`
		} `json:"allocation"`
	}
	if err := d.rpc(ctx, "cota", "/users/get_space_usage", nil, &r); err != nil {
		return Quota{}, err
	}
	return Quota{Total: r.Allocation.Allocated, Used: r.Used}, nil
}

// NewUpload não cria sessão no Server: o Dropbox não tem endereço pré-autorizado para arquivos grandes, então o agente
// recebe um token de curta duração (só files.content.write, restrito à pasta do aplicativo) e abre a sessão em partes.
func (d *Dropbox) NewUpload(ctx context.Context, folder, name string, size int64) (*UploadSession, error) {
	if d.UploadToken == nil {
		return nil, fmt.Errorf("conta Dropbox sem emissor de token de envio")
	}
	tok, exp, err := d.UploadToken(ctx)
	if err != nil {
		return nil, err
	}
	return &UploadSession{Protocol: "dropbox", URL: d.content() + "/files/upload_session", ChunkSize: dropboxChunk,
		ExpiresAt: exp, Token: tok, Path: "/" + folder + "/" + name}, nil
}

type dbxEntry struct {
	Tag         string    `json:".tag"`
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	PathLower   string    `json:"path_lower"`
	Size        int64     `json:"size"`
	ContentHash string    `json:"content_hash"`
	Modified    time.Time `json:"server_modified"`
}

func (e dbxEntry) remote() RemoteFile {
	return RemoteFile{ID: e.ID, Name: e.Name, Size: e.Size, ContentHash: e.ContentHash, Modified: e.Modified}
}

func (d *Dropbox) Stat(ctx context.Context, folder, name string) (*RemoteFile, error) {
	var e dbxEntry
	if err := d.rpc(ctx, "consultar arquivo", "/files/get_metadata", map[string]any{"path": "/" + folder + "/" + name}, &e); err != nil {
		return nil, err
	}
	if e.Tag != "" && e.Tag != "file" {
		return nil, ErrNotFound
	}
	f := e.remote()
	return &f, nil
}

func (d *Dropbox) List(ctx context.Context, folder string) ([]RemoteFile, error) {
	var r struct {
		Entries []dbxEntry `json:"entries"`
		Cursor  string     `json:"cursor"`
		HasMore bool       `json:"has_more"`
	}
	err := d.rpc(ctx, "listar pasta", "/files/list_folder", map[string]any{"path": "/" + folder}, &r)
	if err != nil {
		if errorsIsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []RemoteFile
	for page := 0; ; page++ {
		for _, e := range r.Entries {
			if e.Tag == "file" {
				out = append(out, e.remote())
			}
		}
		if !r.HasMore {
			return out, nil
		}
		if page > 500 {
			return nil, fmt.Errorf("listar pasta: páginas demais")
		}
		cursor := r.Cursor
		r.Entries, r.HasMore = nil, false
		if err := d.rpc(ctx, "listar pasta", "/files/list_folder/continue", map[string]any{"cursor": cursor}, &r); err != nil {
			return nil, err
		}
	}
}

// Delete apaga o arquivo. No Dropbox ele ainda pode ser recuperado por um período (regra do plano da conta).
func (d *Dropbox) Delete(ctx context.Context, folder string, f RemoteFile) error {
	err := d.rpc(ctx, "apagar arquivo", "/files/delete_v2", map[string]any{"path": "/" + folder + "/" + f.Name}, nil)
	if errorsIsNotFound(err) {
		return nil
	}
	return err
}

func (d *Dropbox) Open(ctx context.Context, folder, name string) (io.ReadCloser, int64, error) {
	tok, err := d.TS.Access(ctx)
	if err != nil {
		return nil, 0, err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", d.content()+"/files/download", nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Dropbox-API-Arg", ASCIIJSON(map[string]string{"path": "/" + folder + "/" + name}))
	cl := *d.http()
	cl.Timeout = 0 // download grande: quem limita é o contexto
	resp, err := cl.Do(req)
	if err != nil {
		return nil, 0, err
	}
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
		resp.Body.Close()
		return nil, 0, dbxFail("baixar arquivo", resp.StatusCode, b)
	}
	return resp.Body, resp.ContentLength, nil
}
