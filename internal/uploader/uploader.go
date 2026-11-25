// Package uploader envia um arquivo do agente direto para a nuvem por uma sessão de upload retomável (OneDrive/Graph
// ou Google Drive). O agente nunca recebe tokens de acesso: só a URL da sessão, que já carrega a autorização.
package uploader

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/AprendendoLinux/corujabackup/internal/cloud"
)

type Session struct {
	Protocol  string // "graph" | "gdrive"
	URL       string
	ChunkSize int64
}

type Result struct {
	RemoteID       string
	RemoteSize     int64
	RemoteMD5      string
	RemoteSHA1     string
	RemoteQuickXor string
	Digest         cloud.Digest
	VerifiedBy     string // quickxor | sha1 | md5 | size
}

var (
	ErrSessionExpired = errors.New("a sessão de upload expirou")
	errRetry          = errors.New("tentar de novo")
)

type Options struct {
	HTTP     *http.Client
	Progress func(sent, total int64)
	Sleep    func(context.Context, time.Duration) // trocável nos testes
	Retries  int
}

func (o *Options) client() *http.Client {
	if o.HTTP != nil {
		return o.HTTP
	}
	return &http.Client{Timeout: 5 * time.Minute}
}

func (o *Options) sleep(ctx context.Context, d time.Duration) {
	if o.Sleep != nil {
		o.Sleep(ctx, d)
		return
	}
	select {
	case <-time.After(d):
	case <-ctx.Done():
	}
}

// HashFile calcula todos os hashes numa leitura do arquivo.
func HashFile(ctx context.Context, path string) (cloud.Digest, error) {
	f, err := os.Open(path)
	if err != nil {
		return cloud.Digest{}, err
	}
	defer f.Close()
	s := cloud.NewSums()
	buf := make([]byte, 1<<20)
	for {
		if ctx.Err() != nil {
			return cloud.Digest{}, ctx.Err()
		}
		n, err := f.Read(buf)
		s.Write(buf[:n])
		if err == io.EOF {
			break
		}
		if err != nil {
			return cloud.Digest{}, err
		}
	}
	return s.Digest(), nil
}

// Upload envia path pela sessão e confere o resultado com os hashes que a própria nuvem devolve.
func Upload(ctx context.Context, sess Session, path string, opt Options) (Result, error) {
	var res Result
	f, err := os.Open(path)
	if err != nil {
		return res, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return res, err
	}
	total := st.Size()
	if total == 0 {
		return res, errors.New("arquivo vazio")
	}
	if res.Digest, err = HashFile(ctx, path); err != nil {
		return res, err
	}
	chunk := sess.ChunkSize
	if chunk <= 0 {
		chunk = 8 << 20
	}
	retries := opt.Retries
	if retries <= 0 {
		retries = 6
	}
	buf := make([]byte, chunk)
	var offset int64
	attempt := 0
	var final []byte
	for offset < total && final == nil {
		if ctx.Err() != nil {
			return res, ctx.Err()
		}
		n := chunk
		if offset+n > total {
			n = total - offset
		}
		if _, err := f.ReadAt(buf[:n], offset); err != nil && err != io.EOF {
			return res, err
		}
		body, next, err := putChunk(ctx, opt.client(), sess, buf[:n], offset, total)
		switch {
		case err == nil:
			attempt = 0
			if body != nil {
				final = body
			}
			offset = next
			if opt.Progress != nil {
				opt.Progress(offset, total)
			}
		case errors.Is(err, errRetry):
			attempt++
			if attempt > retries {
				return res, fmt.Errorf("o envio falhou após %d tentativas: %w", retries, errors.Unwrap(err))
			}
			opt.sleep(ctx, backoff(attempt))
			if ctx.Err() != nil {
				return res, ctx.Err()
			}
			// pergunta à nuvem até onde ela recebeu e continua dali
			off, done, qerr := queryStatus(ctx, opt.client(), sess, total)
			if errors.Is(qerr, ErrSessionExpired) {
				return res, qerr
			}
			if done != nil {
				final, offset = done, total
			} else if qerr == nil {
				offset = off
			}
		default:
			return res, err
		}
	}
	if final == nil {
		return res, errors.New("a nuvem não confirmou o fim do envio")
	}
	return finish(sess, final, res, total)
}

func backoff(n int) time.Duration {
	d := time.Duration(1<<uint(n-1)) * time.Second
	if d > 30*time.Second {
		d = 30 * time.Second
	}
	return d
}

func retryable(code int) bool {
	return code == 408 || code == 429 || code >= 500
}

// putChunk devolve (corpo final, próximo offset, erro). Corpo final != nil quando a nuvem confirmou o arquivo completo.
func putChunk(ctx context.Context, c *http.Client, s Session, data []byte, offset, total int64) ([]byte, int64, error) {
	req, err := http.NewRequestWithContext(ctx, "PUT", s.URL, bytes.NewReader(data))
	if err != nil {
		return nil, offset, err
	}
	req.ContentLength = int64(len(data))
	req.Header.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", offset, offset+int64(len(data))-1, total))
	resp, err := c.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, offset, ctx.Err()
		}
		return nil, offset, fmt.Errorf("%w: %w", errRetry, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	last := offset+int64(len(data)) >= total
	switch {
	case resp.StatusCode == 200 || resp.StatusCode == 201:
		if !last {
			// alguns provedores respondem 200 ao completar antes do esperado: trata como fim
			return body, total, nil
		}
		return body, total, nil
	case resp.StatusCode == 202 || resp.StatusCode == 308:
		next := offset + int64(len(data))
		if s.Protocol == "gdrive" {
			if rg := resp.Header.Get("Range"); rg != "" { // "bytes=0-N"
				if i := strings.LastIndex(rg, "-"); i >= 0 {
					if v, err := strconv.ParseInt(rg[i+1:], 10, 64); err == nil {
						next = v + 1
					}
				}
			}
		}
		return nil, next, nil
	case resp.StatusCode == 404 || resp.StatusCode == 410:
		return nil, offset, ErrSessionExpired
	case retryable(resp.StatusCode):
		if ra := resp.Header.Get("Retry-After"); ra != "" {
			if sec, err := strconv.Atoi(ra); err == nil && sec > 0 && sec <= 120 {
				time.Sleep(time.Duration(sec) * time.Second)
			}
		}
		return nil, offset, fmt.Errorf("%w: %w", errRetry, fmt.Errorf("HTTP %d", resp.StatusCode))
	}
	return nil, offset, fmt.Errorf("a nuvem recusou o envio: HTTP %d %s", resp.StatusCode, trim(string(body)))
}

func trim(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 300 {
		return s[:300] + "…"
	}
	return s
}

// queryStatus pergunta à nuvem o próximo byte esperado. Se o envio já estiver completo, devolve o corpo final.
func queryStatus(ctx context.Context, c *http.Client, s Session, total int64) (int64, []byte, error) {
	var req *http.Request
	var err error
	if s.Protocol == "gdrive" {
		req, err = http.NewRequestWithContext(ctx, "PUT", s.URL, nil)
		if err == nil {
			req.ContentLength = 0
			req.Header.Set("Content-Range", fmt.Sprintf("bytes */%d", total))
		}
	} else {
		req, err = http.NewRequestWithContext(ctx, "GET", s.URL, nil)
	}
	if err != nil {
		return 0, nil, err
	}
	resp, err := c.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == 404 || resp.StatusCode == 410 {
		return 0, nil, ErrSessionExpired
	}
	if s.Protocol == "gdrive" {
		switch resp.StatusCode {
		case 200, 201:
			return total, body, nil
		case 308:
			if rg := resp.Header.Get("Range"); rg != "" {
				if i := strings.LastIndex(rg, "-"); i >= 0 {
					if v, err := strconv.ParseInt(rg[i+1:], 10, 64); err == nil {
						return v + 1, nil, nil
					}
				}
			}
			return 0, nil, nil
		}
		return 0, nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	if resp.StatusCode != 200 {
		return 0, nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var st struct {
		Ranges []string `json:"nextExpectedRanges"`
	}
	if json.Unmarshal(body, &st) != nil || len(st.Ranges) == 0 {
		return 0, nil, errors.New("resposta de status inválida")
	}
	first := strings.SplitN(st.Ranges[0], "-", 2)[0]
	v, err := strconv.ParseInt(first, 10, 64)
	if err != nil {
		return 0, nil, err
	}
	return v, nil, nil
}

// finish lê os hashes devolvidos pela nuvem e confere com os calculados localmente.
func finish(s Session, body []byte, res Result, total int64) (Result, error) {
	if s.Protocol == "gdrive" {
		var d struct {
			ID   string `json:"id"`
			Size string `json:"size"`
			MD5  string `json:"md5Checksum"`
		}
		if err := json.Unmarshal(body, &d); err != nil {
			return res, fmt.Errorf("resposta final inválida: %w", err)
		}
		res.RemoteID, res.RemoteMD5 = d.ID, strings.ToLower(d.MD5)
		res.RemoteSize, _ = strconv.ParseInt(d.Size, 10, 64)
	} else {
		var d struct {
			ID   string `json:"id"`
			Size int64  `json:"size"`
			File *struct {
				Hashes struct {
					QuickXor string `json:"quickXorHash"`
					SHA1     string `json:"sha1Hash"`
				} `json:"hashes"`
			} `json:"file"`
		}
		if err := json.Unmarshal(body, &d); err != nil {
			return res, fmt.Errorf("resposta final inválida: %w", err)
		}
		res.RemoteID, res.RemoteSize = d.ID, d.Size
		if d.File != nil {
			res.RemoteQuickXor, res.RemoteSHA1 = d.File.Hashes.QuickXor, strings.ToLower(d.File.Hashes.SHA1)
		}
	}
	if res.RemoteSize != 0 && res.RemoteSize != total {
		return res, fmt.Errorf("a nuvem guardou %d bytes, esperado %d", res.RemoteSize, total)
	}
	switch {
	case res.RemoteQuickXor != "":
		if res.RemoteQuickXor != res.Digest.QuickXor {
			return res, errors.New("o hash quickXor da nuvem difere do arquivo enviado")
		}
		res.VerifiedBy = "quickxor"
	case res.RemoteSHA1 != "":
		if res.RemoteSHA1 != res.Digest.SHA1 {
			return res, errors.New("o hash SHA-1 da nuvem difere do arquivo enviado")
		}
		res.VerifiedBy = "sha1"
	case res.RemoteMD5 != "":
		if res.RemoteMD5 != res.Digest.MD5 {
			return res, errors.New("o hash MD5 da nuvem difere do arquivo enviado")
		}
		res.VerifiedBy = "md5"
	case res.RemoteSize == total:
		res.VerifiedBy = "size"
	default:
		return res, errors.New("a nuvem não devolveu hash nem tamanho para conferir")
	}
	return res, nil
}
