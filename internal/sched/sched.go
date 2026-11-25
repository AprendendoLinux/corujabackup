// Package sched interpreta agendas cron de 5 campos com fuso horário (usado pelo Server e pelo Agente).
package sched

import (
	"fmt"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
)

var parser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)

type Schedule struct {
	s   cron.Schedule
	Loc *time.Location
}

// Parse valida "m h dom mon dow" e o fuso (nome IANA). Descritores como @daily não são aceitos (só 5 campos).
func Parse(spec, tz string) (*Schedule, error) {
	spec = strings.TrimSpace(spec)
	if len(strings.Fields(spec)) != 5 {
		return nil, fmt.Errorf("a agenda precisa ter 5 campos (minuto hora dia mês dia-da-semana)")
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return nil, fmt.Errorf("fuso horário inválido: %q", tz)
	}
	s, err := parser.Parse(spec)
	if err != nil {
		return nil, fmt.Errorf("agenda inválida: %v", err)
	}
	return &Schedule{s: s, Loc: loc}, nil
}

// Next devolve o próximo disparo depois de t, calculado no fuso do job (em UTC).
func (sc *Schedule) Next(t time.Time) time.Time { return sc.s.Next(t.In(sc.Loc)).UTC() }

// Prev devolve o último disparo previsto em (after, upTo]; zero se não houve nenhum.
func (sc *Schedule) Prev(after, upTo time.Time) time.Time {
	var last time.Time
	for t := sc.Next(after); !t.IsZero() && !t.After(upTo); t = sc.Next(t) {
		last = t
		if t.Sub(after) > 400*24*time.Hour {
			break
		}
	}
	return last
}
