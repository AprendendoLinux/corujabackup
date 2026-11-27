// Package bkname define o nome dos arquivos de backup e a retenção por cadeia.
//
// Nomes: <prefixo>-AAAAMMDD-HHMMSS.tar.gz (full) e <prefixo>-AAAAMMDD-HHMMSS-inc.tar.gz (incremental). Uma cadeia é um full
// seguido dos incrementais feitos depois dele; restaurar um incremental exige o full e todos os incrementais anteriores
// da mesma cadeia. Por isso a retenção apaga cadeias inteiras (nunca um pedaço do meio) e nunca a mais recente.
package bkname

import (
	"regexp"
	"sort"
	"strings"
	"time"
)

// IncSuffix marca um backup incremental no nome.
const IncSuffix = "-inc"

// Any reconhece qualquer nome de backup (de qualquer job).
var Any = regexp.MustCompile(`^[a-z0-9-]+-\d{8}-\d{6}(-inc)?\.tar\.gz$`)

var tail = regexp.MustCompile(`^-(\d{8}-\d{6})(-inc)?\.tar\.gz$`)

// Name monta o nome de um backup.
func Name(prefix string, t time.Time, incremental bool) string {
	n := prefix + "-" + t.Format("20060102-150405")
	if incremental {
		n += IncSuffix
	}
	return n + ".tar.gz"
}

// Parse lê um nome do job de prefixo prefix: o carimbo (no fuso do job, sem fuso no nome) e se é incremental.
func Parse(name, prefix string) (stamp string, incremental, ok bool) {
	if !strings.HasPrefix(name, prefix) {
		return "", false, false
	}
	m := tail.FindStringSubmatch(name[len(prefix):])
	if m == nil {
		return "", false, false
	}
	return m[1], m[2] != "", true
}

// File é um arquivo de backup visto num destino (local, nuvem ou storage).
type File struct {
	Name     string
	Modified time.Time
}

// Chains agrupa os arquivos do job em cadeias, da mais antiga para a mais nova. Incrementais sem full antes deles (o full
// foi apagado à mão, por exemplo) formam uma cadeia própria.
func Chains(files []File, prefix string) [][]File {
	type item struct {
		f     File
		stamp string
		inc   bool
	}
	var mine []item
	for _, f := range files {
		if s, inc, ok := Parse(f.Name, prefix); ok {
			mine = append(mine, item{f, s, inc})
		}
	}
	sort.SliceStable(mine, func(i, j int) bool {
		if mine[i].stamp != mine[j].stamp {
			return mine[i].stamp < mine[j].stamp
		}
		return !mine[i].inc && mine[j].inc // no mesmo segundo, o full vem antes
	})
	var out [][]File
	for i, x := range mine {
		if i == 0 || !x.inc {
			out = append(out, []File{x.f})
			continue
		}
		out[len(out)-1] = append(out[len(out)-1], x.f)
	}
	return out
}

// Retention devolve os arquivos a apagar: mantém as keep cadeias mais novas (se keep > 0) e apaga as cadeias cujo
// arquivo MAIS NOVO tem mais de keepDays dias (se keepDays > 0). A cadeia mais nova nunca é apagada. Num job sem
// incrementais cada arquivo é uma cadeia, então "cadeias" e "cópias" são a mesma coisa.
func Retention(files []File, prefix string, keep, keepDays int, now time.Time) []File {
	chains := Chains(files, prefix)
	var del []File
	for i := len(chains) - 1; i >= 0; i-- { // da mais nova para a mais velha (como os logs mostram)
		ch := chains[i]
		age := len(chains) - 1 - i // 0 = a mais nova
		if age == 0 {
			continue
		}
		old := keep > 0 && age >= keep
		if keepDays > 0 {
			if t := stampTime(ch[len(ch)-1], prefix); !t.IsZero() && now.Sub(t) > time.Duration(keepDays)*24*time.Hour {
				old = true
			}
		}
		if old {
			for k := len(ch) - 1; k >= 0; k-- {
				del = append(del, ch[k])
			}
		}
	}
	return del
}

// stampTime é o horário do carimbo no nome (lido como UTC, como sempre foi) ou, sem carimbo legível, a data do arquivo.
func stampTime(f File, prefix string) time.Time {
	if s, _, ok := Parse(f.Name, prefix); ok {
		if t, err := time.Parse("20060102-150405", s); err == nil {
			return t
		}
	}
	return f.Modified
}

// ChainUpTo devolve, em ordem, os arquivos necessários para restaurar name: o full da cadeia e os incrementais até ele.
func ChainUpTo(files []File, prefix, name string) []string {
	for _, ch := range Chains(files, prefix) {
		for i, f := range ch {
			if f.Name == name {
				out := make([]string, 0, i+1)
				for _, x := range ch[:i+1] {
					out = append(out, x.Name)
				}
				return out
			}
		}
	}
	return nil
}
