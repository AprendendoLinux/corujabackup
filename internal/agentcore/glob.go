// Package agentcore reúne o que o agente faz: executar jobs de arquivos, agendar sozinho e guardar resultados
// numa fila local enquanto o Server estiver fora do ar.
package agentcore

import (
	"regexp"
	"strings"
)

// Excluder decide quais caminhos ficam fora do backup. Padrões: "*" casa qualquer sequência (inclusive "/"), "?" casa um
// caractere. Sem "/" no padrão, ele vale para o nome do arquivo em qualquer pasta (ex.: "*.tmp"); com "/", vale para o
// caminho completo (ex.: "*/data/cache/*", "/var/log/*").
type Excluder struct {
	res     []*regexp.Regexp // padrões com / : valem para o caminho inteiro (*/data/cache/*, /srv/x)
	base    []*regexp.Regexp // padrões sem / : valem para o nome em qualquer pasta (*.log)
	dirRes  []*regexp.Regexp // terminados em / e com outra /: só pastas, pelo caminho (*/www/cache/)
	dirBase []*regexp.Regexp // terminados em /: só pastas, pelo nome em qualquer lugar (cache/, tmp/)
}

func NewExcluder(patterns []string) *Excluder {
	e := &Excluder{}
	for _, p := range patterns {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		dirOnly := len(p) > 1 && strings.HasSuffix(p, "/")
		if dirOnly {
			p = strings.TrimRight(p, "/")
		}
		re := compileGlob(p)
		if re == nil {
			continue
		}
		switch hasSlash := strings.Contains(p, "/"); {
		case dirOnly && hasSlash:
			e.dirRes = append(e.dirRes, re)
		case dirOnly:
			e.dirBase = append(e.dirBase, re)
		case hasSlash:
			e.res = append(e.res, re)
		default:
			e.base = append(e.base, re)
		}
	}
	return e
}

func compileGlob(p string) *regexp.Regexp {
	var b strings.Builder
	b.WriteString("^")
	for _, r := range p {
		switch r {
		case '*':
			b.WriteString(".*")
		case '?':
			b.WriteString(".")
		default:
			b.WriteString(regexp.QuoteMeta(string(r)))
		}
	}
	b.WriteString("$")
	re, err := regexp.Compile(b.String())
	if err != nil {
		return nil
	}
	return re
}

// Match recebe o caminho absoluto "lógico" (sem o prefixo do host root). Padrões só de pasta (cache/) não valem aqui:
// use MatchEntry, que sabe se o item é uma pasta.
func (e *Excluder) Match(fullPath string) bool { return e.MatchEntry(fullPath, false) }

// MatchEntry diz se o item fica de fora do backup. Uma pasta que fica de fora leva junto tudo o que há dentro dela.
func (e *Excluder) MatchEntry(fullPath string, isDir bool) bool {
	for _, re := range e.res {
		if re.MatchString(fullPath) {
			return true
		}
	}
	name := fullPath
	if i := strings.LastIndex(fullPath, "/"); i >= 0 {
		name = fullPath[i+1:]
	}
	for _, re := range e.base {
		if re.MatchString(name) {
			return true
		}
	}
	if isDir {
		for _, re := range e.dirRes {
			if re.MatchString(fullPath) {
				return true
			}
		}
		for _, re := range e.dirBase {
			if re.MatchString(name) {
				return true
			}
		}
	}
	return false
}
