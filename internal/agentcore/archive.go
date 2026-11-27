package agentcore

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/AprendendoLinux/corujabackup/internal/bkname"
	"github.com/AprendendoLinux/corujabackup/internal/netdest"
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
	// OwnDirs são outras pastas onde o agente grava (além da pasta de backups) e que também ficam fora da origem.
	OwnDirs []string
	// DestPath, quando informado, é a pasta real onde gravar (escolhida na interface); senão, Subdir dentro da pasta de backups.
	DestPath string
	// Artifacts são as pastas de backup local escolhidas (deste e dos outros jobs): os arquivos gerados nelas ficam fora.
	Artifacts []ArtifactDir
	// DB != nil: job de banco de dados (o executor padrão usa RunDatabase em vez de RunFiles).
	DB *pb.DbSpec
	// Mikrotik != nil: job de roteador MikroTik.
	Mikrotik *pb.MikrotikSpec
	// out != nil: grava o .tar.gz direto num storage (modo direto), em vez de um arquivo na pasta de backups.
	out archiveOut
	// inc != nil: job incremental (com inc.base nil, este backup é o full que abre uma cadeia).
	inc *incPlan
}

type Result struct {
	// Stored é o resultado no storage quando o backup foi gravado direto nele (sem arquivo local).
	Stored *netdest.Result
	// Kind é "full" ou "incremental"; Snapshot é o novo retrato (jobs incrementais), gravado só se a execução terminar bem.
	Kind        string
	Snapshot    *Snapshot
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
	prefix := Slug(job.Name)
	out := job.out
	dest := ""
	if out == nil {
		dest = job.DestPath
		if dest == "" {
			var err error
			if dest, err = SafeSubdir(backupDir, job.Subdir); err != nil {
				return res, err
			}
		}
		if err := os.MkdirAll(dest, 0o750); err != nil {
			return res, fmt.Errorf("não foi possível criar a pasta de destino: %w", err)
		}
		var st syscall.Statfs_t
		if syscall.Statfs(dest, &st) == nil {
			h.log("info", "espaço livre no destino: %s", HumanBytes(int64(st.Bavail)*int64(st.Bsize)))
		}
		out = &localOut{dest: dest}
	}
	arts := job.Artifacts
	if dest != "" {
		arts = append([]ArtifactDir{{Path: dest, Prefix: prefix}}, arts...)
	}
	generated := newArtifacts(arts)
	skipInDir := map[string][]*regexp.Regexp{} // por pasta percorrida: o que o agente gerou nela
	virtual := readVirtualMounts()
	inc := job.inc
	if inc != nil {
		inc.next = &Snapshot{Version: snapshotVersion, Files: map[string]fileMeta{}}
	}
	name, w, err := out.begin(prefix, loc, now, inc != nil && inc.base != nil)
	if err != nil {
		return res, err
	}
	hash := sha256.New()
	cw := &countingWriter{w: io.MultiWriter(w, hash)}
	gz := gzip.NewWriter(cw)
	tw := tar.NewWriter(gz)
	ex := NewExcluder(job.Excludes)
	own := findOwnDirs(append([]string{backupDir}, job.OwnDirs...)...)
	var entries int64
	last := time.Now()

	fail := func(err error) (Result, error) { tw.Close(); gz.Close(); out.abort(); return res, err }

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
		// a origem escolhida está dentro de uma pasta do agente: avisa, em vez de simplesmente não salvar nada
		top := hostRoot
		if top == "" {
			top = "/"
		}
		if own.contains(real, top) {
			h.log("warn", "origem ignorada: %s fica dentro de uma pasta do próprio agente (o que é do agente não entra no backup)", p)
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
			if d.IsDir() && own.is(info) {
				h.log("info", "pasta do próprio agente deixada de fora: %s", logical)
				return fs.SkipDir
			}
			for _, re := range skipInDir[filepath.Dir(cur)] {
				if cur != real && re.MatchString(d.Name()) {
					if d.IsDir() {
						return fs.SkipDir
					}
					return nil
				}
			}
			// a pasta virtual entra vazia (para a restauração ter o ponto de montagem), sem o conteúdo
			var after error
			if d.IsDir() {
				if p := generated.patterns(info); p != nil {
					skipInDir[cur] = p
				}
				if typ, ok := virtual[cur]; ok {
					h.log("info", "sistema de arquivos virtual deixado de fora: %s (%s)", logical, typ)
					after = fs.SkipDir
				}
			}
			mode := info.Mode()
			if !(mode.IsRegular() || mode.IsDir() || mode&fs.ModeSymlink != 0) {
				h.log("warn", "tipo de arquivo não suportado, ignorado: %s (%s)", logical, mode.Type())
				res.Warnings++
				return nil
			}
			meta := metaOf(info)
			record := func() {
				if inc != nil {
					inc.next.Files[logical] = meta
				}
			}
			// incremental: o que não mudou desde o backup anterior fica de fora (mas continua no retrato)
			if inc != nil && !mode.IsDir() && inc.unchanged(logical, meta) {
				record()
				inc.same++
				return after
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
				} else {
					record() // só entra no retrato o que foi lido por inteiro
				}
				if inc != nil {
					inc.reads++
				}
				res.BytesSource += hdr.Size
				res.Files++
			} else {
				if err := tw.WriteHeader(hdr); err != nil {
					return err
				}
				record()
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
			return after
		})
		if walkErr != nil {
			return fail(walkErr)
		}
	}
	if entries == 0 {
		return fail(errors.New("nada foi salvo: nenhuma das origens existe ou todas foram excluídas"))
	}
	if inc != nil {
		m := Manifest{Format: 1, Kind: inc.kind(), Chain: name, Created: now.In(loc).Format(time.RFC3339), Deleted: deletedSince(inc.base, inc.next)}
		if inc.base != nil {
			m.Chain, m.Base = inc.base.Chain, inc.base.Last
		}
		b, _ := json.MarshalIndent(m, "", "  ")
		if err := tw.WriteHeader(&tar.Header{Name: manifestName, Mode: 0o640, Size: int64(len(b)), ModTime: now, Typeflag: tar.TypeReg}); err != nil {
			return fail(err)
		}
		if _, err := tw.Write(b); err != nil {
			return fail(err)
		}
		entries++
		if inc.base != nil {
			h.log("info", "incremental sobre %s: %d arquivo(s) novo(s) ou alterado(s), %d sem mudança, %d apagado(s) desde o anterior", inc.base.Last, inc.reads, inc.same, len(m.Deleted))
		}
		inc.next.Sig, inc.next.Chain, inc.next.Last = "", m.Chain, name
		res.Snapshot = inc.next
		res.Kind = inc.kind()
	}
	if err := tw.Close(); err != nil {
		return fail(err)
	}
	if err := gz.Close(); err != nil {
		out.abort()
		return res, err
	}
	res.BytesOutput = cw.n
	res.SHA256 = hex.EncodeToString(hash.Sum(nil))
	res.OutputName, res.Prefix = name, prefix
	h.log("info", "arquivo gerado: %s (%s, %d itens)", name, HumanBytes(res.BytesOutput), entries)
	if d, ok := out.(*directOut); ok {
		h.log("info", "conferindo o arquivo no storage")
		if err := out.commit(ctx, res.SHA256, entries); err != nil {
			return res, err
		}
		res.Stored = &d.result
		if d.result.VerifiedBy == "sha256" {
			h.log("info", "conferido no storage: SHA-256 relido e %d itens legíveis", entries)
		} else {
			h.log("info", "conferido no storage pelo tamanho (conferência rápida)")
		}
		return res, nil
	}
	if err := out.commit(ctx, res.SHA256, entries); err != nil {
		return res, err
	}
	res.OutputPath, res.DestDir = filepath.Join(dest, name), dest
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

// ApplyRetention mantém as keep cadeias mais novas do job (um full e os incrementais dele; num job só de fulls, as keep
// cópias mais novas) e apaga as outras. Só mexe em arquivos com o padrão de nome do job.
func ApplyRetention(dir, prefix string, keep int) []string {
	ents, err := os.ReadDir(dir)
	if err != nil || keep < 1 {
		return nil
	}
	var files []bkname.File
	for _, e := range ents {
		if !e.IsDir() {
			files = append(files, bkname.File{Name: e.Name()})
		}
	}
	var removed []string
	for _, f := range bkname.Retention(files, prefix, keep, 0, time.Time{}) {
		if os.Remove(filepath.Join(dir, f.Name)) == nil {
			removed = append(removed, f.Name)
		}
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
