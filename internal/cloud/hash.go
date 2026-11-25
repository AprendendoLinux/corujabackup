package cloud

import (
	"crypto/md5"  //nolint:gosec // o Google Drive expõe md5Checksum; usado só para conferir envio
	"crypto/sha1" //nolint:gosec // o OneDrive pessoal expõe sha1Hash; usado só para conferir envio
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"hash"
)

// QuickXor implementa o QuickXorHash da Microsoft (hash de 160 bits usado pelo OneDrive for Business).
// Cada byte é combinado por XOR numa posição que avança 11 bits por byte, de forma circular em 160 bits; no fim o
// tamanho do arquivo (64 bits, little-endian) é combinado nos últimos 8 bytes.
type QuickXor struct {
	data   [20]byte
	pos    int // deslocamento em bits do próximo byte (0..159)
	length uint64
}

func NewQuickXor() *QuickXor { return &QuickXor{} }

func (q *QuickXor) Reset()         { *q = QuickXor{} }
func (q *QuickXor) Size() int      { return 20 }
func (q *QuickXor) BlockSize() int { return 64 }

func (q *QuickXor) Write(p []byte) (int, error) {
	pos := q.pos
	for _, b := range p {
		idx, off := pos>>3, uint(pos&7)
		v := uint16(b) << off
		q.data[idx] ^= byte(v)
		q.data[(idx+1)%20] ^= byte(v >> 8)
		if pos += 11; pos >= 160 {
			pos -= 160
		}
	}
	q.pos = pos
	q.length += uint64(len(p))
	return len(p), nil
}

func (q *QuickXor) Sum(b []byte) []byte {
	out := q.data
	var l [8]byte
	binary.LittleEndian.PutUint64(l[:], q.length)
	for i := 0; i < 8; i++ {
		out[12+i] ^= l[i]
	}
	return append(b, out[:]...)
}

var _ hash.Hash = (*QuickXor)(nil)

// Sums reúne todos os hashes calculados numa única leitura do arquivo.
type Sums struct {
	size                  int64
	sha256, sha1, md5, qx hash.Hash
}

func NewSums() *Sums {
	return &Sums{sha256: sha256.New(), sha1: sha1.New(), md5: md5.New(), qx: NewQuickXor()} //nolint:gosec
}

func (s *Sums) Write(p []byte) (int, error) {
	s.size += int64(len(p))
	s.sha256.Write(p)
	s.sha1.Write(p)
	s.md5.Write(p)
	s.qx.Write(p)
	return len(p), nil
}

type Digest struct {
	Size     int64
	SHA256   string // hex
	SHA1     string // hex
	MD5      string // hex
	QuickXor string // base64 (formato do OneDrive)
}

func (s *Sums) Digest() Digest {
	return Digest{Size: s.size, SHA256: hex.EncodeToString(s.sha256.Sum(nil)), SHA1: hex.EncodeToString(s.sha1.Sum(nil)),
		MD5: hex.EncodeToString(s.md5.Sum(nil)), QuickXor: base64.StdEncoding.EncodeToString(s.qx.Sum(nil))}
}
