import { useCallback, useEffect, useState } from 'react'
import { Job, Run, RunLog, User, api, ApiError, fmtBytes, fmtDate, fmtDuration, STATUS } from '../api'
import { Badge, Button, Card, ErrorBox, Modal } from '../ui'

export function StatusBadge({ s }: { s: string }) {
  const v = STATUS[s] ?? { label: s, tone: 'off' as const }
  return <Badge tone={v.tone}>{v.label}</Badge>
}

export default function Runs({ me, jobFilter }: { me: User; jobFilter: number }) {
  const [runs, setRuns] = useState<Run[]>([])
  const [jobs, setJobs] = useState<Job[]>([])
  const [status, setStatus] = useState('')
  const [job, setJob] = useState(jobFilter)
  const [err, setErr] = useState('')
  const [open, setOpen] = useState<number | null>(null)

  const load = useCallback(() => {
    const q = new URLSearchParams({ limit: '200' })
    if (status) q.set('status', status)
    if (job) q.set('job_id', String(job))
    api<{ runs: Run[] }>('GET', `/api/runs?${q}`).then((r) => { setRuns(r.runs); setErr('') }).catch((x) => setErr((x as ApiError).message))
  }, [status, job])
  useEffect(() => { load(); const t = setInterval(load, 5000); return () => clearInterval(t) }, [load])
  useEffect(() => { api<{ jobs: Job[] }>('GET', '/api/jobs').then((r) => setJobs(r.jobs)).catch(() => {}) }, [])
  useEffect(() => setJob(jobFilter), [jobFilter])

  const sel = 'rounded-md border border-border bg-surface px-2 py-1.5 text-sm'
  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold">Histórico</h1>
        <p className="text-sm text-muted">Cada execução de backup, com o log completo.</p>
      </div>
      <div className="flex flex-wrap gap-3">
        <select aria-label="Filtrar por job" className={sel} value={job} onChange={(e) => setJob(Number(e.target.value))}>
          <option value={0}>Todos os jobs</option>
          {jobs.map((j) => <option key={j.id} value={j.id}>{j.name}</option>)}
        </select>
        <select aria-label="Filtrar por estado" className={sel} value={status} onChange={(e) => setStatus(e.target.value)}>
          <option value="">Todos os estados</option>
          {Object.entries(STATUS).map(([k, v]) => <option key={k} value={k}>{v.label}</option>)}
        </select>
      </div>
      <ErrorBox message={err} />
      <Card className="overflow-x-auto p-0">
        <table className="w-full text-sm">
          <thead className="border-b border-border text-left text-muted">
            <tr><th className="p-3">Quando</th><th className="p-3">Job</th><th className="p-3">Estado</th><th className="p-3">Duração</th><th className="p-3">Tamanho</th><th className="p-3">Itens</th></tr>
          </thead>
          <tbody>
            {runs.length === 0 && <tr><td colSpan={6} className="p-6 text-center text-muted">Nenhuma execução encontrada.</td></tr>}
            {runs.map((r) => (
              <tr key={r.id} className="cursor-pointer border-b border-border last:border-0 hover:bg-bg" onClick={() => setOpen(r.id)}>
                <td className="p-3 whitespace-nowrap">{fmtDate(r.started_at ?? r.scheduled_for ?? r.created_at)}<div className="text-xs text-muted">{r.trigger === 'manual' ? 'manual' : 'agendado'}</div></td>
                <td className="p-3"><b>{r.job_name}</b><div className="text-xs text-muted">{r.agent_name}</div></td>
                <td className="p-3"><StatusBadge s={r.status} /></td>
                <td className="p-3 text-muted">{fmtDuration(r.started_at, r.finished_at)}</td>
                <td className="p-3 text-muted">{fmtBytes(r.bytes_output)}</td>
                <td className="p-3 text-muted">{r.files || '—'}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </Card>
      {open !== null && <Modal title="Detalhe da execução" onClose={() => setOpen(null)}><Detail id={open} me={me} onChange={load} /></Modal>}
    </div>
  )
}

function Detail({ id, me, onChange }: { id: number; me: User; onChange: () => void }) {
  const [d, setD] = useState<{ run: Run; logs: RunLog[] } | null>(null)
  const [err, setErr] = useState('')
  const load = useCallback(() => { api<{ run: Run; logs: RunLog[] }>('GET', `/api/runs/${id}`).then(setD).catch((x) => setErr((x as ApiError).message)) }, [id])
  useEffect(() => { load(); const t = setInterval(load, 4000); return () => clearInterval(t) }, [load])
  if (!d) return <p className="text-muted">{err || 'Carregando…'}</p>
  const r = d.run
  const live = r.status === 'running' || r.status === 'queued'
  const color = (l: string) => (l === 'error' ? 'text-danger' : l === 'warn' ? 'text-accent' : 'text-text')
  return (
    <div className="space-y-4 text-sm">
      <div className="flex flex-wrap items-center gap-3">
        <b className="text-base">{r.job_name}</b><StatusBadge s={r.status} />
        <span className="text-muted">{r.agent_name}</span>
        {live && me.role !== 'viewer' && <Button variant="danger" className="ml-auto" onClick={() => confirm('Cancelar este backup?') && api('POST', `/api/runs/${r.id}/cancel`).then(() => { onChange(); load() }).catch((x) => setErr((x as ApiError).message))}>Cancelar</Button>}
      </div>
      <ErrorBox message={err || (r.status === 'failed' || r.status === 'missed' ? r.error : '')} />
      <dl className="grid grid-cols-2 gap-x-4 gap-y-2 sm:grid-cols-3">
        <Item k="Início" v={fmtDate(r.started_at)} /><Item k="Fim" v={fmtDate(r.finished_at)} /><Item k="Duração" v={fmtDuration(r.started_at, r.finished_at)} />
        <Item k="Origem" v={fmtBytes(r.bytes_source)} /><Item k="Arquivo gerado" v={fmtBytes(r.bytes_output)} /><Item k="Itens" v={String(r.files || '—')} />
        <div className="col-span-2 sm:col-span-3"><Item k="Arquivo" v={r.output_name || '—'} /></div>
        <div className="col-span-2 sm:col-span-3"><Item k="SHA-256" v={r.sha256 || '—'} mono /></div>
      </dl>
      <div>
        <div className="mb-1 font-medium">Log</div>
        <pre className="max-h-72 overflow-auto rounded-lg border border-border bg-bg p-3 text-xs leading-relaxed">
          {d.logs.length === 0 ? <span className="text-muted">{live ? 'O log completo aparece quando o backup terminar.' : 'Sem registros.'}</span> :
            d.logs.map((l) => <div key={l.seq} className={color(l.level)}><span className="text-muted">{new Date(l.at).toLocaleTimeString('pt-BR')} </span>{l.text}</div>)}
        </pre>
      </div>
    </div>
  )
}

function Item({ k, v, mono }: { k: string; v: string; mono?: boolean }) {
  return <div><dt className="text-xs text-muted">{k}</dt><dd className={`break-all ${mono ? 'font-mono text-xs' : ''}`}>{v}</dd></div>
}
