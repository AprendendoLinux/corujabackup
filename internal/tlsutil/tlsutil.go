// Package tlsutil gera/carrega o certificado autoassinado do canal dos agentes e calcula a impressão digital
// (fingerprint) que o agente "fixa" (pinning): é ela que garante que o agente fala com o Server certo.
package tlsutil

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"time"
)

// LoadOrCreate lê dir/server.crt e dir/server.key; se não existirem, cria um par ECDSA P-256 válido por 10 anos.
func LoadOrCreate(dir string) (tls.Certificate, error) {
	crt, key := filepath.Join(dir, "server.crt"), filepath.Join(dir, "server.key")
	if c, err := tls.LoadX509KeyPair(crt, key); err == nil {
		return c, nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return tls.Certificate{}, err
	}
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 100))
	tpl := x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "Coruja Backup Server", Organization: []string{"Coruja Backup"}},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().AddDate(10, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
	}
	der, err := x509.CreateCertificate(rand.Reader, &tpl, &tpl, &priv.PublicKey, priv)
	if err != nil {
		return tls.Certificate{}, err
	}
	keyDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		return tls.Certificate{}, err
	}
	if err := os.WriteFile(crt, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644); err != nil {
		return tls.Certificate{}, err
	}
	if err := os.WriteFile(key, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		return tls.Certificate{}, err
	}
	return tls.LoadX509KeyPair(crt, key)
}

// Fingerprint é o SHA-256 (hex) do certificado folha.
func Fingerprint(c tls.Certificate) string {
	if len(c.Certificate) == 0 {
		return ""
	}
	return FingerprintDER(c.Certificate[0])
}

func FingerprintDER(der []byte) string {
	h := sha256.Sum256(der)
	return hex.EncodeToString(h[:])
}

// PinnedConfig devolve a configuração TLS do cliente: ignora a cadeia/nome (autoassinado) e exige que a
// impressão digital do certificado comece com fp. Sem ela, nenhuma conexão é aceita.
func PinnedConfig(fp string) *tls.Config {
	return &tls.Config{
		MinVersion:         tls.VersionTLS13,
		InsecureSkipVerify: true, //nolint:gosec // a verificação é feita por pinning em VerifyPeerCertificate
		NextProtos:         []string{"h2"},
		VerifyPeerCertificate: func(raw [][]byte, _ [][]*x509.Certificate) error {
			if fp == "" || len(raw) == 0 {
				return errors.New("sem impressão digital para conferir")
			}
			got := FingerprintDER(raw[0])
			if len(fp) > len(got) || got[:len(fp)] != fp {
				return errors.New("o certificado do Server não confere com a impressão digital do token")
			}
			return nil
		},
	}
}
