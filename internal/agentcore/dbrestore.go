package agentcore

// Restauração de bancos pela interface (0.0.24): o agente traz o backup (do agente ou do storage de rede), separa o
// dump de cada banco escolhido e o restaura no mesmo servidor do job — num banco novo (padrão) ou sobrescrevendo.

import (
	"archive/tar"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/AprendendoLinux/corujabackup/internal/dbdump"
	"github.com/AprendendoLinux/corujabackup/internal/pb"
)

// DBContents é o que um backup de banco contém.
type DBContents struct {
	Engine  string            `json:"engine"`
	Version string            `json:"version"`
	Files   []dbdump.DumpFile `json:"files"`
}

func (s *State) dbContentsPath(jobID int64, archive string) (string, error) {
	p, err := s.catalogPath(jobID, archive)
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(p, ".json.gz") + ".dbs.json", nil
}

// SaveDBContents guarda a lista de bancos de um backup.
func (s *State) SaveDBContents(jobID int64, archive string, d *DBContents) error {
	p, err := s.dbContentsPath(jobID, archive)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	b, err := json.Marshal(d)
	if err != nil {
		return err
	}
	if err := writeAtomic(p, b); err != nil {
		return err
	}
	s.pruneCatalogs(jobID)
	return nil
}

func (s *State) loadDBContents(jobID int64, archive string) (*DBContents, error) {
	p, err := s.dbContentsPath(jobID, archive)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	var d DBContents
	if err := json.Unmarshal(b, &d); err != nil {
		return nil, err
	}
	return &d, nil
}

// dbContents devolve o que o backup contém: o guardado no agente ou, para backups antigos, o manifest.json do arquivo.
func (c *Core) dbContents(ctx context.Context, act *pb.AgentAction, al *actionLog) (*DBContents, error) {
	if c.st != nil {
		if d, err := c.st.loadDBContents(act.JobId, act.FileName); err == nil {
			return d, nil
		}
	}
	files, fetched, err := c.obtain(ctx, act, al, []string{act.FileName}, map[string]string{act.FileName: act.Sha256})
	defer func() {
		for _, f := range fetched {
			_ = os.Remove(f)
		}
	}()
	if err != nil {
		return nil, err
	}
	var d *DBContents
	err = eachMember(ctx, files[0], func(rel string) bool { return path.Base(rel) == "manifest.json" }, func(_ *tar.Header, r io.Reader, _ string) error {
		var m dbdump.Manifest
		if err := json.NewDecoder(io.LimitReader(r, 16<<20)).Decode(&m); err != nil {
			return err
		}
		d = &DBContents{Engine: m.Engine, Version: m.Version, Files: m.Databases}
		return nil
	})
	if err == nil && d == nil {
		err = errors.New("o arquivo não tem manifest.json (não parece um backup de banco do Coruja)")
	}
	if err != nil {
		return nil, err
	}
	if c.st != nil {
		_ = c.st.SaveDBContents(act.JobId, act.FileName, d)
	}
	return d, nil
}

// restoreDB restaura os bancos escolhidos (act.Paths = bancos do backup; act.Targets = nomes de destino; act.Original =
// sobrescrever se existir).
func (c *Core) restoreDB(ctx context.Context, act *pb.AgentAction, al *actionLog) error {
	if len(act.Paths) == 0 || len(act.Paths) != len(act.Targets) {
		return errors.New("escolha os bancos a restaurar")
	}
	c.mu.Lock()
	spec := c.jobs[act.JobId]
	c.mu.Unlock()
	if spec == nil || spec.Db == nil {
		return errors.New("job de banco desconhecido neste agente (aguarde a sincronização)")
	}
	cfg := DBConfigFrom(spec.Db)
	if cfg.Mode == "docker" && !ContainerAllowed(c.cfg.AllowedContainers, cfg.Container) {
		return fmt.Errorf("este agente não tem permissão para acessar o contêiner %q (CORUJA_ALLOWED_CONTAINERS)", cfg.Container)
	}
	contents, err := c.dbContents(ctx, act, al)
	if err != nil {
		return err
	}
	byDB := map[string]dbdump.DumpFile{}
	for _, f := range contents.Files {
		if f.DB != "" {
			byDB[f.DB] = f
		}
	}
	want := map[string]bool{}
	for i, src := range act.Paths {
		if _, ok := byDB[src]; !ok {
			return fmt.Errorf("o banco %q não está neste backup", src)
		}
		if !dbdump.ValidName(act.Targets[i]) {
			return fmt.Errorf("nome de banco inválido: %q", act.Targets[i])
		}
		want[byDB[src].Name] = true
	}
	files, fetched, err := c.obtain(ctx, act, al, []string{act.FileName}, map[string]string{act.FileName: act.Sha256})
	defer func() {
		for _, f := range fetched {
			_ = os.Remove(f)
		}
	}()
	if err != nil {
		return err
	}
	work, err := os.MkdirTemp(c.cfg.BackupDir, ".tmp-restaurar-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work) // dumps com dados: não ficam no agente
	al.logf("info", "separando o dump de %d banco(s) do backup", len(act.Paths))
	got := map[string]string{}
	err = eachMember(ctx, files[0], func(rel string) bool { return want[path.Base(rel)] }, func(h *tar.Header, r io.Reader, rel string) error {
		if h.Typeflag != tar.TypeReg {
			return nil
		}
		p := filepath.Join(work, path.Base(rel))
		f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		if err != nil {
			return err
		}
		_, err = io.Copy(f, r)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		got[path.Base(rel)] = p
		return err
	})
	if err != nil {
		return err
	}
	runner, err := dbdump.ResolveRunner(ctx, &cfg, c.docker)
	if err != nil {
		return err
	}
	hooks := dbdump.Hooks{Logf: al.logf}
	ok := 0
	for i, src := range act.Paths {
		f := byDB[src]
		f.Path = got[f.Name]
		if f.Path == "" {
			return fmt.Errorf("o dump de %s não foi encontrado no arquivo", src)
		}
		tgt := act.Targets[i]
		n, err := dbdump.RestoreInto(ctx, runner, cfg, f, tgt, act.Original, hooks)
		if err != nil {
			return fmt.Errorf("banco %s: %w", src, err)
		}
		switch {
		case n < 0:
			al.logf("info", "banco %s restaurado em %s", src, tgt)
		case n == f.Tables:
			al.logf("info", "banco %s restaurado em %s: %d tabela(s)/coleção(ões), igual ao backup", src, tgt, n)
		default:
			al.logf("warn", "banco %s restaurado em %s com %d tabela(s)/coleção(ões); o backup tinha %d", src, tgt, n, f.Tables)
		}
		ok++
	}
	al.logf("info", "restauração de bancos concluída: %d banco(s)", ok)
	al.flush("ok", fmt.Sprintf("%d banco(s) restaurado(s)", ok), nil)
	return nil
}
