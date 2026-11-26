package agentcore

// Dois tipos de coisa também ficam fora da origem:
//   - sistemas de arquivos virtuais (/proc, /sys, /dev...), que não são dados e travariam um backup do "/" inteiro;
//   - os arquivos que o agente gerou nas pastas escolhidas para o backup local. Só eles: a pasta escolhida pode ter outros
//     dados (ex.: /home), e esses continuam sendo salvos.

import (
	"bufio"
	"io"
	"os"
	"regexp"
	"strings"
)

var virtualFS = map[string]bool{"proc": true, "sysfs": true, "devtmpfs": true, "devpts": true, "cgroup": true, "cgroup2": true,
	"securityfs": true, "debugfs": true, "tracefs": true, "bpf": true, "pstore": true, "configfs": true, "fusectl": true,
	"mqueue": true, "hugetlbfs": true, "efivarfs": true, "binfmt_misc": true, "nsfs": true, "rpc_pipefs": true}

// virtualMounts devolve os pontos de montagem que não devem ser percorridos: os virtuais e as visões montadas dos
// contêineres Docker (overlay), que repetiriam o conteúdo das imagens.
func virtualMounts(r io.Reader) map[string]string {
	out := map[string]string{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		before, after, ok := strings.Cut(sc.Text(), " - ")
		f, t := strings.Fields(before), strings.Fields(after)
		if !ok || len(f) < 5 || len(t) < 1 {
			continue
		}
		p, typ := unescapeMount(f[4]), t[0]
		if virtualFS[typ] || (typ == "overlay" && strings.Contains(p, "/docker/")) {
			out[p] = typ
		}
	}
	return out
}

func readVirtualMounts() map[string]string {
	f, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return nil
	}
	defer f.Close()
	return virtualMounts(f)
}

// ArtifactDir é uma pasta escolhida para o backup local de um job.
type ArtifactDir struct {
	Path   string // caminho real, como o agente grava
	Prefix string // prefixo dos arquivos do job
}

type artifact struct {
	dir os.FileInfo
	re  *regexp.Regexp
}

type artifacts []artifact

func newArtifacts(dirs []ArtifactDir) artifacts {
	var out artifacts
	for _, d := range dirs {
		st, err := os.Stat(d.Path)
		if err != nil || !st.IsDir() {
			continue
		}
		// o arquivo final, o temporário, o que está sendo trazido de um destino e as pastas de trabalho dos bancos
		name := regexp.QuoteMeta(d.Prefix) + `-\d{8}-\d{6}\.tar\.gz`
		out = append(out, artifact{st, regexp.MustCompile(`^(` + name + `|\.` + name + `\.tmp|` + name + `\.baixando|\.tmp-[0-9a-f]{12})$`)})
	}
	return out
}

// patterns devolve as regras que valem dentro da pasta st (nil se ela não for uma pasta de backup local).
func (a artifacts) patterns(st os.FileInfo) []*regexp.Regexp {
	var out []*regexp.Regexp
	for _, x := range a {
		if os.SameFile(x.dir, st) {
			out = append(out, x.re)
		}
	}
	return out
}
