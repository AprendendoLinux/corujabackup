package agentcore

import (
	"archive/tar"
	"compress/gzip"
	"context"
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

	"github.com/AprendendoLinux/corujabackup/internal/pb"
)

var (
	backupFileRE = regexp.MustCompile(`^[a-z0-9-]+-\d{8}-\d{6}\.tar\.gz$`)
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
	key := act.Kind + ":" + act.RunUid
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
	dest, err = SafeSubdir(c.cfg.BackupDir, act.Subdir)
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
	_, file, err := c.backupPath(act)
	if err != nil {
		return err
	}
	dest := filepath.Join(c.cfg.BackupDir, "restores", act.Target)
	if _, err := os.Lstat(dest); err == nil {
		return fmt.Errorf("a pasta %s já existe; escolha outro nome", dest)
	}
	al.logf("info", "extraindo %s para %s", act.FileName, dest)
	n, size, err := ExtractArchive(ctx, file, dest, al.logf)
	if err != nil {
		_ = os.RemoveAll(dest)
		return err
	}
	al.logf("info", "extração concluída: %d itens, %s, em %s", n, HumanBytes(size), dest)
	al.flush("ok", "extraído", func(m *pb.ActionLog) { m.Path = dest })
	return nil
}

// ExtractArchive extrai um .tar.gz com segurança: nenhum item sai de dest (nomes com ".." ou absolutos são neutralizados) e
// nenhum arquivo é gravado através de link simbólico que aponte para fora.
func ExtractArchive(ctx context.Context, archive, dest string, logf func(level, f string, a ...any)) (items int, bytes int64, err error) {
	f, err := os.Open(archive)
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return 0, 0, err
	}
	if err := os.MkdirAll(dest, 0o750); err != nil {
		return 0, 0, err
	}
	root, err := filepath.EvalSymlinks(dest)
	if err != nil {
		return 0, 0, err
	}
	tr := tar.NewReader(gz)
	for {
		if ctx.Err() != nil {
			return items, bytes, ctx.Err()
		}
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return items, bytes, nil
		}
		if err != nil {
			return items, bytes, err
		}
		rel := strings.TrimPrefix(path.Clean("/"+h.Name), "/")
		if rel == "" {
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
				return items, bytes, err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
				return items, bytes, err
			}
			if real, err := filepath.EvalSymlinks(filepath.Dir(target)); err != nil || !within(root, real) {
				logf("warn", "item ignorado (pasta aponta para fora do destino): %s", h.Name)
				continue
			}
			_ = os.Remove(target) // nunca escreve através de um link que já exista
			out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_EXCL, os.FileMode(h.Mode)&0o777)
			if err != nil {
				return items, bytes, err
			}
			n, err := io.Copy(out, tr)
			cerr := out.Close()
			if err != nil || cerr != nil {
				return items, bytes, errors.Join(err, cerr)
			}
			bytes += n
			_ = os.Chtimes(target, h.ModTime, h.ModTime)
		case tar.TypeSymlink:
			if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
				return items, bytes, err
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
