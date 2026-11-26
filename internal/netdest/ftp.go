package netdest

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net/textproto"
	"path"
	"strings"

	"github.com/jlaffaye/ftp"
)

type ftpConn struct {
	cl   *ftp.ServerConn
	root string // pasta inicial do login
	base string
}

func dialFTP(ctx context.Context, c Config) (Conn, error) {
	opts := []ftp.DialOption{ftp.DialWithContext(ctx), ftp.DialWithTimeout(dialTimeout)}
	tc := &tls.Config{ServerName: c.Host, MinVersion: tls.VersionTLS12, InsecureSkipVerify: c.SkipVerify, //nolint:gosec // opção explícita do usuário
		ClientSessionCache: tls.NewLRUClientSessionCache(16)} // vários servidores exigem reuso da sessão no canal de dados
	switch c.TLS {
	case TLSExplicit:
		opts = append(opts, ftp.DialWithExplicitTLS(tc))
	case TLSImplicit:
		opts = append(opts, ftp.DialWithTLS(tc))
	}
	cl, err := ftp.Dial(c.address(), opts...)
	if err != nil {
		return nil, fmt.Errorf("não foi possível conectar em %s: %w", c.address(), err)
	}
	if err := cl.Login(c.User, c.Password); err != nil {
		_ = cl.Quit()
		return nil, fmt.Errorf("o servidor FTP recusou o login: %w", err)
	}
	root, err := cl.CurrentDir()
	if err != nil {
		_ = cl.Quit()
		return nil, err
	}
	return &ftpConn{cl: cl, root: root, base: c.BasePath}, nil
}

func (f *ftpConn) p(parts ...string) string {
	return path.Join(append([]string{f.root, f.base}, parts...)...)
}

func isFTPCode(err error, code int) bool {
	var e *textproto.Error
	return errors.As(err, &e) && e.Code == code
}

func (f *ftpConn) MkdirAll(dir string) error {
	cur := f.root
	for _, seg := range strings.Split(path.Join(f.base, dir), "/") {
		if seg == "" || seg == "." {
			continue
		}
		cur = path.Join(cur, seg)
		if err := f.cl.MakeDir(cur); err != nil {
			// já existe? Confirma entrando na pasta (os códigos de erro variam entre servidores)
			if cerr := f.cl.ChangeDir(cur); cerr != nil {
				return err
			}
			if cerr := f.cl.ChangeDir(f.root); cerr != nil {
				return cerr
			}
		}
	}
	return nil
}

func (f *ftpConn) Write(_ context.Context, dir, name string, r io.Reader) error {
	return f.cl.Stor(f.p(dir, name), r)
}

func (f *ftpConn) Open(_ context.Context, dir, name string) (io.ReadCloser, error) {
	return f.cl.Retr(f.p(dir, name))
}

func (f *ftpConn) Size(dir, name string) (int64, error) {
	if n, err := f.cl.FileSize(f.p(dir, name)); err == nil {
		return n, nil
	}
	files, err := f.List(dir) // servidores sem o comando SIZE
	if err != nil {
		return 0, err
	}
	for _, x := range files {
		if x.Name == name {
			return x.Size, nil
		}
	}
	return 0, errors.New("arquivo não encontrado no destino")
}

func (f *ftpConn) List(dir string) ([]File, error) {
	es, err := f.cl.List(f.p(dir))
	if isFTPCode(err, ftp.StatusFileUnavailable) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []File
	for _, e := range es {
		if e.Type == ftp.EntryTypeFile {
			out = append(out, File{Name: path.Base(e.Name), Size: int64(e.Size), Modified: e.Time}) //nolint:gosec
		}
	}
	return out, nil
}

func (f *ftpConn) Dirs(dir string) ([]string, error) {
	es, err := f.cl.List(f.p(dir))
	if isFTPCode(err, ftp.StatusFileUnavailable) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range es {
		if n := path.Base(e.Name); e.Type == ftp.EntryTypeFolder && n != "." && n != ".." {
			out = append(out, n)
		}
	}
	return out, nil
}

func (f *ftpConn) Rename(dir, from, to string) error {
	return f.cl.Rename(f.p(dir, from), f.p(dir, to))
}
func (f *ftpConn) Remove(dir, name string) error { return f.cl.Delete(f.p(dir, name)) }
func (f *ftpConn) Close() error                  { return f.cl.Quit() }
