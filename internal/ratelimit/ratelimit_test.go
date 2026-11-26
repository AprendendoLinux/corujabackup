package ratelimit

import (
	"testing"
	"time"
)

func TestBloqueiaEDesbloqueiaPelaJanela(t *testing.T) {
	now := time.Unix(1000, 0)
	l := New(3, time.Minute)
	l.now = func() time.Time { return now }
	for i := 0; i < 3; i++ {
		if !l.Allow("a") {
			t.Fatal("bloqueou cedo demais")
		}
		l.Fail("a")
	}
	if l.Allow("a") || !l.Allow("b") {
		t.Fatal("deveria bloquear só a chave 'a'")
	}
	now = now.Add(61 * time.Second)
	if !l.Allow("a") {
		t.Fatal("deveria liberar após a janela")
	}
	l.Fail("a")
	l.Reset("a")
	if len(l.fails) != 0 {
		t.Fatal("Reset deveria limpar")
	}
}

func TestSweepLimpaChavesAntigas(t *testing.T) {
	now := time.Unix(1000, 0)
	l := New(3, time.Minute)
	l.now = func() time.Time { return now }
	for i := 0; i < 50; i++ {
		l.Fail(string(rune('a'+i%26)) + string(rune('A'+i/26)))
	}
	if l.Len() != 50 {
		t.Fatal(l.Len())
	}
	now = now.Add(2 * time.Minute)
	l.Sweep()
	if l.Len() != 0 {
		t.Fatalf("o Sweep deveria esvaziar: %d", l.Len())
	}
}
