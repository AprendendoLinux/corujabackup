package agentcore

// Para onde vai o .tar.gz gerado: um arquivo na pasta de backups do agente (padrão) ou, no modo "direto no storage",
// um envio em fluxo para um storage de rede/pasta local, sem arquivo intermediário no agente.

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/AprendendoLinux/corujabackup/internal/netdest"
)

type archiveOut interface {
	// begin escolhe um nome livre (<prefixo>-AAAAMMDD-HHMMSS.tar.gz) e começa a gravar.
	begin(prefix string, loc *time.Location, now time.Time) (name string, w io.Writer, err error)
	// commit encerra a gravação, confere o conteúdo e só então publica o arquivo com o nome final.
	commit(ctx context.Context, sha string, entries int64) error
	// abort desiste e não deixa nada pela metade.
	abort()
}

// archiveName monta o nome do arquivo, avançando o horário enquanto o nome já existir (duas execuções no mesmo segundo
// não podem sobrescrever uma à outra).
func archiveName(prefix string, loc *time.Location, now time.Time, taken func(string) bool) string {
	name := ""
	for i := 0; i < 120; i++ {
		name = fmt.Sprintf("%s-%s.tar.gz", prefix, now.In(loc).Format("20060102-150405"))
		if !taken(name) {
			break
		}
		now = now.Add(time.Second)
	}
	return name
}

// ---------------------------------------------------------------- arquivo local

type localOut struct {
	dest, name, final, tmp string
	f                      *os.File
}

func (o *localOut) begin(prefix string, loc *time.Location, now time.Time) (string, io.Writer, error) {
	o.name = archiveName(prefix, loc, now, func(n string) bool { _, err := os.Lstat(filepath.Join(o.dest, n)); return err == nil })
	o.final, o.tmp = filepath.Join(o.dest, o.name), filepath.Join(o.dest, "."+o.name+".tmp")
	f, err := os.OpenFile(o.tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o640)
	if err != nil {
		return "", nil, err
	}
	o.f = f
	return o.name, f, nil
}

func (o *localOut) commit(ctx context.Context, sha string, entries int64) error {
	if err := o.f.Sync(); err != nil {
		o.abort()
		return err
	}
	if err := o.f.Close(); err != nil {
		_ = os.Remove(o.tmp)
		return err
	}
	if err := os.Rename(o.tmp, o.final); err != nil {
		_ = os.Remove(o.tmp)
		return err
	}
	if err := VerifyArchive(ctx, o.final, sha, entries); err != nil {
		_ = os.Remove(o.final)
		return fmt.Errorf("verificação falhou (arquivo removido): %w", err)
	}
	return nil
}

func (o *localOut) abort() {
	if o.f != nil {
		o.f.Close()
	}
	if o.tmp != "" {
		_ = os.Remove(o.tmp)
	}
}

// ---------------------------------------------------------------- direto no storage

// directOut grava o .tar.gz direto num storage. A conferência relê o arquivo do storage (SHA-256 e estrutura do tar,
// na conferência completa) antes de dar o nome final; a retenção do storage só roda depois disso.
type directOut struct {
	t      *netdest.Target
	u      *netdest.Upload
	job    netdest.Job // pasta, prefixo e retenção do storage
	result netdest.Result
}

func (o *directOut) begin(prefix string, loc *time.Location, now time.Time) (string, io.Writer, error) {
	name := archiveName(prefix, loc, now, o.t.Exists)
	o.u = o.t.Create(name)
	o.job.Name, o.job.Prefix = name, prefix
	return name, o.u, nil
}

func (o *directOut) commit(ctx context.Context, sha string, entries int64) error {
	o.job.SHA256 = sha
	r, err := o.u.Finish(o.job, func(r io.Reader) error { return verifyTar(ctx, r, entries) })
	if err != nil {
		return err
	}
	o.result = r
	return nil
}

func (o *directOut) abort() {
	if o.u != nil {
		o.u.Abort()
	}
}

// ---------------------------------------------------------------- conferência

// VerifyArchive confere um .tar.gz local: SHA-256 do arquivo e todos os itens do tar legíveis.
func VerifyArchive(ctx context.Context, path, wantSHA string, wantEntries int64) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	r := io.TeeReader(f, h)
	if err := verifyTar(ctx, r, wantEntries); err != nil {
		return err
	}
	if _, err := io.Copy(io.Discard, r); err != nil { // consome o resto (rodapé do gzip)
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != wantSHA {
		return fmt.Errorf("SHA-256 diferente do gravado (%s)", got[:12])
	}
	return nil
}

// verifyTar lê o gzip e todos os itens do tar (o conteúdo de cada um inclusive) e confere a quantidade.
func verifyTar(ctx context.Context, r io.Reader, wantEntries int64) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	var n int64
	for {
		if ctx.Err() != nil {
			return ErrCanceled
		}
		_, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		if _, err := io.Copy(io.Discard, tr); err != nil {
			return err
		}
		n++
	}
	if _, err := io.Copy(io.Discard, gz); err != nil { // fim do gzip: confere o CRC
		return err
	}
	if wantEntries > 0 && n != wantEntries {
		return fmt.Errorf("o tar tem %d itens, esperado %d", n, wantEntries)
	}
	return nil
}
