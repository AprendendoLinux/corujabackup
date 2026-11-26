// Package cloudmgr guarda as contas de nuvem (credenciais cifradas), faz a autorização por device code, mantém os
// tokens renovados e executa, em nome dos agentes, a abertura de sessões de upload, a conferência do envio e a
// retenção na nuvem.
package cloudmgr

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/AprendendoLinux/corujabackup/internal/cloud"
	"github.com/AprendendoLinux/corujabackup/internal/secrets"
	"github.com/AprendendoLinux/corujabackup/internal/slug"
	"github.com/AprendendoLinux/corujabackup/internal/store"
)

// Endpoints permite apontar os provedores para servidores falsos nos testes. Vazio = endereços reais.
type Endpoints struct {
	MSDevice, MSToken string
	Graph             string
	GDevice, GToken   string
	GAuth             string
	DBXToken          string
	DBXAPI            string
	DBXContent        string
	GAPI, GUpload     string
}

type AccountConfig struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret,omitempty"`
	Tenant       string `json:"tenant,omitempty"`
	DriveID      string `json:"drive_id,omitempty"`
	HardDelete   bool   `json:"hard_delete"`
	// UseDefault: usa o aplicativo OAuth padrão compilado (as credenciais não são gravadas na conta).
	UseDefault bool `json:"use_default,omitempty"`
	// Web: o aplicativo padrão é o "da Web" (login por popup), não o de TVs/dispositivos.
	Web bool `json:"web,omitempty"`
}

type Manager struct {
	Store *store.Store
	Box   *secrets.Box
	HTTP  *http.Client
	EP    Endpoints
	Now   func() time.Time

	mu       sync.Mutex
	cache    map[int64]cloud.Provider
	lastPoll map[int64]time.Time
	pending  map[string]pendingWeb // logins por popup em andamento (nonce -> dados)
}

type pendingWeb struct {
	Name, Typ, Origin, Verifier string
	AccountID                   int64 // != 0: reautorização de uma conta existente
	Expires                     time.Time
}

func New(st *store.Store, box *secrets.Box) *Manager {
	return &Manager{Store: st, Box: box, Now: time.Now, cache: map[int64]cloud.Provider{}, lastPoll: map[int64]time.Time{}, pending: map[string]pendingWeb{}}
}

var ErrNotConnected = errors.New("a conta de nuvem não está conectada")

func (m *Manager) oauth(typ string, cfg AccountConfig) (*cloud.OAuth, error) {
	if cfg.UseDefault && cfg.Web {
		if !cloud.HasWebApp(typ) {
			return nil, errors.New("este Server não tem o aplicativo de login por popup: reconecte a conta")
		}
		cfg.ClientID, cfg.ClientSecret = cloud.WebAppCreds(typ)
	} else if cfg.UseDefault {
		if !cloud.HasDefaultApp(typ) {
			return nil, errors.New("este Server não tem aplicativo padrão para esse provedor: reconecte informando as credenciais")
		}
		cfg.ClientID, cfg.ClientSecret = cloud.DefaultGoogleClientID, cloud.DefaultGoogleClientSecret
	}
	var o *cloud.OAuth
	switch typ {
	case cloud.TypeOneDrive:
		o = cloud.OneDriveOAuth(cfg.ClientID, cfg.ClientSecret, cfg.Tenant)
		if m.EP.MSDevice != "" {
			o.DeviceURL, o.TokenURL = m.EP.MSDevice, m.EP.MSToken
		}
	case cloud.TypeGDrive:
		o = cloud.GDriveOAuth(cfg.ClientID, cfg.ClientSecret)
		if m.EP.GDevice != "" {
			o.DeviceURL, o.TokenURL = m.EP.GDevice, m.EP.GToken
		}
		if m.EP.GAuth != "" {
			o.AuthURL = m.EP.GAuth
		}
	case cloud.TypeDropbox:
		o = cloud.DropboxOAuth(cfg.ClientID, cfg.ClientSecret)
		if m.EP.DBXToken != "" {
			o.TokenURL = m.EP.DBXToken
		}
	default:
		return nil, fmt.Errorf("tipo de nuvem desconhecido: %q", typ)
	}
	o.HTTP = m.HTTP
	return o, nil
}

func (m *Manager) openConfig(a *store.CloudAccount) (AccountConfig, error) {
	var c AccountConfig
	plain, err := m.Box.Open(a.Config)
	if err != nil {
		return c, err
	}
	return c, json.Unmarshal([]byte(plain), &c)
}

// Create cadastra a conta (pendente) e inicia a autorização: o usuário abre a URL, digita o código e aprova.
func (m *Manager) Create(ctx context.Context, name, typ string, cfg AccountConfig) (int64, *cloud.Device, error) {
	if cfg.ClientID == "" && cloud.HasDefaultApp(typ) {
		cfg.UseDefault, cfg.ClientSecret = true, ""
	}
	if cfg.ClientID == "" && !cfg.UseDefault {
		return 0, nil, errors.New("informe o Client ID do aplicativo cadastrado no provedor")
	}
	if typ == cloud.TypeGDrive && cfg.ClientSecret == "" && !cfg.UseDefault {
		return 0, nil, errors.New("o Google exige o Client Secret (credencial do tipo “TVs e dispositivos de entrada limitada”)")
	}
	o, err := m.oauth(typ, cfg)
	if err != nil {
		return 0, nil, err
	}
	dev, err := o.StartDevice(ctx, m.Now())
	if err != nil {
		return 0, nil, err
	}
	cj, _ := json.Marshal(cfg)
	sc, err := m.Box.Seal(string(cj))
	if err != nil {
		return 0, nil, err
	}
	dj, _ := json.Marshal(dev)
	sd, err := m.Box.Seal(string(dj))
	if err != nil {
		return 0, nil, err
	}
	id, err := m.Store.CreateCloud(ctx, name, typ, sc, sd)
	if err != nil {
		return 0, nil, err
	}
	return id, dev, nil
}

// Reauthorize reinicia a autorização de uma conta existente (token revogado, por exemplo).
func (m *Manager) Reauthorize(ctx context.Context, id int64) (*cloud.Device, error) {
	a, err := m.Store.GetCloud(ctx, id)
	if err != nil {
		return nil, err
	}
	cfg, err := m.openConfig(a)
	if err != nil {
		return nil, err
	}
	o, err := m.oauth(a.Type, cfg)
	if err != nil {
		return nil, err
	}
	dev, err := o.StartDevice(ctx, m.Now())
	if err != nil {
		return nil, err
	}
	dj, _ := json.Marshal(dev)
	sd, _ := m.Box.Seal(string(dj))
	m.Invalidate(id)
	return dev, m.Store.SetCloudDevice(ctx, id, sd)
}

// PollDevice faz uma tentativa (respeitando o intervalo) e devolve "pending", "connected", "expired" ou "denied".
func (m *Manager) PollDevice(ctx context.Context, id int64) (string, *cloud.Device, error) {
	a, err := m.Store.GetCloud(ctx, id)
	if err != nil {
		return "", nil, err
	}
	if a.Status == "connected" {
		return "connected", nil, nil
	}
	if a.Device == "" {
		return "expired", nil, nil
	}
	plain, err := m.Box.Open(a.Device)
	if err != nil {
		return "", nil, err
	}
	var dev cloud.Device
	if err := json.Unmarshal([]byte(plain), &dev); err != nil {
		return "", nil, err
	}
	now := m.Now()
	if now.After(dev.ExpiresAt) {
		return "expired", &dev, nil
	}
	m.mu.Lock()
	last := m.lastPoll[id]
	if now.Sub(last) < time.Duration(dev.Interval)*time.Second {
		m.mu.Unlock()
		return "pending", &dev, nil
	}
	m.lastPoll[id] = now
	m.mu.Unlock()

	cfg, err := m.openConfig(a)
	if err != nil {
		return "", nil, err
	}
	o, err := m.oauth(a.Type, cfg)
	if err != nil {
		return "", nil, err
	}
	res, tok, err := o.PollDevice(ctx, &dev, now)
	if err != nil {
		_ = m.Store.SetCloudStatus(ctx, id, "pending", err.Error())
		return "", &dev, err
	}
	switch res {
	case cloud.PollOK:
		tj, _ := json.Marshal(tok)
		st, err := m.Box.Seal(string(tj))
		if err != nil {
			return "", nil, err
		}
		if err := m.Store.SetCloudTokens(ctx, id, st, ""); err != nil {
			return "", nil, err
		}
		m.Invalidate(id)
		label, _, terr := m.Test(ctx, id)
		if terr != nil {
			_ = m.Store.SetCloudStatus(ctx, id, "error", terr.Error())
			return "", &dev, terr
		}
		_ = label
		return "connected", nil, nil
	case cloud.PollExpired:
		return "expired", &dev, nil
	case cloud.PollDenied:
		return "denied", &dev, nil
	case cloud.PollSlowDown:
		dev.Interval += 5
		dj, _ := json.Marshal(dev)
		sd, _ := m.Box.Seal(string(dj))
		_ = m.Store.SetCloudDevice(ctx, id, sd)
	}
	return "pending", &dev, nil
}

func (m *Manager) Invalidate(id int64) {
	m.mu.Lock()
	delete(m.cache, id)
	m.mu.Unlock()
}

type guard struct {
	m  *Manager
	id int64
	ts *cloud.TokenSource
}

func (g guard) Access(ctx context.Context) (string, error) {
	tok, err := g.ts.Access(ctx)
	if errors.Is(err, cloud.ErrRefreshRejected) {
		_ = g.m.Store.SetCloudStatus(ctx, g.id, "error", err.Error())
	}
	return tok, err
}

// Provider devolve (e guarda em cache) o conector da conta, já com renovação automática de token.
func (m *Manager) Provider(ctx context.Context, id int64) (cloud.Provider, error) {
	m.mu.Lock()
	if p, ok := m.cache[id]; ok {
		m.mu.Unlock()
		return p, nil
	}
	m.mu.Unlock()
	a, err := m.Store.GetCloud(ctx, id)
	if err != nil {
		return nil, err
	}
	if a.Status != "connected" || a.Tokens == "" {
		return nil, ErrNotConnected
	}
	cfg, err := m.openConfig(a)
	if err != nil {
		return nil, err
	}
	tp, err := m.Box.Open(a.Tokens)
	if err != nil {
		return nil, err
	}
	var tok cloud.Token
	if err := json.Unmarshal([]byte(tp), &tok); err != nil {
		return nil, err
	}
	o, err := m.oauth(a.Type, cfg)
	if err != nil {
		return nil, err
	}
	ts := cloud.NewTokenSource(o, tok, func(ctx context.Context, t cloud.Token) error {
		tj, _ := json.Marshal(t)
		s, err := m.Box.Seal(string(tj))
		if err != nil {
			return err
		}
		return m.Store.SetCloudTokensOnly(ctx, id, s)
	})
	var p cloud.Provider
	switch a.Type {
	case cloud.TypeOneDrive:
		p = &cloud.OneDrive{TS: guard{m, id, ts}, HTTP: m.HTTP, Graph: m.EP.Graph, DriveID: cfg.DriveID, HardDelete: cfg.HardDelete}
	case cloud.TypeGDrive:
		p = &cloud.GDrive{TS: guard{m, id, ts}, HTTP: m.HTTP, API: m.EP.GAPI, Upload: m.EP.GUpload}
	case cloud.TypeDropbox:
		p = &cloud.Dropbox{TS: guard{m, id, ts}, HTTP: m.HTTP, API: m.EP.DBXAPI, Content: m.EP.DBXContent,
			UploadToken: func(ctx context.Context) (string, time.Time, error) {
				t, err := o.RefreshScoped(ctx, ts.RefreshToken(), cloud.DropboxAgentScope, m.Now())
				if err != nil {
					return "", time.Time{}, err
				}
				return t.Access, t.Expiry, nil
			}}
	default:
		return nil, fmt.Errorf("tipo de nuvem desconhecido: %q", a.Type)
	}
	m.mu.Lock()
	m.cache[id] = p
	m.mu.Unlock()
	return p, nil
}

// Test confirma o acesso, grava o rótulo da conta e a cota.
func (m *Manager) Test(ctx context.Context, id int64) (string, cloud.Quota, error) {
	p, err := m.Provider(ctx, id)
	if err != nil {
		return "", cloud.Quota{}, err
	}
	label, err := p.Account(ctx)
	if err != nil {
		_ = m.Store.SetCloudStatus(ctx, id, "error", err.Error())
		return "", cloud.Quota{}, err
	}
	q, qerr := p.Quota(ctx)
	if qerr == nil {
		_ = m.Store.SetCloudQuota(ctx, id, q.Total, q.Used)
	}
	_ = m.Store.SetCloudLabel(ctx, id, label)
	return label, q, nil
}

// ---------------------------------------------------------------- upload, conferência e retenção

type Upload struct {
	Session    *cloud.UploadSession
	RemotePath string
}

func jobFolder(j *store.Job) (string, error) { return cloud.CleanFolder(j.CloudFolder) }

// OpenUpload abre a sessão de upload do arquivo do job (e recusa se a cota não comporta).
func (m *Manager) OpenUpload(ctx context.Context, j *store.Job, fileName string, size int64) (*Upload, error) {
	if j.CloudAccountID == nil {
		return nil, errors.New("este job não tem destino de nuvem")
	}
	folder, err := jobFolder(j)
	if err != nil {
		return nil, err
	}
	p, err := m.Provider(ctx, *j.CloudAccountID)
	if err != nil {
		return nil, err
	}
	if q, err := p.Quota(ctx); err == nil && q.Total > 0 && q.Total-q.Used < size {
		return nil, fmt.Errorf("sem espaço na nuvem: livre %d bytes, o arquivo precisa de %d", q.Total-q.Used, size)
	}
	s, err := p.NewUpload(ctx, folder, fileName, size)
	if err != nil {
		return nil, err
	}
	return &Upload{Session: s, RemotePath: folder + "/" + fileName}, nil
}

type Hashes struct{ QuickXor, SHA1, MD5, Dropbox string }

type Verdict struct {
	OK         bool
	Message    string
	VerifiedBy string
	RemotePath string
	RemoteHash string
	Retention  []string
}

// VerifyAndRetain confere o arquivo na nuvem (tamanho + hash calculado pela própria nuvem) e SÓ ENTÃO aplica a
// retenção. Se a conferência falhar, apaga o arquivo ruim e não toca nos backups antigos.
func (m *Manager) VerifyAndRetain(ctx context.Context, j *store.Job, fileName string, size int64, h Hashes, now time.Time) Verdict {
	v := Verdict{}
	if j.CloudAccountID == nil {
		v.Message = "este job não tem destino de nuvem"
		return v
	}
	folder, err := jobFolder(j)
	if err != nil {
		v.Message = err.Error()
		return v
	}
	v.RemotePath = folder + "/" + fileName
	p, err := m.Provider(ctx, *j.CloudAccountID)
	if err != nil {
		v.Message = err.Error()
		return v
	}
	rf, err := p.Stat(ctx, folder, fileName)
	if err != nil {
		v.Message = "o arquivo não foi encontrado na nuvem após o envio: " + err.Error()
		return v
	}
	switch {
	case rf.Size != size:
		v.Message = fmt.Sprintf("tamanho na nuvem (%d) diferente do enviado (%d)", rf.Size, size)
	case rf.ContentHash != "" && h.Dropbox != "":
		v.RemoteHash, v.VerifiedBy = rf.ContentHash, "dropbox"
		if rf.ContentHash != h.Dropbox {
			v.Message = "o content_hash do Dropbox difere do arquivo enviado"
		}
	case rf.QuickXor != "" && h.QuickXor != "":
		v.RemoteHash, v.VerifiedBy = rf.QuickXor, "quickxor"
		if rf.QuickXor != h.QuickXor {
			v.Message = "o hash quickXor na nuvem difere do arquivo enviado"
		}
	case rf.SHA1 != "" && h.SHA1 != "":
		v.RemoteHash, v.VerifiedBy = rf.SHA1, "sha1"
		if rf.SHA1 != h.SHA1 {
			v.Message = "o hash SHA-1 na nuvem difere do arquivo enviado"
		}
	case rf.MD5 != "" && h.MD5 != "":
		v.RemoteHash, v.VerifiedBy = rf.MD5, "md5"
		if rf.MD5 != h.MD5 {
			v.Message = "o hash MD5 na nuvem difere do arquivo enviado"
		}
	default:
		v.VerifiedBy = "size"
	}
	if v.Message != "" {
		_ = p.Delete(ctx, folder, *rf) // não deixa um arquivo corrompido passar por backup
		v.Message += " (arquivo removido da nuvem; os backups antigos foram preservados)"
		return v
	}
	v.OK = true
	// retenção na nuvem, só agora que o envio foi verificado
	files, err := p.List(ctx, folder)
	if err != nil {
		v.Retention = append(v.Retention, "retenção ignorada: não foi possível listar a pasta: "+err.Error())
		return v
	}
	for _, f := range cloud.Retention(files, slug.Of(j.Name), j.CloudKeep, j.CloudKeepDays, now) {
		if err := p.Delete(ctx, folder, f); err != nil {
			v.Retention = append(v.Retention, "não foi possível apagar "+f.Name+": "+err.Error())
			continue
		}
		v.Retention = append(v.Retention, "removido da nuvem: "+f.Name)
	}
	return v
}

// Open baixa um arquivo da conta de nuvem (restauração/download).
func (m *Manager) Open(ctx context.Context, accountID int64, folder, name string) (io.ReadCloser, int64, error) {
	f, err := cloud.CleanFolder(folder)
	if err != nil {
		return nil, 0, err
	}
	p, err := m.Provider(ctx, accountID)
	if err != nil {
		return nil, 0, err
	}
	return p.Open(ctx, f, name)
}

// OpenConfigForTest expõe a configuração decifrada (somente para testes).
func (m *Manager) OpenConfigForTest(a *store.CloudAccount) (AccountConfig, error) {
	return m.openConfig(a)
}

var originRE = regexp.MustCompile(`^https?://[A-Za-z0-9.\-]+(:[0-9]{1,5})?$|^https?://\[[0-9a-fA-F:]+\](:[0-9]{1,5})?$`)

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// BeginWeb prepara o login por popup: devolve o endereço do provedor (que o navegador abre) e o "state" a conferir na volta.
// origin é a origem da interface do Coruja (a janela que abriu o popup): só ela recebe o código.
func (m *Manager) BeginWeb(ctx context.Context, name, typ, origin string) (loginURL, state string, err error) {
	return m.beginWeb(ctx, name, typ, origin, 0)
}

// BeginWebReauth refaz o login por popup de uma conta já existente (token revogado, por exemplo).
func (m *Manager) BeginWebReauth(ctx context.Context, id int64, origin string) (loginURL, state string, err error) {
	a, err := m.Store.GetCloud(ctx, id)
	if err != nil {
		return "", "", err
	}
	if !m.UsesPopup(ctx, id) {
		return "", "", errors.New("esta conta não foi conectada pelo login por popup")
	}
	return m.beginWeb(ctx, a.Name, a.Type, origin, id)
}

// UsesPopup informa se a conta foi conectada pelo login por popup (aplicativo padrão "da Web").
func (m *Manager) UsesPopup(ctx context.Context, id int64) bool {
	a, err := m.Store.GetCloud(ctx, id)
	if err != nil {
		return false
	}
	cfg, err := m.openConfig(a)
	return err == nil && cfg.UseDefault && cfg.Web
}

func (m *Manager) beginWeb(ctx context.Context, name, typ, origin string, accountID int64) (loginURL, state string, err error) {
	if !cloud.HasWebApp(typ) {
		return "", "", errors.New("login por popup indisponível neste Server")
	}
	if !originRE.MatchString(origin) {
		return "", "", errors.New("origem da interface inválida")
	}
	if accs, e := m.Store.ListClouds(ctx); e == nil && accountID == 0 {
		for _, a := range accs {
			if a.Name == name {
				return "", "", store.ErrDuplicate
			}
		}
	}
	nonce, ver := make([]byte, 18), make([]byte, 32)
	if _, err = rand.Read(nonce); err != nil {
		return "", "", err
	}
	if _, err = rand.Read(ver); err != nil {
		return "", "", err
	}
	state = b64(nonce) + "." + b64([]byte(origin))
	cfg := AccountConfig{UseDefault: true, Web: true}
	o, err := m.oauth(typ, cfg)
	if err != nil {
		return "", "", err
	}
	ch := sha256.Sum256([]byte(b64(ver)))
	m.mu.Lock()
	now := m.Now()
	for k, v := range m.pending { // limpa vencidos e limita o total
		if now.After(v.Expires) {
			delete(m.pending, k)
		}
	}
	if len(m.pending) > 50 {
		m.mu.Unlock()
		return "", "", errors.New("muitos logins em andamento: tente de novo em instantes")
	}
	m.pending[b64(nonce)] = pendingWeb{Name: name, Typ: typ, Origin: origin, Verifier: b64(ver), AccountID: accountID, Expires: now.Add(10 * time.Minute)}
	m.mu.Unlock()
	return o.AuthCodeURL(cloud.BrokerRedirect(typ), state, b64(ch[:])), state, nil
}

// FinishWeb conclui o login por popup: confere o state (uso único), troca o código por tokens e cria a conta já conectada.
func (m *Manager) FinishWeb(ctx context.Context, state, code string) (int64, error) {
	i := strings.IndexByte(state, '.')
	if i < 1 || code == "" || len(code) > 2048 {
		return 0, errors.New("resposta de login inválida")
	}
	nonce := state[:i]
	m.mu.Lock()
	p, ok := m.pending[nonce]
	delete(m.pending, nonce)
	m.mu.Unlock()
	if !ok || m.Now().After(p.Expires) {
		return 0, errors.New("o login expirou ou já foi usado: comece de novo")
	}
	if o, e := base64.RawURLEncoding.DecodeString(state[i+1:]); e != nil || string(o) != p.Origin {
		return 0, errors.New("resposta de login inválida")
	}
	cfg := AccountConfig{UseDefault: true, Web: true, HardDelete: p.Typ == cloud.TypeOneDrive}
	o, err := m.oauth(p.Typ, cfg)
	if err != nil {
		return 0, err
	}
	tok, err := o.ExchangeCode(ctx, code, p.Verifier, cloud.BrokerRedirect(p.Typ), m.Now())
	if err != nil {
		return 0, err
	}
	id := p.AccountID
	if id == 0 {
		cj, _ := json.Marshal(cfg)
		sc, err := m.Box.Seal(string(cj))
		if err != nil {
			return 0, err
		}
		if id, err = m.Store.CreateCloud(ctx, p.Name, p.Typ, sc, ""); err != nil {
			return 0, err
		}
	}
	tj, _ := json.Marshal(tok)
	st, err := m.Box.Seal(string(tj))
	if err != nil {
		return 0, err
	}
	if err := m.Store.SetCloudTokens(ctx, id, st, ""); err != nil {
		return 0, err
	}
	m.Invalidate(id)
	if _, _, terr := m.Test(ctx, id); terr != nil {
		_ = m.Store.SetCloudStatus(ctx, id, "error", terr.Error())
		return id, terr
	}
	return id, nil
}
