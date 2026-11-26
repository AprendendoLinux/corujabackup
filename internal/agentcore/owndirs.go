package agentcore

// As pastas do próprio agente (backups, destinos locais e os dados dele) nunca entram na origem de um backup. Sem isso,
// salvar uma pasta que as contém (ex.: /srv com os backups em /srv/coruja-backups) incluiria os backups anteriores e o que
// está sendo gerado, e salvar o "/" levaria a credencial do agente e as senhas dos jobs para o destino. Dentro do contêiner os caminhos são outros (/backups na gravação, /host/srv/coruja-backups na leitura),
// então a pasta é reconhecida pela identidade no disco (dispositivo e inode), e não pelo nome.

import (
	"bufio"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type ownDirs []os.FileInfo

// findOwnDirs reúne as pastas informadas e tudo o que estiver montado dentro delas (ex.: -v /mnt/nas:/destinos/nas).
func findOwnDirs(roots ...string) ownDirs {
	var mounts []string
	if f, err := os.Open("/proc/self/mountinfo"); err == nil {
		mounts = mountPoints(f)
		f.Close()
	}
	return resolveOwnDirs(mounts, roots...)
}

func resolveOwnDirs(mounts []string, roots ...string) ownDirs {
	var out ownDirs
	add := func(p string) {
		if st, err := os.Stat(p); err == nil && st.IsDir() {
			out = append(out, st)
		}
	}
	for _, r := range roots {
		if r == "" {
			continue
		}
		r = filepath.Clean(r)
		add(r)
		for _, m := range mounts {
			if strings.HasPrefix(m, strings.TrimSuffix(r, "/")+"/") {
				add(m)
			}
		}
	}
	return out
}

// mountPoints lê os pontos de montagem do formato de /proc/self/mountinfo (5º campo, com espaços escapados em octal).
func mountPoints(r io.Reader) []string {
	var out []string
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		if f := strings.Fields(sc.Text()); len(f) > 4 {
			out = append(out, unescapeMount(f[4]))
		}
	}
	return out
}

func unescapeMount(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) {
			if n, err := strconv.ParseUint(s[i+1:i+4], 8, 8); err == nil {
				b.WriteByte(byte(n))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func (o ownDirs) is(st os.FileInfo) bool {
	for _, d := range o {
		if os.SameFile(d, st) {
			return true
		}
	}
	return false
}

// contains informa se p (um caminho real) é uma das pastas do agente ou está dentro de uma delas, sem subir além de top.
func (o ownDirs) contains(p, top string) bool {
	p, top = filepath.Clean(p), filepath.Clean(top)
	for {
		if st, err := os.Stat(p); err == nil && o.is(st) {
			return true
		}
		parent := filepath.Dir(p)
		if p == top || parent == p {
			return false
		}
		p = parent
	}
}
