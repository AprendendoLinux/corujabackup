// Package secrets cifra segredos (tokens de nuvem etc.) com AES-256-GCM antes de gravá-los no banco.
// A chave fica em arquivo no volume de dados do Server (não no banco): quem só obtém um dump do MySQL não lê os tokens.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Box struct{ aead cipher.AEAD }

const prefix = "v1:"

// Open lê (ou cria, com permissão 0600) a chave em dir/secret.key.
func Open(dir string) (*Box, error) {
	path := filepath.Join(dir, "secret.key")
	key, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, err
		}
		key = make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return nil, err
		}
		if err := os.WriteFile(path, []byte(base64.StdEncoding.EncodeToString(key)), 0o600); err != nil {
			return nil, err
		}
	} else if key, err = base64.StdEncoding.DecodeString(strings.TrimSpace(string(key))); err != nil {
		return nil, errors.New("secret.key corrompida")
	}
	return FromKey(key)
}

func FromKey(key []byte) (*Box, error) {
	if len(key) != 32 {
		return nil, errors.New("a chave precisa ter 32 bytes")
	}
	b, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	g, err := cipher.NewGCM(b)
	if err != nil {
		return nil, err
	}
	return &Box{aead: g}, nil
}

func (b *Box) Seal(plain string) (string, error) {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	return prefix + base64.StdEncoding.EncodeToString(b.aead.Seal(nonce, nonce, []byte(plain), nil)), nil
}

func (b *Box) Open(s string) (string, error) {
	if s == "" {
		return "", nil
	}
	if !strings.HasPrefix(s, prefix) {
		return "", errors.New("formato de segredo desconhecido")
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(s, prefix))
	if err != nil || len(raw) < b.aead.NonceSize() {
		return "", errors.New("segredo corrompido")
	}
	n := b.aead.NonceSize()
	out, err := b.aead.Open(nil, raw[:n], raw[n:], nil)
	if err != nil {
		return "", errors.New("não foi possível decifrar (a chave mudou?)")
	}
	return string(out), nil
}

// Verify confere se a chave abre os valores cifrados informados; devolve erro se algum não abrir (chave trocada/perdida).
func (b *Box) Verify(samples []string) error {
	bad := 0
	for _, s := range samples {
		if _, err := b.Open(s); err != nil {
			bad++
		}
	}
	if bad > 0 {
		return errors.New("a chave de criptografia atual não abre " + itoa(bad) + " dos " + itoa(len(samples)) + " valores guardados (o arquivo secret.key foi trocado ou perdido?)")
	}
	return nil
}

func itoa(n int) string { return strconv.Itoa(n) }
