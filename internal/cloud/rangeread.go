package cloud

import (
	"fmt"
	"io"
	"net/http"
)

// setRange pede ao provedor só o trecho [off, off+n) (n <= 0 = até o fim). Sem trecho (0, 0), não mexe no pedido.
func setRange(req *http.Request, off, n int64) {
	switch {
	case off <= 0 && n <= 0:
	case n <= 0:
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", off))
	default:
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", off, off+n-1))
	}
}

// fitRange devolve o trecho pedido. Se o provedor ignorou o Range e mandou o arquivo inteiro (200), pula o começo e
// corta no tamanho pedido: o resultado é o mesmo, só mais lento.
func fitRange(resp *http.Response, off, n int64) (io.ReadCloser, int64, error) {
	if (off <= 0 && n <= 0) || resp.StatusCode == http.StatusPartialContent {
		return resp.Body, resp.ContentLength, nil
	}
	if off > 0 {
		if _, err := io.CopyN(io.Discard, resp.Body, off); err != nil {
			resp.Body.Close()
			return nil, 0, fmt.Errorf("o arquivo na nuvem é menor que o esperado: %w", err)
		}
	}
	size := resp.ContentLength
	if size >= 0 {
		size -= off
	}
	if n <= 0 {
		return resp.Body, size, nil
	}
	return limitedBody{io.LimitReader(resp.Body, n), resp.Body}, n, nil
}

type limitedBody struct {
	io.Reader
	c io.Closer
}

func (l limitedBody) Close() error { return l.c.Close() }
