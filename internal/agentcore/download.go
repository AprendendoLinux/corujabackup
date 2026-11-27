package agentcore

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"strings"
	"sync"

	"google.golang.org/grpc"

	"github.com/AprendendoLinux/corujabackup/internal/pb"
)

// DownloadStream abre o fluxo de download com o Server (nil = sem conexão).
type DownloadStream func(ctx context.Context) (grpc.ClientStreamingClient[pb.DownloadChunk, pb.DownloadDone], error)

type downloader struct {
	mu sync.Mutex
	fn DownloadStream
}

func (d *downloader) get() DownloadStream {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.fn
}

// SetDownloader liga/desliga a abertura de downloads (enquanto houver conexão com o Server).
func (c *Core) SetDownloader(f DownloadStream) {
	c.dl.mu.Lock()
	c.dl.fn = f
	c.dl.mu.Unlock()
}

const chunkSize = 256 << 10

// chunkWriter junta o que for escrito em pedaços e os envia pelo fluxo.
type chunkWriter struct {
	s     grpc.ClientStreamingClient[pb.DownloadChunk, pb.DownloadDone]
	id    string
	name  string
	buf   []byte
	first bool
	n     int64
}

func (w *chunkWriter) Write(p []byte) (int, error) {
	n := len(p)
	for len(p) > 0 {
		room := chunkSize - len(w.buf)
		if room > len(p) {
			room = len(p)
		}
		w.buf = append(w.buf, p[:room]...)
		p = p[room:]
		if len(w.buf) == chunkSize {
			if err := w.flush(); err != nil {
				return 0, err
			}
		}
	}
	return n, nil
}

func (w *chunkWriter) flush() error {
	m := &pb.DownloadChunk{Data: w.buf}
	if !w.first {
		m.Id, m.FileName, w.first = w.id, w.name, true
	}
	w.n += int64(len(w.buf))
	err := w.s.Send(m)
	w.buf = make([]byte, 0, chunkSize)
	return err
}

// download entrega ao Server (e daí ao navegador) o que foi escolhido de um backup: um arquivo como está ou um .zip
// com arquivos e pastas, cada item tirado do pacote da cadeia que guarda aquela versão.
func (c *Core) download(ctx context.Context, act *pb.AgentAction, al *actionLog) error {
	open := c.dl.get()
	if open == nil {
		return errors.New("sem conexão com o Server")
	}
	s, err := open(ctx)
	if err != nil {
		return err
	}
	fail := func(err error) error {
		_ = s.Send(&pb.DownloadChunk{Id: act.DownloadId, Error: err.Error()})
		_, _ = s.CloseAndRecv()
		return err
	}
	if len(act.Paths) == 0 || len(act.Paths) > 5000 {
		return fail(errors.New("escolha o que baixar"))
	}
	cat, err := c.catalogFor(ctx, act, al)
	if err != nil {
		return fail(err)
	}
	groups, n := selectFiles(cat, act.Paths)
	if n == 0 {
		return fail(errors.New("nenhum dos itens escolhidos está neste backup"))
	}
	single := ""
	if !act.Zip {
		p := path.Clean("/" + act.Paths[0])
		if m, ok := cat.Files[p]; !ok || len(act.Paths) != 1 || !os.FileMode(m.Mode).IsRegular() {
			return fail(errors.New("para baixar sem compactar, escolha um único arquivo"))
		}
		single = strings.TrimPrefix(p, "/")
	}
	idx := make([]int, 0, len(groups))
	for i := range groups {
		if i < 0 || i >= len(cat.Archives) {
			return fail(errors.New("catálogo inconsistente"))
		}
		idx = append(idx, i)
	}
	sort.Ints(idx)
	names := make([]string, len(idx))
	for k, i := range idx {
		names[k] = cat.Archives[i]
	}
	shaOf := map[string]string{}
	for i, nm := range act.Chain {
		if i < len(act.ChainSha256) {
			shaOf[nm] = act.ChainSha256[i]
		}
	}
	files, fetched, err := c.obtain(ctx, act, al, names, shaOf)
	defer func() {
		for _, f := range fetched {
			_ = os.Remove(f)
		}
	}()
	if err != nil {
		return fail(err)
	}
	fname := strings.TrimSuffix(strings.TrimSuffix(act.FileName, ".tar.gz"), "-inc") + ".zip"
	if single != "" {
		fname = path.Base(single)
	}
	w := &chunkWriter{s: s, id: act.DownloadId, name: fname, buf: make([]byte, 0, chunkSize)}
	if single != "" {
		err = copyMember(ctx, files[0], single, w)
	} else {
		zw := zip.NewWriter(w)
		for k, i := range idx {
			if err = zipMembers(ctx, files[k], groups[i], zw); err != nil {
				break
			}
		}
		if cerr := zw.Close(); err == nil {
			err = cerr
		}
	}
	if err == nil && (len(w.buf) > 0 || !w.first) {
		err = w.flush()
	}
	if errors.Is(err, io.EOF) || (err != nil && ctx.Err() == nil && strings.Contains(err.Error(), "transport")) {
		return errors.New("o download foi interrompido (o navegador cancelou ou a conexão caiu)")
	}
	if err != nil {
		return fail(err)
	}
	if _, err := s.CloseAndRecv(); err != nil {
		if errors.Is(err, io.EOF) {
			return errors.New("o download foi interrompido (o navegador cancelou ou a conexão caiu)")
		}
		return err
	}
	al.logf("info", "download entregue: %s (%s)", fname, HumanBytes(w.n))
	al.flush("ok", "download entregue", nil)
	return nil
}

// eachMember percorre o .tar.gz chamando fn para os itens aceitos por keep.
func eachMember(ctx context.Context, archive string, keep func(rel string) bool, fn func(h *tar.Header, r io.Reader, rel string) error) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		rel := strings.TrimPrefix(path.Clean("/"+h.Name), "/")
		if rel == "" || rel == manifestName || !keep(rel) {
			continue
		}
		if err := fn(h, tr, rel); err != nil {
			return err
		}
	}
}

func copyMember(ctx context.Context, archive, rel string, w io.Writer) error {
	found := false
	err := eachMember(ctx, archive, func(r string) bool { return r == rel }, func(h *tar.Header, r io.Reader, _ string) error {
		if h.Typeflag != tar.TypeReg {
			return nil
		}
		found = true
		_, err := io.Copy(w, ctxRd(ctx, r))
		return err
	})
	if err == nil && !found {
		err = fmt.Errorf("%s não está no arquivo de backup", rel)
	}
	return err
}

// zipMembers acrescenta ao .zip os itens escolhidos (arquivos, pastas e atalhos) de um pacote.
func zipMembers(ctx context.Context, archive string, want map[string]bool, zw *zip.Writer) error {
	return eachMember(ctx, archive, func(r string) bool { return want[r] }, func(h *tar.Header, r io.Reader, rel string) error {
		fh := &zip.FileHeader{Name: rel, Modified: h.ModTime, Method: zip.Deflate}
		switch h.Typeflag {
		case tar.TypeDir:
			fh.Name, fh.Method = rel+"/", zip.Store
			fh.SetMode(os.ModeDir | os.FileMode(h.Mode)&0o777)
			_, err := zw.CreateHeader(fh)
			return err
		case tar.TypeSymlink:
			fh.Method = zip.Store
			fh.SetMode(os.ModeSymlink | 0o777)
			out, err := zw.CreateHeader(fh)
			if err == nil {
				_, err = io.WriteString(out, h.Linkname)
			}
			return err
		case tar.TypeReg:
			fh.SetMode(os.FileMode(h.Mode) & 0o777)
			out, err := zw.CreateHeader(fh)
			if err != nil {
				return err
			}
			_, err = io.Copy(out, ctxRd(ctx, r))
			return err
		}
		return nil
	})
}
