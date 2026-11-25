// Package auth reúne senha (argon2id), TOTP (RFC 6238) e geração de segredos aleatórios.
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Parâmetros argon2id (recomendação OWASP: m=19 MiB, t=2, p=1).
const (
	argonTime    = 2
	argonMemory  = 19 * 1024
	argonThreads = 1
	argonKeyLen  = 32
	saltLen      = 16
	MinPassword  = 10
)

var ErrWeakPassword = fmt.Errorf("a senha precisa ter pelo menos %d caracteres", MinPassword)

func ValidatePassword(p string) error {
	if len([]rune(p)) < MinPassword {
		return ErrWeakPassword
	}
	return nil
}

// HashPassword devolve "$argon2id$v=19$m=...,t=...,p=...$salt$hash".
func HashPassword(p string) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(p), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

// CheckPassword compara em tempo constante.
func CheckPassword(p, encoded string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var m, t uint32
	var th uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &th); err != nil {
		return false
	}
	salt, err1 := base64.RawStdEncoding.DecodeString(parts[4])
	want, err2 := base64.RawStdEncoding.DecodeString(parts[5])
	if err1 != nil || err2 != nil || len(want) == 0 {
		return false
	}
	got := argon2.IDKey([]byte(p), salt, t, m, th, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

// DummyHash é usado para gastar o mesmo tempo quando o usuário não existe (evita enumeração por tempo).
var DummyHash, _ = HashPassword("dummy-password-for-timing")

// RandomToken gera n bytes aleatórios em base64url (sem preenchimento).
func RandomToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// SHA256Hex é o hash usado para segredos de alta entropia (tokens, sessões, credenciais de agente).
func SHA256Hex(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

var ErrInvalidCode = errors.New("código inválido")
