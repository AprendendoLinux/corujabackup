package cloud

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

type Token struct {
	Access  string    `json:"access_token"`
	Refresh string    `json:"refresh_token"`
	Expiry  time.Time `json:"expiry"`
}

type Device struct {
	DeviceCode      string    `json:"device_code"`
	UserCode        string    `json:"user_code"`
	VerificationURI string    `json:"verification_uri"`
	ExpiresAt       time.Time `json:"expires_at"`
	Interval        int       `json:"interval"`
}

// OAuth faz o fluxo "device code" (o usuário autoriza em outro navegador, sem precisar de URL de retorno) e a
// renovação de tokens, para Microsoft e Google.
type OAuth struct {
	ClientID     string
	ClientSecret string
	DeviceURL    string
	AuthURL      string // fluxo com retorno (popup)
	TokenURL     string
	Scope        string
	HTTP         *http.Client
}

func (o *OAuth) client() *http.Client {
	if o.HTTP != nil {
		return o.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func (o *OAuth) post(ctx context.Context, endpoint string, form url.Values) (map[string]any, int, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := o.client().Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	return m, resp.StatusCode, nil
}

func str(m map[string]any, k string) string { s, _ := m[k].(string); return s }
func num(m map[string]any, k string) int64  { f, _ := m[k].(float64); return int64(f) }

func (o *OAuth) StartDevice(ctx context.Context, now time.Time) (*Device, error) {
	form := url.Values{"client_id": {o.ClientID}, "scope": {o.Scope}}
	m, code, err := o.post(ctx, o.DeviceURL, form)
	if err != nil {
		return nil, err
	}
	if code != 200 || str(m, "device_code") == "" {
		return nil, fmt.Errorf("não foi possível iniciar a autorização: %s", errText(m, code))
	}
	uri := str(m, "verification_uri")
	if uri == "" {
		uri = str(m, "verification_url") // Google usa verification_url
	}
	iv := int(num(m, "interval"))
	if iv < 1 {
		iv = 5
	}
	return &Device{DeviceCode: str(m, "device_code"), UserCode: str(m, "user_code"), VerificationURI: uri,
		ExpiresAt: now.Add(time.Duration(num(m, "expires_in")) * time.Second), Interval: iv}, nil
}

type PollResult int

const (
	PollPending PollResult = iota
	PollSlowDown
	PollOK
	PollExpired
	PollDenied
)

func errText(m map[string]any, code int) string {
	if d := str(m, "error_description"); d != "" {
		if i := strings.IndexAny(d, "\r\n"); i > 0 {
			d = d[:i]
		}
		return d
	}
	if e := str(m, "error"); e != "" {
		return e
	}
	return fmt.Sprintf("HTTP %d", code)
}

// PollDevice faz uma tentativa de trocar o device_code por tokens.
func (o *OAuth) PollDevice(ctx context.Context, d *Device, now time.Time) (PollResult, *Token, error) {
	form := url.Values{"client_id": {o.ClientID}, "device_code": {d.DeviceCode}, "grant_type": {"urn:ietf:params:oauth:grant-type:device_code"}}
	if o.ClientSecret != "" {
		form.Set("client_secret", o.ClientSecret)
	}
	m, code, err := o.post(ctx, o.TokenURL, form)
	if err != nil {
		return PollPending, nil, err
	}
	if code == 200 && str(m, "access_token") != "" {
		return PollOK, tokenFrom(m, "", now), nil
	}
	switch str(m, "error") {
	case "authorization_pending":
		return PollPending, nil, nil
	case "slow_down":
		return PollSlowDown, nil, nil
	case "expired_token", "code_expired":
		return PollExpired, nil, nil
	case "access_denied", "authorization_declined":
		return PollDenied, nil, nil
	}
	return PollPending, nil, fmt.Errorf("autorização recusada: %s", errText(m, code))
}

func tokenFrom(m map[string]any, oldRefresh string, now time.Time) *Token {
	t := &Token{Access: str(m, "access_token"), Refresh: str(m, "refresh_token"), Expiry: now.Add(time.Duration(num(m, "expires_in")) * time.Second)}
	if t.Refresh == "" {
		t.Refresh = oldRefresh
	}
	return t
}

var ErrRefreshRejected = errors.New("a nuvem recusou renovar o acesso (reconecte a conta)")

func (o *OAuth) Refresh(ctx context.Context, refresh string, now time.Time) (*Token, error) {
	form := url.Values{"client_id": {o.ClientID}, "refresh_token": {refresh}, "grant_type": {"refresh_token"}}
	if o.ClientSecret != "" {
		form.Set("client_secret", o.ClientSecret)
	}
	if o.Scope != "" && strings.Contains(o.TokenURL, "microsoftonline") {
		form.Set("scope", o.Scope)
	}
	m, code, err := o.post(ctx, o.TokenURL, form)
	if err != nil {
		return nil, err
	}
	if code != 200 || str(m, "access_token") == "" {
		if e := str(m, "error"); e == "invalid_grant" || e == "invalid_client" || e == "unauthorized_client" {
			return nil, fmt.Errorf("%w (%s)", ErrRefreshRejected, errText(m, code))
		}
		return nil, fmt.Errorf("falha ao renovar o acesso: %s", errText(m, code))
	}
	return tokenFrom(m, refresh, now), nil
}

// TokenSource entrega sempre um access token válido, renovando (e persistindo o novo refresh token) quando preciso.
type TokenSource struct {
	mu    sync.Mutex
	oauth *OAuth
	tok   Token
	save  func(context.Context, Token) error
	now   func() time.Time
}

func NewTokenSource(o *OAuth, t Token, save func(context.Context, Token) error) *TokenSource {
	return &TokenSource{oauth: o, tok: t, save: save, now: time.Now}
}

func (ts *TokenSource) Access(ctx context.Context) (string, error) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if ts.tok.Access != "" && ts.now().Add(90*time.Second).Before(ts.tok.Expiry) {
		return ts.tok.Access, nil
	}
	if ts.tok.Refresh == "" {
		return "", ErrAuth
	}
	nt, err := ts.oauth.Refresh(ctx, ts.tok.Refresh, ts.now())
	if err != nil {
		return "", err
	}
	ts.tok = *nt
	if ts.save != nil {
		if err := ts.save(ctx, *nt); err != nil {
			return "", fmt.Errorf("não foi possível gravar o novo token: %w", err)
		}
	}
	return nt.Access, nil
}

// AuthCodeURL monta o endereço de login do fluxo "código de autorização" com PKCE (S256).
func (o *OAuth) AuthCodeURL(redirect, state, challenge string) string {
	q := url.Values{"client_id": {o.ClientID}, "redirect_uri": {redirect}, "response_type": {"code"}, "scope": {o.Scope},
		"state": {state}, "code_challenge": {challenge}, "code_challenge_method": {"S256"},
		"access_type": {"offline"}, "prompt": {"consent"}, "include_granted_scopes": {"false"}}
	return o.AuthURL + "?" + q.Encode()
}

// ExchangeCode troca o código recebido na volta do login pelos tokens (com o verificador do PKCE).
func (o *OAuth) ExchangeCode(ctx context.Context, code, verifier, redirect string, now time.Time) (*Token, error) {
	form := url.Values{"client_id": {o.ClientID}, "code": {code}, "code_verifier": {verifier}, "redirect_uri": {redirect}, "grant_type": {"authorization_code"}}
	if o.ClientSecret != "" {
		form.Set("client_secret", o.ClientSecret)
	}
	m, status, err := o.post(ctx, o.TokenURL, form)
	if err != nil {
		return nil, err
	}
	if status != 200 || str(m, "access_token") == "" {
		return nil, fmt.Errorf("o provedor recusou o código de autorização: %s", errText(m, status))
	}
	t := tokenFrom(m, "", now)
	if t.Refresh == "" {
		return nil, errors.New("o provedor não devolveu a renovação de acesso (refresh token): revogue o acesso do Coruja na sua conta e tente de novo")
	}
	return t, nil
}
