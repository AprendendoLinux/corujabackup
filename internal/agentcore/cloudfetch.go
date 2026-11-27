package agentcore

// Arquivo de backup que só está na nuvem (0.0.28): o agente não tem as credenciais das contas de nuvem, então pede o
// arquivo ao Server, que o lê da nuvem e o entrega num fluxo gRPC próprio. O conteúdo é conferido pelo SHA-256 da
// execução antes de ser usado.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/AprendendoLinux/corujabackup/internal/pb"
)

// FetchStream abre o fluxo que traz um arquivo de backup da nuvem, via Server (nil = sem conexão).
type FetchStream func(ctx context.Context, req *pb.FetchRequest) (grpc.ServerStreamingClient[pb.FetchChunk], error)

type fetcher struct {
	mu sync.Mutex
	fn FetchStream
}

// SetFetcher liga/desliga a busca de arquivos na nuvem (enquanto houver conexão com o Server).
func (c *Core) SetFetcher(f FetchStream) {
	c.cf.mu.Lock()
	c.cf.fn = f
	c.cf.mu.Unlock()
}

func (c *Core) fetcherFn() FetchStream {
	c.cf.mu.Lock()
	defer c.cf.mu.Unlock()
	return c.cf.fn
}

// cloudStream abre, pelo Server, o arquivo name do job na nuvem (ou só o trecho [off, off+n), n > 0). size é o tamanho do
// que será entregue.
func (c *Core) cloudStream(ctx context.Context, jobID int64, name string, off, n int64) (io.ReadCloser, int64, error) {
	fn := c.fetcherFn()
	if fn == nil {
		return nil, 0, errors.New("sem conexão com o Server para trazer o arquivo da nuvem")
	}
	ctx, cancel := context.WithCancel(ctx)
	st, err := fn(ctx, &pb.FetchRequest{JobId: jobID, FileName: name, Offset: off, Length: n})
	if err != nil {
		cancel()
		return nil, 0, fetchErr(err)
	}
	first, err := st.Recv()
	if err != nil {
		cancel()
		return nil, 0, fetchErr(err)
	}
	return &fetchReader{st: st, cancel: cancel, left: first.Size}, first.Size, nil
}

// fetchReader lê o fluxo do Server como um io.Reader; o tamanho anunciado é conferido no fim.
type fetchReader struct {
	st     grpc.ServerStreamingClient[pb.FetchChunk]
	cancel context.CancelFunc
	buf    []byte
	left   int64 // < 0: tamanho desconhecido
}

func (f *fetchReader) Read(p []byte) (int, error) {
	for len(f.buf) == 0 {
		m, err := f.st.Recv()
		if errors.Is(err, io.EOF) {
			if f.left > 0 {
				return 0, fmt.Errorf("o arquivo veio incompleto da nuvem (faltaram %d bytes)", f.left)
			}
			return 0, io.EOF
		}
		if err != nil {
			return 0, fetchErr(err)
		}
		f.buf = m.Data
	}
	n := copy(p, f.buf)
	f.buf = f.buf[n:]
	if f.left >= 0 {
		f.left -= int64(n)
	}
	return n, nil
}

func (f *fetchReader) Close() error { f.cancel(); return nil }

// fetchFromCloud grava em dst o arquivo name do job, trazido da nuvem pelo Server, e confere o SHA-256 (se conhecido).
// Usado quando o arquivo inteiro é necessário (extração completa, restauração de banco).
func (c *Core) fetchFromCloud(ctx context.Context, jobID int64, name, dst, wantSha string, al *actionLog) error {
	rc, size, err := c.cloudStream(ctx, jobID, name, 0, 0)
	if err != nil {
		return err
	}
	defer rc.Close()
	al.logf("info", "trazendo %s da nuvem pelo Server (%s)", name, HumanBytes(size))
	tmp := dst + ".coruja-baixando"
	_ = os.Remove(tmp)
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		if !ok {
			_ = os.Remove(tmp)
		}
	}()
	h := sha256.New()
	w := io.MultiWriter(f, h)
	buf := make([]byte, 1<<20)
	var got int64
	for {
		n, rerr := rc.Read(buf)
		if n > 0 {
			if _, err := w.Write(buf[:n]); err != nil {
				f.Close()
				return err
			}
			before := got
			got += int64(n)
			if size >= 100<<20 && got*10/size > before*10/size && got < size { // arquivo grande: andamento a cada 10%
				al.logf("info", "  %d%% (%s de %s)", got*100/size, HumanBytes(got), HumanBytes(size))
			}
		}
		if errors.Is(rerr, io.EOF) {
			break
		}
		if rerr != nil {
			f.Close()
			return rerr
		}
	}
	if err := f.Close(); err != nil {
		return err
	}
	if sum := hex.EncodeToString(h.Sum(nil)); wantSha != "" && sum != wantSha {
		return fmt.Errorf("%s veio da nuvem diferente do backup original (SHA-256 não confere)", name)
	}
	if err := os.Rename(tmp, dst); err != nil {
		return err
	}
	ok = true
	return nil
}

func fetchErr(err error) error {
	if s, ok := status.FromError(err); ok {
		if s.Code() == codes.Unimplemented {
			return errors.New("o Server não entrega arquivos da nuvem ao agente: atualize o Server para a versão 0.0.28 ou mais nova")
		}
		if s.Code() == codes.Canceled {
			return context.Canceled
		}
		return errors.New(s.Message())
	}
	return err
}
