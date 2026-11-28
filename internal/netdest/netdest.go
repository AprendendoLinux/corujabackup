// Package netdest implementa os destinos "diretos do agente": SFTP, FTP/FTPS, SMB/CIFS, NFS (cliente embutido) e pasta
// local (qualquer pasta da máquina do agente, o que cobre iSCSI e discos externos já montados). Quem envia, confere e aplica a retenção é o AGENTE, que é quem alcança a rede onde o
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
	"slices"
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
	TypeNFS   = "nfs"

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
	return typ == TypeSFTP || typ == TypeFTP || typ == TypeSMB || typ == TypeLocal || typ == TypeNFS
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
	Export     string `json:"export,omitempty"`      // NFS: caminho exportado no servidor
	UID        *int   `json:"uid,omitempty"`         // NFS: usuário (AUTH_SYS) que grava; vazio = 0
	GID        *int   `json:"gid,omitempty"`         // NFS: grupo
	Path       string `json:"path,omitempty"`        // pasta local: caminho absoluto na máquina do agente
	Verify     string `json:"verify,omitempty"`
}

var (
	hostRE  = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.\-]{0,251}[A-Za-z0-9])?$|^[0-9a-fA-F:]+$`)
	shareRE = regexp.MustCompile(`^[^\\/:*?"<>|\x00]{1,80}$`)
)

// forbiddenLocal são pastas virtuais ou voláteis do sistema, onde um backup não pode ser gravado.
var forbiddenLocal = []string{"/proc", "/sys", "/dev", "/run"}

// CleanAbs valida um caminho absoluto da máquina do agente para receber backups e o devolve normalizado.
func CleanAbs(p string) (string, error) {
	p = strings.TrimSpace(p)
	if !strings.HasPrefix(p, "/") || len(p) > 1024 || strings.ContainsAny(p, "\x00\r\n") {
		return "", fmt.Errorf("pasta inválida: %q (use um caminho absoluto, ex.: /mnt/backups)", p)
	}
	for _, s := range strings.Split(p, "/") {
		if s == ".." {
			return "", fmt.Errorf("pasta inválida: %q", p)
		}
	}
	clean := path.Clean(p)
	if clean == "/" {
		return "", errors.New("escolha uma pasta: os backups não podem ficar direto na raiz")
	}
	for _, f := range forbiddenLocal {
		if clean == f || strings.HasPrefix(clean, f+"/") {
			return "", fmt.Errorf("%s é uma pasta do sistema e não pode receber backups", clean)
		}
	}
	return clean, nil
}

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
		if c.Path != "" {
			if c.Path, err = CleanAbs(c.Path); err != nil {
				return err
			}
		}
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
	if c.Type == TypeNFS {
		return c.normalizeNFS()
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

func (c *Config) normalizeNFS() error {
	c.User, c.Password, c.PrivateKey, c.Share, c.Domain = "", "", "", "", ""
	ex := strings.TrimSpace(c.Export)
	if !strings.HasPrefix(ex, "/") || len(ex) > 1024 || strings.ContainsAny(ex, "\x00\r\n\\") {
		return errors.New("informe a exportação NFS como caminho absoluto (ex.: /srv/backups)")
	}
	for _, s := range strings.Split(ex, "/") {
		if s == ".." {
			return errors.New("exportação NFS inválida")
		}
	}
	c.Export = path.Clean(ex)
	if c.Port < 0 || c.Port > 65535 {
		return errors.New("porta inválida")
	}
	for _, id := range []*int{c.UID, c.GID} {
		if id != nil && (*id < 0 || *id > 1<<31-1) {
			return errors.New("UID/GID inválido")
		}
	}
	return nil
}

// Label é um resumo legível (sem segredos).
func (c Config) Label() string {
	switch c.Type {
	case TypeLocal:
		if c.Path != "" {
			return "pasta " + c.Path
		}
		return "pasta /" + strings.TrimPrefix(c.BasePath, "/")
	case TypeNFS:
		if c.Port != 0 {
			return fmt.Sprintf("%s:%s (porta %d)", c.Host, c.Export, c.Port)
		}
		return c.Host + ":" + c.Export
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
	List(dir string) ([]File, error)   // só arquivos; pasta inexistente = lista vazia
	Dirs(dir string) ([]string, error) // só subpastas; pasta inexistente = lista vazia
	Rename(dir, from, to string) error
	Remove(dir, name string) error
	RemoveAll(dir string) error // apaga uma pasta com tudo dentro (inexistente = nada a fazer)
	Close() error
}

// Env é o contexto local do agente.
type Env struct {
	LocalRoot string // raiz dos destinos "local" antigos, sem caminho absoluto (ex.: /destinos)
	// HostDir traduz uma pasta absoluta da máquina do agente para o caminho em que ele grava (aplicando as regras de
	// segurança do agente). nil = o agente não grava fora das próprias pastas.
	HostDir func(abs string) (string, error)
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
	case TypeNFS:
		conn, err = dialNFS(ctx, c)
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

// Target é uma pasta aberta num storage, pronta para receber backups.
type Target struct {
	ctx    context.Context
	c      Config
	env    Env
	conn   Conn
	folder string
}

// OpenTarget conecta ao storage e cria a pasta do job.
func OpenTarget(ctx context.Context, c Config, env Env, folder string) (*Target, error) {
	if err := c.Normalize(); err != nil {
		return nil, err
	}
	folder, err := CleanPath(folder)
	if err != nil {
		return nil, err
	}
	if c.Type == TypeSFTP && c.HostKey == "" {
		return nil, errors.New("destino SFTP sem impressão digital do servidor: teste e salve o destino de novo")
	}
	conn, _, err := Dial(ctx, c, env)
	if err != nil {
		return nil, err
	}
	if err := conn.MkdirAll(folder); err != nil {
		conn.Close()
		return nil, fmt.Errorf("não foi possível criar a pasta no destino: %w", err)
	}
	return &Target{ctx: ctx, c: c, env: env, conn: conn, folder: folder}, nil
}

// Exists informa se já há um arquivo com esse nome na pasta (para não sobrescrever um backup).
func (t *Target) Exists(name string) bool {
	_, err := t.conn.Size(t.folder, name)
	return err == nil
}

// Close encerra a conexão.
func (t *Target) Close() error { return t.conn.Close() }

// Upload é um arquivo sendo gravado no storage com nome temporário (.coruja-part). Finish confere, dá o nome final e
// só então aplica a retenção; Abort apaga o temporário.
type Upload struct {
	t    *Target
	name string
	part string
	pw   *io.PipeWriter
	done chan error
	n    int64
	end  bool
}

// Create começa a gravar name na pasta. Os dados escritos em Upload vão direto para o storage.
func (t *Target) Create(name string) *Upload {
	pr, pw := io.Pipe()
	u := &Upload{t: t, name: name, part: name + partSuffix, pw: pw, done: make(chan error, 1)}
	go func() {
		err := t.conn.Write(t.ctx, t.folder, u.part, pr)
		if err == nil {
			err = io.ErrClosedPipe // o storage parou de ler antes do fim
		}
		pr.CloseWithError(err)
		u.done <- err
	}()
	return u
}

func (u *Upload) Write(p []byte) (int, error) {
	n, err := u.pw.Write(p)
	u.n += int64(n)
	return n, err
}

// Written é quanto já foi gravado.
func (u *Upload) Written() int64 { return u.n }

// Name é o nome final do arquivo.
func (u *Upload) Name() string { return u.name }

func (u *Upload) cleanup() {
	t := u.t
	if t.conn.Remove(t.folder, u.part) == nil || t.ctx.Err() == nil {
		return
	}
	// cancelado: a conexão já foi fechada junto com o contexto; apaga o temporário por uma conexão nova
	cctx, cancel := context.WithTimeout(context.WithoutCancel(t.ctx), 30*time.Second)
	defer cancel()
	if c2, _, err := Dial(cctx, t.c, t.env); err == nil {
		_ = c2.Remove(t.folder, u.part)
		c2.Close()
	}
}

// Abort desiste do envio e apaga o temporário.
func (u *Upload) Abort() {
	if u.end {
		return
	}
	u.end = true
	u.pw.CloseWithError(errors.New("envio interrompido"))
	<-u.done
	u.cleanup()
}

// Finish fecha o arquivo, confere o tamanho e (na conferência completa) relê tudo do storage comparando o SHA-256;
// check, se informado, também recebe o conteúdo relido (ex.: para validar o tar.gz). Só depois renomeia e aplica a
// retenção. Em qualquer falha o temporário é apagado e os backups antigos ficam intactos.
func (u *Upload) Finish(j Job, check func(io.Reader) error) (Result, error) {
	var res Result
	t := u.t
	if u.end {
		return res, errors.New("envio já encerrado")
	}
	u.end = true
	_ = u.pw.Close()
	if err := <-u.done; err != nil && !errors.Is(err, io.ErrClosedPipe) {
		u.cleanup()
		return res, fmt.Errorf("falha ao gravar no destino: %w", err)
	}
	size, err := t.conn.Size(t.folder, u.part)
	if err != nil {
		u.cleanup()
		return res, fmt.Errorf("não foi possível conferir o arquivo no destino: %w", err)
	}
	if size != u.n {
		u.cleanup()
		return res, fmt.Errorf("o destino guardou %d bytes, esperado %d (arquivo descartado)", size, u.n)
	}
	res.VerifiedBy = VerifySize
	if t.c.Verify == VerifyFull {
		sum, err := rereadRemote(t.ctx, t.conn, t.folder, u.part, check)
		if err != nil {
			u.cleanup()
			return res, fmt.Errorf("não foi possível reler o arquivo para conferir (arquivo descartado): %w", err)
		}
		if sum != j.SHA256 {
			u.cleanup()
			return res, errors.New("o SHA-256 relido do destino difere do arquivo enviado (arquivo descartado; os backups antigos foram preservados)")
		}
		res.Hash, res.VerifiedBy = sum, "sha256"
	}
	if err := t.conn.Rename(t.folder, u.part, u.name); err != nil {
		u.cleanup()
		return res, fmt.Errorf("não foi possível dar o nome final ao arquivo: %w", err)
	}
	res.Path = path.Join(t.folder, u.name)
	files, err := t.conn.List(t.folder)
	if err != nil {
		res.Retention = append(res.Retention, "retenção ignorada: não foi possível listar a pasta: "+err.Error())
		return res, nil
	}
	remote := make([]cloud.RemoteFile, 0, len(files))
	for _, x := range files {
		remote = append(remote, cloud.RemoteFile{Name: x.Name, Size: x.Size, Modified: x.Modified})
	}
	for _, old := range cloud.Retention(remote, j.Prefix, j.Keep, j.KeepDays, j.Now) {
		if old.Name == u.name {
			continue
		}
		if err := t.conn.Remove(t.folder, old.Name); err != nil {
			res.Retention = append(res.Retention, "não foi possível apagar "+old.Name+": "+err.Error())
			continue
		}
		res.Retention = append(res.Retention, "removido do destino: "+old.Name)
	}
	return res, nil
}

// rereadRemote relê o arquivo do storage, calcula o SHA-256 e, se check vier, entrega o conteúdo a ele também.
func rereadRemote(ctx context.Context, conn Conn, dir, name string, check func(io.Reader) error) (string, error) {
	rc, err := conn.Open(ctx, dir, name)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	r := io.TeeReader(&ctxReader{ctx: ctx, r: rc}, h)
	if check != nil {
		err = check(r)
	}
	if err == nil {
		_, err = io.Copy(io.Discard, r) // o que check não consumiu (ex.: o rodapé do gzip)
	}
	if cerr := rc.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Send envia um arquivo local: grava com nome temporário, confere, renomeia e SÓ ENTÃO aplica a retenção.
func Send(ctx context.Context, c Config, env Env, j Job, progress func(sent, total int64)) (Result, error) {
	f, err := os.Open(j.LocalPath)
	if err != nil {
		return Result{}, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return Result{}, err
	}
	total := st.Size()
	t, err := OpenTarget(ctx, c, env, j.Folder)
	if err != nil {
		return Result{}, err
	}
	defer t.Close()
	u := t.Create(j.Name)
	rd := &ctxReader{ctx: ctx, r: f, on: func(n int64) {
		if progress != nil {
			progress(n, total)
		}
	}}
	if _, err := io.Copy(u, rd); err != nil {
		u.Abort()
		return Result{}, fmt.Errorf("falha ao gravar no destino: %w", err)
	}
	return u.Finish(j, nil)
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

// OpenStream abre um arquivo do destino para leitura em fluxo, a partir de off (restauração seletiva: sem gravar o
// arquivo inteiro no agente). Fechar o leitor encerra a conexão.
func OpenStream(ctx context.Context, c Config, env Env, folder, name string, off int64) (io.ReadCloser, error) {
	folder, err := CleanPath(folder)
	if err != nil {
		return nil, err
	}
	conn, _, err := Dial(ctx, c, env)
	if err != nil {
		return nil, err
	}
	rc, err := conn.Open(ctx, folder, name)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("não foi possível abrir o arquivo no destino: %w", err)
	}
	if off > 0 {
		skipped := false
		if sk, ok := rc.(io.Seeker); ok {
			_, err = sk.Seek(off, io.SeekStart)
			skipped = err == nil
		}
		if !skipped { // sem posicionamento: lê e descarta até o início do trecho
			_, err = io.CopyN(io.Discard, &ctxReader{ctx: ctx, r: rc}, off)
		}
		if err != nil {
			rc.Close()
			conn.Close()
			return nil, fmt.Errorf("não foi possível chegar ao trecho do arquivo no destino: %w", err)
		}
	}
	return &streamConn{ReadCloser: rc, conn: conn}, nil
}

type streamConn struct {
	io.ReadCloser
	conn Conn
}

func (s *streamConn) Close() error {
	err := s.ReadCloser.Close()
	if cerr := s.conn.Close(); err == nil {
		err = cerr
	}
	return err
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

// Folders lista as subpastas de dir (relativo à pasta base), para a escolha da pasta na interface. Só devolve nomes que
// podem ser usados como pasta de um job, em ordem alfabética.
func Folders(ctx context.Context, c Config, env Env, dir string) ([]string, error) {
	dir, err := CleanPath(dir)
	if err != nil {
		return nil, err
	}
	if c.Type == TypeSFTP && c.HostKey == "" {
		return nil, errors.New("destino SFTP sem impressão digital do servidor: teste e salve o destino de novo")
	}
	conn, _, err := Dial(ctx, c, env)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	names, err := conn.Dirs(dir)
	if err != nil {
		return nil, fmt.Errorf("não foi possível listar as pastas no destino: %w", err)
	}
	out := []string{}
	for _, n := range names {
		if clean, err := CleanPath(n); err == nil && clean == n && n != "" && !strings.HasPrefix(n, ".") {
			out = append(out, n)
		}
	}
	slices.SortFunc(out, func(a, b string) int { return strings.Compare(strings.ToLower(a), strings.ToLower(b)) })
	return out, nil
}

// TestResult é o resultado do teste de um destino.
type TestResult struct {
	HostKey string `json:"host_key,omitempty"` // SFTP: impressão digital vista
	Detail  string `json:"detail"`
	Total   int64  `json:"total,omitempty"` // espaço do storage, quando o protocolo informa
	Free    int64  `json:"free,omitempty"`
}

// spacer é um storage que sabe informar o espaço livre (NFS e pasta local).
type spacer interface {
	Space() (total, free int64, err error)
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
	if sp, ok := conn.(spacer); ok {
		if t, f, err := sp.Space(); err == nil && t > 0 {
			out.Total, out.Free = t, f
		}
	}
	return out, nil
}
