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

// uploadDropbox envia em partes por uma sessão de upload do Dropbox (start, append_v2, finish). Retoma pelo
// "correct_offset" quando o Dropbox diz ter recebido outra quantidade. O token é de curta duração: se vencer, devolve
// ErrSessionExpired para o agente pedir um novo.
func uploadDropbox(ctx context.Context, s Session, f *os.File, res Result, total int64, opt Options) (Result, error) {
	chunk := s.ChunkSize
	if chunk <= 0 {
		chunk = 8 << 20
	}
	retries := opt.Retries
	if retries <= 0 {
		retries = 6
	}
	base := strings.TrimRight(s.URL, "/")
	buf := make([]byte, chunk)
	var offset int64
	var id string
	attempt := 0
	for {
		if ctx.Err() != nil {
			return res, ctx.Err()
		}
		n := min(chunk, total-offset)
		if n > 0 {
			if _, err := f.ReadAt(buf[:n], offset); err != nil && err != io.EOF {
				return res, err
			}
		}
		data := buf[:max(n, 0)]
		last := offset+int64(len(data)) >= total
		var url string
		var arg any
		switch {
		case id == "":
			url, arg = base+"/start", map[string]any{"close": false}
		case !last:
			url, arg = base+"/append_v2", map[string]any{"cursor": map[string]any{"session_id": id, "offset": offset}, "close": false}
		default:
			url = base + "/finish"
			arg = map[string]any{"cursor": map[string]any{"session_id": id, "offset": offset},
				"commit": map[string]any{"path": s.Path, "mode": "overwrite", "autorename": false, "mute": true}}
		}
		body, correct, err := dbxPost(ctx, opt.client(), url, s.Token, arg, data)
		if err == nil {
			attempt = 0
			if id == "" {
				var r struct {
					ID string `json:"session_id"`
				}
				if json.Unmarshal(body, &r) != nil || r.ID == "" {
					return res, errors.New("o Dropbox não devolveu a sessão de upload")
				}
				id = r.ID
				offset += int64(len(data))
			} else if !last {
				offset += int64(len(data))
			} else {
				return finishDropbox(body, res, total)
			}
			if opt.Progress != nil {
				opt.Progress(min(offset, total), total)
			}
			continue
		}
		if correct >= 0 && correct <= total { // o Dropbox sabe até onde recebeu: continua dali
			offset = correct
			attempt++
		} else if errors.Is(err, errRetry) {
			attempt++
			opt.sleep(ctx, backoff(attempt))
		} else {
			return res, err
		}
		if attempt > retries {
			return res, fmt.Errorf("o envio falhou após %d tentativas: %w", retries, err)
		}
	}
}

// dbxPost devolve o corpo (HTTP 200) ou um erro; correct >= 0 quando o Dropbox informa o deslocamento certo.
func dbxPost(ctx context.Context, c *http.Client, url, token string, arg any, data []byte) (body []byte, correct int64, err error) {
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(data))
	if err != nil {
		return nil, -1, err
	}
	req.ContentLength = int64(len(data))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Dropbox-API-Arg", cloud.ASCIIJSON(arg))
	resp, err := c.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, -1, ctx.Err()
		}
		return nil, -1, fmt.Errorf("%w: %w", errRetry, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == 200 {
		return b, -1, nil
	}
	var e struct {
		Summary string `json:"error_summary"`
		Error   struct {
			Tag     string `json:".tag"`
			Correct *int64 `json:"correct_offset"`
			Lookup  *struct {
				Correct *int64 `json:"correct_offset"`
			} `json:"lookup_failed"`
		} `json:"error"`
	}
	_ = json.Unmarshal(b, &e)
	switch {
	case resp.StatusCode == 401:
		return nil, -1, ErrSessionExpired // token de curta duração vencido
	case resp.StatusCode == 409 && e.Error.Correct != nil:
		return nil, *e.Error.Correct, fmt.Errorf("deslocamento incorreto: %s", e.Summary)
	case resp.StatusCode == 409 && e.Error.Lookup != nil && e.Error.Lookup.Correct != nil:
		return nil, *e.Error.Lookup.Correct, fmt.Errorf("deslocamento incorreto: %s", e.Summary)
	case resp.StatusCode == 409 && strings.Contains(e.Summary, "not_found"):
		return nil, -1, ErrSessionExpired
	case retryable(resp.StatusCode):
		if sec, perr := strconv.Atoi(resp.Header.Get("Retry-After")); perr == nil && sec > 0 && sec <= 120 {
			time.Sleep(time.Duration(sec) * time.Second)
		}
		return nil, -1, fmt.Errorf("%w: HTTP %d", errRetry, resp.StatusCode)
	}
	return nil, -1, fmt.Errorf("a nuvem recusou o envio: HTTP %d %s", resp.StatusCode, trim(string(b)))
}

func finishDropbox(body []byte, res Result, total int64) (Result, error) {
	var d struct {
		ID   string `json:"id"`
		Size int64  `json:"size"`
		Hash string `json:"content_hash"`
	}
	if err := json.Unmarshal(body, &d); err != nil {
		return res, fmt.Errorf("resposta final inválida: %w", err)
	}
	res.RemoteID, res.RemoteSize, res.RemoteDropbox = d.ID, d.Size, d.Hash
	if d.Size != total {
		return res, fmt.Errorf("%w: a nuvem guardou %d bytes, esperado %d", ErrMismatch, d.Size, total)
	}
	if d.Hash == "" {
		res.VerifiedBy = "size"
		return res, nil
	}
	if d.Hash != res.Digest.Dropbox {
		return res, fmt.Errorf("%w: o content_hash do Dropbox difere do arquivo enviado", ErrMismatch)
	}
	res.VerifiedBy = "dropbox"
	return res, nil
}
