package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1" //nolint:gosec // exigido pelo RFC 6238 (compatível com Google Authenticator e similares)
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"
)

const totpStep = 30

// NewTOTPSecret gera um segredo de 160 bits em base32.
func NewTOTPSecret() (string, error) {
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b), nil
}

func hotp(secret string, counter int64) (string, error) {
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(strings.ReplaceAll(secret, " ", "")))
	if err != nil {
		return "", err
	}
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], uint64(counter))
	mac := hmac.New(sha1.New, key)
	mac.Write(buf[:])
	sum := mac.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	code := (uint32(sum[off]&0x7f) << 24) | (uint32(sum[off+1]) << 16) | (uint32(sum[off+2]) << 8) | uint32(sum[off+3])
	return fmt.Sprintf("%06d", code%1_000_000), nil
}

// TOTPCode devolve o código de 6 dígitos de um instante (útil em testes).
func TOTPCode(secret string, at time.Time) (string, error) { return hotp(secret, at.Unix()/totpStep) }

// VerifyTOTP aceita o passo atual e ±1 (relógios levemente fora). Devolve o contador usado, que o chamador deve
// gravar para impedir reuso do mesmo código (anti-replay): só vale contador > último usado.
func VerifyTOTP(secret, code string, now time.Time, lastCounter int64) (int64, bool) {
	code = strings.TrimSpace(code)
	if len(code) != 6 {
		return 0, false
	}
	cur := now.Unix() / totpStep
	for _, d := range []int64{0, -1, 1} {
		c := cur + d
		if c <= lastCounter {
			continue
		}
		want, err := hotp(secret, c)
		if err == nil && hmac.Equal([]byte(want), []byte(code)) {
			return c, true
		}
	}
	return 0, false
}

// OTPAuthURI monta o link otpauth:// que apps autenticadores leem (também em QR).
func OTPAuthURI(issuer, account, secret string) string {
	v := url.Values{}
	v.Set("secret", secret)
	v.Set("issuer", issuer)
	v.Set("algorithm", "SHA1")
	v.Set("digits", "6")
	v.Set("period", "30")
	return "otpauth://totp/" + url.PathEscape(issuer+":"+account) + "?" + v.Encode()
}

// NewRecoveryCodes gera n códigos de recuperação (formato xxxxx-xxxxx).
func NewRecoveryCodes(n int) ([]string, error) {
	out := make([]string, 0, n)
	const alphabet = "abcdefghjkmnpqrstuvwxyz23456789"
	for i := 0; i < n; i++ {
		b := make([]byte, 10)
		if _, err := rand.Read(b); err != nil {
			return nil, err
		}
		for j := range b {
			b[j] = alphabet[int(b[j])%len(alphabet)]
		}
		out = append(out, string(b[:5])+"-"+string(b[5:]))
	}
	return out, nil
}
