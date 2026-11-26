import { useCallback, useEffect, useRef, useState } from 'react'
import { Job, PHASE, Run, RunLog, User, api, ApiError, fmtBytes, fmtDate, fmtDuration, STATUS } from '../api'
import { Badge, Button, Card, CopyBox, ErrorBox, Modal } from '../ui'

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
                <td className="p-3"><StatusBadge s={r.status} />{r.status === 'running' && r.phase && <div className="mt-1 text-xs text-muted">{PHASE[r.phase] ?? r.phase}{r.phase === 'uploading' ? ` ${r.progress_pct}%` : ''}</div>}{r.cloud_status === 'ok' && <div className="mt-1 text-xs text-muted">☁ no destino</div>}{r.cloud_status === 'failed' && <div className="mt-1 text-xs text-danger">☁ envio falhou</div>}</td>
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

type JobInfo = { kind?: string; has_cloud?: boolean; net_destination?: boolean; keep?: number; engine?: string }

function Detail({ id, me, onChange }: { id: number; me: User; onChange: () => void }) {
  const [run, setRun] = useState<Run | null>(null)
  const [job, setJob] = useState<JobInfo>({})
  const [logs, setLogs] = useState<RunLog[]>([])
  const [note, setNote] = useState('')
  const [err, setErr] = useState('')
  const [live, setLive] = useState(false)
  const [pending, setPending] = useState(0) // segundos restantes de atualização após uma ação (reenviar/extrair)
  const box = useRef<HTMLPreElement>(null)
  const stick = useRef(true)
  const lastSeq = useRef(0)

  const merge = useCallback((add: RunLog[]) => {
    if (!add.length) return
    setLogs((cur) => {
      const m = new Map(cur.map((l) => [l.seq, l]))
      add.forEach((l) => m.set(l.seq, l))
      const out = [...m.values()].sort((a, b) => a.seq - b.seq)
      lastSeq.current = out.length ? out[out.length - 1].seq : 0
      return out
    })
  }, [])

  const load = useCallback(() => {
    api<{ run: Run; logs: RunLog[]; job: JobInfo }>('GET', `/api/runs/${id}`).then((d) => { setRun(d.run); setJob(d.job ?? {}); merge(d.logs) }).catch((x) => setErr((x as ApiError).message))
  }, [id, merge])
  useEffect(() => { load() }, [load])

  // transmissão ao vivo enquanto a execução roda; depois dela, só atualiza se houver uma ação em andamento
  const running = run && (run.status === 'running' || run.status === 'queued')
  useEffect(() => {
    if (!running) return
    const es = new EventSource(`/api/runs/${id}/stream?after=${lastSeq.current}`)
    setLive(true)
    es.addEventListener('log', (e) => merge([JSON.parse((e as MessageEvent).data)]))
    es.addEventListener('run', (e) => setRun(JSON.parse((e as MessageEvent).data)))
    es.addEventListener('end', () => { es.close(); setLive(false); load(); onChange() })
    es.onerror = () => { es.close(); setLive(false); setTimeout(load, 3000) } // cai para a atualização comum
    return () => { es.close(); setLive(false) }
  }, [id, running, merge, load, onChange])
  useEffect(() => {
    if (pending <= 0) return
    const t = setInterval(() => { load(); setPending((p) => p - 3) }, 3000)
    return () => clearInterval(t)
  }, [pending, load])
  useEffect(() => { if (stick.current && box.current) box.current.scrollTop = box.current.scrollHeight }, [logs])

  if (!run) return <p className="text-muted">{err || 'Carregando…'}</p>
  const r = run
  const color = (l: string) => (l === 'error' ? 'text-danger' : l === 'warn' ? 'text-accent' : 'text-text')
  const canAct = me.role !== 'viewer' && !running && !!r.output_name
  const post = (path: string, body?: unknown, ok?: string) => api('POST', path, body).then(() => { setErr(''); setPending(60); if (ok) setNote(ok) }).catch((x) => { setNote(''); setErr((x as ApiError).message) })
  return (
    <div className="space-y-4 text-sm">
      <div className="flex flex-wrap items-center gap-3">
        <b className="text-base">{r.job_name}</b><StatusBadge s={r.status} />
        <span className="text-muted">{r.agent_name}</span>
        {live && <span className="inline-flex items-center gap-1 text-xs text-ok"><span className="inline-block h-2 w-2 animate-pulse rounded-full bg-ok" />ao vivo</span>}
        {running && me.role !== 'viewer' && <Button variant="danger" className="ml-auto" onClick={() => confirm('Cancelar este backup?') && api('POST', `/api/runs/${r.id}/cancel`).then(() => { onChange(); load() }).catch((x) => setErr((x as ApiError).message))}>Cancelar</Button>}
      </div>
      <ErrorBox message={err || (r.status === 'failed' || r.status === 'missed' ? r.error : '')} />
      {note && <div className="rounded-lg border border-ok/40 bg-ok/10 px-3 py-2 text-ok">{note}</div>}
      {running && r.phase && (
        <div>
          <div className="mb-1 text-xs text-muted">{PHASE[r.phase] ?? r.phase}{r.phase === 'uploading' ? ` · ${r.progress_pct}%` : ''}</div>
          <div className="h-2 overflow-hidden rounded-full bg-border"><div className="h-full bg-primary transition-all" style={{ width: `${r.phase === 'uploading' ? r.progress_pct : 8}%` }} /></div>
        </div>
      )}
      <dl className="grid grid-cols-2 gap-x-4 gap-y-2 sm:grid-cols-3">
        <Item k="Início" v={fmtDate(r.started_at)} /><Item k="Fim" v={fmtDate(r.finished_at)} /><Item k="Duração" v={fmtDuration(r.started_at, r.finished_at)} />
        <Item k="Origem" v={fmtBytes(r.bytes_source)} /><Item k="Arquivo gerado" v={fmtBytes(r.bytes_output)} /><Item k="Itens" v={String(r.files || '—')} />
        <div className="col-span-2 sm:col-span-3"><Item k="Arquivo" v={r.output_name || '—'} /></div>
        <div className="col-span-2 sm:col-span-3"><Item k="SHA-256" v={r.sha256 || '—'} mono /></div>
        {r.cloud_status !== 'none' && <div className="col-span-2 sm:col-span-3"><Item k={`Destino (${r.cloud_status === 'ok' ? `conferido por ${r.cloud_verified_by}` : 'falhou'})`} v={r.cloud_path || '—'} /></div>}
      </dl>

      {canAct && <RestorePanel run={r} job={job} onAction={(p, b) => post(p, b, 'Pedido enviado ao agente; acompanhe o log abaixo.')} />}

      <div>
        <div className="mb-1 font-medium">Log</div>
        <pre ref={box} onScroll={(e) => { const el = e.currentTarget; stick.current = el.scrollHeight - el.scrollTop - el.clientHeight < 40 }} className="max-h-72 overflow-auto rounded-lg border border-border bg-bg p-3 text-xs leading-relaxed">
          {logs.length === 0 ? <span className="text-muted">{running ? 'Aguardando as primeiras linhas…' : 'Sem registros.'}</span> :
            logs.map((l) => <div key={l.seq} className={color(l.level)}><span className="text-muted">{new Date(l.at).toLocaleTimeString('pt-BR')} </span>{l.seq >= 10000 && <span className="text-muted">[ação] </span>}{l.text}</div>)}
        </pre>
      </div>
    </div>
  )
}

function RestorePanel({ run, job, onAction }: { run: Run; job: JobInfo; onAction: (path: string, body?: unknown, ok?: string) => Promise<void> }) {
  const [target, setTarget] = useState('')
  const [guide, setGuide] = useState(false)
  const inCloud = run.cloud_status === 'ok'
  const done = run.status === 'ok' || run.status === 'warning' || run.cloud_status === 'ok' || run.cloud_status === 'failed' || run.status === 'failed'
  if (!done) return null
  return (
    <div className="space-y-3 rounded-lg border border-border bg-bg p-3">
      <div className="font-medium">Restauração e ações</div>
      <div className="flex flex-wrap items-center gap-2">
        {inCloud && job.net_destination && <Button variant="ghost" onClick={() => onAction(`/api/runs/${run.id}/fetch`, undefined)}>⬇ Trazer do destino para o agente</Button>}
        {inCloud && !job.net_destination && <a className="inline-flex items-center rounded-lg bg-primary px-3.5 py-2 text-sm font-medium text-primary-contrast hover:opacity-90" href={`/api/runs/${run.id}/download`}>⬇ Baixar da nuvem</a>}
        {run.cloud_status === 'failed' && job.has_cloud && <Button variant="ghost" onClick={() => confirm('Reenviar o arquivo local ao destino?') && onAction(`/api/runs/${run.id}/reupload`, undefined)}>☁ Reenviar ao destino</Button>}
        <input aria-label="Pasta de destino" className="w-56 rounded-lg border border-border bg-surface px-3 py-2 text-sm" placeholder="pasta de restauração (opcional)" value={target} onChange={(e) => setTarget(e.target.value)} />
        <Button variant="ghost" onClick={() => onAction(`/api/runs/${run.id}/extract`, { target }, undefined)}>📂 Extrair no agente</Button>
        <button className="text-primary hover:underline" onClick={() => setGuide(!guide)}>{guide ? 'Ocultar guia' : 'Como restaurar manualmente'}</button>
      </div>
      <p className="text-xs text-muted">“Extrair no agente” descompacta o arquivo local em <code>restores/&lt;pasta&gt;</code> dentro da pasta de backups do agente, sem sobrescrever nada. O andamento aparece no log abaixo. Se a cópia local já foi removida, baixe da nuvem (ou, num destino de rede, use “Trazer do destino”).</p>
      {guide && <Guide run={run} job={job} />}
    </div>
  )
}

function Guide({ run, job }: { run: Run; job: JobInfo }) {
  const f = run.output_name
  const dir = f.replace(/\.tar\.gz$/, '')
  const eng = job.engine
  const files = `# 1) Confira o arquivo (o SHA-256 está no detalhe desta execução)
sha256sum ${f}
# 2) Veja o que há dentro, sem extrair
tar -tzf ${f} | head
# 3) Extraia numa pasta SEGURA (não direto em /)
mkdir -p /restaurar && tar -xzf ${f} -C /restaurar
# 4) Copie de volta só o que precisar, por exemplo:
cp -a /restaurar/etc/exemplo.conf /etc/exemplo.conf`
  const dbs: Record<string, string> = {
    mysql: `mkdir -p /restaurar && tar -xzf ${f} -C /restaurar
# cada banco é um arquivo .sql (já inclui CREATE DATABASE)
mysql -u root -p < /restaurar/${dir.replace(/-\d{8}-\d{6}$/, '')}/NOME_DO_BANCO.sql
# em contêiner:  docker exec -i NOME_DO_CONTEINER mysql -u root -p < arquivo.sql`,
    postgres: `mkdir -p /restaurar && tar -xzf ${f} -C /restaurar
# formato custom: crie o banco e restaure
createdb -U postgres NOME_DO_BANCO
pg_restore -U postgres -d NOME_DO_BANCO --no-owner /restaurar/*/NOME_DO_BANCO.dump
# papéis/globais (opcional): psql -U postgres -f /restaurar/*/globals.sql`,
    mongodb: `mkdir -p /restaurar && tar -xzf ${f} -C /restaurar
mongorestore --archive=/restaurar/*/NOME_DO_BANCO.archive.gz --gzip --drop
# em contêiner:  docker exec -i NOME_DO_CONTEINER mongorestore --archive --gzip --drop < arquivo.archive.gz`,
  }
  dbs.mariadb = dbs.mysql
  const text = job.kind === 'database' ? (dbs[eng ?? ''] ?? files) : files
  return (
    <div className="space-y-2">
      <CopyBox text={text} />
      {run.cloud_path && <p className="text-xs text-muted">Cópia no destino: <code>{run.cloud_path}</code>.</p>}
      <p className="text-xs text-muted">Teste a restauração num ambiente separado antes de aplicar em produção.</p>
    </div>
  )
}

function Item({ k, v, mono }: { k: string; v: string; mono?: boolean }) {
  return <div><dt className="text-xs text-muted">{k}</dt><dd className={`break-all ${mono ? 'font-mono text-xs' : ''}`}>{v}</dd></div>
}
