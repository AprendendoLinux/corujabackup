package httpapi

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// nameInput é o nome/sobrenome recebido nos formulários. display_name (antigo) ainda é aceito: a primeira
// palavra vira o nome e o resto, o sobrenome.
type nameInput struct {
	FirstName   *string `json:"first_name"`
	LastName    *string `json:"last_name"`
	DisplayName *string `json:"display_name"`
}

func (n nameInput) given() bool {
	return n.FirstName != nil || n.LastName != nil || n.DisplayName != nil
}

// resolve devolve nome e sobrenome finais partindo dos atuais; ok=false traz a mensagem de erro.
func (n nameInput) resolve(first, last string) (string, string, string) {
	if n.FirstName == nil && n.LastName == nil && n.DisplayName != nil {
		d := strings.Join(strings.Fields(*n.DisplayName), " ")
		first, last, _ = strings.Cut(d, " ")
	}
	if n.FirstName != nil {
		first = *n.FirstName
	}
	if n.LastName != nil {
		last = *n.LastName
	}
	first, last = cleanName(first), cleanName(last)
	if utf8.RuneCountInString(first) > 60 || utf8.RuneCountInString(last) > 60 {
		return "", "", "nome e sobrenome podem ter até 60 caracteres cada"
	}
	return first, last, ""
}

// cleanName tira espaços repetidos e caracteres de controle.
func cleanName(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
	return strings.Join(strings.Fields(s), " ")
}
