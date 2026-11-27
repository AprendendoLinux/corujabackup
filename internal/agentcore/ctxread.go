package agentcore

import (
	"context"
	"io"
)

// ctxRd interrompe a leitura assim que ctx é cancelado. Sem isso, um cancelamento só valeria ao fim da cópia do
// arquivo atual, o que num arquivo de dezenas de GB leva minutos.
func ctxRd(ctx context.Context, r io.Reader) io.Reader { return &ctxReader{ctx: ctx, r: r} }

type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c *ctxReader) Read(p []byte) (int, error) {
	if c.ctx.Err() != nil {
		return 0, ErrCanceled
	}
	return c.r.Read(p)
}
