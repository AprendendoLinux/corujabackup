package netdest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"path"
	"strings"

	smb2 "github.com/cloudsoda/go-smb2"
)

type smbConn struct {
	sess  *smb2.Session
	share *smb2.Share
	base  string
}

func dialSMB(ctx context.Context, c Config) (Conn, error) {
	d := &smb2.Dialer{Initiator: &smb2.NTLMInitiator{User: c.User, Password: c.Password, Domain: c.Domain}}
	dctx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()
	nd := net.Dialer{}
	raw, err := nd.DialContext(dctx, "tcp", c.address())
	if err != nil {
		return nil, fmt.Errorf("não foi possível conectar em %s: %w", c.address(), err)
	}
	sess, err := d.DialConn(dctx, raw, c.address())
	if err != nil {
		raw.Close()
		return nil, fmt.Errorf("o servidor SMB recusou a conexão ou o login: %w", err)
	}
	share, err := sess.Mount(c.Share)
	if err != nil {
		_ = sess.Logoff()
		return nil, fmt.Errorf("não foi possível abrir o compartilhamento %q: %w", c.Share, err)
	}
	return &smbConn{sess: sess, share: share.WithContext(ctx), base: c.BasePath}, nil
}

// p monta o caminho no formato do SMB (relativo, com barras invertidas).
func (s *smbConn) p(parts ...string) string {
	p := path.Join(append([]string{s.base}, parts...)...)
	if p == "." {
		p = ""
	}
	return strings.ReplaceAll(p, "/", `\`)
}

func (s *smbConn) MkdirAll(dir string) error {
	p := s.p(dir)
	if p == "" {
		return nil
	}
	return s.share.MkdirAll(p, 0o750)
}

func (s *smbConn) Write(_ context.Context, dir, name string, r io.Reader) error {
	f, err := s.share.OpenFile(s.p(dir, name), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o640)
	if err != nil {
		return err
	}
	_, err = io.CopyBuffer(struct{ io.Writer }{f}, r, make([]byte, 1<<20))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

func (s *smbConn) Open(_ context.Context, dir, name string) (io.ReadCloser, error) {
	return s.share.Open(s.p(dir, name))
}

func (s *smbConn) Size(dir, name string) (int64, error) {
	st, err := s.share.Stat(s.p(dir, name))
	if err != nil {
		return 0, err
	}
	return st.Size(), nil
}

func (s *smbConn) List(dir string) ([]File, error) {
	es, err := s.share.ReadDir(s.p(dir))
	if errors.Is(err, fs.ErrNotExist) || os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []File
	for _, e := range es {
		if e.Mode().IsRegular() {
			out = append(out, File{Name: e.Name(), Size: e.Size(), Modified: e.ModTime()})
		}
	}
	return out, nil
}

func (s *smbConn) Rename(dir, from, to string) error {
	_ = s.share.Remove(s.p(dir, to))
	return s.share.Rename(s.p(dir, from), s.p(dir, to))
}

func (s *smbConn) Remove(dir, name string) error { return s.share.Remove(s.p(dir, name)) }

func (s *smbConn) Close() error {
	_ = s.share.Umount()
	return s.sess.Logoff()
}
