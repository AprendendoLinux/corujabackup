// Package mikrotik faz o backup de um roteador MikroTik (RouterOS 6 e 7) por SSH: o export da configuração em texto
// (.rsc, com ou sem dados sensíveis) e o backup binário (.backup, cifrado com senha), baixado por SFTP e apagado do
// roteador em seguida. Quem conecta é o agente, que alcança a rede do roteador.
package mikrotik

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// Config descreve o roteador e o que salvar.
type Config struct {
	Host            string `json:"host"`
	Port            int    `json:"port,omitempty"`
	User            string `json:"user"`
	Password        string `json:"password,omitempty"`
	PrivateKey      string `json:"private_key,omitempty"` // PEM/OpenSSH sem frase-senha (alternativa à senha)
	HostKey         string `json:"host_key,omitempty"`    // impressão digital SHA256 fixada no primeiro teste
	Export          bool   `json:"export"`                // .rsc sem dados sensíveis
	ExportSensitive bool   `json:"export_sensitive"`      // .rsc com senhas e chaves
	Binary          bool   `json:"binary"`                // .backup cifrado
	BackupPassword  string `json:"backup_password,omitempty"`
}

var hostRE = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.\-]{0,251}[A-Za-z0-9])?$|^[0-9a-fA-F:]+$`)

// Normalize valida e completa a configuração.
func (c *Config) Normalize() error {
	c.Host, c.User = strings.TrimSpace(c.Host), strings.TrimSpace(c.User)
	if !hostRE.MatchString(c.Host) {
		return errors.New("informe o endereço do roteador (nome ou IP, sem protocolo nem porta)")
	}
	if c.Port == 0 {
		c.Port = 22
	}
	if c.Port < 1 || c.Port > 65535 {
		return errors.New("porta inválida")
	}
	if c.User == "" || strings.ContainsAny(c.User, " \r\n\x00\"") {
		return errors.New("informe o usuário do roteador")
	}
	if c.Password == "" && c.PrivateKey == "" {
		return errors.New("informe a senha ou a chave privada do usuário do roteador")
	}
	if !c.Export && !c.ExportSensitive && !c.Binary {
		return errors.New("escolha pelo menos um item para salvar (export .rsc ou arquivo .backup)")
	}
	if c.Binary {
		if len(c.BackupPassword) < 8 {
			return errors.New("o arquivo .backup precisa de uma senha com pelo menos 8 caracteres (sem ela, o backup não restaura)")
		}
		if len(c.BackupPassword) > 64 || strings.ContainsAny(c.BackupPassword, "\r\n\x00") {
			return errors.New("senha do .backup inválida (até 64 caracteres, sem quebra de linha)")
		}
	} else {
		c.BackupPassword = ""
	}
	return nil
}

// Info identifica o roteador.
type Info struct {
	Identity string `json:"identity"`
	Version  string `json:"version"`
	Board    string `json:"board"`
	Arch     string `json:"arch,omitempty"`
	Serial   string `json:"serial,omitempty"`
	HostKey  string `json:"host_key,omitempty"` // impressão digital vista nesta conexão
}

// Major é a versão principal do RouterOS (6, 7...); 0 se ilegível.
func (i Info) Major() int {
	n, _ := strconv.Atoi(strings.SplitN(i.Version, ".", 2)[0])
	return n
}

const dialTimeout = 20 * time.Second

// Router é uma conexão SSH aberta com o roteador.
type Router struct {
	cl   *ssh.Client
	stop func() bool
	Seen string // impressão digital vista
}

// Dial conecta. Sem impressão digital fixada (primeiro contato), aceita e devolve a vista em Seen para o usuário confirmar;
// com ela fixada, recusa qualquer outra.
func Dial(ctx context.Context, c Config) (*Router, error) {
	var auth []ssh.AuthMethod
	if c.PrivateKey != "" {
		signer, err := ssh.ParsePrivateKey([]byte(c.PrivateKey))
		if err != nil {
			return nil, errors.New("chave privada inválida (use PEM/OpenSSH sem frase-senha)")
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
	r := &Router{}
	cfg := &ssh.ClientConfig{User: c.User, Auth: auth, Timeout: dialTimeout,
		HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
			r.Seen = ssh.FingerprintSHA256(key)
			if c.HostKey != "" && r.Seen != c.HostKey {
				return fmt.Errorf("a chave do roteador mudou (esperada %s, recebida %s): pode ser um ataque; se o roteador foi reinstalado, teste e salve o job de novo", c.HostKey, r.Seen)
			}
			return nil
		}}
	port := c.Port
	if port == 0 {
		port = 22
	}
	addr := net.JoinHostPort(c.Host, strconv.Itoa(port))
	d := net.Dialer{Timeout: dialTimeout}
	raw, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("não foi possível conectar em %s: %w", addr, friendly(err))
	}
	cc, chans, reqs, err := ssh.NewClientConn(raw, addr, cfg)
	if err != nil {
		raw.Close()
		if i := strings.Index(err.Error(), "a chave do roteador mudou"); i >= 0 {
			return nil, errors.New(err.Error()[i:])
		}
		if strings.Contains(err.Error(), "unable to authenticate") {
			return nil, errors.New("o roteador recusou o usuário ou a senha (confira também se o grupo do usuário tem a política ssh)")
		}
		return nil, fmt.Errorf("falha no SSH: %w", err)
	}
	r.cl = ssh.NewClient(cc, chans, reqs)
	r.stop = context.AfterFunc(ctx, func() { r.cl.Close() })
	return r, nil
}

func friendly(err error) error {
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return errors.New("tempo esgotado (roteador desligado, endereço errado ou firewall bloqueando)")
	}
	switch s := err.Error(); {
	case strings.Contains(s, "connection refused"):
		return errors.New("conexão recusada (o serviço SSH do roteador está desligado ou em outra porta: /ip service)")
	case strings.Contains(s, "no such host"):
		return errors.New("o nome do roteador não foi encontrado (DNS)")
	}
	return err
}

// Close encerra a conexão.
func (r *Router) Close() error {
	r.stop()
	return r.cl.Close()
}

// run executa um comando do RouterOS e devolve a saída. Erros do RouterOS (que saem com código 0) são detectados pelo texto.
func (r *Router) run(cmd string) (string, error) {
	s, err := r.cl.NewSession()
	if err != nil {
		return "", err
	}
	defer s.Close()
	var out, errb bytes.Buffer
	s.Stdout, s.Stderr = &out, &errb
	err = s.Run(cmd)
	text := strings.ReplaceAll(out.String(), "\r\n", "\n")
	if err != nil {
		var ee *ssh.ExitError
		if !errors.As(err, &ee) {
			return text, err
		}
	}
	for _, bad := range []string{"bad command name", "syntax error", "expected end of command", "failure:", "not enough permissions", "input does not match any value"} {
		if strings.Contains(text, bad) || strings.Contains(errb.String(), bad) {
			line := strings.TrimSpace(strings.SplitN(strings.TrimSpace(text+"\n"+errb.String()), "\n", 2)[0])
			if strings.Contains(text+errb.String(), "not enough permissions") {
				return text, fmt.Errorf("o usuário do roteador não tem permissão para isso (%s): veja as políticas necessárias na documentação", line)
			}
			return text, fmt.Errorf("o roteador recusou o comando: %s", line)
		}
	}
	return text, nil
}

// get lê um valor com :put.
func (r *Router) get(expr string) string {
	out, err := r.run(":put " + expr)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// Info lê a identificação do roteador.
func (r *Router) Info() (Info, error) {
	i := Info{HostKey: r.Seen}
	i.Identity = r.get("[/system identity get name]")
	i.Version = r.get("[/system resource get version]")
	i.Board = r.get("[/system resource get board-name]")
	i.Arch = r.get("[/system resource get architecture-name]")
	i.Serial = r.get("[/system routerboard get serial-number]") // CHR e x86 não têm
	if i.Version == "" {
		return i, errors.New("não foi possível ler a versão do RouterOS (o usuário precisa da política read)")
	}
	if i.Major() < 6 {
		return i, fmt.Errorf("RouterOS %s não é suportado (use a 6 ou a 7)", i.Version)
	}
	return i, nil
}

// Export devolve o /export. Na v7, os dados sensíveis ficam de fora por padrão (show-sensitive inclui); na v6, entram por
// padrão (hide-sensitive tira).
func (r *Router) Export(info Info, sensitive bool) ([]byte, error) {
	cmd := "/export"
	switch {
	case info.Major() >= 7 && sensitive:
		cmd += " show-sensitive"
	case info.Major() < 7 && !sensitive:
		cmd += " hide-sensitive"
	}
	out, err := r.run(cmd)
	if err != nil {
		return nil, err
	}
	if !strings.Contains(out, "by RouterOS") || !strings.Contains(out, "\n/") {
		return nil, errors.New("o export veio vazio ou incompleto (o usuário precisa das políticas read e, para dados sensíveis, sensitive)")
	}
	// grava com \r\n, como o /export file= do próprio RouterOS
	return []byte(strings.ReplaceAll(out, "\n", "\r\n")), nil
}

// quote escreve um texto como string do RouterOS.
func quote(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, `$`, `\$`, "?", `\?`)
	return `"` + r.Replace(s) + `"`
}

var fileNameRE = regexp.MustCompile(`name="?([^"\s]+)"?`)

// Backup cria o .backup cifrado no roteador, baixa por SFTP para w e o apaga do roteador (mesmo se algo falhar).
func (r *Router) Backup(ctx context.Context, password string, w io.Writer) (int64, error) {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	name := "coruja-" + hex.EncodeToString(b)
	out, err := r.run("/system backup save name=" + name + " password=" + quote(password))
	if err == nil && strings.Contains(strings.ToLower(out), "failed") {
		err = fmt.Errorf("%s (o usuário precisa das políticas write, policy, sensitive e test)", strings.TrimSpace(out))
	}
	if err != nil {
		return 0, fmt.Errorf("o roteador não gerou o .backup: %w", err)
	}
	// o arquivo pode ficar na raiz ou em flash/, conforme o modelo
	defer func() { _, _ = r.run(`/file remove [find where name~"` + name + `\\.backup\$"]`) }()
	var path string
	for i := 0; i < 20 && path == ""; i++ {
		out, _ := r.run(`/file print terse where name~"` + name + `\\.backup\$"`)
		if m := fileNameRE.FindStringSubmatch(out); m != nil {
			path = m[1]
			break
		}
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	if path == "" {
		return 0, errors.New("o .backup foi gerado, mas não apareceu na lista de arquivos do roteador")
	}
	sc, err := sftp.NewClient(r.cl)
	if err != nil {
		return 0, fmt.Errorf("não foi possível baixar o .backup por SFTP (o usuário precisa da política ftp): %w", err)
	}
	defer sc.Close()
	f, err := sc.Open("/" + strings.TrimPrefix(path, "/"))
	if err != nil {
		return 0, fmt.Errorf("não foi possível abrir %s no roteador: %w", path, err)
	}
	defer f.Close()
	n, err := io.Copy(w, f)
	if err != nil {
		return n, fmt.Errorf("falha ao baixar o .backup: %w", err)
	}
	if n == 0 {
		return 0, errors.New("o .backup baixado está vazio")
	}
	return n, nil
}

// Test conecta, lê a identificação e confere as permissões do que foi pedido (sem gravar nada no roteador).
func Test(ctx context.Context, c Config) (Info, error) {
	if err := c.Normalize(); err != nil {
		return Info{}, err
	}
	r, err := Dial(ctx, c)
	if err != nil {
		return Info{HostKey: seenOf(r)}, err
	}
	defer r.Close()
	info, err := r.Info()
	if err != nil {
		return info, err
	}
	if c.ExportSensitive || c.Export {
		if _, err := r.Export(info, c.ExportSensitive); err != nil {
			return info, err
		}
	}
	return info, nil
}

func seenOf(r *Router) string {
	if r == nil {
		return ""
	}
	return r.Seen
}
