import { ReactNode, useEffect, useState } from 'react'
import { CLOUD_LABEL, STATUS, api, fmtBytes } from '../api'
import { Badge, Button, Card } from '../ui'
import Icon from '../Icon'
import { Day, Legend, Meter, STATUS_SERIES, StatusColumns, VolumeArea, finished } from '../Charts'
import PeriodPicker, { Period, br, periodShort, presetPeriod, ymd } from '../Period'

type Totals = { runs: number; ok: number; warning: number; failed: number; missed: number; canceled: number; bytes: number }
type JobHealth = { id: number; name: string; agent: string; enabled: boolean; runs: number; good: number; bytes: number; last: string; last_at: string | null; last_size: number; last_secs: number; storage: string }
type StorageUse = { name: string; type: string; status: string; used: number; total: number; jobs: number }
type Overview = { days: number; from: string; to: string; history: Day[]; totals: Totals; previous: Totals; jobs: JobHealth[] | null; storages: StorageUse[] | null; agents_total: number; agents_online: number; running: number }
type Health = { status: string; database: boolean }

const rateOf = (t: Totals) => { const n = t.ok + t.warning + t.failed + t.missed; return n ? (t.ok + t.warning) / n : null }
const pct = (v: number) => `${Math.floor(v * 1000) / 10}%`.replace('.', ',')

function ago(s: string | null) {
  if (!s) return 'nunca'
  const m = Math.round((Date.now() - new Date(s).getTime()) / 60000)
  if (m < 1) return 'agora'
  if (m < 60) return `há ${m} min`
  const h = Math.round(m / 60)
  if (h < 48) return `há ${h} h`
  return `há ${Math.round(h / 24)} dias`
}

function dur(s: number) {
  if (!s) return '—'
  if (s < 60) return `${s}s`
  if (s < 3600) return `${Math.floor(s / 60)}min ${s % 60}s`
  return `${Math.floor(s / 3600)}h ${Math.floor((s % 3600) / 60)}min`
}

function Tile({ label, value, children, dot }: { label: string; value: string; children?: ReactNode; dot?: 'ok' | 'danger' | 'warn' }) {
  return (
    <div className="min-w-0 rounded-2xl border border-border bg-surface p-4 shadow-sm">
      <div className="text-sm text-muted">{label}</div>
      <div className="mt-1 flex items-center gap-2 text-2xl font-semibold">
        {dot && <span aria-hidden className="inline-block h-2.5 w-2.5 rounded-full" style={{ background: { ok: 'var(--viz-good)', danger: 'var(--viz-crit)', warn: 'var(--viz-warn)' }[dot] }} />}
        {value}
      </div>
      {children && <div className="mt-1 text-xs text-muted">{children}</div>}
    </div>
  )
}

/** Variação contra o período anterior: a cor diz se a mudança é boa ou ruim. */
function Delta({ now, before, unit, upIsGood = true, fmt }: { now: number | null; before: number | null; unit: string; upIsGood?: boolean; fmt: (v: number) => string }) {
  if (now === null || before === null) return <>sem dados do período anterior</>
  const d = now - before
  if (Math.abs(d) < 1e-9) return <>igual ao período anterior</>
  const good = d > 0 === upIsGood
  return <><span className={good ? 'text-ok' : 'text-danger'}>{d > 0 ? '▲' : '▼'} {fmt(Math.abs(d))}{unit}</span> vs. período anterior</>
}

function RateBar({ good, runs }: { good: number; runs: number }) {
  if (!runs) return <span className="text-xs text-muted">sem execuções</span>
  const f = good / runs
  const color = f >= 1 ? 'var(--viz-good)' : f >= 0.8 ? 'var(--viz-warn)' : 'var(--viz-crit)'
  return (
    <div className="flex items-center gap-2">
      <div className="h-1.5 w-20 overflow-hidden rounded-full" style={{ background: `color-mix(in srgb, ${color} 18%, var(--surface))` }} role="meter" aria-valuenow={Math.round(f * 100)} aria-valuemin={0} aria-valuemax={100} aria-label="Taxa de sucesso">
        <div className="h-full rounded-full" style={{ width: `${f * 100}%`, background: color }} />
      </div>
      <span className="text-xs tabular-nums">{good}/{runs}</span>
    </div>
  )
}

function ChartCard({ title, sub, legend, children, table }: { title: string; sub?: string; legend?: ReactNode; children: ReactNode; table: ReactNode }) {
  return (
    <Card className="min-w-0">
      <div className="mb-3 flex flex-wrap items-start justify-between gap-2">
        <div><h2 className="font-semibold">{title}</h2>{sub && <p className="text-xs text-muted">{sub}</p>}</div>
        {legend}
      </div>
      {children}
      <details className="mt-2 text-sm">
        <summary className="cursor-pointer text-xs text-muted hover:text-text">Ver em tabela</summary>
        <div className="mt-2 max-h-64 overflow-auto">{table}</div>
      </details>
    </Card>
  )
}

export default function Dashboard({ name, canReport }: { name: string; canReport: boolean }) {
  // atalhos são recalculados a cada dia; um período personalizado fica como foi escolhido
  const [period, setPeriod] = useState<Period>(() => {
    try {
      const p = JSON.parse(localStorage.getItem('coruja.painel.periodo') ?? 'null') as Period | null
      if (p?.preset === 'custom' && p.from && p.to) return p
      if (p?.preset) return presetPeriod(p.preset)
    } catch { /* sem armazenamento */ }
    return presetPeriod('14')
  })
  const [ov, setOv] = useState<Overview | null>(null)
  const [health, setHealth] = useState<Health | null>(null)
  const [err, setErr] = useState('')
  useEffect(() => {
    let live = true
    const tz = Intl.DateTimeFormat().resolvedOptions().timeZone
    const load = () => {
      const cur = period.preset === 'custom' ? period : presetPeriod(period.preset) // virada do dia com a página aberta
      api<Overview>('GET', `/api/dashboard?from=${cur.from}&to=${cur.to}&tz=${encodeURIComponent(tz)}`).then((r) => { if (live) { setOv(r); setErr('') } }).catch((x) => live && setErr((x as Error).message))
      fetch('/api/health').then((r) => r.json()).then((h) => live && setHealth(h)).catch(() => live && setHealth(null))
    }
    load()
    const t = setInterval(load, 15000)
    return () => { live = false; clearInterval(t) }
  }, [period])
  function pick(p: Period | null) { if (!p) return; setPeriod(p); try { localStorage.setItem('coruja.painel.periodo', JSON.stringify(p)) } catch { /* sem armazenamento */ } }
  const label = periodShort(period)

  const t = ov?.totals, prev = ov?.previous
  const rate = t ? rateOf(t) : null
  const bad = t ? t.failed + t.missed : 0
  const jobs = ov?.jobs ?? []
  const storages = ov?.storages ?? []
  const attention = jobs.filter((j) => j.enabled && (j.last === 'failed' || j.last === 'missed'))
  const sys = [
    { label: 'Server', ok: health?.status === 'ok', text: health ? (health.status === 'ok' ? 'online' : 'degradado') : 'sem resposta' },
    { label: 'Banco de dados', ok: !!health?.database, text: health ? (health.database ? 'conectado' : 'indisponível') : '…' },
    { label: 'Agentes', ok: !!ov && ov.agents_online === ov.agents_total, text: ov ? `${ov.agents_online} de ${ov.agents_total} online` : '…' },
  ]
  return (
    <div className="space-y-5">
      <div className="flex flex-wrap items-end justify-between gap-3">
        <div>
          <h1 className="text-2xl font-semibold">Painel</h1>
          <p className="mt-1 text-sm text-muted">Visão geral do {name}.</p>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <PeriodPicker value={period} onChange={pick} />
          {canReport && <a href="/api/reports/daily.pdf" target="_blank" rel="noopener noreferrer"><Button variant="ghost" className="gap-2"><Icon name="pdf" />Relatório de hoje (PDF)</Button></a>}
        </div>
      </div>

      <ul className="flex flex-wrap gap-x-5 gap-y-1 text-sm" aria-label="Estado do sistema">
        {sys.map((s) => (
          <li key={s.label} className="flex items-center gap-1.5">
            <span aria-hidden className="inline-block h-2 w-2 rounded-full" style={{ background: s.ok ? 'var(--viz-good)' : 'var(--viz-crit)' }} />
            <span className="text-muted">{s.label}:</span> {s.text}
          </li>
        ))}
        {!!ov?.running && <li className="flex items-center gap-1.5"><span aria-hidden className="inline-block h-2 w-2 animate-pulse rounded-full bg-primary" /><span className="text-muted">Em execução:</span> {ov.running}</li>}
      </ul>

      {err && <div className="rounded-lg border border-danger/40 bg-danger/10 px-3 py-2 text-sm text-danger">{err}</div>}

      {ov && t && prev && (<>
        <section className="grid gap-4 sm:grid-cols-2 xl:grid-cols-4" aria-label="Indicadores do período">
          <Tile label={`Taxa de sucesso (${label})`} value={rate === null ? '—' : pct(rate)} dot={rate === null ? undefined : rate === 1 ? 'ok' : rate >= 0.9 ? 'warn' : 'danger'}>
            <Delta now={rate === null ? null : rate * 100} before={rateOf(prev) === null ? null : rateOf(prev)! * 100} unit=" p.p." fmt={(v) => v.toFixed(1).replace('.', ',')} />
          </Tile>
          <Tile label={`Execuções (${label})`} value={String(t.runs)}>
            {t.ok + t.warning} concluídas · {t.canceled} canceladas
          </Tile>
          <Tile label="Falhas e perdidos" value={String(bad)} dot={bad ? 'danger' : 'ok'}>
            <Delta now={bad} before={prev.failed + prev.missed} unit="" upIsGood={false} fmt={(v) => String(v)} />
          </Tile>
          <Tile label={`Volume gerado (${label})`} value={t.bytes ? fmtBytes(t.bytes) : '0'}>
            <Delta now={t.bytes} before={prev.runs ? prev.bytes : null} unit="" fmt={fmtBytes} />
          </Tile>
        </section>

        {(() => {
          const f = new Date(`${ov.from}T12:00:00`)
          const pf = new Date(f); pf.setDate(f.getDate() - ov.days)
          const pt = new Date(f); pt.setDate(f.getDate() - 1)
          return <p className="-mt-2 text-xs text-muted">Comparações com o período anterior de mesmo tamanho: {br(ymd(pf))} a {br(ymd(pt))}.</p>
        })()}

        {attention.length > 0 && (
          <div className="flex items-start gap-3 rounded-xl border border-danger/40 bg-danger/10 px-4 py-3 text-sm">
            <span className="mt-0.5 text-danger" aria-hidden>●</span>
            <div><b>Precisa de atenção:</b> a última execução de {attention.map((j, i) => <span key={j.id}>{i > 0 && ', '}<a className="font-medium underline" href="#/historico">{j.name}</a> ({STATUS[j.last]?.label.toLowerCase()})</span>)} não terminou bem.</div>
          </div>
        )}

        <div className="grid gap-4 xl:grid-cols-2">
          <ChartCard title="Execuções por dia" sub={`Pelo dia de início, no seu fuso horário · ${ov.history.length} dia${ov.history.length > 1 ? 's' : ''}`} legend={<Legend items={STATUS_SERIES.map((s) => ({ label: s.label, color: s.color }))} />}
            table={<table className="w-full text-xs"><thead className="text-muted"><tr><th className="py-1 text-left">Dia</th>{STATUS_SERIES.map((s) => <th key={s.key} className="py-1 text-right">{s.label}</th>)}<th className="py-1 text-right">Total</th></tr></thead>
              <tbody className="tabular-nums">{[...ov.history].reverse().map((d) => <tr key={d.date} className="border-t border-border"><td className="py-1">{d.date.split('-').reverse().join('/')}</td>{STATUS_SERIES.map((s) => <td key={s.key} className="py-1 text-right">{d[s.key]}</td>)}<td className="py-1 text-right font-medium">{finished(d)}</td></tr>)}</tbody></table>}>
            <StatusColumns days={ov.history} />
          </ChartCard>
          <ChartCard title="Volume gerado por dia" sub="Tamanho dos backups concluídos (compactados)"
            table={<table className="w-full text-xs"><thead className="text-muted"><tr><th className="py-1 text-left">Dia</th><th className="py-1 text-right">Volume</th><th className="py-1 text-right">Concluídos</th></tr></thead>
              <tbody className="tabular-nums">{[...ov.history].reverse().map((d) => <tr key={d.date} className="border-t border-border"><td className="py-1">{d.date.split('-').reverse().join('/')}</td><td className="py-1 text-right">{d.bytes ? fmtBytes(d.bytes) : '0'}</td><td className="py-1 text-right">{d.ok + d.warning}</td></tr>)}</tbody></table>}>
            <VolumeArea days={ov.history} fmt={fmtBytes} />
          </ChartCard>
        </div>

        <div className="grid gap-4 xl:grid-cols-3">
          <Card className="min-w-0 overflow-x-auto p-0 xl:col-span-2">
            <div className="flex items-baseline justify-between px-5 pt-5"><h2 className="font-semibold">Saúde dos jobs</h2><span className="text-xs text-muted">sucesso no período ({label})</span></div>
            {jobs.length === 0 ? <p className="p-5 text-sm text-muted">Nenhum job cadastrado. Crie o primeiro em <a className="text-primary underline" href="#/jobs">Jobs</a>.</p> : (
              <table className="mt-3 w-full text-sm">
                <thead className="border-y border-border text-left text-xs text-muted"><tr><th className="px-5 py-2">Job</th><th className="px-3 py-2">Última</th><th className="px-3 py-2">Sucesso</th><th className="px-3 py-2 text-right">Tamanho</th><th className="px-3 py-2 text-right">Duração</th><th className="px-5 py-2">Storage</th></tr></thead>
                <tbody>{jobs.map((j) => (
                  <tr key={j.id} className="border-b border-border last:border-0">
                    <td className="px-5 py-2.5"><div className="font-medium">{j.name}</div><div className="text-xs text-muted">{j.agent}{!j.enabled && ' · pausado'}</div></td>
                    <td className="px-3 py-2.5">{j.last ? <div className="space-y-0.5"><Badge tone={STATUS[j.last]?.tone ?? 'off'}>{STATUS[j.last]?.label ?? j.last}</Badge><div className="text-xs text-muted">{ago(j.last_at)}</div></div> : <span className="text-xs text-muted">nunca rodou</span>}</td>
                    <td className="px-3 py-2.5"><RateBar good={j.good} runs={j.runs} /></td>
                    <td className="px-3 py-2.5 text-right tabular-nums">{fmtBytes(j.last_size)}</td>
                    <td className="px-3 py-2.5 text-right tabular-nums">{dur(j.last_secs)}</td>
                    <td className="px-5 py-2.5 text-xs text-muted">{j.storage || 'só local'}</td>
                  </tr>))}
                </tbody>
              </table>
            )}
          </Card>
          <Card className="min-w-0">
            <h2 className="font-semibold">Storages</h2>
            {storages.length === 0 ? <p className="mt-2 text-sm text-muted">Nenhum storage. Cadastre em <a className="text-primary underline" href="#/nuvem">Storages</a>.</p> : (
              <ul className="mt-3 space-y-4">
                {storages.map((s) => {
                  const frac = s.total ? s.used / s.total : 0
                  return (
                    <li key={s.name}>
                      <div className="flex items-baseline justify-between gap-2 text-sm">
                        <span className="min-w-0 truncate font-medium">{s.name}</span>
                        <span className="shrink-0 text-xs text-muted">{s.total ? `${Math.round(frac * 100)}% em uso` : CLOUD_LABEL[s.type as keyof typeof CLOUD_LABEL] ?? s.type}</span>
                      </div>
                      {s.total > 0 && <div className="mt-1.5"><Meter frac={frac} label={`Uso de ${s.name}`} /></div>}
                      <div className="mt-1 text-xs text-muted">
                        {s.total > 0 ? `${fmtBytes(s.used)} de ${fmtBytes(s.total)} · ` : ''}{s.jobs} job(s){s.status === 'error' && <span className="text-danger"> · com erro</span>}
                      </div>
                    </li>
                  )
                })}
              </ul>
            )}
          </Card>
        </div>
      </>)}
      {ov && ov.agents_total === 0 && (
        <Card>
          <b>Comece adicionando um agente.</b>
          <p className="mt-1 text-sm text-muted">Vá em <a className="text-primary underline" href="#/agentes">Agentes</a> e use o assistente Novo agente.</p>
        </Card>
      )}
    </div>
  )
}
