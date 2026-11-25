package sched

import (
	"testing"
	"time"
)

func TestParseEProximoNoFuso(t *testing.T) {
	sc, err := Parse("10 0 * * *", "America/Sao_Paulo")
	if err != nil {
		t.Fatal(err)
	}
	// 2026-11-26 02:00 UTC = 25/11 23:00 em São Paulo (UTC-3): o próximo 00:10 local é 26/11 00:10 = 03:10 UTC
	got := sc.Next(time.Date(2026, 11, 26, 2, 0, 0, 0, time.UTC))
	if want := time.Date(2026, 11, 26, 3, 10, 0, 0, time.UTC); !got.Equal(want) {
		t.Fatalf("esperava %v, veio %v", want, got)
	}
}

func TestPrev(t *testing.T) {
	sc, _ := Parse("0 */6 * * *", "UTC")
	now := time.Date(2026, 11, 26, 14, 0, 0, 0, time.UTC)
	if got := sc.Prev(now.Add(-36*time.Hour), now); !got.Equal(time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)) {
		t.Fatalf("%v", got)
	}
	if got := sc.Prev(now.Add(-time.Hour), now); !got.IsZero() {
		t.Fatalf("não havia disparo na última hora: %v", got)
	}
}

func TestInvalidos(t *testing.T) {
	for _, c := range [][2]string{{"@daily", "UTC"}, {"* * * *", "UTC"}, {"61 * * * *", "UTC"}, {"0 0 * * *", "Marte/Phobos"}, {"", "UTC"}} {
		if _, err := Parse(c[0], c[1]); err == nil {
			t.Fatalf("aceitou %v", c)
		}
	}
}
