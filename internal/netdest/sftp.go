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

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

type sftpConn struct {
	ssh  *ssh.Client
	cl   *sftp.Client
	base string
	stop func() bool
}

func dialSFTP(ctx context.Context, c Config) (Conn, string, error) {
	var auth []ssh.AuthMethod
	if c.PrivateKey != "" {
		signer, err := ssh.ParsePrivateKey([]byte(c.PrivateKey))
		if err != nil {
			return nil, "", errors.New("chave privada inválida (use PEM/OpenSSH sem senha)")
		}
		auth = append(auth, ssh.PublicKeys(signer))
	}
	if c.Password != "" {
		pw := c.Password
		auth = append(auth, ssh.Password(pw), ssh.KeyboardInteractive(func(_, _ string, q []string, _ []bool) ([]string, error) {
			a := make([]string, len(q))
			for i := range a {
				a[i] = pw
			}
			return a, nil
		}))
	}
	seen := ""
	cfg := &ssh.ClientConfig{User: c.User, Auth: auth, Timeout: dialTimeout,
		HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
			seen = ssh.FingerprintSHA256(key)
			if c.HostKey != "" && seen != c.HostKey {
				return fmt.Errorf("a chave do servidor mudou (esperada %s, recebida %s): pode ser um ataque; se o servidor foi reinstalado, salve o destino de novo", c.HostKey, seen)
			}
			return nil
		}}
	d := net.Dialer{Timeout: dialTimeout}
	raw, err := d.DialContext(ctx, "tcp", c.address())
	if err != nil {
		return nil, "", fmt.Errorf("não foi possível conectar em %s: %w", c.address(), err)
	}
	cc, chans, reqs, err := ssh.NewClientConn(raw, c.address(), cfg)
	if err != nil {
		raw.Close()
		return nil, seen, fmt.Errorf("falha no SSH: %w", err)
	}
	client := ssh.NewClient(cc, chans, reqs)
	cl, err := sftp.NewClient(client, sftp.UseConcurrentWrites(true))
	if err != nil {
		client.Close()
		return nil, seen, fmt.Errorf("o servidor não aceitou SFTP: %w", err)
	}
	s := &sftpConn{ssh: client, cl: cl, base: c.BasePath}
	s.stop = context.AfterFunc(ctx, func() { client.Close() }) // cancelamento interrompe transferências travadas
	return s, seen, nil
}

func (s *sftpConn) p(parts ...string) string {
	p := path.Join(append([]string{s.base}, parts...)...)
	if p == "" {
		return "."
	}
	return p
}

func (s *sftpConn) MkdirAll(dir string) error { return s.cl.MkdirAll(s.p(dir)) }

func (s *sftpConn) Write(_ context.Context, dir, name string, r io.Reader) error {
	f, err := s.cl.OpenFile(s.p(dir, name), os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
	if err != nil {
		return err
	}
	_, err = io.Copy(f, r)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

func (s *sftpConn) Open(_ context.Context, dir, name string) (io.ReadCloser, error) {
	return s.cl.Open(s.p(dir, name))
}

func (s *sftpConn) Size(dir, name string) (int64, error) {
	st, err := s.cl.Stat(s.p(dir, name))
	if err != nil {
		return 0, err
	}
	return st.Size(), nil
}

func (s *sftpConn) List(dir string) ([]File, error) {
	es, err := s.cl.ReadDir(s.p(dir))
	if errors.Is(err, fs.ErrNotExist) {
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

func (s *sftpConn) Dirs(dir string) ([]string, error) {
	es, err := s.cl.ReadDir(s.p(dir))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
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

func (s *sftpConn) Rename(dir, from, to string) error {
	a, b := s.p(dir, from), s.p(dir, to)
	if err := s.cl.PosixRename(a, b); err == nil {
		return nil
	}
	_ = s.cl.Remove(b)
	return s.cl.Rename(a, b)
}

func (s *sftpConn) Remove(dir, name string) error { return s.cl.Remove(s.p(dir, name)) }

func (s *sftpConn) RemoveAll(dir string) error {
	if _, err := s.cl.Lstat(s.p(dir)); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return s.cl.RemoveAll(s.p(dir))
}

// Link usa a extensão hardlink@openssh.com (OpenSSH e a maioria dos servidores Linux).
func (s *sftpConn) Link(from, to string) error { return s.cl.Link(s.p(from), s.p(to)) }

func (s *sftpConn) Chmod(name string, mode fs.FileMode) error { return s.cl.Chmod(s.p(name), mode) }

func (s *sftpConn) Close() error {
	s.stop()
	_ = s.cl.Close()
	return s.ssh.Close()
}
