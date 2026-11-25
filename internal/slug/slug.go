// Package slug gera o prefixo seguro de arquivo a partir do nome do job (igual no agente e no Server).
package slug

import (
	"regexp"
	"strings"
)

var re = regexp.MustCompile(`[^a-z0-9]+`)

func Of(s string) string {
	s = strings.Trim(re.ReplaceAllString(strings.ToLower(s), "-"), "-")
	if s == "" {
		return "backup"
	}
	return s
}
