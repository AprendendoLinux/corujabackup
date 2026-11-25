package auth

import (
	"strings"
	"testing"
	"time"
)

func TestSenhaHashEVerificacao(t *testing.T) {
	h, err := HashPassword("uma-senha-forte-123")
	if err != nil || !strings.HasPrefix(h, "$argon2id$") {
		t.Fatalf("hash inválido: %v %q", err, h)
	}
	if !CheckPassword("uma-senha-forte-123", h) || CheckPassword("outra", h) || CheckPassword("x", "lixo") {
		t.Fatal("verificação incorreta")
	}
	h2, _ := HashPassword("uma-senha-forte-123")
	if h == h2 {
		t.Fatal("o sal deveria tornar os hashes diferentes")
	}
}

func TestPoliticaDeSenha(t *testing.T) {
	if ValidatePassword("curta") == nil || ValidatePassword("1234567890") != nil {
		t.Fatal("política de tamanho mínimo")
	}
}

// Vetor de teste do RFC 6238 (SHA-1, segredo "12345678901234567890", T=59 => 94287082; 6 dígitos => 287082).
func TestTOTPVetorRFC6238(t *testing.T) {
	secret := "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"
	got, err := TOTPCode(secret, time.Unix(59, 0))
	if err != nil || got != "287082" {
		t.Fatalf("esperava 287082, veio %q (%v)", got, err)
	}
}

func TestTOTPJanelaEAntiReplay(t *testing.T) {
	secret, _ := NewTOTPSecret()
	now := time.Unix(1_700_000_000, 0)
	code, _ := TOTPCode(secret, now)
	c, ok := VerifyTOTP(secret, code, now, 0)
	if !ok {
		t.Fatal("código válido recusado")
	}
	if _, ok := VerifyTOTP(secret, code, now, c); ok {
		t.Fatal("o mesmo código não pode valer duas vezes")
	}
	prev, _ := TOTPCode(secret, now.Add(-30*time.Second))
	if _, ok := VerifyTOTP(secret, prev, now, 0); !ok {
		t.Fatal("deveria aceitar o passo anterior")
	}
	old, _ := TOTPCode(secret, now.Add(-5*time.Minute))
	if _, ok := VerifyTOTP(secret, old, now, 0); ok {
		t.Fatal("código antigo aceito")
	}
	if _, ok := VerifyTOTP(secret, "abc", now, 0); ok {
		t.Fatal("formato inválido aceito")
	}
}

func TestRecoveryEToken(t *testing.T) {
	codes, _ := NewRecoveryCodes(8)
	seen := map[string]bool{}
	for _, c := range codes {
		if len(c) != 11 || c[5] != '-' || seen[c] {
			t.Fatalf("código inválido/duplicado: %q", c)
		}
		seen[c] = true
	}
	a, _ := RandomToken(32)
	b, _ := RandomToken(32)
	if a == b || len(a) < 40 || SHA256Hex(a) == SHA256Hex(b) {
		t.Fatal("tokens devem ser longos e distintos")
	}
}
