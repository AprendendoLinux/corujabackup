// Package netdest implementa os destinos "diretos do agente": SFTP, FTP/FTPS, SMB/CIFS e pasta local (volume montado no
// host, o que cobre NFS e iSCSI). Quem envia, confere e aplica a retenção é o AGENTE, que é quem alcança a rede onde o
// destino está.
package netdest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/AprendendoLinux/corujabackup/internal/cloud"
)

const (
	TypeSFTP  = "sftp"
	TypeFTP   = "ftp"
	TypeSMB   = "smb"
	TypeLocal = "local"

	// Modos de TLS do FTP.
	TLSExplicit = "explicit" // FTPS (AUTH TLS), recomendado
	TLSImplicit = "implicit"
	TLSNone     = "none" // senha e dados em texto puro

	VerifyFull = "full" // relê o arquivo do destino e compara o SHA-256
	VerifySize = "size" // só compara o tamanho (não relê)

	dialTimeout = 20 * time.Second
	partSuffix  = ".coruja-part"
)

// Is informa se o tipo é um destino direto do agente.
func Is(typ string) bool {
	return typ == TypeSFTP || typ == TypeFTP || typ == TypeSMB || typ == TypeLocal
}

// Config descreve um destino. Viaja para o agente pelo canal TLS e fica cifrada no Server.
type Config struct {
	Type       string `json:"type"`
	Host       string `json:"host,omitempty"`
	Port       int    `json:"port,omitempty"`
	User       string `json:"user,omitempty"`
	Password   string `json:"password,omitempty"`
	PrivateKey string `json:"private_key,omitempty"` // SFTP (PEM), alternativa à senha
	HostKey    string `json:"host_key,omitempty"`    // SFTP: impressão digital SHA256 fixada
	TLS        string `json:"tls,omitempty"`         // FTP
	SkipVerify bool   `json:"skip_verify,omitempty"` // FTPS com certificado próprio
	Share      string `json:"share,omitempty"`       // SMB
	Domain     string `json:"domain,omitempty"`      // SMB
	BasePath   string `json:"base_path,omitempty"`   // pasta base dentro do destino
	Verify     string `json:"verify,omitempty"`
}

var (
	hostRE  = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.\-]{0,251}[A-Za-z0-9])?$|^[0-9a-fA-F:]+$`)
	shareRE = regexp.MustCompile(`^[^\\/:*?"<>|\x00]{1,80}$`)
)

// DefaultPort devolve a porta padrão do protocolo.
func DefaultPort(c Config) int {
	switch c.Type {
	case TypeSFTP:
		return 22
	case TypeFTP:
		if c.TLS == TLSImplicit {
			return 990
		}
		return 21
	case TypeSMB:
		return 445
	}
	return 0
}

// CleanPath valida um caminho relativo (sem "..", sem barras invertidas). Vazio é permitido.
func CleanPath(p string) (string, error) {
	p = strings.Trim(strings.TrimSpace(p), "/")
	if p == "" {
		return "", nil
	}
	for _, s := range strings.Split(p, "/") {
		if s == "" || s == "." || s == ".." || len(s) > 200 || strings.ContainsAny(s, "\\\x00\r\n:*?\"<>|") {
			return "", fmt.Errorf("caminho inválido: %q", p)
		}
	}
	return p, nil
}

// Normalize valida e completa a configuração.
func (c *Config) Normalize() error {
	c.Host, c.User, c.Share, c.Domain = strings.TrimSpace(c.Host), strings.TrimSpace(c.User), strings.Trim(strings.TrimSpace(c.Share), `/\`), strings.TrimSpace(c.Domain)
	abs := c.Type == TypeSFTP && strings.HasPrefix(strings.TrimSpace(c.BasePath), "/")
	bp, err := CleanPath(c.BasePath)
	if err != nil {
		return err
	}
	if abs {
		bp = "/" + bp // no SFTP, caminho começado por "/" é absoluto no servidor
	}
	c.BasePath = bp
	if c.Verify == "" {
		c.Verify = VerifyFull
	}
	if c.Verify != VerifyFull && c.Verify != VerifySize {
		return errors.New("modo de conferência inválido")
	}
	if strings.ContainsAny(c.User+c.Password+c.Domain, "\r\n\x00") {
		return errors.New("usuário ou senha com caracteres inválidos")
	}
	if c.Type == TypeLocal {
		return nil
	}
	if !Is(c.Type) {
		return fmt.Errorf("tipo de destino desconhecido: %q", c.Type)
	}
	if !hostRE.MatchString(c.Host) {
		return errors.New("informe o endereço do servidor (nome ou IP, sem protocolo nem porta)")
	}
	if c.Type == TypeFTP {
		if c.TLS == "" {
			c.TLS = TLSExplicit
		}
		if c.TLS != TLSExplicit && c.TLS != TLSImplicit && c.TLS != TLSNone {
			return errors.New("modo de TLS inválido")
		}
	}
	if c.Port == 0 {
		c.Port = DefaultPort(*c)
	}
	if c.Port < 1 || c.Port > 65535 {
		return errors.New("porta inválida")
	}
	if c.User == "" {
		return errors.New("informe o usuário")
	}
	if c.Type == TypeSFTP && c.Password == "" && c.PrivateKey == "" {
		return errors.New("informe a senha ou a chave privada")
	}
	if c.Type == TypeSMB && !shareRE.MatchString(c.Share) {
		return errors.New("informe o nome do compartilhamento SMB")
	}
	return nil
}

// Label é um resumo legível (sem segredos).
func (c Config) Label() string {
	switch c.Type {
	case TypeLocal:
		return "pasta /" + strings.TrimPrefix(c.BasePath, "/")
	case TypeSMB:
		return fmt.Sprintf(`//%s/%s (%s)`, c.Host, c.Share, c.User)
	}
	return fmt.Sprintf("%s@%s:%d", c.User, c.Host, c.Port)
}

func (c Config) address() string { return net.JoinHostPort(c.Host, strconv.Itoa(c.Port)) }

// File é um arquivo no destino.
type File struct {
	Name     string
	Size     int64
	Modified time.Time
}

// Conn é uma conexão aberta com um destino. Os caminhos (dir) são relativos à pasta base.
type Conn interface {
	MkdirAll(dir string) error
	Write(ctx context.Context, dir, name string, r io.Reader) error
	Open(ctx context.Context, dir, name string) (io.ReadCloser, error)
	Size(dir, name string) (int64, error)
	List(dir string) ([]File, error) // só arquivos; pasta inexistente = lista vazia
	Rename(dir, from, to string) error
	Remove(dir, name string) error
	Close() error
}

// Env é o contexto local do agente.
type Env struct {
	LocalRoot string // raiz permitida para destinos "local" (ex.: /destinos)
}

// Dial abre a conexão. Para SFTP sem impressão digital fixada, devolve a encontrada em seen (primeiro contato).
func Dial(ctx context.Context, c Config, env Env) (conn Conn, seen string, err error) {
	if err = c.Normalize(); err != nil {
		return nil, "", err
	}
	switch c.Type {
	case TypeSFTP:
		return dialSFTP(ctx, c)
	case TypeFTP:
		conn, err = dialFTP(ctx, c)
	case TypeSMB:
		conn, err = dialSMB(ctx, c)
	case TypeLocal:
		conn, err = openLocal(c, env)
	}
	return conn, "", err
}

type ctxReader struct {
	ctx context.Context
	r   io.Reader
	n   int64
	on  func(int64)
}

func (c *ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := c.r.Read(p)
	c.n += int64(n)
	if c.on != nil && n > 0 {
		c.on(c.n)
	}
	return n, err
}

// Job é o que o agente precisa para enviar um backup.
type Job struct {
	Folder    string // pasta do job dentro do destino
	LocalPath string // arquivo gerado pelo agente
	Name      string // nome final
	SHA256    string // do arquivo local
	Prefix    string // prefixo dos arquivos do job (para a retenção)
	Keep      int
	KeepDays  int
	Now       time.Time
}

// Result é o resultado de um envio conferido.
type Result struct {
	Path       string
	Hash       string
	VerifiedBy string // sha256 | size
	Retention  []string
}

// Send envia o arquivo com nome temporário, confere, renomeia e SÓ ENTÃO aplica a retenção.
func Send(ctx context.Context, c Config, env Env, j Job, progress func(sent, total int64)) (Result, error) {
	var res Result
	if err := c.Normalize(); err != nil {
		return res, err
	}
	folder, err := CleanPath(j.Folder)
	if err != nil {
		return res, err
	}
	if c.Type == TypeSFTP && c.HostKey == "" {
		return res, errors.New("destino SFTP sem impressão digital do servidor: teste e salve o destino de novo")
	}
	f, err := os.Open(j.LocalPath)
	if err != nil {
		return res, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return res, err
	}
	total := st.Size()
	conn, _, err := Dial(ctx, c, env)
	if err != nil {
		return res, err
	}
	defer conn.Close()
	if err := conn.MkdirAll(folder); err != nil {
		return res, fmt.Errorf("não foi possível criar a pasta no destino: %w", err)
	}
	part := j.Name + partSuffix
	cleanup := func() { _ = conn.Remove(folder, part) }
	rd := &ctxReader{ctx: ctx, r: f, on: func(n int64) {
		if progress != nil {
			progress(n, total)
		}
	}}
	if err := conn.Write(ctx, folder, part, rd); err != nil {
		cleanup()
		return res, fmt.Errorf("falha ao gravar no destino: %w", err)
	}
	size, err := conn.Size(folder, part)
	if err != nil {
		cleanup()
		return res, fmt.Errorf("não foi possível conferir o arquivo no destino: %w", err)
	}
	if size != total {
		cleanup()
		return res, fmt.Errorf("o destino guardou %d bytes, esperado %d (arquivo descartado)", size, total)
	}
	res.VerifiedBy = VerifySize
	if c.Verify == VerifyFull {
		sum, err := hashRemote(ctx, conn, folder, part)
		if err != nil {
			cleanup()
			return res, fmt.Errorf("não foi possível reler o arquivo para conferir: %w", err)
		}
		if sum != j.SHA256 {
			cleanup()
			return res, errors.New("o SHA-256 relido do destino difere do arquivo enviado (arquivo descartado; os backups antigos foram preservados)")
		}
		res.Hash, res.VerifiedBy = sum, "sha256"
	}
	if err := conn.Rename(folder, part, j.Name); err != nil {
		cleanup()
		return res, fmt.Errorf("não foi possível dar o nome final ao arquivo: %w", err)
	}
	res.Path = path.Join(folder, j.Name)
	files, err := conn.List(folder)
	if err != nil {
		res.Retention = append(res.Retention, "retenção ignorada: não foi possível listar a pasta: "+err.Error())
		return res, nil
	}
	remote := make([]cloud.RemoteFile, 0, len(files))
	for _, x := range files {
		remote = append(remote, cloud.RemoteFile{Name: x.Name, Size: x.Size, Modified: x.Modified})
	}
	for _, old := range cloud.Retention(remote, j.Prefix, j.Keep, j.KeepDays, j.Now) {
		if old.Name == j.Name {
			continue
		}
		if err := conn.Remove(folder, old.Name); err != nil {
			res.Retention = append(res.Retention, "não foi possível apagar "+old.Name+": "+err.Error())
			continue
		}
		res.Retention = append(res.Retention, "removido do destino: "+old.Name)
	}
	return res, nil
}

func hashRemote(ctx context.Context, conn Conn, dir, name string) (string, error) {
	rc, err := conn.Open(ctx, dir, name)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	_, err = io.Copy(h, &ctxReader{ctx: ctx, r: rc})
	if cerr := rc.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Fetch traz um arquivo do destino para localPath (restauração), conferindo o SHA-256 quando informado.
func Fetch(ctx context.Context, c Config, env Env, folder, name, localPath, wantSHA string) (int64, error) {
	folder, err := CleanPath(folder)
	if err != nil {
		return 0, err
	}
	conn, _, err := Dial(ctx, c, env)
	if err != nil {
		return 0, err
	}
	defer conn.Close()
	rc, err := conn.Open(ctx, folder, name)
	if err != nil {
		return 0, fmt.Errorf("não foi possível abrir o arquivo no destino: %w", err)
	}
	tmp := localPath + ".baixando"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		rc.Close()
		return 0, err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(out, h), &ctxReader{ctx: ctx, r: rc})
	if cerr := rc.Close(); err == nil {
		err = cerr
	}
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err == nil && wantSHA != "" && hex.EncodeToString(h.Sum(nil)) != wantSHA {
		err = errors.New("o arquivo trazido do destino não confere com o SHA-256 do backup")
	}
	if err != nil {
		_ = os.Remove(tmp)
		return 0, err
	}
	return n, os.Rename(tmp, localPath)
}

// TestResult é o resultado do teste de um destino.
type TestResult struct {
	HostKey string `json:"host_key,omitempty"` // SFTP: impressão digital vista
	Detail  string `json:"detail"`
}

// Test conecta, grava, relê e apaga um arquivo pequeno na pasta base: prova que o destino aceita tudo o que um backup precisa.
func Test(ctx context.Context, c Config, env Env) (TestResult, error) {
	var out TestResult
	conn, seen, err := Dial(ctx, c, env)
	out.HostKey = seen
	if err != nil {
		return out, err
	}
	defer conn.Close()
	if err := conn.MkdirAll(""); err != nil {
		return out, fmt.Errorf("não foi possível criar a pasta base: %w", err)
	}
	name := fmt.Sprintf(".coruja-teste-%d", time.Now().UnixNano())
	data := []byte("teste de escrita do Coruja Backup\n")
	defer func() { _ = conn.Remove("", name); _ = conn.Remove("", name+".2") }()
	if err := conn.Write(ctx, "", name, strings.NewReader(string(data))); err != nil {
		return out, fmt.Errorf("sem permissão para gravar: %w", err)
	}
	if err := conn.Rename("", name, name+".2"); err != nil {
		return out, fmt.Errorf("não foi possível renomear: %w", err)
	}
	sum, err := hashRemote(ctx, conn, "", name+".2")
	if err != nil {
		return out, fmt.Errorf("não foi possível reler: %w", err)
	}
	if want := sha256.Sum256(data); sum != hex.EncodeToString(want[:]) {
		return out, errors.New("o conteúdo relido não confere")
	}
	files, err := conn.List("")
	if err != nil {
		return out, fmt.Errorf("não foi possível listar: %w", err)
	}
	if err := conn.Remove("", name+".2"); err != nil {
		return out, fmt.Errorf("não foi possível apagar (a retenção precisa disso): %w", err)
	}
	out.Detail = fmt.Sprintf("gravação, leitura, renomeação, listagem (%d arquivo(s)) e exclusão funcionando", len(files))
	return out, nil
}
