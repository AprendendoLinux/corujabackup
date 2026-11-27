// Agenda por cliques: o usuário escolhe frequência, dias e horário; a expressão cron é gerada por baixo e nunca aparece.
import { ReactNode, useEffect, useState } from 'react'

export type Sched =
  | { kind: 'hourly'; every: number; start: number; minute: number }
  | { kind: 'daily'; hour: number; minute: number }
  | { kind: 'weekly'; days: number[]; hour: number; minute: number }
  | { kind: 'monthly'; days: number[]; hour: number; minute: number }

const WEEK = ['Dom', 'Seg', 'Ter', 'Qua', 'Qui', 'Sex', 'Sáb']
const WEEK_LONG = ['domingo', 'segunda', 'terça', 'quarta', 'quinta', 'sexta', 'sábado']
const EVERY = [1, 2, 3, 4, 6, 8, 12]
const pad = (n: number) => String(n).padStart(2, '0')
const hm = (h: number, m: number) => `${pad(h)}:${pad(m)}`
const list = (xs: string[]) => (xs.length <= 1 ? xs.join('') : `${xs.slice(0, -1).join(', ')} e ${xs[xs.length - 1]}`)

export function toCron(s: Sched): string {
  switch (s.kind) {
    case 'hourly': return `${s.minute} ${s.every === 1 ? '*' : s.start === 0 ? `*/${s.every}` : `${s.start}-23/${s.every}`} * * *`
    case 'daily': return `${s.minute} ${s.hour} * * *`
    case 'weekly': return `${s.minute} ${s.hour} * * ${[...s.days].sort((a, b) => a - b).join(',')}`
    case 'monthly': return `${s.minute} ${s.hour} ${[...s.days].sort((a, b) => a - b).join(',')} * *`
  }
}

const num = (v: string, lo: number, hi: number) => (/^\d+$/.test(v) && +v >= lo && +v <= hi ? +v : null)
const nums = (v: string, lo: number, hi: number) => {
  const out = v.split(',').map((x) => num(x, lo, hi))
  return out.every((x) => x !== null) ? (out as number[]) : null
}

/** Reconstrói os cliques a partir de uma agenda salva; null = agenda que a tela não sabe representar. */
export function fromCron(c: string): Sched | null {
  const f = c.trim().split(/\s+/)
  if (f.length !== 5) return null
  const [mi, ho, dom, mon, dow] = f
  const minute = num(mi, 0, 59)
  if (minute === null || mon !== '*') return null
  if (dom === '*' && dow === '*') {
    if (ho === '*') return { kind: 'hourly', every: 1, start: 0, minute }
    let m = ho.match(/^\*\/(\d+)$/)
    if (m && EVERY.includes(+m[1])) return { kind: 'hourly', every: +m[1], start: 0, minute }
    m = ho.match(/^(\d+)-23\/(\d+)$/)
    if (m && EVERY.includes(+m[2]) && +m[1] < +m[2]) return { kind: 'hourly', every: +m[2], start: +m[1], minute }
    const hour = num(ho, 0, 23)
    return hour === null ? null : { kind: 'daily', hour, minute }
  }
  const hour = num(ho, 0, 23)
  if (hour === null) return null
  if (dom === '*') {
    const days = nums(dow.replace(/\b7\b/, '0'), 0, 6)
    return days ? { kind: 'weekly', days: [...new Set(days)], hour, minute } : null
  }
  if (dow === '*') {
    const days = nums(dom, 1, 31)
    return days ? { kind: 'monthly', days: [...new Set(days)], hour, minute } : null
  }
  return null
}

/** Frase em português para listas e revisão. */
export function describeSched(c: string): string {
  const s = fromCron(c)
  if (!s) return `Agenda avançada (${c})`
  switch (s.kind) {
    case 'hourly': return s.every === 1 ? `A cada hora, no minuto ${pad(s.minute)}` : `A cada ${s.every} horas, a partir das ${hm(s.start, s.minute)}`
    case 'daily': return `Todo dia às ${hm(s.hour, s.minute)}`
    case 'weekly': return s.days.length === 7 ? `Todo dia às ${hm(s.hour, s.minute)}` : `${list(s.days.slice().sort((a, b) => ((a + 6) % 7) - ((b + 6) % 7)).map((d) => WEEK_LONG[d]))}, às ${hm(s.hour, s.minute)}`.replace(/^./, (x) => x.toUpperCase())
    case 'monthly': return `Todo mês, no${s.days.length > 1 ? 's dias' : ' dia'} ${list(s.days.slice().sort((a, b) => a - b).map(String))}, às ${hm(s.hour, s.minute)}`
  }
}

function Chip({ on, onClick, children, title }: { on: boolean; onClick: () => void; children: ReactNode; title?: string }) {
  return (
    <button type="button" aria-pressed={on} title={title} onClick={onClick}
      className={`min-w-11 rounded-lg border px-3 py-1.5 text-sm font-medium transition ${on ? 'border-primary bg-primary text-primary-contrast' : 'border-border bg-surface hover:border-primary/60'}`}>
      {children}
    </button>
  )
}

const selCls = 'rounded-lg border border-border bg-surface px-3 py-2 text-sm'

/** Horário digitado (hh:mm, 24 h). hours limita a hora (ex.: "a cada 4 horas" só aceita a primeira até 03:59). */
function TimePick({ hour, minute, onChange, label = 'Horário', hours = 24 }: { hour: number; minute: number; onChange: (h: number, m: number) => void; label?: string; hours?: number }) {
  const current = hm(hour, minute)
  const [text, setText] = useState(current)
  const [bad, setBad] = useState(false)
  useEffect(() => { setText(current); setBad(false) }, [current])
  const max = `${pad(hours - 1)}:59`
  function type(v: string) {
    const d = v.replace(/\D/g, '').slice(0, 4)
    const shown = d.length > 2 ? `${d.slice(0, 2)}:${d.slice(2)}` : d
    setText(shown)
    if (d.length === 4) {
      const h = +d.slice(0, 2), m = +d.slice(2)
      if (h < hours && m < 60) { setBad(false); onChange(h, m) } else setBad(true)
    } else setBad(false)
  }
  return (
    <label className="block">
      <span className="mb-1.5 block text-sm font-medium">{label}</span>
      <input inputMode="numeric" autoComplete="off" aria-label={label} placeholder="hh:mm" value={text}
        onFocus={(e) => e.currentTarget.select()} onMouseUp={(e) => e.preventDefault() /* mantém o horário selecionado: é só digitar por cima */}
        onChange={(e) => type(e.target.value)} onBlur={() => { if (text !== current) { setText(current); setBad(false) } }}
        onKeyDown={(e) => { if (e.key === 'Enter') e.currentTarget.blur() }}
        className={`w-28 rounded-lg border bg-surface px-3 py-2 text-center font-mono text-base tracking-wider ${bad ? 'border-danger' : 'border-border'}`} />
      <span className={`mt-1 block text-xs ${bad ? 'text-danger' : 'text-muted'}`}>{bad ? `Horário inválido: use de 00:00 a ${max}.` : hours < 24 ? `Até ${max}: depois repete a cada ${hours} horas.` : 'Formato 24 horas, ex.: 22:30'}</span>
    </label>
  )
}

const KINDS: { kind: Sched['kind']; label: string; hint: string }[] = [
  { kind: 'daily', label: 'Todo dia', hint: 'uma vez por dia' },
  { kind: 'weekly', label: 'Dias da semana', hint: 'em dias escolhidos' },
  { kind: 'monthly', label: 'Todo mês', hint: 'em dias do mês' },
  { kind: 'hourly', label: 'Várias vezes ao dia', hint: 'a cada algumas horas' },
]

export function ScheduleEditor({ cron, onChange }: { cron: string; onChange: (cron: string) => void }) {
  const parsed = fromCron(cron)
  const s: Sched = parsed ?? { kind: 'daily', hour: 0, minute: 10 }
  const set = (n: Sched) => onChange(toCron(n))
  const time = 'hour' in s ? { hour: s.hour, minute: s.minute } : { hour: 0, minute: 10 }
  function pick(kind: Sched['kind']) {
    if (parsed && kind === s.kind) return
    const t = 'hour' in s ? time : { hour: s.start, minute: s.minute }
    if (kind === 'daily') set({ kind, ...t })
    if (kind === 'weekly') set({ kind, days: [1, 2, 3, 4, 5], ...t })
    if (kind === 'monthly') set({ kind, days: [1], ...t })
    if (kind === 'hourly') set({ kind, every: 6, start: 0, minute: t.minute })
  }
  return (
    <div className="space-y-4">
      {!parsed && <p className="rounded-lg border border-accent/50 bg-accent/10 px-3 py-2 text-sm">Este job tinha uma agenda avançada ({cron}). Escolha abaixo como ele deve rodar; a agenda antiga só é trocada quando você salvar.</p>}
      <div role="radiogroup" aria-label="Frequência" className="grid grid-cols-2 gap-2 sm:grid-cols-4">
        {KINDS.map((k) => (
          <button key={k.kind} type="button" role="radio" aria-checked={parsed !== null && s.kind === k.kind} onClick={() => pick(k.kind)}
            className={`rounded-xl border p-3 text-left transition ${parsed !== null && s.kind === k.kind ? 'border-primary ring-2 ring-primary/30' : 'border-border hover:border-primary/50'}`}>
            <b className="block text-sm">{k.label}</b><span className="text-xs text-muted">{k.hint}</span>
          </button>
        ))}
      </div>
      {s.kind === 'weekly' && (
        <div>
          <span className="mb-1.5 block text-sm font-medium">Em quais dias</span>
          <div className="flex flex-wrap gap-1.5">
            {[1, 2, 3, 4, 5, 6, 0].map((d) => (
              <Chip key={d} on={s.days.includes(d)} title={WEEK_LONG[d]} onClick={() => {
                const days = s.days.includes(d) ? s.days.filter((x) => x !== d) : [...s.days, d]
                if (days.length) set({ ...s, days })
              }}>{WEEK[d]}</Chip>
            ))}
            <span className="mx-1 w-px self-stretch bg-border" />
            <Chip on={false} onClick={() => set({ ...s, days: [1, 2, 3, 4, 5] })}>Dias úteis</Chip>
            <Chip on={false} onClick={() => set({ ...s, days: [0, 6] })}>Fim de semana</Chip>
          </div>
        </div>
      )}
      {s.kind === 'monthly' && (
        <div>
          <span className="mb-1.5 block text-sm font-medium">Em quais dias do mês</span>
          <div className="grid max-w-md grid-cols-7 gap-1.5">
            {Array.from({ length: 31 }, (_, i) => i + 1).map((d) => (
              <Chip key={d} on={s.days.includes(d)} title={d > 28 ? 'Meses mais curtos não têm este dia: nesses meses o backup não roda nele.' : undefined}
                onClick={() => { const days = s.days.includes(d) ? s.days.filter((x) => x !== d) : [...s.days, d]; if (days.length) set({ ...s, days }) }}>{d}</Chip>
            ))}
          </div>
          {s.days.some((d) => d > 28) && <p className="mt-1.5 text-xs text-muted">Dias 29, 30 e 31 não existem em todos os meses: nesses meses, o backup não roda nesse dia.</p>}
        </div>
      )}
      {s.kind === 'hourly' ? (
        <div className="flex flex-wrap items-end gap-4">
          <div>
            <span className="mb-1.5 block text-sm font-medium">A cada</span>
            <div className="flex flex-wrap gap-1.5">
              {EVERY.map((n) => <Chip key={n} on={s.every === n} onClick={() => set({ ...s, every: n, start: Math.min(s.start, n - 1) })}>{n === 1 ? '1 hora' : `${n} horas`}</Chip>)}
            </div>
          </div>
          {s.every > 1 && <TimePick label="Primeira do dia às" hours={s.every} hour={s.start} minute={s.minute} onChange={(h, m) => set({ ...s, start: Math.min(h, s.every - 1), minute: m })} />}
          {s.every === 1 && (
            <div>
              <span className="mb-1.5 block text-sm font-medium">No minuto</span>
              <select aria-label="Minuto" className={selCls} value={s.minute} onChange={(e) => set({ ...s, minute: +e.target.value })}>
                {Array.from({ length: 12 }, (_, i) => i * 5).map((m) => <option key={m} value={m}>{pad(m)}</option>)}
              </select>
            </div>
          )}
        </div>
      ) : (
        <TimePick hour={time.hour} minute={time.minute} onChange={(h, m) => set({ ...s, hour: h, minute: m } as Sched)} />
      )}
      <p className="rounded-lg bg-bg px-3 py-2 text-sm"><b>Resumo:</b> {describeSched(toCron(s))}</p>
    </div>
  )
}
