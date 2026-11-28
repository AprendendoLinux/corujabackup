package agentcore

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
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
	if isDirBackup(act.FileName) { // backup em pasta: o catálogo também está dentro dela
		al.logf("info", "este backup não tem catálogo neste agente: lendo o que está dentro da pasta")
		if cat, err = c.catalogFromDir(ctx, act); err != nil {
			return nil, err
		}
		if err := c.st.SaveCatalog(act.JobId, act.FileName, cat); err != nil {
			al.logf("warn", "não foi possível guardar o catálogo: %v", err)
		}
		al.logf("info", "catálogo lido: %d itens", len(cat.Files))
		return cat, nil
	}
	al.logf("info", "este backup não tem catálogo neste agente: montando a partir do arquivo")
	rc, where, err := c.openArchive(ctx, act, act.FileName, 0, 0)
	if err != nil {
		return nil, err
	}
	h := sha256.New()
	cat, err = catalogFromReader(ctx, io.TeeReader(ctxRd(ctx, rc), h), act.FileName)
	if err == nil {
		_, err = io.Copy(h, ctxRd(ctx, rc)) // o resto (fim do tar): o arquivo inteiro passa pela conferência
	}
	rc.Close()
	if err != nil {
		return nil, err
	}
	if where != "agente" && act.Sha256 != "" && hex.EncodeToString(h.Sum(nil)) != act.Sha256 {
		return nil, fmt.Errorf("%s veio de %s diferente do backup original (SHA-256 não confere)", act.FileName, where)
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
	where := dest
	switch {
	case act.Original:
		where = "o local original (os arquivos existentes são substituídos)"
	case act.DestDir != "":
		where = c.shownPath(dest) + " (arquivos com o mesmo nome são substituídos)"
	}
	al.logf("info", "restaurando %d item(ns) de %d arquivo(s) de backup para %s", n, len(groups), where)
	fresh := !act.Original && act.DestDir == "" // pasta nova em restores/: apagada se a restauração falhar
	root, err := extractRoot(dest)
	if err != nil {
		return err
	}
	var items int
	var size int64
	err = c.eachSelected(ctx, act, al, cat, groups, chainShas(act), func(h *tar.Header, r io.Reader, rel string) error {
		if strip != "" { // grava a partir da pasta-mãe comum: home/ana/.ssh/x -> .ssh/x
			if !strings.HasPrefix(rel, strip+"/") {
				return nil
			}
			rel = rel[len(strip)+1:]
		}
		ok, n, err := writeEntry(ctx, root, h, r, rel, al.logf)
		if ok {
			items++
			size += n
		}
		return err
	})
	if err != nil {
		if fresh {
			_ = os.RemoveAll(dest)
		}
		return err
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

// chainShas devolve o SHA-256 conhecido de cada arquivo da cadeia (para conferir o que vier de fora do agente).
func chainShas(act *pb.AgentAction) map[string]string {
	shaOf := map[string]string{}
	for i, nm := range act.Chain {
		if i < len(act.ChainSha256) {
			shaOf[nm] = act.ChainSha256[i]
		}
	}
	if act.Sha256 != "" {
		shaOf[act.FileName] = act.Sha256
	}
	return shaOf
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
