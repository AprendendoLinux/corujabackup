// Package enroll define o formato do token de matrícula: "cb1.<impressão digital>.<segredo>".
// A impressão digital (32 hex do SHA-256 do certificado do Server) vai dentro do token, então o agente só
// precisa de IP:porta + token e ainda assim confirma que está falando com o Server certo (sem confiar no primeiro uso).
package enroll

import (
	"errors"
	"strings"

	"github.com/AprendendoLinux/corujabackup/internal/auth"
)

const FingerprintLen = 32

type Token struct {
	Fingerprint string
	Secret      string
}

func New(fingerprint string) (string, error) {
	if len(fingerprint) < FingerprintLen {
		return "", errors.New("impressão digital inválida")
	}
	secret, err := auth.RandomToken(32)
	if err != nil {
		return "", err
	}
	return "cb1." + fingerprint[:FingerprintLen] + "." + secret, nil
}

func Parse(s string) (Token, error) {
	p := strings.Split(strings.TrimSpace(s), ".")
	if len(p) != 3 || p[0] != "cb1" || len(p[1]) != FingerprintLen || len(p[2]) < 40 {
		return Token{}, errors.New("token de matrícula inválido")
	}
	return Token{Fingerprint: p[1], Secret: p[2]}, nil
}

// Hash é o que o Server guarda (o token em si nunca fica no banco).
func (t Token) Hash() string { return auth.SHA256Hex(t.Secret) }
