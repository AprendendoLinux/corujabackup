package netdest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// localConn grava numa pasta da máquina do agente: qualquer pasta escolhida na interface (disco extra, iSCSI, USB já
// montados) ou, nos storages antigos, uma subpasta da raiz de destinos (/destinos). Usa os.Root, que impede que qualquer
// caminho (inclusive por link simbólico) escape da pasta.
type localConn struct {
	root *os.Root
	dir  string
	base string
}

func openLocal(c Config, env Env) (Conn, error) {
	if c.Path != "" {
		if env.HostDir == nil {
			return nil, errors.New("este agente não grava fora das próprias pastas: adicione o volume \"- /:/host-rw\" à stack do agente")
		}
		dir, err := env.HostDir(c.Path)
		if err != nil {
			return nil, err
		}
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return nil, fmt.Errorf("não foi possível criar a pasta %s: %w", c.Path, err)
		}
		r, err := os.OpenRoot(dir)
		if err != nil {
			return nil, fmt.Errorf("não foi possível abrir a pasta %s: %w", c.Path, err)
		}
		return &localConn{root: r, dir: dir, base: c.BasePath}, nil
	}
	if env.LocalRoot == "" {
		return nil, errors.New("este agente não tem uma raiz para destinos locais (CORUJA_DEST_DIR)")
	}
	r, err := os.OpenRoot(env.LocalRoot)
	if err != nil {
		return nil, fmt.Errorf("a raiz de destinos %s não existe no agente: monte o volume nela (veja a documentação): %w", env.LocalRoot, err)
	}
	return &localConn{root: r, dir: env.LocalRoot, base: c.BasePath}, nil
}

// Space devolve o espaço total e o livre do disco da pasta.
func (l *localConn) Space() (total, free int64, err error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(l.dir, &st); err != nil {
		return 0, 0, err
	}
	bs := int64(st.Bsize)
	return int64(st.Blocks) * bs, int64(st.Bavail) * bs, nil
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

func (l *localConn) Dirs(dir string) ([]string, error) {
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
	var out []string
	for _, e := range es {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	return out, nil
}

func (l *localConn) Rename(dir, from, to string) error {
	return l.root.Rename(l.p(dir, from), l.p(dir, to))
}
func (l *localConn) Remove(dir, name string) error { return l.root.Remove(l.p(dir, name)) }
func (l *localConn) RemoveAll(dir string) error    { return l.root.RemoveAll(l.p(dir)) }

func (l *localConn) Link(from, to string) error { return l.root.Link(l.p(from), l.p(to)) }

func (l *localConn) Symlink(name, target string) error { return l.root.Symlink(target, l.p(name)) }

func (l *localConn) Chmod(name string, mode fs.FileMode) error { return l.root.Chmod(l.p(name), mode) }

// SetAttrs devolve dono, permissões e data originais (o que der: sem ser root, o dono fica o do agente).
func (l *localConn) SetAttrs(name string, mode fs.FileMode, uid, gid int, mtime time.Time) {
	p := l.p(name)
	_ = l.root.Lchown(p, uid, gid)
	if mode&fs.ModeSymlink != 0 {
		return
	}
	_ = l.root.Chmod(p, mode&fs.ModePerm)
	_ = l.root.Chtimes(p, mtime, mtime)
}
func (l *localConn) Close() error { return l.root.Close() }
