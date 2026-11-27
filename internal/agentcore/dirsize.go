package agentcore

// Tamanho de pastas para o seletor do que salvar (0.0.25). O tamanho de uma pasta não existe pronto no disco: é
// preciso percorrer tudo o que há dentro dela, como o du. Por isso o cálculo tem limite de tempo, roda com prioridade
// de disco baixa, não atravessa para outras partições e guarda o resultado por alguns minutos.

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"strings"
	"sync"
	"syscall"
	"time"
)

// SizeRequest é o pedido "fs_size".
type SizeRequest struct {
	Path string `json:"path"`
}

// SizeResult é o tamanho (aparente, como o tar lê) de uma pasta da máquina.
type SizeResult struct {
	Path     string `json:"path"`
	Bytes    int64  `json:"bytes"`
	Files    int64  `json:"files"`
	Complete bool   `json:"complete"`          // false: o limite de tempo acabou antes; Bytes é o mínimo
	Skipped  int    `json:"skipped,omitempty"` // partições montadas dentro dela, que não entram na conta
}

const (
	sizeLimit = 20 * time.Second
	sizeTTL   = 5 * time.Minute
)

type sizeEntry struct {
	at  time.Time
	res SizeResult
}

var sizer = struct {
	sem   chan struct{}
	mu    sync.Mutex
	cache map[string]sizeEntry
}{sem: make(chan struct{}, 2), cache: map[string]sizeEntry{}}

// skipTree são áreas virtuais do Linux: não são arquivos de verdade e nunca entram num backup útil.
var skipTree = map[string]bool{"/proc": true, "/sys": true, "/dev": true}

// DirSize soma o tamanho de uma pasta da máquina (base = raiz do host no agente).
func DirSize(ctx context.Context, base string, req SizeRequest, allowed []string) (SizeResult, error) {
	logical := path.Clean("/" + strings.TrimSpace(req.Path))
	if strings.ContainsRune(logical, 0) {
		return SizeResult{}, errors.New("caminho inválido")
	}
	if !leadsToAllowed(allowed, logical) {
		return SizeResult{}, fmt.Errorf("o caminho %q não é permitido por este agente (CORUJA_ALLOWED_PATHS)", logical)
	}
	key := base + "\x00" + logical
	sizer.mu.Lock()
	if e, ok := sizer.cache[key]; ok && time.Since(e.at) < sizeTTL {
		sizer.mu.Unlock()
		return e.res, nil
	}
	sizer.mu.Unlock()
	select { // no máximo dois cálculos ao mesmo tempo: o resto espera a vez
	case sizer.sem <- struct{}{}:
	case <-ctx.Done():
		return SizeResult{}, ctx.Err()
	}
	defer func() { <-sizer.sem }()
	res, err := walkSize(ctx, base, logical, allowed)
	if err != nil {
		return res, err
	}
	sizer.mu.Lock()
	for k, e := range sizer.cache {
		if time.Since(e.at) >= sizeTTL {
			delete(sizer.cache, k)
		}
	}
	sizer.cache[key] = sizeEntry{at: time.Now(), res: res}
	sizer.mu.Unlock()
	return res, nil
}

func walkSize(ctx context.Context, base, logical string, allowed []string) (SizeResult, error) {
	res := SizeResult{Path: logical}
	if skipTree[logical] {
		return SizeResult{Path: logical, Complete: true}, nil
	}
	root, err := os.OpenRoot(base) // nada abaixo daqui consegue sair da raiz
	if err != nil {
		return res, fmt.Errorf("não foi possível abrir a pasta base do agente: %w", err)
	}
	defer root.Close()
	rel := "."
	if logical != "/" {
		rel = strings.TrimPrefix(logical, "/")
	}
	st, err := root.Lstat(rel)
	if errors.Is(err, fs.ErrNotExist) {
		return res, fmt.Errorf("a pasta %s não existe no agente", logical)
	}
	if err != nil {
		return res, fmt.Errorf("sem acesso a %s", logical)
	}
	if !st.IsDir() {
		return res, fmt.Errorf("%s não é uma pasta", logical)
	}
	dev := devOf(st)

	done := make(chan struct{})
	go func() { // a thread que percorre o disco fica com prioridade de E/S ociosa (como o ionice -c3)
		defer close(done)
		lowIOPriority()
		deadline := time.Now().Add(sizeLimit)
		seen := map[[2]uint64]bool{} // arquivos com vários hard links contam uma vez só
		n := 0
		werr := fs.WalkDir(root.FS(), rel, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				if d != nil && d.IsDir() && p != rel {
					return fs.SkipDir // sem permissão numa subpasta: segue com o resto
				}
				return nil
			}
			n++
			if n%512 == 0 && (ctx.Err() != nil || time.Now().After(deadline)) {
				return fs.SkipAll
			}
			full := path.Clean("/" + p) // caminho como o usuário vê ("." é a raiz)
			if p != rel && !leadsToAllowed(allowed, full) {
				if d.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			if d.IsDir() {
				if p != rel && skipTree[full] {
					return fs.SkipDir
				}
				if p != rel {
					if info, err := d.Info(); err == nil && devOf(info) != dev {
						res.Skipped++
						return fs.SkipDir // outra partição montada aqui dentro (como o du -x)
					}
				}
				return nil
			}
			if !d.Type().IsRegular() {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return nil
			}
			if sys, ok := info.Sys().(*syscall.Stat_t); ok && sys.Nlink > 1 {
				k := [2]uint64{uint64(sys.Dev), sys.Ino}
				if seen[k] {
					return nil
				}
				if len(seen) < 1_000_000 {
					seen[k] = true
				}
			}
			res.Bytes += info.Size()
			res.Files++
			return nil
		})
		res.Complete = werr == nil && ctx.Err() == nil && !time.Now().After(deadline)
	}()
	<-done
	if ctx.Err() != nil {
		return res, ctx.Err()
	}
	return res, nil
}

func devOf(fi fs.FileInfo) uint64 {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return uint64(st.Dev)
	}
	return 0
}
