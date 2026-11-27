// Gráficos do painel, em SVG puro: colunas empilhadas por estado, área de uma série e medidores. Marcas finas, grade
// recessiva, legenda sempre que houver mais de uma série, dica ao passar o mouse (ou focar pelo teclado) e a tabela
// com os mesmos números logo abaixo.
import { ReactNode, useEffect, useRef, useState } from 'react'

export const STATUS_SERIES = [
  { key: 'ok', label: 'Sucesso', color: 'var(--viz-good)' },
  { key: 'warning', label: 'Com avisos', color: 'var(--viz-warn)' },
  { key: 'failed', label: 'Falhas', color: 'var(--viz-crit)' },
  { key: 'missed', label: 'Perdidos', color: 'var(--viz-serious)' },
] as const

export type Day = { date: string; ok: number; warning: number; failed: number; missed: number; canceled: number; bytes: number }

const dayLabel = (d: string) => `${d.slice(8, 10)}/${d.slice(5, 7)}`
const weekday = (d: string) => { const s = new Date(`${d}T12:00:00`).toLocaleDateString('pt-BR', { weekday: 'long', day: '2-digit', month: '2-digit' }); return s.charAt(0).toUpperCase() + s.slice(1) }
export const finished = (d: Day) => d.ok + d.warning + d.failed + d.missed

function useWidth<T extends HTMLElement>() {
  const ref = useRef<T>(null)
  const [w, setW] = useState(600)
  useEffect(() => {
    const el = ref.current
    if (!el) return
    const ro = new ResizeObserver(([e]) => setW(Math.max(260, Math.floor(e.contentRect.width))))
    ro.observe(el)
    return () => ro.disconnect()
  }, [])
  return [ref, w] as const
}

/** Topo "redondo" do eixo (1, 2 ou 5 × 10ⁿ) e o passo das marcas. */
export function niceScale(max: number, ticks = 4) {
  if (max <= 0) return { top: ticks, step: 1 }
  const raw = max / ticks
  const mag = 10 ** Math.floor(Math.log10(raw))
  const step = [1, 2, 5, 10].map((m) => m * mag).find((s) => s >= raw) ?? raw
  return { top: step * Math.ceil(max / step), step }
}

export function Legend({ items }: { items: { label: string; color: string }[] }) {
  return (
    <ul className="flex flex-wrap gap-x-4 gap-y-1 text-xs text-muted" aria-label="Legenda">
      {items.map((i) => <li key={i.label} className="flex items-center gap-1.5"><span aria-hidden className="inline-block h-2.5 w-2.5 rounded-[3px]" style={{ background: i.color }} />{i.label}</li>)}
    </ul>
  )
}

function Tip({ x, y, w, children }: { x: number; y: number; w: number; children: ReactNode }) {
  const left = Math.min(Math.max(x - 90, 0), w - 180)
  return (
    <div role="status" className="pointer-events-none absolute z-10 w-[180px] rounded-lg border border-border bg-surface px-3 py-2 text-xs shadow-lg" style={{ left, top: Math.max(y - 8, 0), transform: 'translateY(-100%)' }}>
      {children}
    </div>
  )
}

const H = 220, PAD_L = 34, PAD_B = 22, PAD_T = 14

/** Colunas empilhadas: execuções por dia, por estado. */
export function StatusColumns({ days }: { days: Day[] }) {
  const [ref, w] = useWidth<HTMLDivElement>()
  const [hover, setHover] = useState<number | null>(null)
  const max = Math.max(0, ...days.map(finished))
  const { top, step } = niceScale(max)
  const plotW = w - PAD_L, plotH = H - PAD_B - PAD_T
  const band = plotW / days.length
  const bw = Math.min(24, band * 0.62)
  const y = (v: number) => PAD_T + plotH - (v / top) * plotH
  const ticks: number[] = []
  for (let v = 0; v <= top + 1e-9; v += step) ticks.push(v)
  const every = Math.ceil(days.length / Math.max(1, Math.floor(plotW / 52))) // rótulos do eixo que cabem
  const peak = days.reduce((m, d, i) => (finished(d) > finished(days[m]) ? i : m), 0)
  const h = hover !== null ? days[hover] : null
  return (
    <div ref={ref} className="relative w-full min-w-0" onMouseLeave={() => setHover(null)}>
      <svg width={w} height={H} role="img" aria-label={`Execuções por dia nos últimos ${days.length} dias. Máximo de ${max} em um dia.`} className="block overflow-visible">
        {ticks.map((v) => (
          <g key={v}>
            <line x1={PAD_L} x2={w} y1={y(v)} y2={y(v)} stroke={v === 0 ? 'var(--viz-axis)' : 'var(--viz-grid)'} strokeWidth={1} shapeRendering="crispEdges" />
            <text x={PAD_L - 6} y={y(v)} dy="0.32em" textAnchor="end" className="fill-muted text-[11px] tabular-nums">{v}</text>
          </g>
        ))}
        {days.map((d, i) => {
          const cx = PAD_L + band * i + band / 2
          let base = y(0)
          const segs = STATUS_SERIES.map((s) => ({ ...s, n: d[s.key] })).filter((s) => s.n > 0)
          const dim = hover !== null && hover !== i
          return (
            <g key={d.date} opacity={dim ? 0.45 : 1}>
              {segs.map((s, k) => {
                const hgt = (s.n / top) * plotH
                const yTop = base - hgt
                const last = k === segs.length - 1
                const gap = k > 0 ? 2 : 0 // 2 px do fundo entre segmentos
                const r = last ? Math.min(4, (hgt - gap) / 2) : 0
                const x0 = cx - bw / 2, x1 = cx + bw / 2, yb = base - gap
                const path = last
                  ? `M${x0},${yb} V${yTop + r} Q${x0},${yTop} ${x0 + r},${yTop} H${x1 - r} Q${x1},${yTop} ${x1},${yTop + r} V${yb} Z`
                  : `M${x0},${yb} V${yTop} H${x1} V${yb} Z`
                base = yTop
                return <path key={s.key} d={path} fill={s.color} />
              })}
              {(i === peak || i === days.length - 1) && finished(d) > 0 && (
                <text x={cx} y={base - 5} textAnchor="middle" className="fill-text text-[11px] font-semibold">{finished(d)}</text>
              )}
              {(i % every === 0 || i === days.length - 1) && (days.length - 1 - i >= every || i === days.length - 1) && (
                <text x={cx} y={H - 6} textAnchor="middle" className="fill-muted text-[11px]">{dayLabel(d.date)}</text>
              )}
              <rect x={PAD_L + band * i} y={PAD_T} width={band} height={plotH} fill="transparent" tabIndex={0}
                aria-label={`${weekday(d.date)}: ${STATUS_SERIES.map((s) => `${d[s.key]} ${s.label.toLowerCase()}`).join(', ')}`}
                onMouseEnter={() => setHover(i)} onFocus={() => setHover(i)} onBlur={() => setHover(null)} className="outline-none focus-visible:stroke-primary" />
            </g>
          )
        })}
      </svg>
      {h && hover !== null && (
        <Tip x={PAD_L + band * hover + band / 2} y={y(finished(h))} w={w}>
          <div className="mb-1 font-semibold">{weekday(h.date)}</div>
          {STATUS_SERIES.map((s) => (
            <div key={s.key} className="flex items-center justify-between gap-2">
              <span className="flex items-center gap-1.5 text-muted"><span className="inline-block h-2 w-2 rounded-[2px]" style={{ background: s.color }} />{s.label}</span>
              <span className="tabular-nums">{h[s.key]}</span>
            </div>
          ))}
          {h.canceled > 0 && <div className="mt-1 text-muted">{h.canceled} cancelada(s)</div>}
          <div className="mt-1 flex justify-between border-t border-border pt-1 font-medium"><span>Total</span><span className="tabular-nums">{finished(h)}</span></div>
        </Tip>
      )}
    </div>
  )
}

/** Área de uma série (volume gerado por dia). */
export function VolumeArea({ days, fmt }: { days: Day[]; fmt: (n: number) => string }) {
  const [ref, w] = useWidth<HTMLDivElement>()
  const [hover, setHover] = useState<number | null>(null)
  const max = Math.max(0, ...days.map((d) => d.bytes))
  // escala em GiB/MiB redondos: o passo é calculado na unidade que o maior valor usa
  const unit = max >= 2 ** 30 ? 2 ** 30 : max >= 2 ** 20 ? 2 ** 20 : 2 ** 10
  const { top: topU, step: stepU } = niceScale(max / unit)
  const top = topU * unit, step = stepU * unit
  const plotW = w - PAD_L - 8, plotH = H - PAD_B - PAD_T
  const x = (i: number) => PAD_L + (days.length === 1 ? plotW / 2 : (plotW * i) / (days.length - 1))
  const y = (v: number) => PAD_T + plotH - (v / top) * plotH
  const pts = days.map((d, i) => `${x(i)},${y(d.bytes)}`).join(' ')
  const ticks: number[] = []
  for (let v = 0; v <= top + 1; v += step) ticks.push(v)
  const tickLabel = (v: number) => (v === 0 ? '0' : `${+(v / unit).toFixed(1)} ${unit === 2 ** 30 ? 'GiB' : unit === 2 ** 20 ? 'MiB' : 'KiB'}`)
  const every = Math.ceil(days.length / Math.max(1, Math.floor(plotW / 56)))
  const last = days.length - 1
  const h = hover !== null ? days[hover] : null
  function move(e: React.MouseEvent<SVGRectElement>) {
    const r = e.currentTarget.getBoundingClientRect()
    const rel = (e.clientX - r.left) / r.width
    setHover(Math.max(0, Math.min(last, Math.round(rel * last))))
  }
  return (
    <div ref={ref} className="relative w-full min-w-0" onMouseLeave={() => setHover(null)}>
      <svg width={w} height={H} role="img" aria-label={`Volume gerado por dia. Maior: ${fmt(max)}.`} className="block overflow-visible">
        {ticks.map((v) => (
          <g key={v}>
            <line x1={PAD_L} x2={w - 8} y1={y(v)} y2={y(v)} stroke={v === 0 ? 'var(--viz-axis)' : 'var(--viz-grid)'} strokeWidth={1} shapeRendering="crispEdges" />
            <text x={PAD_L - 6} y={y(v)} dy="0.32em" textAnchor="end" className="fill-muted text-[11px] tabular-nums">{tickLabel(v)}</text>
          </g>
        ))}
        {max > 0 && <>
          <polygon points={`${x(0)},${y(0)} ${pts} ${x(last)},${y(0)}`} fill="var(--viz-series)" opacity={0.1} />
          <polyline points={pts} fill="none" stroke="var(--viz-series)" strokeWidth={2} strokeLinejoin="round" strokeLinecap="round" />
          <circle cx={x(last)} cy={y(days[last].bytes)} r={4} fill="var(--viz-series)" stroke="var(--surface)" strokeWidth={2} />
          <text x={x(last)} y={y(days[last].bytes) - 10} textAnchor="end" className="fill-text text-[11px] font-semibold">{fmt(days[last].bytes)}</text>
        </>}
        {days.map((d, i) => (i % every === 0 || i === last) && (last - i >= every || i === last) && (
          <text key={d.date} x={x(i)} y={H - 6} textAnchor={i === last ? 'end' : 'middle'} className="fill-muted text-[11px]">{dayLabel(d.date)}</text>
        ))}
        {h && hover !== null && <>
          <line x1={x(hover)} x2={x(hover)} y1={PAD_T} y2={y(0)} stroke="var(--viz-axis)" strokeWidth={1} />
          <circle cx={x(hover)} cy={y(h.bytes)} r={4} fill="var(--viz-series)" stroke="var(--surface)" strokeWidth={2} />
        </>}
        <rect x={PAD_L} y={PAD_T} width={plotW} height={plotH} fill="transparent" onMouseMove={move} tabIndex={0}
          aria-label="Volume por dia: use as setas para percorrer" onFocus={() => setHover(last)} onBlur={() => setHover(null)} className="outline-none"
          onKeyDown={(e) => { if (e.key === 'ArrowLeft') setHover((v) => Math.max(0, (v ?? last) - 1)); if (e.key === 'ArrowRight') setHover((v) => Math.min(last, (v ?? 0) + 1)) }} />
      </svg>
      {h && hover !== null && (
        <Tip x={x(hover)} y={y(h.bytes)} w={w}>
          <div className="mb-1 font-semibold">{weekday(h.date)}</div>
          <div className="flex justify-between"><span className="text-muted">Volume gerado</span><span className="tabular-nums">{h.bytes ? fmt(h.bytes) : '0'}</span></div>
          <div className="flex justify-between"><span className="text-muted">Backups concluídos</span><span className="tabular-nums">{h.ok + h.warning}</span></div>
        </Tip>
      )}
    </div>
  )
}

/** Medidor: a cor carrega a gravidade; o trilho é um tom claro da mesma cor. */
export function Meter({ frac, label, thin }: { frac: number; label: string; thin?: boolean }) {
  const color = frac >= 0.9 ? 'var(--viz-crit)' : frac >= 0.75 ? 'var(--viz-warn)' : 'var(--viz-series)'
  return (
    <div role="meter" aria-valuemin={0} aria-valuemax={100} aria-valuenow={Math.round(frac * 100)} aria-label={label}
      className={`w-full overflow-hidden rounded-full ${thin ? 'h-1.5' : 'h-2'}`} style={{ background: `color-mix(in srgb, ${color} 18%, var(--surface))` }}>
      <div className="h-full rounded-full" style={{ width: `${Math.max(frac > 0 ? 2 : 0, Math.min(100, frac * 100))}%`, background: color }} />
    </div>
  )
}
