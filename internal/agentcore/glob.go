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
	res  []*regexp.Regexp
	base []*regexp.Regexp
}

func NewExcluder(patterns []string) *Excluder {
	e := &Excluder{}
	for _, p := range patterns {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		re := compileGlob(p)
		if re == nil {
			continue
		}
		if strings.Contains(p, "/") {
			e.res = append(e.res, re)
		} else {
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

// Match recebe o caminho absoluto "lógico" (sem o prefixo do host root).
func (e *Excluder) Match(fullPath string) bool {
	for _, re := range e.res {
		if re.MatchString(fullPath) {
			return true
		}
	}
	if len(e.base) > 0 {
		name := fullPath
		if i := strings.LastIndex(fullPath, "/"); i >= 0 {
			name = fullPath[i+1:]
		}
		for _, re := range e.base {
			if re.MatchString(name) {
				return true
			}
		}
	}
	return false
}
