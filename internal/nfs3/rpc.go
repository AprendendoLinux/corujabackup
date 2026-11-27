package nfs3

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// ONC RPC (RFC 5531) sobre TCP, com marcação de registros e várias chamadas em andamento na mesma conexão.

const (
	maxRecord   = 8 << 20 // nenhuma resposta usada aqui passa de ~1 MiB de dados
	callTimeout = 60 * time.Second
	dialTimeout = 15 * time.Second
)

// hintPrivPort explica a causa mais comum de recusa quando o agente não conseguiu usar uma porta de origem baixa.
const hintPrivPort = " (o agente não conseguiu usar uma porta de origem abaixo de 1024, que a exportação pode estar exigindo: adicione a opção \"insecure\" na exportação, ex.: /srv/backups 10.0.0.0/24(rw,insecure))"

type reply struct {
	body []byte
	err  error
}

type rpcConn struct {
	c       net.Conn
	cred    []byte // credencial AUTH_UNIX já codificada (flavor + corpo)
	wmu     sync.Mutex
	mu      sync.Mutex
	pending map[uint32]chan reply
	dead    error
	xid     atomic.Uint32
	priv    bool // a porta de origem é privilegiada
}

// dialRPC conecta em addr tentando, quando possível, uma porta de origem privilegiada (exigida pela maioria das
// exportações NFS). Sem permissão para isso (agente sem root), usa uma porta comum.
func dialRPC(ctx context.Context, addr string, uid, gid uint32) (*rpcConn, error) {
	var (
		c    net.Conn
		err  error
		priv bool
	)
	for port := 1023; port >= 665; port-- {
		d := net.Dialer{Timeout: dialTimeout, LocalAddr: &net.TCPAddr{Port: port}}
		c, err = d.DialContext(ctx, "tcp", addr)
		if err == nil {
			priv = true
			break
		}
		if errors.Is(err, syscall.EADDRINUSE) || errors.Is(err, syscall.EADDRNOTAVAIL) {
			continue
		}
		if errors.Is(err, syscall.EACCES) || errors.Is(err, syscall.EPERM) {
			break // sem privilégio para portas baixas
		}
		return nil, err // recusada, sem rota, tempo esgotado...: tentar outras portas não muda nada
	}
	if !priv {
		d := net.Dialer{Timeout: dialTimeout}
		if c, err = d.DialContext(ctx, "tcp", addr); err != nil {
			return nil, err
		}
	}
	var e enc
	e.u32(1) // AUTH_UNIX
	var body enc
	body.u32(uint32(time.Now().Unix()))
	body.str("coruja-agente")
	body.u32(uid)
	body.u32(gid)
	body.u32(1)
	body.u32(gid)
	e.opaque(body.b)
	r := &rpcConn{c: c, cred: e.b, pending: map[uint32]chan reply{}, priv: priv}
	r.xid.Store(uint32(time.Now().UnixNano()))
	go r.readLoop()
	return r, nil
}

func (r *rpcConn) close(err error) {
	r.mu.Lock()
	if r.dead == nil {
		r.dead = err
		for x, ch := range r.pending {
			ch <- reply{err: err}
			delete(r.pending, x)
		}
	}
	r.mu.Unlock()
	_ = r.c.Close()
}

func (r *rpcConn) readLoop() {
	br := bufio.NewReaderSize(r.c, 256<<10)
	for {
		msg, err := readRecord(br)
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
				err = errors.New("o servidor NFS encerrou a conexão")
			}
			r.close(err)
			return
		}
		if len(msg) < 8 {
			continue
		}
		x := binary.BigEndian.Uint32(msg)
		r.mu.Lock()
		ch := r.pending[x]
		delete(r.pending, x)
		r.mu.Unlock()
		if ch != nil {
			ch <- reply{body: msg[4:]}
		}
	}
}

func readRecord(br *bufio.Reader) ([]byte, error) {
	var out []byte
	for {
		var h [4]byte
		if _, err := io.ReadFull(br, h[:]); err != nil {
			return nil, err
		}
		n := binary.BigEndian.Uint32(h[:])
		size := int(n & 0x7fffffff)
		if len(out)+size > maxRecord {
			return nil, errors.New("resposta NFS grande demais")
		}
		start := len(out)
		out = append(out, make([]byte, size)...)
		if _, err := io.ReadFull(br, out[start:]); err != nil {
			return nil, err
		}
		if n&0x80000000 != 0 {
			return out, nil
		}
	}
}

// call faz uma chamada e devolve o corpo dos resultados (depois do cabeçalho de resposta aceita).
func (r *rpcConn) call(ctx context.Context, prog, vers, proc uint32, args []byte) ([]byte, error) {
	x := r.xid.Add(1)
	var e enc
	e.u32(0) // marcador de registro, preenchido abaixo
	e.u32(x)
	e.u32(0) // CALL
	e.u32(2) // versão do RPC
	e.u32(prog)
	e.u32(vers)
	e.u32(proc)
	e.b = append(e.b, r.cred...)
	e.u32(0) // verificador AUTH_NONE
	e.u32(0)
	e.b = append(e.b, args...)
	binary.BigEndian.PutUint32(e.b, uint32(len(e.b)-4)|0x80000000)

	ch := make(chan reply, 1)
	r.mu.Lock()
	if r.dead != nil {
		r.mu.Unlock()
		return nil, r.dead
	}
	r.pending[x] = ch
	r.mu.Unlock()

	r.wmu.Lock()
	_ = r.c.SetWriteDeadline(time.Now().Add(callTimeout))
	_, err := r.c.Write(e.b)
	r.wmu.Unlock()
	if err != nil {
		r.close(fmt.Errorf("falha ao enviar ao servidor NFS: %w", err))
		return nil, err
	}
	t := time.NewTimer(callTimeout)
	defer t.Stop()
	var rep reply
	select {
	case rep = <-ch:
	case <-ctx.Done():
		r.close(ctx.Err())
		return nil, ctx.Err()
	case <-t.C:
		err := errors.New("o servidor NFS parou de responder")
		r.close(err)
		return nil, err
	}
	if rep.err != nil {
		return nil, rep.err
	}
	return parseReply(rep.body)
}

func parseReply(b []byte) ([]byte, error) {
	d := dec{b: b}
	if d.u32() != 1 { // REPLY
		return nil, errors.New("resposta RPC inválida")
	}
	switch d.u32() {
	case 0: // MSG_ACCEPTED
		d.u32()
		d.opaque(400)
		st := d.u32()
		if d.err != nil {
			return nil, d.err
		}
		switch st {
		case 0:
			return d.b, nil
		case 1, 2:
			return nil, errors.New("o servidor não oferece este serviço na versão 3 (é preciso NFSv3)")
		case 3:
			return nil, errors.New("o servidor NFS não suporta esta operação")
		default:
			return nil, fmt.Errorf("o servidor NFS recusou a chamada (código %d)", st)
		}
	case 1: // MSG_DENIED
		if d.u32() == 1 {
			switch d.u32() {
			case 5:
				return nil, errors.New("o servidor NFS exige uma autenticação mais forte (Kerberos), que o Coruja não suporta: use sec=sys na exportação")
			default:
				return nil, errors.New("o servidor NFS recusou as credenciais do agente (AUTH_SYS)")
			}
		}
		return nil, errors.New("o servidor recusou a versão do RPC")
	}
	return nil, errors.New("resposta RPC inválida")
}

const (
	progPortmap = 100000
	progMount   = 100005
	progNFS     = 100003
)

// getport consulta o portmapper (porta 111) pela porta TCP de um programa.
func getport(ctx context.Context, host string, prog, vers uint32) (int, error) {
	r, err := dialRPC(ctx, net.JoinHostPort(host, "111"), 0, 0)
	if err != nil {
		return 0, err
	}
	defer r.close(errors.New("fechada"))
	var e enc
	e.u32(prog)
	e.u32(vers)
	e.u32(6) // TCP
	e.u32(0)
	b, err := r.call(ctx, progPortmap, 2, 3, e.b)
	if err != nil {
		return 0, err
	}
	d := dec{b: b}
	p := d.u32()
	if d.err != nil {
		return 0, d.err
	}
	if p == 0 || p > 65535 {
		return 0, errors.New("serviço não registrado no portmapper")
	}
	return int(p), nil
}
