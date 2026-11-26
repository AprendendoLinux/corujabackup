// Package ratelimit é um limitador simples em memória: bloqueia uma chave após N falhas dentro de uma janela.
package ratelimit

import (
	"sync"
	"time"
)

type Limiter struct {
	max    int
	window time.Duration
	mu     sync.Mutex
	fails  map[string][]time.Time
	now    func() time.Time
}

func New(max int, window time.Duration) *Limiter {
	return &Limiter{max: max, window: window, fails: map[string][]time.Time{}, now: time.Now}
}

func (l *Limiter) prune(key string) []time.Time {
	cut := l.now().Add(-l.window)
	kept := l.fails[key][:0]
	for _, t := range l.fails[key] {
		if t.After(cut) {
			kept = append(kept, t)
		}
	}
	if len(kept) == 0 {
		delete(l.fails, key)
		return nil
	}
	l.fails[key] = kept
	return kept
}

// Allow diz se a chave ainda pode tentar.
func (l *Limiter) Allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.prune(key)) < l.max
}

func (l *Limiter) Fail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.prune(key)
	l.fails[key] = append(l.fails[key], l.now())
}

func (l *Limiter) Reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.fails, key)
}

// Sweep remove as chaves cujas falhas já saíram da janela (evita crescer sem limite sob varredura de IPs).
func (l *Limiter) Sweep() {
	l.mu.Lock()
	defer l.mu.Unlock()
	for k := range l.fails {
		l.prune(k)
	}
}

func (l *Limiter) Len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.fails)
}
