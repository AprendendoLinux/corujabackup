package agentcore

// Navegação pelas pastas do agente, para a interface web oferecer "clicar e selecionar" em vez de digitar caminhos.
// Só lista nomes (nunca conteúdo), não segue links simbólicos e, na origem dos backups, respeita CORUJA_ALLOWED_PATHS.

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"
)

const (
	// BrowseHost é a origem dos backups (o servidor do agente); BrowseBackups é a pasta de backups do agente.
	BrowseHost    = "host"
	BrowseBackups = "backups"

	maxBrowseEntries = 3000
)

// BrowseRequest é o pedido do Server ("fs_list").
type BrowseRequest struct {
	Area string `json:"area"`
	Path string `json:"path"`
}

// BrowseEntry é um item de uma pasta.
type BrowseEntry struct {
	Name string `json:"name"`
	Dir  bool   `json:"dir"`
	Link bool   `json:"link,omitempty"`
	Size int64  `json:"size,omitempty"`
}

// BrowseResult é a listagem de uma pasta (pastas primeiro, em ordem alfabética).
type BrowseResult struct {
	Path      string        `json:"path"`
	Entries   []BrowseEntry `json:"entries"`
	Truncated bool          `json:"truncated,omitempty"`
}

// pastas que o job pode usar como destino local (mesma regra que o Server aplica ao salvar o job)
var subdirNameRE = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// leadsToAllowed informa se p é permitido ou é caminho até algo permitido.
func leadsToAllowed(allowed []string, p string) bool {
	if PathAllowed(allowed, p) {
		return true
	}
	for _, a := range allowed {
		a = path.Clean("/" + strings.TrimPrefix(strings.TrimSpace(a), "/"))
		if p == "/" || strings.HasPrefix(a, p+"/") {
			return true
		}
	}
	return false
}

// Browse lista uma pasta. base é a raiz real no disco do agente; os caminhos do pedido são relativos a ela.
func Browse(base string, req BrowseRequest, allowed []string) (BrowseResult, error) {
	logical := path.Clean("/" + strings.TrimSpace(req.Path))
	res := BrowseResult{Path: logical, Entries: []BrowseEntry{}}
	if strings.ContainsRune(logical, 0) {
		return res, errors.New("caminho inválido")
	}
	if req.Area != BrowseHost {
		allowed = nil
	}
	if !leadsToAllowed(allowed, logical) {
		return res, fmt.Errorf("o caminho %q não é permitido por este agente (CORUJA_ALLOWED_PATHS)", logical)
	}
	root, err := os.OpenRoot(base) // nada abaixo daqui consegue sair da raiz
	if err != nil {
		return res, fmt.Errorf("não foi possível abrir a pasta base do agente: %w", err)
	}
	defer root.Close()
	rel := "."
	if logical != "/" {
		rel = strings.TrimPrefix(logical, "/")
		// nenhum trecho pode ser link: um atalho dentro de uma pasta permitida não pode revelar o que está fora dela
		cur := ""
		for _, seg := range strings.Split(rel, "/") {
			cur = path.Join(cur, seg)
			st, err := root.Lstat(cur)
			if errors.Is(err, fs.ErrNotExist) {
				return res, fmt.Errorf("a pasta %s não existe no agente", logical)
			}
			if err != nil {
				return res, fmt.Errorf("sem acesso a %s", logical)
			}
			if !st.IsDir() {
				return res, fmt.Errorf("%s não é uma pasta", logical)
			}
		}
	}
	d, err := root.Open(rel)
	if err != nil {
		return res, fmt.Errorf("sem acesso a %s", logical)
	}
	defer d.Close()
	items, err := d.ReadDir(-1)
	if err != nil {
		return res, fmt.Errorf("não foi possível listar %s", logical)
	}
	for _, it := range items {
		full := path.Join(logical, it.Name())
		if !leadsToAllowed(allowed, full) {
			continue
		}
		e := BrowseEntry{Name: it.Name(), Dir: it.IsDir(), Link: it.Type()&fs.ModeSymlink != 0}
		if req.Area == BrowseBackups {
			// aqui só interessam pastas que podem virar destino de um job
			if !e.Dir || !subdirNameRE.MatchString(e.Name) || strings.HasPrefix(e.Name, ".") || (logical == "/" && e.Name == "restores") {
				continue
			}
		}
		if it.Type().IsRegular() {
			if info, err := it.Info(); err == nil {
				e.Size = info.Size()
			}
		}
		res.Entries = append(res.Entries, e)
	}
	sort.Slice(res.Entries, func(i, j int) bool {
		a, b := res.Entries[i], res.Entries[j]
		if a.Dir != b.Dir {
			return a.Dir
		}
		return strings.ToLower(a.Name) < strings.ToLower(b.Name)
	})
	if len(res.Entries) > maxBrowseEntries {
		res.Entries, res.Truncated = res.Entries[:maxBrowseEntries], true
	}
	return res, nil
}
