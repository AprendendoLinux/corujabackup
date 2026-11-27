package agentcore

// Catálogo de cada backup de arquivos: o estado de cada item naquele momento (tamanho, data, tipo) e em qual .tar.gz
// da cadeia está o conteúdo. Fica em <dados do agente>/catalogs/<job>/<arquivo>.json.gz (permissão 600: lista nomes de
// arquivos do servidor) e permite navegar num backup, ver as versões de um arquivo e restaurar só o que for escolhido,
// extraindo cada item do pacote certo. Backups sem catálogo (antigos, ou de antes de reinstalar o agente) ganham um a
// partir do próprio .tar.gz, sob demanda.

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/AprendendoLinux/corujabackup/internal/bkname"
)

// maxCatalogs é quantos catálogos o agente guarda por job (os mais novos).
const maxCatalogs = 120

// ErrNoCatalog indica que o backup não tem catálogo neste agente (pode ser gerado a partir do arquivo).
var ErrNoCatalog = errors.New("sem catálogo")

func (s *State) catalogDir(jobID int64) string {
	return filepath.Join(s.dir, "catalogs", strconv.FormatInt(jobID, 10))
}

func (s *State) catalogPath(jobID int64, archive string) (string, error) {
	if !bkname.Any.MatchString(archive) {
		return "", errors.New("nome de backup inválido")
	}
	return filepath.Join(s.catalogDir(jobID), archive+".json.gz"), nil
}

// SaveCatalog grava o catálogo de um backup e apaga os mais velhos além do limite.
func (s *State) SaveCatalog(jobID int64, archive string, cat *Snapshot) error {
	p, err := s.catalogPath(jobID, archive)
	if err != nil {
		return err
	}
	if err := writeSnapshotFile(p, cat); err != nil {
		return err
	}
	catCache.forget(p)
	s.pruneCatalogs(jobID)
	return nil
}

func (s *State) pruneCatalogs(jobID int64) {
	ents, err := os.ReadDir(s.catalogDir(jobID))
	if err != nil {
		return
	}
	var names []string
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), ".json.gz") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names) // o carimbo no nome ordena cronologicamente
	for len(names) > maxCatalogs {
		_ = os.Remove(filepath.Join(s.catalogDir(jobID), names[0]))
		names = names[1:]
	}
}

// catCache guarda os últimos catálogos lidos (navegar abre a mesma pasta várias vezes).
var catCache = &catalogCache{items: map[string]*Snapshot{}}

type catalogCache struct {
	mu    sync.Mutex
	items map[string]*Snapshot
	order []string
}

func (c *catalogCache) get(p string) *Snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.items[p]
}

func (c *catalogCache) put(p string, s *Snapshot) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.items[p]; !ok {
		c.order = append(c.order, p)
	}
	c.items[p] = s
	for len(c.order) > 3 {
		delete(c.items, c.order[0])
		c.order = c.order[1:]
	}
}

func (c *catalogCache) forget(p string) {
	c.mu.Lock()
	delete(c.items, p)
	c.mu.Unlock()
}

// LoadCatalog lê o catálogo de um backup (ErrNoCatalog se não houver).
func (s *State) LoadCatalog(jobID int64, archive string) (*Snapshot, error) {
	p, err := s.catalogPath(jobID, archive)
	if err != nil {
		return nil, err
	}
	if c := catCache.get(p); c != nil {
		return c, nil
	}
	snap, err := readSnapshotFile(p)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNoCatalog
	}
	if err != nil {
		return nil, err
	}
	catCache.put(p, snap)
	return snap, nil
}

// CatalogEntry é um item de uma pasta do backup.
type CatalogEntry struct {
	Name     string `json:"name"`
	Dir      bool   `json:"dir"`
	Link     bool   `json:"link,omitempty"`
	Size     int64  `json:"size"`
	Modified int64  `json:"modified"` // unix
	Here     bool   `json:"here"`     // o conteúdo está neste .tar.gz (num incremental: mudou neste backup)
	Files    int    `json:"files,omitempty"`
}

// CatalogDir lista uma pasta do backup. dir "" ou "/" é a raiz (as origens do job).
func CatalogDir(cat *Snapshot, dir string) []CatalogEntry {
	dir = path.Clean("/" + dir)
	prefix := dir + "/"
	if dir == "/" {
		prefix = "/"
	}
	here := len(cat.Archives) - 1
	children := map[string]*CatalogEntry{}
	for p, m := range cat.Files {
		if !strings.HasPrefix(p, prefix) || p == dir {
			continue
		}
		rest := strings.TrimPrefix(p, prefix)
		name, sub, deeper := strings.Cut(rest, "/")
		e := children[name]
		if e == nil {
			e = &CatalogEntry{Name: name}
			children[name] = e
		}
		if deeper && sub != "" {
			e.Dir = true
			if !fs.FileMode(m.Mode).IsDir() {
				e.Files++
				e.Size += m.Size
				if m.A == here {
					e.Here = true
				}
			}
			continue
		}
		mode := fs.FileMode(m.Mode)
		e.Dir = e.Dir || mode.IsDir()
		e.Link = mode&fs.ModeSymlink != 0
		e.Modified = time.Unix(0, m.MTime).Unix()
		if !mode.IsDir() {
			e.Size, e.Here = m.Size, m.A == here
		}
	}
	out := make([]CatalogEntry, 0, len(children))
	for _, e := range children {
		out = append(out, *e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Dir != out[j].Dir {
			return out[i].Dir
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out
}

// Version é uma versão de um arquivo ao longo dos backups do job.
type Version struct {
	Archive  string `json:"archive"` // backup (o mais antigo em que esta versão aparece... até o próximo)
	Size     int64  `json:"size"`
	Modified int64  `json:"modified"` // unix
	Backups  int    `json:"backups"`  // em quantos backups esta versão aparece
	Latest   string `json:"latest"`   // backup mais novo com esta versão
}

// Versions percorre os catálogos do job (do mais novo para o mais antigo) e devolve as versões distintas de um
// arquivo, a mais nova primeiro.
func (s *State) Versions(jobID int64, file string) ([]Version, error) {
	file = path.Clean("/" + file)
	ents, err := os.ReadDir(s.catalogDir(jobID))
	if err != nil {
		return []Version{}, nil
	}
	var names []string
	for _, e := range ents {
		if n := strings.TrimSuffix(e.Name(), ".json.gz"); n != e.Name() {
			names = append(names, n)
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	out := []Version{}
	var last *fileMeta
	for _, n := range names {
		cat, err := s.LoadCatalog(jobID, n)
		if err != nil {
			continue
		}
		m, ok := cat.Files[file]
		if !ok || fs.FileMode(m.Mode).IsDir() {
			last = nil
			continue
		}
		if last != nil && sameVersion(*last, m) {
			v := &out[len(out)-1]
			v.Archive, v.Backups = n, v.Backups+1
			continue
		}
		mm := m
		last = &mm
		out = append(out, Version{Archive: n, Size: m.Size, Modified: time.Unix(0, m.MTime).Unix(), Backups: 1, Latest: n})
	}
	return out, nil
}

// selectFiles expande os caminhos escolhidos (pastas incluem tudo o que há dentro) e agrupa por .tar.gz da cadeia.
func selectFiles(cat *Snapshot, paths []string) (map[int]map[string]bool, int) {
	groups := map[int]map[string]bool{}
	n := 0
	for p, m := range cat.Files {
		for _, want := range paths {
			want = path.Clean("/" + want)
			if p == want || want == "/" || strings.HasPrefix(p, want+"/") {
				if groups[m.A] == nil {
					groups[m.A] = map[string]bool{}
				}
				groups[m.A][strings.TrimPrefix(p, "/")] = true
				n++
				break
			}
		}
	}
	return groups, n
}

// catalogFromArchive monta o catálogo de um backup lendo o próprio .tar.gz (para backups sem catálogo). Num incremental
// sem catálogo, só o que mudou nele aparece.
func catalogFromArchive(ctx context.Context, file, name string) (*Snapshot, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(gz)
	cat := &Snapshot{Version: snapshotVersion, Chain: name, Last: name, Archives: []string{name}, Files: map[string]fileMeta{}}
	for {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return cat, nil
		}
		if err != nil {
			return nil, fmt.Errorf("arquivo ilegível: %w", err)
		}
		rel := strings.TrimPrefix(path.Clean("/"+h.Name), "/")
		if rel == "" || rel == manifestName {
			continue
		}
		cat.Files["/"+rel] = fileMeta{Size: h.Size, MTime: h.ModTime.UnixNano(), Mode: uint32(h.FileInfo().Mode())}
	}
}

func writeSnapshotFile(p string, snap *Snapshot) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	tmp := p + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	gz := gzip.NewWriter(f)
	err = jsonEncode(gz, snap)
	if cerr := gz.Close(); err == nil {
		err = cerr
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, p)
}

func readSnapshotFile(p string) (*Snapshot, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, err
	}
	var snap Snapshot
	if err := jsonDecode(gz, &snap); err != nil || snap.Version != snapshotVersion || snap.Files == nil {
		return nil, errors.New("catálogo ilegível ou de formato antigo")
	}
	return &snap, nil
}
