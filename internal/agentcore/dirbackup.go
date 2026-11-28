package agentcore

// Backup em pasta (0.0.29): em vez de um .tar.gz, cada execução vira uma pasta <job>-AAAAMMDD-HHMMSS com a árvore
// original, arquivo por arquivo, gravada direto no destino (a pasta local do agente ou um storage de rede). Só vale fora
// da nuvem.
//   - A pasta é gravada com nome provisório (<nome>.coruja-part) e só ganha o nome final no fim, com tudo conferido:
//     uma execução que falha ou é cancelada não deixa nada que pareça um backup.
//   - Cada arquivo tem o SHA-256 calculado na leitura da origem e é conferido depois de gravado (relido, na conferência
//     completa do storage; senão, pelo tamanho).
//   - O catálogo (.coruja-catalogo.json.gz, na raiz da pasta) guarda de cada item o SHA-256, permissões, dono, data e
//     o destino dos links simbólicos, que SMB e FTP não guardam. A restauração pelo Coruja reaplica tudo.
//   - Incremental: o que não mudou desde a pasta anterior vira um link físico para o arquivo de lá, onde o storage
//     permite (pasta local, NFS, SFTP). Cada pasta continua completa por si: dá para abrir qualquer uma, e apagar uma
//     antiga não estraga as outras. Sem links físicos (SMB, FTP), cada pasta é uma cópia completa.

import (
	"archive/tar"
	"bytes"
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
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/AprendendoLinux/corujabackup/internal/bkname"
	"github.com/AprendendoLinux/corujabackup/internal/netdest"
	"github.com/AprendendoLinux/corujabackup/internal/pb"
)

const (
	FormatTar   = "tar"
	FormatDir   = "dir"
	dirManifest = ".coruja-catalogo.json.gz"
)

// isDirBackup diz se o nome é de um backup em pasta.
func isDirBackup(name string) bool { return bkname.IsDir(name) }

// dirTarget é onde gravar um backup em pasta.
type dirTarget struct {
	t        *netdest.Target
	dest     string // caminho real da pasta local (job sem storage); "" num storage
	keep     int    // retenção no storage (no local, quem aplica é o executor, depois do fim)
	keepDays int
}

type countReader struct {
	r io.Reader
	n int64
}

func (c *countReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// RunDir grava o backup como uma pasta com os arquivos abertos (veja o começo do arquivo).
func RunDir(ctx context.Context, job FilesJob, dt dirTarget, hostRoot, backupDir string, loc *time.Location, now time.Time, h Hooks) (Result, error) {
	var res Result
	t := dt.t
	conn, folder := t.Conn(), t.Folder()
	prefix := Slug(job.Name)
	for _, d := range t.SweepParts(prefix) {
		h.log("info", "pasta provisória de uma execução interrompida apagada: %s", d)
	}
	// o carimbo não repete nem com os .tar.gz do job (de quando o formato era outro)
	name := ""
	for i, stamp := 0, now; i < 120; i, stamp = i+1, stamp.Add(time.Second) {
		name = bkname.DirName(prefix, stamp.In(loc))
		if !t.ExistsDir(name) && !t.Exists(name+".tar.gz") && !t.Exists(name+bkname.IncSuffix+".tar.gz") {
			break
		}
	}
	part := name + netdest.PartSuffix
	root := path.Join(folder, part)
	if err := conn.MkdirAll(root); err != nil {
		return res, fmt.Errorf("não foi possível criar a pasta no destino: %w", err)
	}
	if ch, ok := conn.(netdest.Chmoder); ok {
		_ = ch.Chmod(root, 0o700) // os arquivos ficam abertos: a pasta da execução, só para o dono
	}
	done := false
	defer func() {
		if !done {
			if err := t.RemoveTree(part); err != nil {
				h.log("warn", "não foi possível apagar a pasta provisória %s: %v", part, err)
			}
		}
	}()

	// incremental: links físicos para a pasta anterior (se o storage permite e ela ainda está lá)
	inc := job.inc
	linker, canLink := conn.(netdest.Linker)
	var base *Snapshot
	if inc != nil && inc.base != nil {
		switch {
		case !isDirBackup(inc.base.Last):
			h.log("info", "o backup anterior não é uma pasta: esta começa completa")
		case !canLink:
			h.log("info", "este storage (%s) não tem links físicos: cada pasta é uma cópia completa", t.Type())
		case !t.ExistsDir(inc.base.Last):
			h.log("warn", "a pasta anterior (%s) não está mais no destino: esta começa completa", inc.base.Last)
		default:
			base = inc.base
			h.log("info", "o que não mudou desde %s vira link físico para o arquivo de lá (sem ocupar espaço de novo)", base.Last)
		}
		if base == nil {
			inc.base = nil // sem links, é um full (o dia do full passa a ser hoje)
		}
	}
	next := &Snapshot{Version: snapshotVersion, Files: map[string]fileMeta{}, Chain: name, Last: name, Archives: []string{name}}
	if base != nil {
		next.Chain = base.Chain
	}

	attrs, _ := conn.(netdest.AttrSetter)
	symlinks, _ := conn.(netdest.Symlinker)
	full := t.FullVerify()
	var (
		mu                    sync.Mutex
		written               int64
		linked, copied        int
		linkWarned, slInfoned bool
	)
	warn := func(f string, a ...any) {
		h.log("warn", f, a...)
		mu.Lock()
		res.Warnings++
		mu.Unlock()
	}
	type item struct {
		cur, logical, rel string
		meta              fileMeta
		link              bool
	}
	put := func(ctx context.Context, it item) error {
		dst := path.Join(root, it.rel)
		if it.link {
			err := linker.Link(path.Join(folder, base.Last, it.rel), dst)
			if err == nil {
				it.meta.H = base.Files[it.logical].H
				mu.Lock()
				next.Files[it.logical] = it.meta
				linked++
				res.Files++
				mu.Unlock()
				return nil
			}
			if ctx.Err() != nil {
				return ErrCanceled
			}
			mu.Lock()
			first := !linkWarned
			linkWarned = true
			mu.Unlock()
			if first {
				h.log("warn", "o link físico falhou (%s: %v): os arquivos sem link são copiados", it.logical, err)
			}
		}
		src, err := os.Open(it.cur)
		if err != nil {
			warn("sem permissão para ler %s: %v", it.logical, err)
			return nil
		}
		defer src.Close()
		hs := sha256.New()
		cr := &countReader{r: ctxRd(ctx, src)}
		if err := conn.Write(ctx, path.Dir(dst), path.Base(dst), io.TeeReader(cr, hs)); err != nil {
			if ctx.Err() != nil {
				return ErrCanceled
			}
			return fmt.Errorf("falha ao gravar %s no destino: %w", it.logical, err)
		}
		sum := hex.EncodeToString(hs.Sum(nil))
		if err := verifyRemote(ctx, conn, path.Dir(dst), path.Base(dst), cr.n, sum, full); err != nil {
			if ctx.Err() != nil {
				return ErrCanceled
			}
			return fmt.Errorf("%s não confere no destino: %w", it.logical, err)
		}
		if attrs != nil {
			attrs.SetAttrs(dst, fs.FileMode(it.meta.Mode), it.meta.U, it.meta.G, time.Unix(0, it.meta.MTime))
		}
		mu.Lock()
		defer mu.Unlock()
		written += cr.n
		copied++
		res.BytesSource += cr.n
		res.Files++
		if cr.n != it.meta.Size {
			h.log("warn", "arquivo mudou durante o backup: %s", it.logical)
			res.Warnings++
		} else {
			it.meta.H = sum
			next.Files[it.logical] = it.meta // só entra no catálogo o que foi lido por inteiro
		}
		return nil
	}

	// FTP e SMB: tudo pela mesma conexão, um arquivo por vez; nos outros, alguns ao mesmo tempo
	wctx, stop := context.WithCancel(ctx)
	defer stop()
	var firstErr error
	setErr := func(err error) {
		mu.Lock()
		if firstErr == nil {
			firstErr = err
			stop()
		}
		mu.Unlock()
	}
	var work chan item
	var wg sync.WaitGroup
	if n := t.Parallel(); n > 1 {
		work = make(chan item, n)
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for it := range work {
					if wctx.Err() == nil {
						if err := put(wctx, it); err != nil {
							setErr(err)
						}
					}
				}
			}()
		}
	}
	finish := func() {
		if work != nil {
			close(work)
			work = nil
			wg.Wait()
		}
	}
	defer finish()

	made := map[string]bool{"": true}
	ensureDir := func(rel string) error {
		if rel == "." || made[rel] {
			return nil
		}
		if err := conn.MkdirAll(path.Join(root, rel)); err != nil {
			return fmt.Errorf("não foi possível criar a pasta %s no destino: %w", rel, err)
		}
		for d := rel; d != "." && d != ""; d = path.Dir(d) {
			made[d] = true
		}
		return nil
	}
	type dirAttr struct {
		rel  string
		meta fileMeta
	}
	var dirs []dirAttr
	var entries int64
	last := time.Now()
	walkErr := walkSources(wctx, job, hostRoot, backupDir, dt.dest, prefix, "copiando", h, &res, func(cur, logical string, info fs.FileInfo, link string) error {
		if err := wctx.Err(); err != nil {
			return ErrCanceled
		}
		rel := strings.TrimPrefix(logical, "/")
		mode := info.Mode()
		meta := metaOf(info)
		if sys, ok := info.Sys().(*syscall.Stat_t); ok {
			meta.U, meta.G = int(sys.Uid), int(sys.Gid)
		}
		record := func() {
			mu.Lock()
			next.Files[logical] = meta
			mu.Unlock()
		}
		switch {
		case mode.IsDir():
			if err := ensureDir(rel); err != nil {
				return err
			}
			if rel != "" {
				dirs = append(dirs, dirAttr{rel, meta})
			}
			record()
			mu.Lock()
			res.Files++
			mu.Unlock()
		case mode&fs.ModeSymlink != 0:
			if err := ensureDir(path.Dir(rel)); err != nil {
				return err
			}
			meta.L = link
			if symlinks != nil {
				dst := path.Join(root, rel)
				if err := symlinks.Symlink(dst, link); err != nil {
					warn("link simbólico não criado no destino (%s): %v", logical, err)
				} else if attrs != nil {
					attrs.SetAttrs(dst, mode, meta.U, meta.G, info.ModTime())
				}
			} else if !slInfoned {
				slInfoned = true
				h.log("info", "este storage não guarda links simbólicos: eles ficam no catálogo, e a restauração pelo Coruja os recria")
			}
			record()
		default:
			if err := ensureDir(path.Dir(rel)); err != nil {
				return err
			}
			it := item{cur: cur, logical: logical, rel: rel, meta: meta}
			if base != nil && inc.unchanged(logical, meta) && base.Files[logical].H != "" {
				it.link = true
				inc.same++
			} else if inc != nil {
				inc.reads++
			}
			if work == nil {
				if err := put(wctx, it); err != nil {
					return err
				}
			} else {
				select {
				case work <- it:
				case <-wctx.Done():
					return ErrCanceled
				}
			}
		}
		entries++
		if h.Progress != nil && time.Since(last) > 2*time.Second {
			last = time.Now()
			mu.Lock()
			r := res
			r.BytesOutput = written
			mu.Unlock()
			h.Progress(r)
		}
		return nil
	})
	finish()
	switch {
	case ctx.Err() != nil:
		return res, ErrCanceled
	case firstErr != nil:
		return res, firstErr
	case walkErr != nil:
		return res, walkErr
	case entries == 0:
		return res, errors.New("nada foi salvo: nenhuma das origens existe ou todas foram excluídas")
	}

	// pastas: dono, permissões e data originais por último (gravar dentro delas mudaria a data), das mais fundas
	if attrs != nil {
		sort.Slice(dirs, func(i, j int) bool { return dirs[i].rel > dirs[j].rel })
		for _, d := range dirs {
			attrs.SetAttrs(path.Join(root, d.rel), fs.FileMode(d.meta.Mode), d.meta.U, d.meta.G, time.Unix(0, d.meta.MTime))
		}
	}
	// o catálogo vai junto, na raiz da pasta: com ele, qualquer agente restaura esta pasta (inclusive o que o storage
	// não guarda: dono, permissões, links simbólicos)
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if err := json.NewEncoder(zw).Encode(next); err != nil {
		return res, err
	}
	if err := zw.Close(); err != nil {
		return res, err
	}
	sum := sha256.Sum256(buf.Bytes())
	res.SHA256 = hex.EncodeToString(sum[:])
	if err := conn.Write(ctx, root, dirManifest, bytes.NewReader(buf.Bytes())); err != nil {
		return res, fmt.Errorf("falha ao gravar o catálogo no destino: %w", err)
	}
	if err := verifyRemote(ctx, conn, root, dirManifest, int64(buf.Len()), res.SHA256, full); err != nil {
		return res, fmt.Errorf("o catálogo não confere no destino: %w", err)
	}
	if err := conn.Rename(folder, part, name); err != nil {
		return res, fmt.Errorf("não foi possível dar o nome final à pasta: %w", err)
	}
	done = true
	if base != nil && linked == 0 && copied > 0 { // nenhum link físico deu certo: na prática, uma cópia completa
		base, inc.base = nil, nil
	}
	if inc != nil {
		res.Kind = inc.kind()
		if base != nil {
			h.log("info", "incremental sobre %s: %d arquivo(s) novo(s) ou alterado(s) copiado(s), %d sem mudança por link físico", base.Last, copied, linked)
		}
	}
	res.OutputName, res.Prefix, res.Snapshot = name, prefix, next
	res.BytesOutput = written + int64(buf.Len())
	h.log("info", "pasta gravada: %s (%d itens; %s copiados)", name, entries, HumanBytes(written))
	verified := "sha256"
	if full {
		h.log("info", "verificação ok: cada arquivo copiado foi relido do destino e confere com o SHA-256 da origem")
	} else {
		verified = netdest.VerifySize
		h.log("info", "conferido no storage pelo tamanho de cada arquivo (conferência rápida)")
	}
	if dt.dest != "" {
		res.OutputPath, res.DestDir = filepath.Join(dt.dest, name), dt.dest
		return res, nil
	}
	res.Stored = &netdest.Result{Path: path.Join(folder, name), Hash: res.SHA256, VerifiedBy: verified,
		Retention: t.Retain(prefix, dt.keep, dt.keepDays, now.UTC(), name)}
	return res, nil
}

// verifyRemote confere um arquivo gravado: relido inteiro (SHA-256) ou só o tamanho.
func verifyRemote(ctx context.Context, conn netdest.Conn, dir, name string, n int64, sum string, full bool) error {
	if !full {
		size, err := conn.Size(dir, name)
		if err != nil {
			return err
		}
		if size != n {
			return fmt.Errorf("o destino guardou %d bytes, esperado %d", size, n)
		}
		return nil
	}
	rc, err := conn.Open(ctx, dir, name)
	if err != nil {
		return err
	}
	hs := sha256.New()
	m, err := io.Copy(hs, ctxRd(ctx, rc))
	if cerr := rc.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if m != n || hex.EncodeToString(hs.Sum(nil)) != sum {
		return errors.New("o conteúdo relido do destino é diferente do gravado")
	}
	return nil
}

// runDirJob abre o destino de um job em pasta (o storage ou a pasta local do agente) e grava o backup nele.
func (c *Core) runDirJob(ctx context.Context, spec *pb.JobSpec, job FilesJob, loc *time.Location, started time.Time, h Hooks) (Result, error) {
	dt := dirTarget{}
	switch d := spec.Dest; {
	case d != nil:
		cfg, err := destConfig(d)
		if err != nil {
			return Result{}, err
		}
		h.log("info", "gravando a pasta direto no storage %s (%s), sem cópia no agente", cfg.Type, cfg.Label())
		if dt.t, err = netdest.OpenTarget(ctx, cfg, c.destEnv(), d.Folder); err != nil {
			return Result{}, err
		}
		dt.keep, dt.keepDays = int(d.Keep), int(d.KeepDays)
	case spec.CloudAccountId != 0:
		return Result{}, errors.New("o backup em pasta só vale para storages de rede e pasta local (nas nuvens, use o formato .tar.gz)")
	default:
		dest, err := c.localDir(job.Subdir)
		if err != nil {
			return Result{}, err
		}
		if strings.HasPrefix(job.Subdir, "/") {
			h.log("info", "pasta do backup local: %s", job.Subdir)
		}
		if dt.t, err = netdest.LocalTarget(dest); err != nil {
			return Result{}, err
		}
		var st syscall.Statfs_t
		if syscall.Statfs(dest, &st) == nil {
			h.log("info", "espaço livre no destino: %s", HumanBytes(int64(st.Bavail)*int64(st.Bsize)))
		}
		dt.dest = dest
	}
	defer dt.t.Close()
	job.DestPath, job.Artifacts, job.OwnDirs = dt.dest, c.artifactDirs(), c.ownDirs()
	return RunDir(ctx, job, dt, c.cfg.HostRoot, c.cfg.BackupDir, loc, started, h)
}

// ---------------------------------------------------------------- leitura (restauração e download)

// dirSource lê os arquivos de um backup em pasta: da pasta local do agente ou do storage do job.
type dirSource struct {
	open  func(ctx context.Context, rel string) (io.ReadCloser, error)
	close func()
	where string
}

func (c *Core) openDirBackup(ctx context.Context, act *pb.AgentAction, name string) (*dirSource, error) {
	if !isDirBackup(name) {
		return nil, errors.New("nome de backup inválido")
	}
	c.mu.Lock()
	spec := c.jobs[act.JobId]
	c.mu.Unlock()
	if spec != nil && spec.Dest != nil {
		cfg, err := destConfig(spec.Dest)
		if err != nil {
			return nil, err
		}
		folder, err := netdest.CleanPath(spec.Dest.Folder)
		if err != nil {
			return nil, err
		}
		conn, _, err := netdest.Dial(ctx, cfg, c.destEnv())
		if err != nil {
			return nil, fmt.Errorf("não foi possível conectar ao storage: %w", err)
		}
		top := path.Join(folder, name)
		return &dirSource{where: "storage", close: func() { conn.Close() }, open: func(ctx context.Context, rel string) (io.ReadCloser, error) {
			return conn.Open(ctx, path.Join(top, path.Dir(rel)), path.Base(rel))
		}}, nil
	}
	dir, err := c.localDir(act.Subdir)
	if err != nil {
		return nil, err
	}
	r, err := os.OpenRoot(filepath.Join(dir, name)) // nada lido de dentro da pasta escapa dela, nem por link simbólico
	if err != nil {
		return nil, fmt.Errorf("a pasta do backup %s não está mais neste agente (a retenção pode tê-la removido)", name)
	}
	return &dirSource{where: "agente", close: func() { r.Close() }, open: func(_ context.Context, rel string) (io.ReadCloser, error) {
		return r.Open(rel)
	}}, nil
}

// dirHeader descreve um item do catálogo como um item de tar (a restauração e o download tratam os dois formatos igual).
func dirHeader(rel string, m fileMeta) *tar.Header {
	mode := fs.FileMode(m.Mode)
	h := &tar.Header{Name: rel, Mode: int64(mode.Perm()), Uid: m.U, Gid: m.G, ModTime: time.Unix(0, m.MTime), Size: m.Size, Typeflag: tar.TypeReg}
	switch {
	case mode.IsDir():
		h.Typeflag, h.Size = tar.TypeDir, 0
	case mode&fs.ModeSymlink != 0:
		h.Typeflag, h.Linkname, h.Size = tar.TypeSymlink, m.L, 0
	}
	return h
}

// eachFromDir chama fn para cada item escolhido de um backup em pasta, conferindo o conteúdo de cada arquivo pelo
// SHA-256 do catálogo antes de entregá-lo (nada adulterado chega a ser gravado).
func (c *Core) eachFromDir(ctx context.Context, act *pb.AgentAction, al *actionLog, cat *Snapshot, name string, want map[string]bool,
	fn func(h *tar.Header, r io.Reader, rel string) error) error {
	src, err := c.openDirBackup(ctx, act, name)
	if err != nil {
		return err
	}
	defer src.close()
	rels := make([]string, 0, len(want))
	for rel := range want {
		if rel != "" {
			rels = append(rels, rel)
		}
	}
	sort.Strings(rels) // as pastas antes do que há dentro delas
	al.logf("info", "lendo %d item(ns) da pasta %s (%s)", len(rels), name, src.where)
	for _, rel := range rels {
		m, ok := cat.Files["/"+rel]
		if !ok {
			return fmt.Errorf("%s não está no catálogo", rel)
		}
		h := dirHeader(rel, m)
		if h.Typeflag != tar.TypeReg {
			if err := fn(h, bytes.NewReader(nil), rel); err != nil {
				return err
			}
			continue
		}
		if m.H == "" {
			return fmt.Errorf("%s: o catálogo não tem o SHA-256 do arquivo", rel)
		}
		rc, err := src.open(ctx, rel)
		if err != nil {
			return fmt.Errorf("%s: não foi possível ler da pasta do backup: %w", rel, err)
		}
		vb := newVerifiedBlocks(ctxRd(ctx, rc), blockRange{{O: 0, E: m.Size, H: m.H}}, c.cfg.BackupDir)
		err = fn(h, vb, rel)
		vb.Close()
		rc.Close()
		if err != nil {
			return fmt.Errorf("%s: %w", rel, err)
		}
	}
	return nil
}

// catalogFromDir lê o catálogo gravado dentro da pasta do backup (o agente perdeu o dele: reinstalado, por exemplo).
func (c *Core) catalogFromDir(ctx context.Context, act *pb.AgentAction) (*Snapshot, error) {
	src, err := c.openDirBackup(ctx, act, act.FileName)
	if err != nil {
		return nil, err
	}
	defer src.close()
	rc, err := src.open(ctx, dirManifest)
	if err != nil {
		return nil, fmt.Errorf("a pasta do backup não tem o catálogo (%s): %w", dirManifest, err)
	}
	defer rc.Close()
	b, err := io.ReadAll(io.LimitReader(ctxRd(ctx, rc), 1<<30))
	if err != nil {
		return nil, err
	}
	if s := sha256.Sum256(b); act.Sha256 != "" && hex.EncodeToString(s[:]) != act.Sha256 {
		return nil, errors.New("o catálogo da pasta do backup não confere com o SHA-256 registrado na execução")
	}
	zr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		return nil, fmt.Errorf("catálogo ilegível: %w", err)
	}
	var cat Snapshot
	if err := json.NewDecoder(zr).Decode(&cat); err != nil {
		return nil, fmt.Errorf("catálogo ilegível: %w", err)
	}
	if len(cat.Archives) != 1 || cat.Archives[0] != act.FileName {
		return nil, errors.New("o catálogo da pasta é de outro backup")
	}
	return &cat, nil
}
