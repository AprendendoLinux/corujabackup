// Seletor de período: atalhos (hoje, ontem, últimos N dias, este mês, mês passado) e um calendário para marcar o
// início e o fim com cliques. As datas são dias inteiros no fuso de quem vê (AAAA-MM-DD).
import { useEffect, useRef, useState } from 'react'

export type Period = { preset: string; from: string; to: string }

const pad = (n: number) => String(n).padStart(2, '0')
export const ymd = (d: Date) => `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`
const parse = (s: string) => { const [y, m, d] = s.split('-').map(Number); return new Date(y, m - 1, d) }
const addDays = (d: Date, n: number) => new Date(d.getFullYear(), d.getMonth(), d.getDate() + n)
export const br = (s: string) => s.split('-').reverse().join('/')
const MAX_DAYS = 366

export const PRESETS: { id: string; label: string; range: () => [Date, Date] }[] = [
  { id: 'hoje', label: 'Hoje', range: () => [new Date(), new Date()] },
  { id: 'ontem', label: 'Ontem', range: () => [addDays(new Date(), -1), addDays(new Date(), -1)] },
  { id: '7', label: 'Últimos 7 dias', range: () => [addDays(new Date(), -6), new Date()] },
  { id: '14', label: 'Últimos 14 dias', range: () => [addDays(new Date(), -13), new Date()] },
  { id: '30', label: 'Últimos 30 dias', range: () => [addDays(new Date(), -29), new Date()] },
  { id: 'mes', label: 'Este mês', range: () => { const t = new Date(); return [new Date(t.getFullYear(), t.getMonth(), 1), t] } },
  { id: 'mes-passado', label: 'Mês passado', range: () => { const t = new Date(); return [new Date(t.getFullYear(), t.getMonth() - 1, 1), new Date(t.getFullYear(), t.getMonth(), 0)] } },
]

/** Período de um atalho, recalculado no dia de hoje (um "últimos 7 dias" salvo ontem continua sendo os últimos 7). */
export function presetPeriod(id: string): Period {
  const p = PRESETS.find((x) => x.id === id) ?? PRESETS[3]
  const [a, b] = p.range()
  return { preset: p.id, from: ymd(a), to: ymd(b) }
}

export function periodDays(p: Period) { return Math.round((parse(p.to).getTime() - parse(p.from).getTime()) / 86400000) + 1 }

export function periodLabel(p: Period | null, all = 'Todo o período') {
  if (!p) return all
  const pr = PRESETS.find((x) => x.id === p.preset)
  if (pr) return pr.label
  return p.from === p.to ? br(p.from) : `${br(p.from)} a ${br(p.to)}`
}

/** Rótulo curto para títulos: "14 dias", "hoje", "01/11 a 10/11". */
export function periodShort(p: Period) {
  if (p.preset === 'hoje') return 'hoje'
  if (p.preset === 'ontem') return 'ontem'
  if (p.preset === 'mes') return 'este mês'
  if (p.preset === 'mes-passado') return 'mês passado'
  if (/^\d+$/.test(p.preset)) return `${p.preset} dias`
  return p.from === p.to ? br(p.from).slice(0, 5) : `${br(p.from).slice(0, 5)} a ${br(p.to).slice(0, 5)}`
}

const WEEK = ['D', 'S', 'T', 'Q', 'Q', 'S', 'S']

function Calendar({ month, from, to, hover, onPick, onHover }: { month: Date; from: string; to: string; hover: string; onPick: (d: string) => void; onHover: (d: string) => void }) {
  const first = new Date(month.getFullYear(), month.getMonth(), 1)
  const start = addDays(first, -first.getDay())
  const today = ymd(new Date())
  const end = to || (from && hover ? hover : '')
  const [lo, hi] = from && end ? (from <= end ? [from, end] : [end, from]) : [from, from]
  const cells = Array.from({ length: 42 }, (_, i) => addDays(start, i))
  return (
    <div>
      <div className="grid grid-cols-7 text-center text-[11px] font-medium text-muted">{WEEK.map((w, i) => <div key={i} className="py-1">{w}</div>)}</div>
      <div className="grid grid-cols-7" onMouseLeave={() => onHover('')}>
        {cells.map((d) => {
          const s = ymd(d)
          const out = d.getMonth() !== month.getMonth()
          const future = s > today
          const inRange = lo && hi && s >= lo && s <= hi
          const edge = s === lo || s === hi
          return (
            <button key={s} type="button" disabled={future} onClick={() => onPick(s)} onMouseEnter={() => onHover(s)}
              aria-label={`${br(s)}${edge ? ' (selecionado)' : ''}`} aria-pressed={!!edge}
              className={`relative h-9 text-sm tabular-nums transition ${inRange && !edge ? 'bg-primary/12' : ''} ${future ? 'cursor-not-allowed text-muted/40' : out ? 'text-muted/70' : ''}
                ${s === lo && hi && lo !== hi ? 'rounded-l-lg' : ''} ${s === hi && lo && lo !== hi ? 'rounded-r-lg' : ''}`}>
              <span className={`mx-auto flex h-8 w-8 items-center justify-center rounded-lg ${edge ? 'bg-primary font-semibold text-primary-contrast' : future ? '' : 'hover:bg-border/60'} ${s === today && !edge ? 'ring-1 ring-primary/60' : ''}`}>{d.getDate()}</span>
            </button>
          )
        })}
      </div>
    </div>
  )
}

/** Botão + painel de escolha. value null = sem filtro (só quando allowAll). */
export default function PeriodPicker({ value, onChange, allowAll, align = 'right' }: { value: Period | null; onChange: (p: Period | null) => void; allowAll?: boolean; align?: 'left' | 'right' }) {
  const [open, setOpen] = useState(false)
  const [from, setFrom] = useState('')
  const [to, setTo] = useState('')
  const [hover, setHover] = useState('')
  const [month, setMonth] = useState(() => new Date())
  const box = useRef<HTMLDivElement>(null)
  useEffect(() => {
    if (!open) return
    setFrom(value?.from ?? ''); setTo(value?.to ?? ''); setHover('')
    const m = value ? parse(value.to) : new Date()
    setMonth(new Date(m.getFullYear(), m.getMonth(), 1))
    const click = (e: MouseEvent) => { if (box.current && !box.current.contains(e.target as Node)) setOpen(false) }
    const key = (e: KeyboardEvent) => { if (e.key === 'Escape') { e.preventDefault(); setOpen(false) } }
    document.addEventListener('mousedown', click)
    document.addEventListener('keydown', key)
    return () => { document.removeEventListener('mousedown', click); document.removeEventListener('keydown', key) }
  }, [open]) // eslint-disable-line react-hooks/exhaustive-deps
  function pick(s: string) {
    if (!from || to) { setFrom(s); setTo(''); return }
    if (s < from) { setTo(from); setFrom(s) } else setTo(s)
  }
  const lo = from, hi = to || from
  const days = lo ? Math.round((parse(hi).getTime() - parse(lo).getTime()) / 86400000) + 1 : 0
  const tooLong = days > MAX_DAYS
  function apply() { if (lo && !tooLong) { onChange({ preset: 'custom', from: lo, to: hi }); setOpen(false) } }
  const thisMonth = new Date(new Date().getFullYear(), new Date().getMonth(), 1)
  const monthName = month.toLocaleDateString('pt-BR', { month: 'long', year: 'numeric' })
  return (
    <div ref={box} className="relative">
      <button type="button" onClick={() => setOpen(!open)} aria-haspopup="dialog" aria-expanded={open}
        className="inline-flex items-center gap-2 rounded-lg border border-border bg-surface px-3 py-2 text-sm font-medium hover:bg-bg">
        <svg aria-hidden width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" className="text-muted"><rect x="3" y="4" width="18" height="18" rx="2" /><path d="M16 2v4M8 2v4M3 10h18" /></svg>
        <span>{periodLabel(value)}</span>
        {value?.preset !== 'custom' && value && <span className="hidden text-xs font-normal text-muted sm:inline">{value.from === value.to ? br(value.from) : `${br(value.from).slice(0, 5)} a ${br(value.to).slice(0, 5)}`}</span>}
        <svg aria-hidden width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" className="text-muted"><path d="m6 9 6 6 6-6" /></svg>
      </button>
      {open && (
        <div role="dialog" aria-label="Escolher o período" className={`absolute ${align === 'left' ? 'left-0' : 'right-0'} z-30 mt-2 flex w-[min(92vw,600px)] flex-col overflow-hidden rounded-xl max-sm:fixed max-sm:inset-x-3 max-sm:top-16 max-sm:mt-0 max-sm:max-h-[calc(100vh-5rem)] max-sm:w-auto max-sm:overflow-y-auto border border-border bg-surface shadow-xl sm:flex-row`}>
          <ul className="flex flex-wrap gap-1 border-b border-border p-2 sm:w-44 sm:flex-col sm:flex-nowrap sm:border-b-0 sm:border-r">
            {allowAll && (
              <li><button type="button" onClick={() => { onChange(null); setOpen(false) }} className={`flex w-full items-center justify-between rounded-md px-2.5 py-1.5 text-left text-sm hover:bg-bg ${!value ? 'font-semibold' : ''}`}>Todo o período {!value && <Check />}</button></li>
            )}
            {PRESETS.map((p) => (
              <li key={p.id}><button type="button" onClick={() => { onChange(presetPeriod(p.id)); setOpen(false) }}
                className={`flex w-full items-center justify-between gap-2 rounded-md px-2.5 py-1.5 text-left text-sm hover:bg-bg ${value?.preset === p.id ? 'font-semibold' : ''}`}>{p.label} {value?.preset === p.id && <Check />}</button></li>
            ))}
          </ul>
          <div className="flex-1 p-3">
            <div className="mb-2 flex items-center justify-between">
              <button type="button" aria-label="Mês anterior" onClick={() => setMonth(new Date(month.getFullYear(), month.getMonth() - 1, 1))} className="flex h-8 w-8 items-center justify-center rounded-lg hover:bg-bg">‹</button>
              <span className="text-sm font-semibold first-letter:uppercase">{monthName}</span>
              <button type="button" aria-label="Próximo mês" disabled={month >= thisMonth} onClick={() => setMonth(new Date(month.getFullYear(), month.getMonth() + 1, 1))} className="flex h-8 w-8 items-center justify-center rounded-lg hover:bg-bg disabled:opacity-30">›</button>
            </div>
            <Calendar month={month} from={from} to={to} hover={hover} onPick={pick} onHover={setHover} />
            <div className="mt-3 flex flex-wrap items-center justify-between gap-2 border-t border-border pt-3 text-sm">
              <span className={tooLong ? 'text-danger' : 'text-muted'}>
                {!lo ? 'Clique no dia inicial e depois no final.' : tooLong ? `Escolha até ${MAX_DAYS} dias (${days} marcados).` : !to ? `De ${br(lo)}: agora clique no dia final (ou aplique só este dia).` : `${lo === hi ? br(lo) : `${br(lo)} a ${br(hi)}`} · ${days} dia${days > 1 ? 's' : ''}`}
              </span>
              <button type="button" disabled={!lo || tooLong} onClick={apply} className="rounded-lg bg-primary px-3.5 py-1.5 font-medium text-primary-contrast disabled:opacity-40">Aplicar</button>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}

function Check() {
  return <svg aria-hidden width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="3" strokeLinecap="round" strokeLinejoin="round" className="text-primary"><path d="M20 6 9 17l-5-5" /></svg>
}
