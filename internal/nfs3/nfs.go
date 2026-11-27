// Package nfs3 é um cliente NFSv3 (RFC 1813) em espaço de usuário, só com o que o agente precisa para gravar, conferir,
// listar e apagar backups. Não precisa de montagem no sistema, de privilégios especiais nem de pacotes no host: fala o
// protocolo direto por TCP (portmapper, MOUNT e NFS).
package nfs3

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	vers3 = 3

	procGetattr     = 1
	procLookup      = 3
	procRead        = 6
	procWrite       = 7
	procCreate      = 8
	procMkdir       = 9
	procRemove      = 12
	procRename      = 14
	procReaddirplus = 17
	procFsstat      = 18
	procFsinfo      = 19
	procCommit      = 21

	mntProcMnt  = 1
	mntProcUmnt = 3

	typeReg = 1
	typeDir = 2

	maxFH    = 64
	maxIO    = 1 << 20
	inflight = 4 // gravações em andamento ao mesmo tempo
)

// Options descreve o servidor e a exportação.
type Options struct {
	Host      string
	Port      int // porta do NFS; 0 = pergunta ao portmapper (ou 2049)
	MountPort int // porta do MOUNT; 0 = pergunta ao portmapper
	Export    string
	UID, GID  uint32
}

// Attr são os atributos de um arquivo ou pasta.
type Attr struct {
	Dir      bool
	Regular  bool
	Size     int64
	Modified time.Time
}

// Entry é um item de uma pasta.
type Entry struct {
	Name string
	Attr
}

// Client é uma exportação NFS "montada" em espaço de usuário.
type Client struct {
	uid    uint32
	nfs    *rpcConn
	mnt    *rpcConn
	export string
	root   []byte
	wsize  uint32
	rsize  uint32
	stop   func() bool
	mu     sync.Mutex
	dirs   map[string][]byte // caminho -> identificador (cache de pastas)
}

// Dial conecta e monta a exportação. O cancelamento de ctx encerra a conexão (e interrompe transferências travadas).
func Dial(ctx context.Context, o Options) (*Client, error) {
	export := "/" + strings.Trim(o.Export, "/")
	mport := o.MountPort
	if mport == 0 {
		p, err := getport(ctx, o.Host, progMount, vers3)
		switch {
		case err == nil:
			mport = p
		case o.Port != 0:
			mport = o.Port // sem portmapper: servidores que atendem MOUNT e NFS na mesma porta
		default:
			return nil, fmt.Errorf("não foi possível consultar o portmapper em %s (porta 111): %w", o.Host, friendlyNet(err))
		}
	}
	mnt, err := dialRPC(ctx, net.JoinHostPort(o.Host, strconv.Itoa(mport)), o.UID, o.GID)
	if err != nil {
		return nil, fmt.Errorf("não foi possível conectar ao serviço de montagem do NFS: %w", friendlyNet(err))
	}
	var e enc
	e.str(export)
	b, err := mnt.call(ctx, progMount, vers3, mntProcMnt, e.b)
	if err != nil {
		mnt.close(errClosed)
		return nil, err
	}
	d := dec{b: b}
	if st := d.u32(); st != 0 {
		mnt.close(errClosed)
		msg := mountError(st, o.Host, export)
		if (st == 1 || st == 13) && !mnt.priv {
			msg += hintPrivPort
		}
		return nil, errors.New(msg)
	}
	root := d.opaque(maxFH)
	if d.err != nil {
		mnt.close(errClosed)
		return nil, d.err
	}
	nport := o.Port
	if nport == 0 {
		if nport, err = getport(ctx, o.Host, progNFS, vers3); err != nil {
			nport = 2049
		}
	}
	conn, err := dialRPC(ctx, net.JoinHostPort(o.Host, strconv.Itoa(nport)), o.UID, o.GID)
	if err != nil {
		mnt.close(errClosed)
		return nil, fmt.Errorf("não foi possível conectar ao NFS em %s:%d: %w", o.Host, nport, friendlyNet(err))
	}
	c := &Client{uid: o.UID, nfs: conn, mnt: mnt, export: export, root: root, wsize: 64 << 10, rsize: 64 << 10, dirs: map[string][]byte{"": root}}
	c.stop = context.AfterFunc(ctx, func() { c.nfs.close(context.Canceled) })
	if err := c.fsinfo(ctx); err != nil {
		c.Close()
		return nil, err
	}
	return c, nil
}

var errClosed = errors.New("conexão encerrada")

// Close desmonta (avisa o servidor) e fecha as conexões.
func (c *Client) Close() error {
	c.stop()
	var e enc
	e.str(c.export)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	_, _ = c.mnt.call(ctx, progMount, vers3, mntProcUmnt, e.b)
	cancel()
	c.mnt.close(errClosed)
	c.nfs.close(errClosed)
	return nil
}

func friendlyNet(err error) error {
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return errors.New("tempo esgotado (servidor desligado, endereço errado ou firewall bloqueando)")
	}
	s := err.Error()
	switch {
	case strings.Contains(s, "connection refused"):
		return errors.New("conexão recusada (o serviço não está ativo nessa porta)")
	case strings.Contains(s, "no such host"):
		return errors.New("o nome do servidor não foi encontrado (DNS)")
	case strings.Contains(s, "no route to host"), strings.Contains(s, "network is unreachable"):
		return errors.New("sem rota até o servidor")
	}
	return err
}

func mountError(st uint32, host, export string) string {
	switch st {
	case 1, 13: // o mountd do Linux responde assim também para exportação inexistente
		return fmt.Sprintf("o servidor NFS recusou a exportação %s para este agente: confira se ela existe (showmount -e %s) e se o IP do agente está liberado em /etc/exports", export, host)
	case 2:
		return fmt.Sprintf("a exportação %s não existe no servidor NFS (veja as disponíveis com showmount -e)", export)
	case 20:
		return fmt.Sprintf("%s não é uma pasta", export)
	}
	return fmt.Sprintf("o servidor NFS recusou a montagem de %s (código %d)", export, st)
}

// Error é um erro do NFS com o código original.
type Error struct {
	Code uint32
	Op   string
}

func (e *Error) Error() string {
	m, ok := statusText[e.Code]
	if !ok {
		m = fmt.Sprintf("erro NFS %d", e.Code)
	}
	return e.Op + ": " + m
}

// Is permite errors.Is(err, fs.ErrNotExist) e fs.ErrExist.
func (e *Error) Is(t error) bool {
	return (t == fs.ErrNotExist && e.Code == 2) || (t == fs.ErrExist && e.Code == 17) || (t == fs.ErrPermission && (e.Code == 1 || e.Code == 13))
}

var statusText = map[uint32]string{
	1:     "operação não permitida (verifique root_squash/all_squash e o dono da pasta na exportação)",
	2:     "não encontrado",
	5:     "erro de E/S no servidor",
	13:    "acesso negado (verifique as permissões da pasta e o UID/GID usado)",
	17:    "já existe",
	20:    "não é uma pasta",
	21:    "é uma pasta",
	22:    "argumento inválido",
	27:    "arquivo grande demais para o servidor",
	28:    "sem espaço no servidor NFS",
	30:    "a exportação está somente leitura (use rw em /etc/exports)",
	63:    "nome longo demais",
	66:    "a pasta não está vazia",
	69:    "cota de disco excedida",
	70:    "identificador obsoleto (a pasta foi removida ou o servidor reiniciado)",
	10001: "identificador inválido",
	10004: "operação não suportada pelo servidor",
	10006: "falha interna do servidor NFS",
}

// call faz uma chamada NFS e devolve o decodificador posicionado depois do status. Reenvia em NFS3ERR_JUKEBOX
// (dado temporariamente indisponível, comum em armazenamento hierárquico).
func (c *Client) call(ctx context.Context, op string, proc uint32, args []byte) (*dec, uint32, error) {
	for try := 0; ; try++ {
		b, err := c.nfs.call(ctx, progNFS, vers3, proc, args)
		if err != nil {
			return nil, 0, err
		}
		d := &dec{b: b}
		st := d.u32()
		if d.err != nil {
			return nil, 0, d.err
		}
		if st == 10008 && try < 20 {
			select {
			case <-ctx.Done():
				return nil, 0, ctx.Err()
			case <-time.After(time.Second):
			}
			continue
		}
		return d, st, nil
	}
}

func (c *Client) fail(op string, st uint32) error {
	err := &Error{Code: st, Op: op}
	if (st == 1 || st == 13) && !c.nfs.priv {
		return fmt.Errorf("%w%s", err, hintPrivPort)
	}
	if (st == 1 || st == 13) && c.uid == 0 {
		return fmt.Errorf("%w (gravando como root: com root_squash, o padrão dos servidores, o root vira \"nobody\"; informe o UID/GID do dono da pasta em \"Usuário que grava\")", err)
	}
	return err
}

func readFattr(d *dec) Attr {
	typ := d.u32()
	d.skip(4 * 4) // mode, nlink, uid, gid
	size := d.u64()
	d.skip(8 + 8 + 8 + 8 + 8) // used, rdev, fsid, fileid, atime
	sec, nsec := d.u32(), d.u32()
	d.skip(8) // ctime
	return Attr{Dir: typ == typeDir, Regular: typ == typeReg, Size: int64(size), Modified: time.Unix(int64(sec), int64(nsec))}
}

func postOpAttr(d *dec) (Attr, bool) {
	if d.bool() {
		return readFattr(d), true
	}
	return Attr{}, false
}

func skipWcc(d *dec) {
	if d.bool() {
		d.skip(24)
	}
	postOpAttr(d)
}

func (c *Client) fsinfo(ctx context.Context) error {
	var e enc
	e.opaque(c.root)
	d, st, err := c.call(ctx, "fsinfo", procFsinfo, e.b)
	if err != nil {
		return err
	}
	if st != 0 {
		return c.fail("informações da exportação", st)
	}
	postOpAttr(d)
	rtmax, rtpref := d.u32(), d.u32()
	d.u32()
	wtmax, wtpref := d.u32(), d.u32()
	if d.err != nil {
		return d.err
	}
	pick := func(pref, max uint32) uint32 {
		v := pref
		if v == 0 || (max != 0 && v > max) {
			v = max
		}
		if v == 0 {
			v = 64 << 10
		}
		if v > maxIO {
			v = maxIO
		}
		if v < 4096 {
			v = 4096
		}
		return v
	}
	c.rsize, c.wsize = pick(rtpref, rtmax), pick(wtpref, wtmax)
	return nil
}

func splitPath(p string) []string {
	p = strings.Trim(path.Clean("/"+p), "/")
	if p == "" {
		return nil
	}
	return strings.Split(p, "/")
}

func (c *Client) lookup(ctx context.Context, dir []byte, name string) ([]byte, Attr, error) {
	var e enc
	e.opaque(dir)
	e.str(name)
	d, st, err := c.call(ctx, "lookup", procLookup, e.b)
	if err != nil {
		return nil, Attr{}, err
	}
	if st != 0 {
		return nil, Attr{}, c.fail(name, st)
	}
	fh := d.opaque(maxFH)
	a, _ := postOpAttr(d)
	return fh, a, d.err
}

// handle resolve um caminho relativo à exportação.
func (c *Client) handle(ctx context.Context, p string) ([]byte, error) {
	parts := splitPath(p)
	key := strings.Join(parts, "/")
	c.mu.Lock()
	fh, ok := c.dirs[key]
	c.mu.Unlock()
	if ok {
		return fh, nil
	}
	fh = c.root
	for i, s := range parts {
		sub := strings.Join(parts[:i+1], "/")
		c.mu.Lock()
		cached, ok := c.dirs[sub]
		c.mu.Unlock()
		if ok {
			fh = cached
			continue
		}
		next, a, err := c.lookup(ctx, fh, s)
		if err != nil {
			return nil, err
		}
		if a.Dir {
			c.mu.Lock()
			c.dirs[sub] = next
			c.mu.Unlock()
		}
		fh = next
	}
	return fh, nil
}

func (c *Client) forget(p string) {
	key := strings.Join(splitPath(p), "/")
	c.mu.Lock()
	for k := range c.dirs {
		if k == key || strings.HasPrefix(k, key+"/") {
			delete(c.dirs, k)
		}
	}
	c.mu.Unlock()
}

// Stat devolve os atributos de um caminho.
func (c *Client) Stat(ctx context.Context, p string) (Attr, error) {
	fh, err := c.handle(ctx, p)
	if err != nil {
		return Attr{}, err
	}
	var e enc
	e.opaque(fh)
	d, st, err := c.call(ctx, "getattr", procGetattr, e.b)
	if err != nil {
		return Attr{}, err
	}
	if st != 0 {
		return Attr{}, c.fail(p, st)
	}
	a := readFattr(d)
	return a, d.err
}

func sattrMode(e *enc, mode uint32) {
	e.bool(true)
	e.u32(mode)
	e.bool(false) // uid
	e.bool(false) // gid
}

// MkdirAll cria a pasta e as que faltarem no caminho.
func (c *Client) MkdirAll(ctx context.Context, p string) error {
	parts := splitPath(p)
	fh := c.root
	for i, s := range parts {
		sub := strings.Join(parts[:i+1], "/")
		next, err := c.handle(ctx, sub)
		if errors.Is(err, fs.ErrNotExist) {
			var e enc
			e.opaque(fh)
			e.str(s)
			sattrMode(&e, 0o750)
			e.bool(false) // size
			e.u32(0)      // atime: não mudar
			e.u32(0)      // mtime: não mudar
			d, st, cerr := c.call(ctx, "mkdir", procMkdir, e.b)
			if cerr != nil {
				return cerr
			}
			if st == 17 { // criada por outro ao mesmo tempo
				next, err = c.handle(ctx, sub)
			} else if st != 0 {
				return c.fail("criar a pasta "+sub, st)
			} else {
				if d.bool() {
					next = d.opaque(maxFH)
				}
				if d.err != nil {
					return d.err
				}
				if next == nil {
					next, err = c.handle(ctx, sub)
				} else {
					err = nil
					c.mu.Lock()
					c.dirs[sub] = next
					c.mu.Unlock()
				}
			}
		}
		if err != nil {
			return err
		}
		fh = next
	}
	return nil
}

// ReadDir lista uma pasta (sem "." e "..").
func (c *Client) ReadDir(ctx context.Context, p string) ([]Entry, error) {
	fh, err := c.handle(ctx, p)
	if err != nil {
		return nil, err
	}
	var out []Entry
	var cookie uint64
	verf := make([]byte, 8)
	for {
		var e enc
		e.opaque(fh)
		e.u64(cookie)
		e.fixed(verf)
		e.u32(16 << 10)  // dircount
		e.u32(128 << 10) // maxcount
		d, st, err := c.call(ctx, "readdirplus", procReaddirplus, e.b)
		if err != nil {
			return nil, err
		}
		if st != 0 {
			return nil, c.fail(p, st)
		}
		postOpAttr(d)
		copy(verf, d.take(8))
		for d.bool() {
			d.u64() // fileid
			name := string(d.opaque(1024))
			cookie = d.u64()
			a, _ := postOpAttr(d)
			if d.bool() {
				d.opaque(maxFH)
			}
			if d.err != nil {
				return nil, d.err
			}
			if name != "." && name != ".." {
				out = append(out, Entry{Name: name, Attr: a})
			}
		}
		eof := d.bool()
		if d.err != nil {
			return nil, d.err
		}
		if eof {
			return out, nil
		}
	}
}

func splitDir(p string) (string, string) {
	parts := splitPath(p)
	if len(parts) == 0 {
		return "", ""
	}
	return strings.Join(parts[:len(parts)-1], "/"), parts[len(parts)-1]
}

// Remove apaga um arquivo.
func (c *Client) Remove(ctx context.Context, p string) error {
	dir, name := splitDir(p)
	fh, err := c.handle(ctx, dir)
	if err != nil {
		return err
	}
	var e enc
	e.opaque(fh)
	e.str(name)
	_, st, err := c.call(ctx, "remove", procRemove, e.b)
	if err != nil {
		return err
	}
	if st != 0 {
		return c.fail("apagar "+p, st)
	}
	return nil
}

// Rename renomeia (ou move) um arquivo, substituindo o destino se existir.
func (c *Client) Rename(ctx context.Context, from, to string) error {
	fd, fn := splitDir(from)
	td, tn := splitDir(to)
	ffh, err := c.handle(ctx, fd)
	if err != nil {
		return err
	}
	tfh, err := c.handle(ctx, td)
	if err != nil {
		return err
	}
	var e enc
	e.opaque(ffh)
	e.str(fn)
	e.opaque(tfh)
	e.str(tn)
	_, st, err := c.call(ctx, "rename", procRename, e.b)
	if err != nil {
		return err
	}
	if st != 0 {
		return c.fail("renomear "+from, st)
	}
	c.forget(from)
	return nil
}

// FSStat devolve o espaço total e o disponível para o usuário na exportação.
func (c *Client) FSStat(ctx context.Context) (total, avail int64, err error) {
	var e enc
	e.opaque(c.root)
	d, st, err := c.call(ctx, "fsstat", procFsstat, e.b)
	if err != nil {
		return 0, 0, err
	}
	if st != 0 {
		return 0, 0, c.fail("espaço da exportação", st)
	}
	postOpAttr(d)
	t := d.u64()
	d.u64() // fbytes
	a := d.u64()
	return int64(t), int64(a), d.err
}

// Create cria (ou trunca) um arquivo e devolve um gravador. Close confirma os dados no disco do servidor (COMMIT).
func (c *Client) Create(ctx context.Context, p string) (*Writer, error) {
	dir, name := splitDir(p)
	fh, err := c.handle(ctx, dir)
	if err != nil {
		return nil, err
	}
	file, err := c.create(ctx, fh, name, true)
	var ne *Error
	if errors.As(err, &ne) && (ne.Code == 5 || ne.Code == 10004 || ne.Code == 22) {
		// alguns servidores não aceitam atributos na criação: cria sem eles e garante o arquivo vazio
		if file, err = c.create(ctx, fh, name, false); err == nil {
			if a, serr := c.Stat(ctx, p); serr == nil && a.Size > 0 {
				if err = c.Remove(ctx, p); err == nil {
					file, err = c.create(ctx, fh, name, false)
				}
			}
		}
	}
	if err != nil {
		return nil, err
	}
	w := &Writer{c: c, ctx: ctx, fh: file, sem: make(chan struct{}, inflight)}
	return w, nil
}

func (c *Client) create(ctx context.Context, dir []byte, name string, attrs bool) ([]byte, error) {
	var e enc
	e.opaque(dir)
	e.str(name)
	e.u32(0) // UNCHECKED: cria ou reaproveita
	if attrs {
		sattrMode(&e, 0o640)
		e.bool(true) // size = 0 (trunca)
		e.u64(0)
	} else {
		e.bool(false)
		e.bool(false)
		e.bool(false)
		e.bool(false)
	}
	e.u32(0)
	e.u32(0)
	d, st, err := c.call(ctx, "create", procCreate, e.b)
	if err != nil {
		return nil, err
	}
	if st != 0 {
		return nil, c.fail("criar "+name, st)
	}
	var file []byte
	if d.bool() {
		file = d.opaque(maxFH)
	}
	if d.err != nil {
		return nil, d.err
	}
	if file == nil {
		file, _, err = c.lookup(ctx, dir, name)
	}
	return file, err
}

// Writer grava um arquivo em blocos, com algumas gravações em andamento ao mesmo tempo (UNSTABLE + COMMIT no fim).
type Writer struct {
	c    *Client
	ctx  context.Context
	fh   []byte
	off  uint64
	sem  chan struct{}
	wg   sync.WaitGroup
	mu   sync.Mutex
	err  error
	verf []byte
	buf  []byte
}

func (w *Writer) setErr(err error) {
	w.mu.Lock()
	if w.err == nil {
		w.err = err
	}
	w.mu.Unlock()
}

func (w *Writer) failed() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.err
}

// Write junta os dados em blocos do tamanho preferido do servidor e os envia.
func (w *Writer) Write(p []byte) (int, error) {
	if err := w.failed(); err != nil {
		return 0, err
	}
	n := len(p)
	size := int(w.c.wsize)
	for len(p) > 0 {
		room := size - len(w.buf)
		if room > len(p) {
			room = len(p)
		}
		w.buf = append(w.buf, p[:room]...)
		p = p[room:]
		if len(w.buf) == size {
			w.flush()
		}
	}
	return n, w.failed()
}

func (w *Writer) flush() {
	if len(w.buf) == 0 {
		return
	}
	chunk, off := w.buf, w.off
	w.buf = make([]byte, 0, w.c.wsize)
	w.off += uint64(len(chunk))
	w.sem <- struct{}{}
	w.wg.Add(1)
	go func() {
		defer func() { <-w.sem; w.wg.Done() }()
		if err := w.writeAt(chunk, off); err != nil {
			w.setErr(err)
		}
	}()
}

func (w *Writer) writeAt(chunk []byte, off uint64) error {
	for len(chunk) > 0 {
		var e enc
		e.opaque(w.fh)
		e.u64(off)
		e.u32(uint32(len(chunk)))
		e.u32(0) // UNSTABLE
		e.opaque(chunk)
		d, st, err := w.c.call(w.ctx, "write", procWrite, e.b)
		if err != nil {
			return err
		}
		if st != 0 {
			return w.c.fail("gravar", st)
		}
		skipWcc(d)
		n := d.u32()
		d.u32() // committed
		verf := d.take(8)
		if d.err != nil {
			return d.err
		}
		if n == 0 || int(n) > len(chunk) {
			return errors.New("o servidor NFS não aceitou os dados")
		}
		if err := w.checkVerf(verf); err != nil {
			return err
		}
		chunk, off = chunk[n:], off+uint64(n)
	}
	return nil
}

// checkVerf: o verificador muda quando o servidor reinicia, e aí dados ainda não confirmados podem ter se perdido.
func (w *Writer) checkVerf(v []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.verf == nil {
		w.verf = append([]byte(nil), v...)
		return nil
	}
	if string(w.verf) != string(v) {
		return errors.New("o servidor NFS reiniciou durante a gravação: o arquivo foi descartado")
	}
	return nil
}

// Close envia o restante, espera as gravações e confirma tudo no disco do servidor.
func (w *Writer) Close() error {
	w.flush()
	w.wg.Wait()
	if err := w.failed(); err != nil {
		return err
	}
	var e enc
	e.opaque(w.fh)
	e.u64(0)
	e.u32(0) // tudo
	d, st, err := w.c.call(w.ctx, "commit", procCommit, e.b)
	if err != nil {
		return err
	}
	if st != 0 {
		return w.c.fail("confirmar a gravação", st)
	}
	skipWcc(d)
	verf := d.take(8)
	if d.err != nil {
		return d.err
	}
	if w.off > 0 {
		return w.checkVerf(verf)
	}
	return nil
}

// Open abre um arquivo para leitura sequencial.
func (c *Client) Open(ctx context.Context, p string) (io.ReadCloser, error) {
	fh, err := c.handle(ctx, p)
	if err != nil {
		return nil, err
	}
	return &reader{c: c, ctx: ctx, fh: fh}, nil
}

type reader struct {
	c   *Client
	ctx context.Context
	fh  []byte
	off uint64
	eof bool
}

func (r *reader) Read(p []byte) (int, error) {
	if r.eof {
		return 0, io.EOF
	}
	n := len(p)
	if n > int(r.c.rsize) {
		n = int(r.c.rsize)
	}
	var e enc
	e.opaque(r.fh)
	e.u64(r.off)
	e.u32(uint32(n))
	d, st, err := r.c.call(r.ctx, "read", procRead, e.b)
	if err != nil {
		return 0, err
	}
	if st != 0 {
		return 0, r.c.fail("ler", st)
	}
	postOpAttr(d)
	d.u32() // count
	r.eof = d.bool()
	data := d.opaque(maxIO)
	if d.err != nil {
		return 0, d.err
	}
	if len(data) > len(p) {
		return 0, errors.New("o servidor NFS devolveu mais dados que o pedido")
	}
	copy(p, data)
	r.off += uint64(len(data))
	if len(data) == 0 {
		if !r.eof {
			return 0, io.ErrUnexpectedEOF
		}
		return 0, io.EOF
	}
	return len(data), nil
}

func (r *reader) Close() error { return nil }
