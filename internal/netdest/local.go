package netdest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// localConn grava numa pasta do próprio agente (um volume montado no host: NFS, iSCSI, disco USB...). Usa os.Root, que
// impede que qualquer caminho (inclusive por link simbólico) escape da raiz de destinos.
type localConn struct {
	root *os.Root
	base string
}

func openLocal(c Config, env Env) (Conn, error) {
	if env.LocalRoot == "" {
		return nil, errors.New("este agente não tem uma raiz para destinos locais (CORUJA_DEST_DIR)")
	}
	r, err := os.OpenRoot(env.LocalRoot)
	if err != nil {
		return nil, fmt.Errorf("a raiz de destinos %s não existe no agente: monte o volume nela (veja a documentação): %w", env.LocalRoot, err)
	}
	return &localConn{root: r, base: c.BasePath}, nil
}

func (l *localConn) p(parts ...string) string {
	p := filepath.Join(append([]string{l.base}, parts...)...)
	if p == "" {
		return "."
	}
	return p
}

func (l *localConn) MkdirAll(dir string) error {
	p := l.p(dir)
	if p == "." {
		return nil
	}
	return l.root.MkdirAll(p, 0o750)
}

func (l *localConn) Write(_ context.Context, dir, name string, r io.Reader) error {
	f, err := l.root.OpenFile(l.p(dir, name), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o640)
	if err != nil {
		return err
	}
	_, err = io.Copy(f, r)
	if serr := f.Sync(); err == nil {
		err = serr
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

func (l *localConn) Open(_ context.Context, dir, name string) (io.ReadCloser, error) {
	return l.root.Open(l.p(dir, name))
}

func (l *localConn) Size(dir, name string) (int64, error) {
	st, err := l.root.Stat(l.p(dir, name))
	if err != nil {
		return 0, err
	}
	return st.Size(), nil
}

func (l *localConn) List(dir string) ([]File, error) {
	d, err := l.root.Open(l.p(dir))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer d.Close()
	es, err := d.ReadDir(-1)
	if err != nil {
		return nil, err
	}
	var out []File
	for _, e := range es {
		if info, ierr := e.Info(); ierr == nil && info.Mode().IsRegular() {
			out = append(out, File{Name: e.Name(), Size: info.Size(), Modified: info.ModTime()})
		}
	}
	return out, nil
}

func (l *localConn) Rename(dir, from, to string) error {
	return l.root.Rename(l.p(dir, from), l.p(dir, to))
}
func (l *localConn) Remove(dir, name string) error { return l.root.Remove(l.p(dir, name)) }
func (l *localConn) Close() error                  { return l.root.Close() }
