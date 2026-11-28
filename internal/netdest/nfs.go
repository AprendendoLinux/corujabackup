package netdest

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"path"

	"github.com/AprendendoLinux/corujabackup/internal/nfs3"
)

// nfsConn grava numa exportação NFSv3 pelo cliente embutido (sem montar nada na máquina do agente).
type nfsConn struct {
	cl   *nfs3.Client
	ctx  context.Context
	base string
}

func dialNFS(ctx context.Context, c Config) (Conn, error) {
	uid, gid := uint32(0), uint32(0)
	if c.UID != nil {
		uid = uint32(*c.UID)
	}
	if c.GID != nil {
		gid = uint32(*c.GID)
	}
	cl, err := nfs3.Dial(ctx, nfs3.Options{Host: c.Host, Port: c.Port, Export: c.Export, UID: uid, GID: gid})
	if err != nil {
		return nil, err
	}
	return &nfsConn{cl: cl, ctx: ctx, base: c.BasePath}, nil
}

func (n *nfsConn) p(parts ...string) string { return path.Join(append([]string{n.base}, parts...)...) }

func (n *nfsConn) MkdirAll(dir string) error { return n.cl.MkdirAll(n.ctx, n.p(dir)) }

func (n *nfsConn) Write(ctx context.Context, dir, name string, r io.Reader) error {
	w, err := n.cl.Create(ctx, n.p(dir, name))
	if err != nil {
		return err
	}
	_, err = io.Copy(w, r)
	if cerr := w.Close(); err == nil {
		err = cerr
	}
	return err
}

func (n *nfsConn) Open(ctx context.Context, dir, name string) (io.ReadCloser, error) {
	return n.cl.Open(ctx, n.p(dir, name))
}

func (n *nfsConn) Size(dir, name string) (int64, error) {
	a, err := n.cl.Stat(n.ctx, n.p(dir, name))
	return a.Size, err
}

func (n *nfsConn) entries(dir string) ([]nfs3.Entry, error) {
	es, err := n.cl.ReadDir(n.ctx, n.p(dir))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	return es, err
}

func (n *nfsConn) List(dir string) ([]File, error) {
	es, err := n.entries(dir)
	var out []File
	for _, e := range es {
		if e.Regular {
			out = append(out, File{Name: e.Name, Size: e.Size, Modified: e.Modified})
		}
	}
	return out, err
}

func (n *nfsConn) Dirs(dir string) ([]string, error) {
	es, err := n.entries(dir)
	var out []string
	for _, e := range es {
		if e.Dir {
			out = append(out, e.Name)
		}
	}
	return out, err
}

func (n *nfsConn) Rename(dir, from, to string) error {
	return n.cl.Rename(n.ctx, n.p(dir, from), n.p(dir, to))
}

func (n *nfsConn) Remove(dir, name string) error { return n.cl.Remove(n.ctx, n.p(dir, name)) }
func (n *nfsConn) RemoveAll(dir string) error    { return n.cl.RemoveAll(n.ctx, n.p(dir)) }
func (n *nfsConn) Link(from, to string) error    { return n.cl.Link(n.ctx, n.p(from), n.p(to)) }

func (n *nfsConn) Close() error { return n.cl.Close() }

// Space devolve o espaço total e o livre na exportação.
func (n *nfsConn) Space() (total, free int64, err error) { return n.cl.FSStat(n.ctx) }
