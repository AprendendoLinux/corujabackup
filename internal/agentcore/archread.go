package agentcore

// Leitura seletiva de backups (0.0.28): para restaurar ou baixar alguns itens, o agente não precisa mais ter o .tar.gz
// inteiro no disco. Ele lê direto da origem (o arquivo no agente, a nuvem pelo Server ou o storage de rede):
//   - backups com índice de blocos (gerados a partir da 0.0.28): só os blocos onde estão os itens escolhidos (um trecho
//     do arquivo), cada bloco conferido pelo SHA-256 guardado no catálogo;
//   - backups antigos: o arquivo em fluxo, parando assim que todos os itens escolhidos foram lidos. Se a leitura vai até o
//     fim, o SHA-256 do arquivo inteiro é conferido; parando antes, valem as conferências do gzip (CRC) e do tar.

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/AprendendoLinux/corujabackup/internal/netdest"
	"github.com/AprendendoLinux/corujabackup/internal/pb"
)

// openArchive abre o backup name para leitura: inteiro (end <= 0) ou só o trecho [off, end). Procura no agente, depois
// na nuvem do job (pelo Server) ou no storage de rede. where diz de onde veio (para o log).
func (c *Core) openArchive(ctx context.Context, act *pb.AgentAction, name string, off, end int64) (rc io.ReadCloser, where string, err error) {
	if !backupFileRE.MatchString(name) {
		return nil, "", errors.New("nome de arquivo de backup inválido")
	}
	dir, err := c.localDir(act.Subdir)
	if err != nil {
		return nil, "", err
	}
	p := filepath.Join(dir, name)
	if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() {
		f, err := os.Open(p)
		if err != nil {
			return nil, "", err
		}
		if end <= 0 {
			return f, "agente", nil
		}
		if _, err := f.Seek(off, io.SeekStart); err != nil {
			f.Close()
			return nil, "", err
		}
		return limitedFile{io.LimitReader(f, end-off), f}, "agente", nil
	}
	c.mu.Lock()
	spec := c.jobs[act.JobId]
	c.mu.Unlock()
	n := int64(0)
	if end > 0 {
		n = end - off
	}
	switch {
	case spec != nil && spec.Dest == nil && spec.CloudAccountId != 0:
		rc, _, err := c.cloudStream(ctx, act.JobId, name, off, n)
		if err != nil {
			return nil, "", fmt.Errorf("não foi possível ler %s da nuvem: %w", name, err)
		}
		return rc, "nuvem", nil
	case spec != nil && spec.Dest != nil:
		cfg, err := destConfig(spec.Dest)
		if err != nil {
			return nil, "", err
		}
		rc, err := netdest.OpenStream(ctx, cfg, c.destEnv(), spec.Dest.Folder, name, off)
		if err != nil {
			return nil, "", fmt.Errorf("não foi possível ler %s do storage: %w", name, err)
		}
		if n > 0 {
			rc = limitedFile{io.LimitReader(rc, n), rc}
		}
		return rc, "storage", nil
	}
	return nil, "", fmt.Errorf("o arquivo de backup %s não está mais neste agente (a retenção local pode tê-lo removido) e o job não tem storage de onde trazê-lo", name)
}

type limitedFile struct {
	io.Reader
	c io.Closer
}

func (l limitedFile) Close() error { return l.c.Close() }

// blockRange é uma sequência de blocos vizinhos de um .tar.gz, lida de uma vez (um trecho do arquivo).
type blockRange []Block

// rangesFor devolve os trechos do arquivo i da cadeia que contêm os itens de want, ou ok=false se o arquivo não tem
// índice de blocos (backup anterior à 0.0.28) ou algum item não sabe em que bloco está.
func rangesFor(cat *Snapshot, i int, want map[string]bool) (rs []blockRange, ok bool) {
	if i < 0 || i >= len(cat.Archives) {
		return nil, false
	}
	bl := cat.Blocks[cat.Archives[i]]
	if len(bl) == 0 {
		return nil, false
	}
	need := map[int]bool{}
	for rel := range want {
		m, found := cat.Files["/"+rel]
		if !found || m.A != i || m.B < 1 || m.B > len(bl) {
			return nil, false
		}
		need[m.B-1] = true
	}
	idx := make([]int, 0, len(need))
	for k := range need {
		idx = append(idx, k)
	}
	sort.Ints(idx)
	for k, b := range idx {
		if k > 0 && b == idx[k-1]+1 && bl[b].O == bl[b-1].E {
			rs[len(rs)-1] = append(rs[len(rs)-1], bl[b])
			continue
		}
		rs = append(rs, blockRange{bl[b]})
	}
	return rs, true
}

// verifiedBlocks entrega um trecho de blocos vizinhos só depois de conferir cada bloco inteiro pelo SHA-256 do catálogo:
// nada de um bloco adulterado chega a ser descomprimido ou gravado. Blocos pequenos são conferidos na memória; os
// grandes (um arquivo enorme ocupa um bloco só), num arquivo temporário do tamanho do bloco, apagado em seguida.
type verifiedBlocks struct {
	src     io.Reader
	blocks  blockRange
	tmpDir  string
	i       int
	cur     io.Reader
	cleanup func()
}

var memBlock int64 = 8 << 20 // variável só para os testes

func newVerifiedBlocks(src io.Reader, br blockRange, tmpDir string) *verifiedBlocks {
	return &verifiedBlocks{src: src, blocks: br, tmpDir: tmpDir}
}

func (v *verifiedBlocks) Read(p []byte) (int, error) {
	for {
		if v.cur == nil {
			if v.i >= len(v.blocks) {
				return 0, io.EOF
			}
			if err := v.load(v.blocks[v.i]); err != nil {
				return 0, err
			}
			v.i++
		}
		n, err := v.cur.Read(p)
		if errors.Is(err, io.EOF) {
			v.Close()
			if n > 0 {
				return n, nil
			}
			continue
		}
		return n, err
	}
}

func (v *verifiedBlocks) load(b Block) error {
	size := b.E - b.O
	h := sha256.New()
	bad := fmt.Errorf("um bloco do backup não confere com o catálogo (SHA-256 diferente na posição %d): o arquivo foi alterado ou corrompido", b.O)
	short := func(err error) error {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return errors.New("o trecho do backup veio menor que o esperado")
		}
		return err
	}
	if size <= memBlock {
		buf := make([]byte, size)
		if _, err := io.ReadFull(v.src, buf); err != nil {
			return short(err)
		}
		h.Write(buf)
		if hex.EncodeToString(h.Sum(nil)) != b.H {
			return bad
		}
		v.cur, v.cleanup = bytes.NewReader(buf), nil
		return nil
	}
	if err := os.MkdirAll(v.tmpDir, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(v.tmpDir, ".coruja-bloco-*")
	if err != nil {
		return err
	}
	drop := func() { f.Close(); _ = os.Remove(f.Name()) }
	if _, err := io.CopyN(io.MultiWriter(f, h), v.src, size); err != nil {
		drop()
		return short(err)
	}
	if hex.EncodeToString(h.Sum(nil)) != b.H {
		drop()
		return bad
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		drop()
		return err
	}
	v.cur, v.cleanup = f, drop
	return nil
}

// Close apaga o temporário do bloco atual (se houver).
func (v *verifiedBlocks) Close() {
	if v.cleanup != nil {
		v.cleanup()
	}
	v.cur, v.cleanup = nil, nil
}

// eachSelected chama fn para cada item escolhido (groups: arquivo da cadeia → caminhos sem "/" inicial), lendo dos
// backups só o necessário. fn recebe o conteúdo do item em r (arquivos).
func (c *Core) eachSelected(ctx context.Context, act *pb.AgentAction, al *actionLog, cat *Snapshot, groups map[int]map[string]bool,
	shaOf map[string]string, fn func(h *tar.Header, r io.Reader, rel string) error) error {
	idx := make([]int, 0, len(groups))
	for i := range groups {
		if i < 0 || i >= len(cat.Archives) {
			return errors.New("catálogo inconsistente: aponta para um backup fora da cadeia")
		}
		idx = append(idx, i)
	}
	sort.Ints(idx)
	for _, i := range idx {
		name, want := cat.Archives[i], groups[i]
		found := map[string]bool{}
		if rs, ok := rangesFor(cat, i, want); ok {
			var bytes int64
			nb := 0
			for _, r := range rs {
				bytes += r[len(r)-1].E - r[0].O
				nb += len(r)
			}
			total := len(cat.Blocks[name])
			for k, r := range rs {
				rc, where, err := c.openArchive(ctx, act, name, r[0].O, r[len(r)-1].E)
				if err != nil {
					return err
				}
				if k == 0 {
					al.logf("info", "lendo de %s (%s) só %d de %d bloco(s): %s", name, where, nb, total, HumanBytes(bytes))
				}
				vb := newVerifiedBlocks(ctxRd(ctx, rc), r, c.cfg.BackupDir)
				err = scanTar(ctx, vb, want, found, true, fn)
				vb.Close()
				rc.Close()
				if err != nil {
					return fmt.Errorf("%s: %w", name, err)
				}
			}
			if len(found) < len(want) {
				return fmt.Errorf("%s: %d item(ns) do catálogo não foram encontrados nos blocos indicados", name, len(want)-len(found))
			}
			continue
		}
		rc, where, err := c.openArchive(ctx, act, name, 0, 0)
		if err != nil {
			return err
		}
		al.logf("info", "lendo %s (%s) em fluxo até encontrar os %d item(ns)", name, where, len(want))
		h := sha256.New()
		err = scanTar(ctx, io.TeeReader(ctxRd(ctx, rc), h), want, found, false, fn)
		complete := errors.Is(err, errReadToEnd)
		if complete {
			err = nil
		}
		rc.Close()
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		if complete {
			if want := shaOf[name]; want != "" && where != "agente" && hex.EncodeToString(h.Sum(nil)) != want {
				return fmt.Errorf("%s veio de %s diferente do backup original (SHA-256 não confere)", name, where)
			}
		} else {
			al.logf("info", "  todos os itens encontrados: a leitura de %s parou antes do fim", name)
		}
		if len(found) < len(want) {
			return fmt.Errorf("%s: %d item(ns) do catálogo não estão no arquivo", name, len(want)-len(found))
		}
	}
	return nil
}

// errReadToEnd: scanTar leu o arquivo até o fim (sem parar antes).
var errReadToEnd = errors.New("lido até o fim")

// scanTar percorre o tar comprimido em r e chama fn para os itens de want ainda não vistos (marcando em found). Num
// trecho de blocos (drain), lê até o fim do trecho, para conferir todos os blocos; senão, para assim que acha todos.
func scanTar(ctx context.Context, r io.Reader, want, found map[string]bool, drain bool, fn func(h *tar.Header, r io.Reader, rel string) error) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("arquivo ilegível: %w", err)
	}
	tr := tar.NewReader(gz)
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("arquivo ilegível: %w", err)
		}
		rel := strings.TrimPrefix(path.Clean("/"+h.Name), "/")
		if rel == "" || rel == manifestName || !want[rel] || found[rel] {
			continue
		}
		found[rel] = true
		if err := fn(h, tr, rel); err != nil {
			return err
		}
		if !drain && len(found) == len(want) {
			return nil
		}
	}
	if _, err := io.Copy(io.Discard, gz); err != nil { // o resto do trecho (ou o fim do tar): confere blocos e CRC
		return fmt.Errorf("arquivo ilegível: %w", err)
	}
	if drain {
		return nil
	}
	return errReadToEnd
}
