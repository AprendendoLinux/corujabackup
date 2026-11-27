package agentcore

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/AprendendoLinux/corujabackup/internal/pb"
)

// catalogFor devolve o catálogo do backup ou, se não houver, monta um a partir do próprio .tar.gz (trazendo-o do
// storage se preciso) e o guarda.
func (c *Core) catalogFor(ctx context.Context, act *pb.AgentAction, al *actionLog) (*Snapshot, error) {
	if c.st == nil {
		return nil, errors.New("agente sem pasta de dados")
	}
	cat, err := c.st.LoadCatalog(act.JobId, act.FileName)
	if err == nil {
		return cat, nil
	}
	if !errors.Is(err, ErrNoCatalog) {
		return nil, err
	}
	al.logf("info", "este backup não tem catálogo neste agente: montando a partir do arquivo")
	files, fetched, err := c.obtain(ctx, act, al, []string{act.FileName}, map[string]string{act.FileName: act.Sha256})
	defer func() {
		for _, f := range fetched {
			_ = os.Remove(f)
		}
	}()
	if err != nil {
		return nil, err
	}
	cat, err = catalogFromArchive(ctx, files[0], act.FileName)
	if err != nil {
		return nil, err
	}
	if err := c.st.SaveCatalog(act.JobId, act.FileName, cat); err != nil {
		al.logf("warn", "não foi possível guardar o catálogo: %v", err)
	}
	if strings.Contains(act.FileName, "-inc.") {
		al.logf("warn", "incremental sem catálogo: o índice mostra só o que mudou neste backup")
	}
	al.logf("info", "catálogo montado: %d itens", len(cat.Files))
	return cat, nil
}

// index monta (ou confirma) o catálogo de um backup.
func (c *Core) index(ctx context.Context, act *pb.AgentAction, al *actionLog) error {
	cat, err := c.catalogFor(ctx, act, al)
	if err != nil {
		return err
	}
	al.flush("ok", fmt.Sprintf("catálogo com %d itens", len(cat.Files)), nil)
	return nil
}

// restoreFiles restaura só os caminhos escolhidos: numa pasta nova em <backups>/restores/<target>, numa pasta escolhida do
// servidor (act.DestDir, a partir da pasta-mãe comum dos itens) ou no local original (sobrescrevendo). Cada item é extraído do .tar.gz da cadeia que guarda a versão daquele backup.
func (c *Core) restoreFiles(ctx context.Context, act *pb.AgentAction, al *actionLog) error {
	if len(act.Paths) == 0 || len(act.Paths) > 5000 {
		return errors.New("escolha o que restaurar (até 5000 itens)")
	}
	for _, p := range act.Paths {
		if !strings.HasPrefix(p, "/") || strings.ContainsRune(p, 0) || len(p) > 4096 {
			return fmt.Errorf("caminho inválido: %q", p)
		}
	}
	var dest, strip string
	switch {
	case act.DestDir != "" && act.Original:
		return errors.New("escolha um destino só: a pasta do servidor ou o local original")
	case act.DestDir != "":
		if !strings.HasPrefix(act.DestDir, "/") {
			return errors.New("pasta de destino inválida: escolha uma pasta do servidor")
		}
		d, err := c.localDir(act.DestDir)
		if err != nil {
			return err
		}
		dest, strip = d, commonParent(act.Paths)
	case act.Original:
		root, err := c.writeRoot()
		if err != nil {
			return errors.New("este agente não grava fora das próprias pastas (falta o volume /:/host-rw): restaure numa pasta de restauração")
		}
		for _, p := range act.Paths {
			if p != "/" {
				if _, err := CheckLocalDir(p); err != nil {
					return err
				}
			}
		}
		dest = root
	default:
		if !targetRE.MatchString(act.Target) || strings.Contains(act.Target, "..") {
			return errors.New("nome de pasta de destino inválido")
		}
		dest = filepath.Join(c.cfg.BackupDir, "restores", act.Target)
		if _, err := os.Lstat(dest); err == nil {
			return fmt.Errorf("a pasta %s já existe; escolha outro nome", dest)
		}
	}
	cat, err := c.catalogFor(ctx, act, al)
	if err != nil {
		return err
	}
	groups, n := selectFiles(cat, act.Paths)
	if n == 0 {
		return errors.New("nenhum dos itens escolhidos está neste backup")
	}
	idx := make([]int, 0, len(groups))
	for i := range groups {
		if i < 0 || i >= len(cat.Archives) {
			return errors.New("catálogo inconsistente: aponta para um backup fora da cadeia")
		}
		idx = append(idx, i)
	}
	sort.Ints(idx)
	names := make([]string, len(idx))
	for k, i := range idx {
		names[k] = cat.Archives[i]
	}
	shaOf := map[string]string{}
	for i, nm := range act.Chain {
		if i < len(act.ChainSha256) {
			shaOf[nm] = act.ChainSha256[i]
		}
	}
	if act.Sha256 != "" {
		shaOf[act.FileName] = act.Sha256
	}
	where := dest
	switch {
	case act.Original:
		where = "o local original (os arquivos existentes são substituídos)"
	case act.DestDir != "":
		where = c.shownPath(dest) + " (arquivos com o mesmo nome são substituídos)"
	}
	al.logf("info", "restaurando %d item(ns) de %d arquivo(s) de backup para %s", n, len(names), where)
	files, fetched, err := c.obtain(ctx, act, al, names, shaOf)
	defer func() {
		for _, f := range fetched {
			_ = os.Remove(f)
		}
	}()
	if err != nil {
		return err
	}
	fresh := !act.Original && act.DestDir == "" // pasta nova em restores/: apagada se a restauração falhar
	if !act.Original {
		if err := os.MkdirAll(dest, 0o750); err != nil {
			return err
		}
	}
	var items int
	var size int64
	for k, i := range idx {
		want := groups[i]
		k1, sz, _, err := extractSel(ctx, files[k], dest, strip, al.logf, func(rel string) bool { return want[rel] })
		if err != nil {
			if fresh {
				_ = os.RemoveAll(dest)
			}
			return err
		}
		items, size = items+k1, size+sz
	}
	shown := dest
	switch {
	case act.Original:
		shown = "local original"
	case act.DestDir != "":
		shown = c.shownPath(dest)
	}
	al.logf("info", "restauração concluída: %d item(ns), %s, em %s", items, HumanBytes(size), shown)
	al.flush("ok", fmt.Sprintf("%d item(ns) restaurado(s)", items), func(m *pb.ActionLog) {
		if !act.Original {
			m.Path = shown
		}
	})
	return nil
}

// catalogDirRPC lista uma pasta de um backup (para a navegação na interface).
func (c *Core) catalogDirRPC(jobID int64, archive, dir string) (map[string]any, error) {
	if c.st == nil {
		return nil, errors.New("agente sem pasta de dados")
	}
	cat, err := c.st.LoadCatalog(jobID, archive)
	if err != nil {
		return nil, err
	}
	dir = path.Clean("/" + dir)
	entries := CatalogDir(cat, dir)
	files := 0
	for p, m := range cat.Files {
		if dir == "/" || strings.HasPrefix(p, dir+"/") {
			if !os.FileMode(m.Mode).IsDir() {
				files++
			}
		}
	}
	return map[string]any{"dir": dir, "entries": entries, "files": files, "incremental": len(cat.Archives) > 1, "chain": len(cat.Archives)}, nil
}

// commonParent devolve a pasta-mãe comum dos caminhos escolhidos, sem "/" inicial ("" = a raiz): restaurar
// /home/ana/.ssh e /home/ana/.vim numa pasta X dá X/.ssh e X/.vim.
func commonParent(paths []string) string {
	var common []string
	for i, p := range paths {
		parent := strings.Split(strings.Trim(path.Dir(path.Clean("/"+p)), "/"), "/")
		if parent[0] == "" {
			return ""
		}
		if i == 0 {
			common = parent
			continue
		}
		n := 0
		for n < len(common) && n < len(parent) && common[n] == parent[n] {
			n++
		}
		common = common[:n]
		if n == 0 {
			return ""
		}
	}
	return strings.Join(common, "/")
}
