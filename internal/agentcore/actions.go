package agentcore

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/AprendendoLinux/corujabackup/internal/bkname"
	"github.com/AprendendoLinux/corujabackup/internal/netdest"
	"github.com/AprendendoLinux/corujabackup/internal/pb"
)

var (
	backupFileRE = bkname.Any
	targetRE     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
)

type actionLog struct {
	c    *Core
	act  *pb.AgentAction
	mu   sync.Mutex
	buf  []*pb.LogLine
	last time.Time
}

func (a *actionLog) logf(level, f string, args ...any) {
	txt := fmt.Sprintf(f, args...)
	a.c.log.Info(txt, "ação", a.act.Kind, "execução", a.act.RunUid)
	a.mu.Lock()
	a.buf = append(a.buf, &pb.LogLine{UnixMs: time.Now().UnixMilli(), Level: level, Text: txt})
	a.mu.Unlock()
}

// flush envia as linhas acumuladas (e o estado) ao Server; best effort.
func (a *actionLog) flush(state, msg string, mod func(*pb.ActionLog)) {
	a.mu.Lock()
	lines := a.buf
	a.buf = nil
	a.mu.Unlock()
	m := &pb.ActionLog{RunUid: a.act.RunUid, Kind: a.act.Kind, State: state, Message: msg, Logs: lines}
	if mod != nil {
		mod(m)
	}
	a.c.trySend(&pb.AgentMessage{Body: &pb.AgentMessage_ActionLog{ActionLog: m}})
}

// HandleAction executa, em segundo plano, uma ação pedida pelo Server sobre um backup já feito.
func (c *Core) HandleAction(parent context.Context, act *pb.AgentAction) {
	key := act.Kind + ":" + act.RunUid + ":" + act.DownloadId // downloads do mesmo backup podem correr juntos
	c.mu.Lock()
	if c.actions[key] {
		c.mu.Unlock()
		return
	}
	c.actions[key] = true
	c.mu.Unlock()
	go func() {
		defer func() { c.mu.Lock(); delete(c.actions, key); c.mu.Unlock() }()
		al := &actionLog{c: c, act: act}
		ctx, cancel := context.WithCancel(parent)
		defer cancel()
		// envia o andamento a cada 2 s
		done := make(chan struct{})
		go func() {
			t := time.NewTicker(2 * time.Second)
			defer t.Stop()
			for {
				select {
				case <-t.C:
					al.flush("running", "", nil)
				case <-done:
					return
				}
			}
		}()
		var err error
		switch act.Kind {
		case "reupload":
			err = c.reupload(ctx, act, al)
		case "extract":
			err = c.extract(ctx, act, al)
		case "fetch":
			err = c.fetch(ctx, act, al)
		case "restore_files":
			err = c.restoreFiles(ctx, act, al)
		case "index":
			err = c.index(ctx, act, al)
		case "download":
			err = c.download(ctx, act, al)
		case "restore_db":
			err = c.restoreDB(ctx, act, al)
		default:
			err = fmt.Errorf("ação desconhecida: %s", act.Kind)
		}
		close(done)
		if err != nil {
			al.logf("error", "falhou: %v", err)
			al.flush("failed", err.Error(), nil)
		}
	}()
}

// backupPath resolve (com validação) o arquivo de backup local de uma execução.
func (c *Core) backupPath(act *pb.AgentAction) (dest, file string, err error) {
	if !backupFileRE.MatchString(act.FileName) {
		return "", "", errors.New("nome de arquivo de backup inválido")
	}
	dest, err = c.localDir(act.Subdir)
	if err != nil {
		return "", "", err
	}
	file = filepath.Join(dest, act.FileName)
	if st, e := os.Stat(file); e != nil || !st.Mode().IsRegular() {
		return "", "", errors.New("o arquivo de backup não existe mais neste agente (a retenção local pode tê-lo removido)")
	}
	return dest, file, nil
}

func (c *Core) reupload(ctx context.Context, act *pb.AgentAction, al *actionLog) error {
	c.mu.Lock()
	spec := c.jobs[act.JobId]
	c.mu.Unlock()
	if spec == nil || spec.CloudAccountId == 0 {
		return errors.New("o job não tem destino de nuvem configurado neste agente (aguarde a sincronização)")
	}
	dest, file, err := c.backupPath(act)
	if err != nil {
		return err
	}
	st, _ := os.Stat(file)
	res := Result{OutputName: act.FileName, OutputPath: file, DestDir: dest, BytesOutput: st.Size(), Prefix: Slug(spec.Name)}
	al.logf("info", "reenviando %s (%s) para a nuvem", act.FileName, HumanBytes(st.Size()))
	last := -10
	info, err := c.upload(ctx, spec, act.RunUid, res, Hooks{Logf: al.logf}, func(pct int) {
		if pct-last >= 10 {
			last = pct
			al.logf("info", "enviando… %d%%", pct)
		}
	})
	if err != nil {
		return fmt.Errorf("o reenvio falhou: %w", err)
	}
	al.logf("info", "nuvem: %s (verificado por %s)", info.Path, info.VerifiedBy)
	for _, l := range info.Retention {
		al.logf("info", "nuvem: %s", l)
	}
	if spec.Keep <= 0 {
		if os.Remove(file) == nil {
			al.logf("info", "cópia local removida (a nuvem tem o arquivo verificado)")
		}
	}
	al.flush("ok", "reenviado", func(m *pb.ActionLog) {
		m.CloudStatus, m.CloudPath, m.CloudHash, m.CloudVerifiedBy = "ok", info.Path, info.Hash, info.VerifiedBy
	})
	return nil
}

func (c *Core) extract(ctx context.Context, act *pb.AgentAction, al *actionLog) error {
	if !targetRE.MatchString(act.Target) || strings.Contains(act.Target, "..") {
		return errors.New("nome de pasta de destino inválido")
	}
	dest := filepath.Join(c.cfg.BackupDir, "restores", act.Target)
	if _, err := os.Lstat(dest); err == nil {
		return fmt.Errorf("a pasta %s já existe; escolha outro nome", dest)
	}
	files, fetched, err := c.chainFiles(ctx, act, al)
	defer func() { // o que foi trazido do storage só para esta restauração não fica ocupando o agente
		for _, f := range fetched {
			_ = os.Remove(f)
		}
	}()
	if err != nil {
		return err
	}
	var n int
	var size int64
	for i, file := range files {
		if len(files) > 1 {
			al.logf("info", "aplicando %d de %d: %s", i+1, len(files), filepath.Base(file))
		} else {
			al.logf("info", "extraindo %s para %s", act.FileName, dest)
		}
		k, sz, man, err := extractOne(ctx, file, dest, al.logf)
		if err != nil {
			_ = os.RemoveAll(dest)
			return err
		}
		n, size = n+k, size+sz
		if man != nil && len(man.Deleted) > 0 {
			removed := applyDeletions(dest, man.Deleted)
			al.logf("info", "%d item(ns) apagado(s) na origem desde o backup anterior foram removidos da restauração", removed)
		}
	}
	al.logf("info", "extração concluída: %d itens, %s, em %s", n, HumanBytes(size), dest)
	al.flush("ok", "extraído", func(m *pb.ActionLog) { m.Path = dest })
	return nil
}

// chainFiles devolve os arquivos locais a extrair, em ordem. Para um incremental, é a cadeia inteira (full primeiro);
// o que não estiver no agente é trazido do storage de rede do job (conferido pelo SHA-256), quando houver um.
func (c *Core) chainFiles(ctx context.Context, act *pb.AgentAction, al *actionLog) (files, fetched []string, err error) {
	chain, shas := act.Chain, act.ChainSha256
	if len(chain) == 0 { // um backup só (full): o mesmo caminho, com uma cadeia de um arquivo
		if !backupFileRE.MatchString(act.FileName) {
			return nil, nil, errors.New("nome de arquivo de backup inválido")
		}
		chain, shas = []string{act.FileName}, []string{act.Sha256}
	}
	if chain[len(chain)-1] != act.FileName {
		return nil, nil, errors.New("cadeia de restauração inconsistente")
	}
	if len(chain) > 1 {
		al.logf("info", "backup incremental: a restauração aplica %d arquivo(s) em ordem (o full e os incrementais até este)", len(chain))
	}
	shaOf := map[string]string{}
	for i, n := range chain {
		if i < len(shas) {
			shaOf[n] = shas[i]
		}
	}
	return c.obtain(ctx, act, al, chain, shaOf)
}

// obtain devolve o caminho local de cada backup pedido: o que estiver na pasta do job no agente ou, senão, trazido do
// storage de rede do job (conferido pelo SHA-256 informado). fetched lista os trazidos, para apagar depois do uso.
func (c *Core) obtain(ctx context.Context, act *pb.AgentAction, al *actionLog, names []string, shaOf map[string]string) (files, fetched []string, err error) {
	dir, err := c.localDir(act.Subdir)
	if err != nil {
		return nil, nil, err
	}
	c.mu.Lock()
	spec := c.jobs[act.JobId]
	c.mu.Unlock()
	var out, missing []string
	for _, name := range names {
		if !backupFileRE.MatchString(name) {
			return nil, fetched, errors.New("nome de arquivo de backup inválido")
		}
		p := filepath.Join(dir, name)
		if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() {
			out = append(out, p)
			continue
		}
		if spec == nil || spec.Dest == nil {
			missing = append(missing, name)
			continue
		}
		cfg, err := destConfig(spec.Dest)
		if err != nil {
			return nil, fetched, err
		}
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fetched, err
		}
		al.logf("info", "trazendo %s do storage (não está no agente)", name)
		if _, err := netdest.Fetch(ctx, cfg, c.destEnv(), spec.Dest.Folder, name, p, shaOf[name]); err != nil {
			return nil, fetched, fmt.Errorf("não foi possível trazer %s do storage: %w", name, err)
		}
		fetched = append(fetched, p)
		out = append(out, p)
	}
	if len(missing) > 0 {
		if len(names) == 1 {
			return nil, fetched, errors.New("o arquivo de backup não existe mais neste agente (a retenção local pode tê-lo removido): baixe-o da nuvem e copie para a pasta do backup local")
		}
		return nil, fetched, fmt.Errorf("faltam no agente arquivos de backup: %s (baixe-os da nuvem e copie para a pasta do backup local, ou restaure um backup full)", strings.Join(missing, ", "))
	}
	return out, fetched, nil
}

// applyDeletions remove da restauração o que o incremental registrou como apagado na origem (sem sair de dest).
func applyDeletions(dest string, deleted []string) int {
	root, err := filepath.EvalSymlinks(dest)
	if err != nil {
		return 0
	}
	n := 0
	for _, d := range deleted {
		rel := strings.TrimPrefix(path.Clean("/"+d), "/")
		if rel == "" {
			continue
		}
		target := filepath.Join(root, filepath.FromSlash(rel))
		parent, err := filepath.EvalSymlinks(filepath.Dir(target))
		if err != nil || !within(root, parent) || !within(root, target) {
			continue
		}
		if os.RemoveAll(target) == nil {
			n++
		}
	}
	return n
}

// ExtractArchive extrai um .tar.gz com segurança: nenhum item sai de dest (nomes com ".." ou absolutos são neutralizados) e
// nenhum arquivo é gravado através de link simbólico que aponte para fora.
func ExtractArchive(ctx context.Context, archive, dest string, logf func(level, f string, a ...any)) (items int, bytes int64, err error) {
	items, bytes, _, err = extractOne(ctx, archive, dest, logf)
	return items, bytes, err
}

// extractOne extrai um arquivo e devolve o manifesto do incremental (que não é gravado na restauração), se houver.
func extractOne(ctx context.Context, archive, dest string, logf func(level, f string, a ...any)) (items int, bytes int64, man *Manifest, err error) {
	return extractSel(ctx, archive, dest, logf, nil)
}

// extractSel é extractOne só com os itens aceitos por keep (caminho relativo, sem "/" inicial); keep nil = tudo. Cada
// arquivo é gravado num temporário e renomeado no fim (nunca fica pela metade no lugar do original).
func extractSel(ctx context.Context, archive, dest string, logf func(level, f string, a ...any), keep func(rel string) bool) (items int, bytes int64, man *Manifest, err error) {
	f, err := os.Open(archive)
	if err != nil {
		return 0, 0, nil, err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return 0, 0, nil, err
	}
	if err := os.MkdirAll(dest, 0o750); err != nil {
		return 0, 0, nil, err
	}
	root, err := filepath.EvalSymlinks(dest)
	if err != nil {
		return 0, 0, nil, err
	}
	tr := tar.NewReader(gz)
	for {
		if ctx.Err() != nil {
			return items, bytes, man, ctx.Err()
		}
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return items, bytes, man, nil
		}
		if err != nil {
			return items, bytes, man, err
		}
		rel := strings.TrimPrefix(path.Clean("/"+h.Name), "/")
		if rel == "" {
			continue
		}
		if keep != nil && rel != manifestName && !keep(rel) {
			continue
		}
		if rel == manifestName && h.Typeflag == tar.TypeReg { // manifesto do incremental: lido, não gravado
			var m Manifest
			if json.NewDecoder(io.LimitReader(tr, 64<<20)).Decode(&m) == nil {
				man = &m
			}
			continue
		}
		target := filepath.Join(root, filepath.FromSlash(rel))
		if !within(root, target) {
			logf("warn", "item ignorado (fora da pasta de destino): %s", h.Name)
			continue
		}
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, os.FileMode(h.Mode)&0o777|0o700); err != nil {
				return items, bytes, man, err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
				return items, bytes, man, err
			}
			if real, err := filepath.EvalSymlinks(filepath.Dir(target)); err != nil || !within(root, real) {
				logf("warn", "item ignorado (pasta aponta para fora do destino): %s", h.Name)
				continue
			}
			tmp := target + ".coruja-restaurando"
			_ = os.Remove(tmp)
			out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_EXCL, os.FileMode(h.Mode)&0o777)
			if err != nil {
				return items, bytes, man, err
			}
			n, err := io.Copy(out, ctxRd(ctx, tr))
			cerr := out.Close()
			if err != nil || cerr != nil {
				_ = os.Remove(tmp)
				return items, bytes, man, errors.Join(err, cerr)
			}
			bytes += n
			_ = os.Chtimes(tmp, h.ModTime, h.ModTime)
			if os.Geteuid() == 0 {
				_ = os.Lchown(tmp, h.Uid, h.Gid) // como root, devolve o dono original
			}
			// rename troca o arquivo (ou link) que estiver no caminho, sem escrever através dele
			if err := os.Rename(tmp, target); err != nil {
				_ = os.Remove(tmp)
				return items, bytes, man, err
			}
		case tar.TypeSymlink:
			if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
				return items, bytes, man, err
			}
			_ = os.Remove(target)
			if err := os.Symlink(h.Linkname, target); err != nil {
				logf("warn", "link não criado (%s): %v", h.Name, err)
				continue
			}
		default:
			continue
		}
		items++
	}
}

func within(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
