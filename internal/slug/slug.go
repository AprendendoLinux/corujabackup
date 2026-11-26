// Package slug gera o prefixo seguro de arquivo a partir do nome do job (igual no agente e no Server).
package slug

import (
	"regexp"
	"strings"
)

var re = regexp.MustCompile(`[^a-z0-9]+`)

var fold = strings.NewReplacer("á", "a", "à", "a", "â", "a", "ã", "a", "ä", "a", "é", "e", "è", "e", "ê", "e", "ë", "e", "í", "i", "ì", "i", "î", "i", "ï", "i",
	"ó", "o", "ò", "o", "ô", "o", "õ", "o", "ö", "o", "ú", "u", "ù", "u", "û", "u", "ü", "u", "ç", "c", "ñ", "n")

// Of gera o prefixo de arquivo: minúsculas, sem acentos, só letras, números e hífens ("Relatório diário" -> "relatorio-diario").
func Of(s string) string {
	s = strings.Trim(re.ReplaceAllString(fold.Replace(strings.ToLower(s)), "-"), "-")
	if s == "" {
		return "backup"
	}
	return s
}
