package agentcore

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/AprendendoLinux/corujabackup/internal/pb"
	"github.com/AprendendoLinux/corujabackup/internal/slug"
)

// FilesJob é o que o executor precisa para salvar pastas num .tar.gz.
type FilesJob struct {
	Name     string
	Paths    []string
	Excludes []string
	Subdir   string
	Keep     int
	// SkipRetention: quem chama aplica a retenção depois (por exemplo, só após o envio à nuvem ser verificado).
	SkipRetention bool
	// DB != nil: job de banco de dados (o executor padrão usa RunDatabase em vez de RunFiles).
	DB *pb.DbSpec
}

type Result struct {
	OutputName  string
	OutputPath  string
	DestDir     string
	Prefix      string
	BytesSource int64
	BytesOutput int64
	Files       int64
	SHA256      string
	Warnings    int
}

type Hooks struct {
	Logf     func(level, format string, args ...any)
	Progress func(r Result)
}

func (h Hooks) log(level, f string, a ...any) {
	if h.Logf != nil {
		h.Logf(level, f, a...)
	}
}

var ErrCanceled = errors.New("backup cancelado")

// Slug transforma o nome do job em prefixo seguro de arquivo.
func Slug(s string) string { return slug.Of(s) }

// SafeSubdir resolve a subpasta do job dentro de backupDir e recusa qualquer coisa que escape dela.
func SafeSubdir(backupDir, subdir string) (string, error) {
	if subdir == "" || filepath.IsAbs(subdir) {
		return "", fmt.Errorf("pasta de destino inválida: %q", subdir)
	}
	clean := filepath.Clean(subdir)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("pasta de destino fora da área de backups: %q", subdir)
	}
	return filepath.Join(backupDir, clean), nil
}

type countingWriter struct {
	w io.Writer
	n int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

// RunFiles cria <backupDir>/<subdir>/<slug>-AAAAMMDD-HHMMSS.tar.gz, confere o arquivo (gzip + tar + SHA-256) e só
// então aplica a retenção. Em qualquer falha não deixa arquivo parcial.
func RunFiles(ctx context.Context, job FilesJob, hostRoot, backupDir string, loc *time.Location, now time.Time, h Hooks) (Result, error) {
	var res Result
	dest, err := SafeSubdir(backupDir, job.Subdir)
	if err != nil {
		return res, err
	}
	if err := os.MkdirAll(dest, 0o750); err != nil {
		return res, fmt.Errorf("não foi possível criar a pasta de destino: %w", err)
	}
	var st syscall.Statfs_t
	if syscall.Statfs(dest, &st) == nil {
		h.log("info", "espaço livre no destino: %s", HumanBytes(int64(st.Bavail)*int64(st.Bsize)))
	}
	prefix := Slug(job.Name)
	name := fmt.Sprintf("%s-%s.tar.gz", prefix, now.In(loc).Format("20060102-150405"))
	for i := 0; i < 120; i++ { // duas execuções no mesmo segundo não podem sobrescrever uma à outra
		if _, err := os.Lstat(filepath.Join(dest, name)); err != nil {
			break
		}
		now = now.Add(time.Second)
		name = fmt.Sprintf("%s-%s.tar.gz", prefix, now.In(loc).Format("20060102-150405"))
	}
	final := filepath.Join(dest, name)
	tmp := filepath.Join(dest, "."+name+".tmp")
	defer os.Remove(tmp)

	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o640)
	if err != nil {
		return res, err
	}
	hash := sha256.New()
	cw := &countingWriter{w: io.MultiWriter(f, hash)}
	gz := gzip.NewWriter(cw)
	tw := tar.NewWriter(gz)
	ex := NewExcluder(job.Excludes)
	var entries int64
	last := time.Now()

	fail := func(err error) (Result, error) { tw.Close(); gz.Close(); f.Close(); return res, err }

	for _, p := range job.Paths {
		if ctx.Err() != nil {
			return fail(ErrCanceled)
		}
		real := filepath.Join(hostRoot, p)
		if _, err := os.Lstat(real); err != nil {
			h.log("warn", "origem não encontrada, ignorada: %s", p)
			res.Warnings++
			continue
		}
		h.log("info", "compactando %s", p)
		walkErr := filepath.WalkDir(real, func(cur string, d fs.DirEntry, err error) error {
			if ctx.Err() != nil {
				return ErrCanceled
			}
			logical := "/" + strings.TrimPrefix(strings.TrimPrefix(cur, hostRoot), "/")
			if hostRoot == "/" || hostRoot == "" {
				logical = cur
			}
			if err != nil {
				h.log("warn", "sem acesso a %s: %v", logical, err)
				res.Warnings++
				if d != nil && d.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			if ex.Match(logical) {
				if d.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			info, err := d.Info()
			if err != nil {
				h.log("warn", "não foi possível ler %s: %v", logical, err)
				res.Warnings++
				return nil
			}
			mode := info.Mode()
			if !(mode.IsRegular() || mode.IsDir() || mode&fs.ModeSymlink != 0) {
				h.log("warn", "tipo de arquivo não suportado, ignorado: %s (%s)", logical, mode.Type())
				res.Warnings++
				return nil
			}
			link := ""
			if mode&fs.ModeSymlink != 0 {
				if link, err = os.Readlink(cur); err != nil {
					h.log("warn", "link ilegível %s: %v", logical, err)
					res.Warnings++
					return nil
				}
			}
			hdr, err := tar.FileInfoHeader(info, link)
			if err != nil {
				res.Warnings++
				return nil
			}
			hdr.Name = strings.TrimPrefix(logical, "/")
			if mode.IsDir() {
				hdr.Name += "/"
			}
			if sys, ok := info.Sys().(*syscall.Stat_t); ok {
				hdr.Uid, hdr.Gid = int(sys.Uid), int(sys.Gid)
			}
			hdr.Uname, hdr.Gname = "", ""
			if mode.IsRegular() {
				src, err := os.Open(cur)
				if err != nil {
					h.log("warn", "sem permissão para ler %s: %v", logical, err)
					res.Warnings++
					return nil
				}
				defer src.Close()
				if err := tw.WriteHeader(hdr); err != nil {
					return err
				}
				n, err := io.Copy(tw, io.LimitReader(src, hdr.Size))
				if err != nil {
					return err
				}
				if n < hdr.Size { // o arquivo encolheu durante a leitura: completa com zeros para manter o tar válido
					if _, err := io.CopyN(tw, zeroReader{}, hdr.Size-n); err != nil {
						return err
					}
					h.log("warn", "arquivo mudou durante o backup: %s", logical)
					res.Warnings++
				}
				res.BytesSource += hdr.Size
				res.Files++
			} else {
				if err := tw.WriteHeader(hdr); err != nil {
					return err
				}
				if mode.IsDir() {
					res.Files++
				}
			}
			entries++
			if h.Progress != nil && time.Since(last) > 2*time.Second {
				last = time.Now()
				r := res
				r.BytesOutput = cw.n
				h.Progress(r)
			}
			return nil
		})
		if walkErr != nil {
			return fail(walkErr)
		}
	}
	if entries == 0 {
		return fail(errors.New("nada foi salvo: nenhuma das origens existe ou todas foram excluídas"))
	}
	if err := tw.Close(); err != nil {
		return fail(err)
	}
	if err := gz.Close(); err != nil {
		f.Close()
		return res, err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return res, err
	}
	if err := f.Close(); err != nil {
		return res, err
	}
	res.BytesOutput = cw.n
	res.SHA256 = hex.EncodeToString(hash.Sum(nil))
	if err := os.Rename(tmp, final); err != nil {
		return res, err
	}
	res.OutputName, res.OutputPath, res.DestDir, res.Prefix = name, final, dest, prefix
	h.log("info", "arquivo gerado: %s (%s, %d itens)", name, HumanBytes(res.BytesOutput), entries)

	if err := VerifyArchive(ctx, final, res.SHA256, entries); err != nil {
		os.Remove(final)
		return res, fmt.Errorf("verificação falhou (arquivo removido): %w", err)
	}
	h.log("info", "verificação ok: SHA-256 conferido e %d itens legíveis", entries)

	if job.SkipRetention {
		return res, nil
	}
	if removed := ApplyRetention(dest, prefix, job.Keep); len(removed) > 0 {
		h.log("info", "retenção (manter %d): %d cópia(s) antiga(s) removida(s)", job.Keep, len(removed))
	}
	return res, nil
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) { clear(p); return len(p), nil }

// VerifyArchive relê o arquivo inteiro: confere o SHA-256, a integridade do gzip e conta as entradas do tar.
func VerifyArchive(ctx context.Context, path, wantSHA string, wantEntries int64) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	gz, err := gzip.NewReader(io.TeeReader(f, h))
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
	if _, err := io.Copy(io.Discard, io.TeeReader(f, h)); err != nil { // consome o resto (rodapé do gzip)
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != wantSHA {
		return fmt.Errorf("SHA-256 diferente do gravado (%s)", got[:12])
	}
	if wantEntries > 0 && n != wantEntries {
		return fmt.Errorf("o tar tem %d itens, esperado %d", n, wantEntries)
	}
	return nil
}

// ApplyRetention mantém as keep cópias mais novas do job (nome <prefixo>-AAAAMMDD-HHMMSS.tar.gz) e apaga as outras.
// Só mexe em arquivos com esse padrão exato.
func ApplyRetention(dir, prefix string, keep int) []string {
	re := regexp.MustCompile("^" + regexp.QuoteMeta(prefix) + `-\d{8}-\d{6}\.tar\.gz$`)
	ents, err := os.ReadDir(dir)
	if err != nil || keep < 1 {
		return nil
	}
	var names []string
	for _, e := range ents {
		if !e.IsDir() && re.MatchString(e.Name()) {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names) // o carimbo de data no nome ordena cronologicamente
	var removed []string
	for len(names) > keep {
		if os.Remove(filepath.Join(dir, names[0])) == nil {
			removed = append(removed, names[0])
		}
		names = names[1:]
	}
	return removed
}

func HumanBytes(n int64) string {
	const u = 1024
	if n < u {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(u), 0
	for m := n / u; m >= u; m /= u {
		div *= u
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
