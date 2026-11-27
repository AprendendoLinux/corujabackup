package agentcore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"

	"github.com/AprendendoLinux/corujabackup/internal/netdest"
	"github.com/AprendendoLinux/corujabackup/internal/pb"
)

func (c *Core) destEnv() netdest.Env {
	return netdest.Env{LocalRoot: c.cfg.DestDir, HostDir: c.localDir}
}

func destConfig(d *pb.DestSpec) (netdest.Config, error) {
	var cfg netdest.Config
	if d == nil {
		return cfg, errors.New("este job não tem destino de rede configurado neste agente (aguarde a sincronização)")
	}
	if err := json.Unmarshal(d.Config, &cfg); err != nil {
		return cfg, errors.New("configuração do destino ilegível")
	}
	return cfg, nil
}

// sendToDest envia o backup a um destino de rede e devolve o resultado já conferido.
func (c *Core) sendToDest(ctx context.Context, spec *pb.JobSpec, res Result, h Hooks, progress func(pct int)) (CloudInfo, error) {
	var info CloudInfo
	cfg, err := destConfig(spec.Dest)
	if err != nil {
		return info, err
	}
	h.log("info", "conectando ao destino %s (%s)", cfg.Type, cfg.Label())
	r, err := netdest.Send(ctx, cfg, c.destEnv(), netdest.Job{Folder: spec.Dest.Folder, LocalPath: res.OutputPath, Name: res.OutputName, SHA256: res.SHA256,
		Prefix: res.Prefix, Keep: int(spec.Dest.Keep), KeepDays: int(spec.Dest.KeepDays), Now: c.now().UTC()},
		func(sent, total int64) {
			if total > 0 {
				progress(int(sent * 100 / total))
			}
		})
	if err != nil {
		return info, err
	}
	return CloudInfo{Path: r.Path, Hash: r.Hash, VerifiedBy: r.VerifiedBy, Retention: r.Retention}, nil
}

// fetch traz um backup do destino de rede de volta para a pasta de backups do agente (para extrair ou copiar).
func (c *Core) fetch(ctx context.Context, act *pb.AgentAction, al *actionLog) error {
	c.mu.Lock()
	spec := c.jobs[act.JobId]
	c.mu.Unlock()
	if spec == nil {
		return errors.New("job desconhecido neste agente (aguarde a sincronização)")
	}
	cfg, err := destConfig(spec.Dest)
	if err != nil {
		return err
	}
	if !backupFileRE.MatchString(act.FileName) {
		return errors.New("nome de arquivo de backup inválido")
	}
	dir, err := c.localDir(act.Subdir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	local := filepath.Join(dir, act.FileName)
	if _, err := os.Stat(local); err == nil {
		al.flush("ok", "o arquivo já está neste agente", func(m *pb.ActionLog) { m.Path = c.shownPath(local) })
		return nil
	}
	al.logf("info", "trazendo %s do destino %s", path.Join(spec.Dest.Folder, act.FileName), cfg.Label())
	n, err := netdest.Fetch(ctx, cfg, c.destEnv(), spec.Dest.Folder, act.FileName, local, act.Sha256)
	if err != nil {
		return err
	}
	msg := fmt.Sprintf("%s trazido e conferido (%s)", act.FileName, HumanBytes(n))
	al.logf("info", "%s", msg)
	al.flush("ok", msg, func(m *pb.ActionLog) { m.Path = c.shownPath(local) })
	return nil
}
