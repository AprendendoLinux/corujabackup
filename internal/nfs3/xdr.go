package nfs3

import (
	"encoding/binary"
	"errors"
	"math"
)

// Codificação XDR (RFC 4506) no mínimo necessário para o NFSv3: inteiros de 32/64 bits, opacos e strings com
// preenchimento até múltiplos de 4 bytes.

type enc struct{ b []byte }

func (e *enc) u32(v uint32) { e.b = binary.BigEndian.AppendUint32(e.b, v) }
func (e *enc) u64(v uint64) { e.b = binary.BigEndian.AppendUint64(e.b, v) }
func (e *enc) bool(v bool) {
	if v {
		e.u32(1)
	} else {
		e.u32(0)
	}
}
func (e *enc) opaque(p []byte) {
	e.u32(uint32(len(p)))
	e.fixed(p)
}
func (e *enc) fixed(p []byte) {
	e.b = append(e.b, p...)
	for len(e.b)%4 != 0 {
		e.b = append(e.b, 0)
	}
}
func (e *enc) str(s string) { e.opaque([]byte(s)) }

var errShort = errors.New("resposta NFS truncada")

type dec struct {
	b   []byte
	err error
}

func (d *dec) take(n int) []byte {
	if d.err != nil {
		return nil
	}
	if n < 0 || n > len(d.b) {
		d.err = errShort
		return nil
	}
	p := d.b[:n]
	d.b = d.b[n:]
	return p
}

func (d *dec) u32() uint32 {
	p := d.take(4)
	if p == nil {
		return 0
	}
	return binary.BigEndian.Uint32(p)
}

func (d *dec) u64() uint64 {
	p := d.take(8)
	if p == nil {
		return 0
	}
	return binary.BigEndian.Uint64(p)
}

func (d *dec) bool() bool { return d.u32() != 0 }

// opaque lê um opaco de tamanho variável com limite (evita alocar o que um servidor malicioso pedir).
func (d *dec) opaque(max int) []byte {
	n := d.u32()
	if d.err != nil {
		return nil
	}
	if n > uint32(max) || n > math.MaxInt32 {
		d.err = errors.New("resposta NFS com campo grande demais")
		return nil
	}
	p := d.take(int(n))
	d.take(pad(int(n)))
	return p
}

func (d *dec) skip(n int) { d.take(n) }

func pad(n int) int { return (4 - n%4) % 4 }
