package agentcore

// Backup em blocos (0.0.28): o .tar.gz é gravado como uma sequência de membros gzip independentes de ~64 MiB (sem
// comprimir), cada um começando e terminando na divisa entre itens do tar. Continua sendo um .tar.gz comum (tar -xzf,
// gzip -t e qualquer leitor de gzip leem os membros em sequência), mas a restauração de poucos arquivos lê só os blocos
// onde eles estão: um trecho do arquivo, local ou na nuvem. O custo é desprezível (o gzip recomeça o dicionário a
// cada bloco: ~0,1% maior). O catálogo guarda a posição e o SHA-256 de cada bloco e o bloco de cada item.

import (
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"hash"
	"io"
)

var blockTarget int64 = 64 << 20 // variável só para os testes gerarem vários blocos com arquivos pequenos

type blockGz struct {
	out    *countingWriter // saída comprimida: out.n é a posição no arquivo
	h      hash.Hash       // SHA-256 do bloco atual (bytes comprimidos)
	mw     io.Writer
	zw     *gzip.Writer
	start  int64
	raw    int64
	target int64
	blocks []Block
}

func newBlockGz(out *countingWriter, target int64) *blockGz {
	b := &blockGz{out: out, h: sha256.New(), target: target}
	b.mw = io.MultiWriter(out, b.h)
	b.zw = gzip.NewWriter(b.mw)
	return b
}

func (b *blockGz) Write(p []byte) (int, error) {
	n, err := b.zw.Write(p)
	b.raw += int64(n)
	return n, err
}

// full diz se o bloco atual já passou do tamanho alvo (o próximo item começa num bloco novo).
func (b *blockGz) full() bool { return b.raw >= b.target }

// current é o número (1 = o primeiro) do bloco que está recebendo os dados.
func (b *blockGz) current() int { return len(b.blocks) + 1 }

// cut fecha o bloco atual e abre o próximo. Só pode ser chamado na divisa entre itens do tar.
func (b *blockGz) cut() error {
	if b.raw == 0 {
		return nil
	}
	if err := b.zw.Close(); err != nil {
		return err
	}
	b.blocks = append(b.blocks, Block{O: b.start, E: b.out.n, H: hex.EncodeToString(b.h.Sum(nil))})
	b.h.Reset()
	b.start, b.raw = b.out.n, 0
	b.zw.Reset(b.mw)
	return nil
}

// Close fecha o último bloco.
func (b *blockGz) Close() error { return b.cut() }

// SetBlockTargetForTest muda o tamanho alvo dos blocos (só para testes de outros pacotes) e devolve o anterior.
func SetBlockTargetForTest(n int64) int64 {
	old := blockTarget
	blockTarget = n
	return old
}
