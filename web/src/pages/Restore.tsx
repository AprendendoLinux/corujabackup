// Restaurar: escolha o job, um ponto na linha do tempo (cada backup), navegue nos arquivos como estavam naquele momento,
// veja as versões de um arquivo e restaure só o que marcar (numa pasta de restauração ou no local original).
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Job, Run, RunLog, User, api, ApiError, fmtBytes } from '../api'
import { Badge, Button, Card, ErrorBox, Input, Modal, Spinner } from '../ui'
import { confirmar } from '../Dialogs'
import { FolderList, FolderPicker } from '../Picker'

type Entry = { name: string; dir: boolean; link?: boolean; size: number; modified: number; here: boolean; files?: number }
type Listing = { dir: string; entries: Entry[]; files: number; incremental: boolean; chain: number }
type Version = { archive: string; size: number; modified: number; backups: number; latest: string }

const fmtWhen = (s: string | null) => (s ? new Date(s).toLocaleString('pt-BR', { day: '2-digit', month: '2-digit', year: 'numeric', hour: '2-digit', minute: '2-digit' }) : '—')
const fmtUnix = (n: number) => (n ? new Date(n * 1000).toLocaleString('pt-BR', { day: '2-digit', month: '2-digit', year: 'numeric', hour: '2-digit', minute: '2-digit' }) : '—')
const join = (dir: string, name: string) => (dir === '/' ? '/' + name : dir + '/' + name)
const dayKey = (s: string | null) => {
  const d = s ? new Date(s).toLocaleDateString('pt-BR', { weekday: 'long', day: '2-digit', month: 'long' }) : ''
  return d.charAt(0).toUpperCase() + d.slice(1)
}

function FolderIcon() {
  return <svg aria-hidden width="18" height="18" viewBox="0 0 24 24" fill="currentColor" className="shrink-0 text-accent"><path d="M3 6a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v10a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z" /></svg>
}
function FileIcon() {
  return <svg aria-hidden width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" className="shrink-0 text-muted"><path d="M14 3H6a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V9z" /><path d="M14 3v6h6" /></svg>
}

export default function Restore({ me, jobParam }: { me: User; jobParam: number }) {
  const [jobs, setJobs] = useState<Job[]>([])
  const [jobId, setJobId] = useState(jobParam)
  const [runs, setRuns] = useState<Run[] | null>(null)
  const [runId, setRunId] = useState(0)
  const [err, setErr] = useState('')
  useEffect(() => { api<{ jobs: Job[] }>('GET', '/api/jobs').then((r) => { setJobs(r.jobs); if (!jobParam) { const f = r.jobs.find((j) => j.kind !== 'mikrotik'); if (f) setJobId(f.id) } }).catch((x) => setErr((x as ApiError).message)) }, [jobParam])
  useEffect(() => {
    setRuns(null); setRunId(0)
    if (!jobId) return
    api<{ runs: Run[] }>('GET', `/api/runs?job_id=${jobId}&limit=500`).then((r) => {
      const ok = r.runs.filter((x) => (x.status === 'ok' || x.status === 'warning') && x.output_name)
      setRuns(ok)
      if (ok[0]) setRunId(ok[0].id)
    }).catch((x) => setErr((x as ApiError).message))
  }, [jobId])
  const job = jobs.find((j) => j.id === jobId)
  const run = runs?.find((r) => r.id === runId) ?? null
  const byArchive = useMemo(() => new Map((runs ?? []).map((r) => [r.output_name, r])), [runs])
  const groups = useMemo(() => {
    const g: { day: string; runs: Run[] }[] = []
    for (const r of runs ?? []) {
      const d = dayKey(r.started_at)
      if (!g.length || g[g.length - 1].day !== d) g.push({ day: d, runs: [] })
      g[g.length - 1].runs.push(r)
    }
    return g
  }, [runs])
  const sel = 'rounded-lg border border-border bg-surface px-2.5 py-2 text-sm'
  return (
    <div className="space-y-5">
      <div>
        <h1 className="text-2xl font-semibold">Restaurar</h1>
        <p className="mt-1 text-sm text-muted">Escolha um backup na linha do tempo: navegue nos arquivos como estavam naquele momento e restaure ou baixe só o que precisar, ou restaure bancos de dados.</p>
      </div>
      <div className="flex flex-wrap items-center gap-3">
        <select aria-label="Job" className={sel} value={jobId} onChange={(e) => setJobId(Number(e.target.value))}>
          <option value={0}>Escolha um job…</option>
          {jobs.map((j) => <option key={j.id} value={j.id} disabled={j.kind === 'mikrotik'}>{j.name}{j.kind === 'database' ? ' (banco de dados)' : j.kind === 'mikrotik' ? ' (MikroTik: use o Histórico)' : ''}</option>)}
        </select>
        {job && <span className="text-sm text-muted">{job.agent_name} · {job.kind === 'database' ? 'banco de dados' : job.backup_mode === 'incremental' ? 'full + incrementais' : 'completo a cada execução'}</span>}
      </div>
      <ErrorBox message={err} />
      {jobId !== 0 && runs && runs.length === 0 && <Card><p className="text-sm text-muted">Este job ainda não tem backups concluídos.</p></Card>}
      {runs && runs.length > 0 && job && (
        <div className="grid gap-4 lg:grid-cols-[18rem_1fr]">
          <Card className="min-w-0 p-0">
            <div className="border-b border-border px-4 py-3"><h2 className="font-semibold">Linha do tempo</h2><p className="text-xs text-muted">{runs.length} backup(s); o mais recente no topo</p></div>
            <ol className="scroll-fino max-h-[70vh] overflow-y-auto p-2" aria-label="Backups">
              {groups.map((g) => (
                <li key={g.day}>
                  <div className="px-2 pb-1 pt-2 text-xs font-medium text-muted">{g.day}</div>
                  <ul className="space-y-1">
                    {g.runs.map((r) => (
                      <li key={r.id}>
                        <button type="button" onClick={() => setRunId(r.id)} aria-current={r.id === runId}
                          className={`flex w-full items-center gap-2 rounded-lg px-2.5 py-2 text-left text-sm transition ${r.id === runId ? 'bg-primary text-primary-contrast' : 'hover:bg-bg'}`}>
                          <span className={`h-2.5 w-2.5 shrink-0 rounded-full ${r.backup_kind === 'incremental' ? 'border-2 border-current' : 'bg-current'}`} aria-hidden />
                          <span className="min-w-0 flex-1">
                            <span className="block font-medium tabular-nums">{new Date(r.started_at ?? r.created_at).toLocaleTimeString('pt-BR', { hour: '2-digit', minute: '2-digit' })}</span>
                            <span className={`block truncate text-xs ${r.id === runId ? 'opacity-80' : 'text-muted'}`}>{r.backup_kind === 'incremental' ? 'incremental' : 'full'} · {fmtBytes(r.bytes_output)}{r.status === 'warning' ? ' · com avisos' : ''}</span>
                          </span>
                        </button>
                      </li>
                    ))}
                  </ul>
                </li>
              ))}
            </ol>
          </Card>
          {run && job.kind === 'database' && <DbRestore key={run.id} me={me} run={run} />}
          {run && job.kind === 'files' && <Browser key={run.id} me={me} job={job} run={run} byArchive={byArchive} onPick={(id) => setRunId(id)} />}
        </div>
      )}
    </div>
  )
}

function Browser({ me, job, run, byArchive, onPick }: { me: User; job: Job; run: Run; byArchive: Map<string, Run>; onPick: (id: number) => void }) {
  const [dir, setDir] = useState('/')
  const [list, setList] = useState<Listing | null>(null)
  const [err, setErr] = useState('')
  const [noIndex, setNoIndex] = useState(false)
  const [indexing, setIndexing] = useState(false)
  const [picked, setPicked] = useState<Set<string>>(new Set())
  const [kinds] = useState(() => new Map<string, boolean>()) // caminho → é pasta
  const [versions, setVersions] = useState<string | null>(null)
  const [restoreReq, setRestoreReq] = useState<{ run: Run; paths: string[] } | null>(null)
  const [dlBusy, setDlBusy] = useState(false)
  const load = useCallback((d: string) => {
    setErr('')
    api<Listing>('GET', `/api/runs/${run.id}/browse?dir=${encodeURIComponent(d)}`).then((r) => { setList(r); setNoIndex(false); setIndexing(false) }).catch((x) => {
      const m = (x as ApiError).message
      if (m.includes('sem catálogo')) setNoIndex(true); else setErr(m)
      setList(null)
    })
  }, [run.id])
  useEffect(() => { load(dir) }, [dir, load])
  // enquanto o índice é montado, tenta de novo a cada 3 s
  useEffect(() => {
    if (!indexing) return
    const t = setInterval(() => load(dir), 3000)
    return () => clearInterval(t)
  }, [indexing, dir, load])
  const crumbs = dir === '/' ? [] : dir.split('/').slice(1)
  const covered = (p: string) => [...picked].some((x) => p === x || p.startsWith(x + '/'))
  async function download(r: Run, paths: string[], zip: boolean) {
    setErr(''); setDlBusy(true)
    try {
      const d = await api<{ url: string; name: string }>('POST', `/api/runs/${r.id}/download`, { paths, zip })
      const a = document.createElement('a')
      a.href = d.url; a.download = d.name; document.body.appendChild(a); a.click(); a.remove()
    } catch (x) { setErr((x as ApiError).message) } finally { setDlBusy(false) }
  }
  const onlyFile = picked.size === 1 && kinds.get([...picked][0]) === false
  function toggle(p: string) {
    const n = new Set([...picked].filter((x) => !x.startsWith(p + '/')))
    if (picked.has(p)) n.delete(p); else n.add(p)
    setPicked(n)
  }
  const allHere = list?.entries.every((e) => covered(join(dir, e.name))) ?? false
  return (
    <Card className="min-w-0 p-0">
      <div className="flex flex-wrap items-center justify-between gap-2 border-b border-border px-4 py-3">
        <div className="min-w-0">
          <h2 className="font-semibold">{fmtWhen(run.started_at)} <span className="ml-1 align-middle"><Badge tone={run.backup_kind === 'incremental' ? 'warn' : 'ok'}>{run.backup_kind === 'incremental' ? 'Incremental' : 'Full'}</Badge></span></h2>
          <p className="truncate text-xs text-muted">{run.output_name}{list ? ` · ${list.files.toLocaleString('pt-BR')} arquivo(s) nesta pasta e subpastas` : ''}</p>
        </div>
      </div>
      <nav aria-label="Pasta atual" className="flex flex-wrap items-center gap-1 px-4 pt-3 text-sm">
        <button type="button" className="rounded px-1.5 py-0.5 font-medium text-primary hover:bg-bg" onClick={() => setDir('/')}>Origens</button>
        {crumbs.map((c, i) => (
          <span key={i} className="flex items-center gap-1"><span className="text-muted">/</span>
            <button type="button" className="rounded px-1.5 py-0.5 text-primary hover:bg-bg" onClick={() => setDir('/' + crumbs.slice(0, i + 1).join('/'))}>{c}</button></span>
        ))}
      </nav>
      <div className="p-4 pt-2">
        <ErrorBox message={err} />
        {noIndex && (
          <div className="rounded-xl border border-border bg-bg p-4 text-sm">
            <b>Este backup ainda não tem índice no agente.</b> Ele é de antes da 0.0.23 (ou o agente foi reinstalado). O agente pode montar o índice lendo o próprio arquivo{job.cloud_account_id ? ' (trazendo-o do storage de rede, se preciso)' : ''}.
            {run.backup_kind === 'incremental' && ' Num incremental antigo, o índice mostra só o que mudou nele.'}
            <div className="mt-3">{indexing ? <span className="inline-flex items-center gap-2 text-muted"><Spinner /> Montando o índice…</span>
              : <Button onClick={() => api('POST', `/api/runs/${run.id}/index`).then(() => setIndexing(true)).catch((x) => setErr((x as ApiError).message))}>Gerar índice</Button>}</div>
          </div>
        )}
        {list && (
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead className="border-b border-border text-left text-xs text-muted">
                <tr>
                  <th className="w-8 py-2"><input type="checkbox" aria-label="Marcar tudo nesta pasta" checked={allHere && list.entries.length > 0} onChange={() => {
                    const n = new Set(picked)
                    for (const e of list.entries) { const p = join(dir, e.name); kinds.set(p, e.dir); if (allHere) n.delete(p); else if (!covered(p)) n.add(p) }
                    setPicked(n)
                  }} /></th>
                  <th className="py-2">Nome</th><th className="py-2 text-right">Tamanho</th><th className="px-3 py-2">Modificado</th><th className="py-2" />
                </tr>
              </thead>
              <tbody>
                {dir !== '/' && <tr className="border-b border-border"><td /><td colSpan={4} className="py-2"><button type="button" className="text-primary hover:underline" onClick={() => setDir(dir.split('/').slice(0, -1).join('/') || '/')}>↑ pasta acima</button></td></tr>}
                {list.entries.length === 0 && <tr><td colSpan={5} className="py-6 text-center text-muted">Pasta vazia neste backup.</td></tr>}
                {list.entries.map((e) => {
                  const p = join(dir, e.name)
                  const inherited = covered(p) && !picked.has(p)
                  return (
                    <tr key={e.name} className="border-b border-border last:border-0 hover:bg-bg">
                      <td className="py-1.5"><input type="checkbox" aria-label={`Marcar ${e.name}`} checked={covered(p)} disabled={inherited} onChange={() => { kinds.set(p, e.dir); toggle(p) }} /></td>
                      <td className="py-1.5">
                        {e.dir
                          ? <button type="button" className="flex items-center gap-2 text-left hover:underline" onClick={() => setDir(p)}><FolderIcon /><span className="font-medium">{e.name}</span></button>
                          : <span className="flex items-center gap-2"><FileIcon /><span>{e.name}{e.link && <span className="ml-1 text-xs text-muted">(atalho)</span>}</span></span>}
                        {list.incremental && e.here && <span className="ml-7 text-[11px] text-accent">{e.dir ? 'tem mudanças neste backup' : 'mudou neste backup'}</span>}
                      </td>
                      <td className="py-1.5 text-right tabular-nums text-muted">{e.dir ? (e.files ? <span title={`${e.files.toLocaleString('pt-BR')} arquivo(s)`}>{e.size ? fmtBytes(e.size) : '0 B'}<span className="block text-[11px]">{e.files.toLocaleString('pt-BR')} arq.</span></span> : '') : fmtBytes(e.size)}</td>
                      <td className="px-3 py-1.5 tabular-nums text-muted">{e.dir ? '' : fmtUnix(e.modified)}</td>
                      <td className="py-1.5 text-right">{!e.dir && !e.link && <button type="button" className="text-xs text-primary hover:underline" onClick={() => setVersions(p)}>versões</button>}</td>
                    </tr>
                  )
                })}
              </tbody>
            </table>
          </div>
        )}
      </div>
      {picked.size > 0 && (
        <div className="sticky bottom-0 flex flex-wrap items-center justify-between gap-3 border-t border-border bg-surface/95 px-4 py-3 backdrop-blur">
          <div className="min-w-0 text-sm"><b>{picked.size}</b> item(ns) marcado(s): <span className="text-muted">{[...picked].slice(0, 3).join(', ')}{picked.size > 3 ? '…' : ''}</span></div>
          <div className="flex flex-wrap gap-2">
            <Button variant="ghost" onClick={() => setPicked(new Set())}>Limpar</Button>
            <Button variant="ghost" disabled={dlBusy} onClick={() => download(run, [...picked], !onlyFile)}>{dlBusy ? 'Preparando…' : onlyFile ? '⬇ Baixar arquivo' : '⬇ Baixar .zip'}</Button>
            <Button onClick={() => setRestoreReq({ run, paths: [...picked] })}>Restaurar…</Button>
          </div>
        </div>
      )}
      {versions && <Modal title="Versões do arquivo" onClose={() => setVersions(null)}><Versions job={job} path={versions} byArchive={byArchive} dlBusy={dlBusy}
        onOpen={(runId) => { setVersions(null); onPick(runId) }}
        onRestore={(r) => { const p = versions; setVersions(null); setRestoreReq({ run: r, paths: [p] }) }}
        onDownload={(r) => download(r, [versions], false)} /></Modal>}
      {restoreReq && <Modal title={restoreReq.run.id === run.id ? 'Restaurar os itens marcados' : 'Restaurar esta versão'} onClose={() => setRestoreReq(null)}>
        <RestoreForm me={me} run={restoreReq.run} paths={restoreReq.paths} onDone={() => { if (restoreReq.run.id === run.id) setPicked(new Set()) }} /></Modal>}
    </Card>
  )
}

function Versions({ job, path, byArchive, onOpen, onRestore, onDownload, dlBusy }: {
  job: Job; path: string; byArchive: Map<string, Run>; dlBusy: boolean
  onOpen: (runId: number) => void; onRestore: (r: Run) => void; onDownload: (r: Run) => void
}) {
  const [vs, setVs] = useState<Version[] | null>(null)
  const [err, setErr] = useState('')
  useEffect(() => { api<{ versions: Version[] }>('GET', `/api/jobs/${job.id}/versions?path=${encodeURIComponent(path)}`).then((r) => setVs(r.versions)).catch((x) => setErr((x as ApiError).message)) }, [job.id, path])
  return (
    <div className="space-y-3 text-sm">
      <p className="break-all font-mono text-xs text-muted">{path}</p>
      <ErrorBox message={err} />
      {!vs && !err && <span className="inline-flex items-center gap-2 text-muted"><Spinner /> Procurando nos backups…</span>}
      {vs && vs.length === 0 && <p className="text-muted">Nenhuma versão encontrada nos backups com índice neste agente.</p>}
      {vs && vs.length > 0 && (
        <ol className="space-y-2">
          {vs.map((v, i) => {
            const r = byArchive.get(v.latest)
            return (
              <li key={v.archive + i} className="flex flex-wrap items-center justify-between gap-2 rounded-lg border border-border p-3">
                <div>
                  <div className="font-medium">{i === 0 ? 'Versão mais recente' : `Versão de ${fmtUnix(v.modified)}`}</div>
                  <div className="text-xs text-muted">modificado em {fmtUnix(v.modified)} · {fmtBytes(v.size)} · presente em {v.backups} backup(s)</div>
                </div>
                {r ? (
                  <div className="flex flex-wrap gap-2">
                    <Button onClick={() => onRestore(r)}>Restaurar esta versão</Button>
                    <Button variant="ghost" disabled={dlBusy} onClick={() => onDownload(r)}>{dlBusy ? 'Preparando…' : '⬇ Baixar'}</Button>
                    <button type="button" className="text-xs text-primary hover:underline" onClick={() => onOpen(r.id)}>abrir o backup</button>
                  </div>
                ) : <span className="text-xs text-muted">backup fora do histórico</span>}
              </li>
            )
          })}
        </ol>
      )}
      <p className="text-xs text-muted">“Restaurar esta versão” restaura só este arquivo, na versão escolhida; “Baixar” entrega o arquivo ao navegador.</p>
    </div>
  )
}

function RestoreForm({ me, run, paths: initial, onDone }: { me: User; run: Run; paths: string[]; onDone: () => void }) {
  const [paths] = useState(initial) // a lista do momento em que a janela abriu (a seleção é limpa ao concluir)
  const stamp = new Date().toISOString().slice(0, 16).replace(/[-:T]/g, '')
  const [mode, setMode] = useState<'restores' | 'dest' | 'original'>('restores')
  const original = mode === 'original'
  const [target, setTarget] = useState(`restaurar-${stamp}`)
  const [dest, setDest] = useState('') // pasta do servidor escolhida na navegação (absoluta)
  const [picking, setPicking] = useState(false)
  // pasta-mãe comum dos itens: é a partir dela que eles entram na pasta escolhida
  const common = useMemo(() => {
    let c: string[] | null = null
    for (const p of paths) {
      const parent = p.split('/').filter(Boolean).slice(0, -1)
      if (c === null) c = parent
      else { let n = 0; while (n < c.length && n < parent.length && c[n] === parent[n]) n++; c = c.slice(0, n) }
    }
    return '/' + (c ?? []).join('/')
  }, [paths])
  const example = paths[0] ? (dest || '<pasta>') + '/' + paths[0].slice(common === '/' ? 1 : common.length + 1) : ''
  async function loadDisk(p: string): Promise<FolderList> {
    const r = await api<{ entries: { name: string }[]; free?: number; total?: number; used?: number; blocked?: string; mount?: string; fs?: string; disks?: FolderList['disks'] }>(
      'GET', `/api/agents/${run.agent_id}/browse?area=disk&path=${encodeURIComponent('/' + p)}`)
    return { folders: r.entries.map((e) => e.name), free: r.free, total: r.total, used: r.used, mount: r.mount, fs: r.fs, blocked: r.blocked?.replace('o backup não é gravado', 'nada é restaurado').replace('gravar backups', 'gravar'), disks: r.disks }
  }
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState(false)
  const [logs, setLogs] = useState<RunLog[] | null>(null)
  const [done, setDone] = useState<'' | 'ok' | 'failed'>('')
  const since = useRef(0)
  async function go() {
    if (original && !(await confirmar({ title: 'Sobrescrever no local original?', message: `Os ${paths.length} item(ns) marcados voltam para o lugar de origem no agente, SUBSTITUINDO os arquivos que estiverem lá agora. Não há como desfazer.`, confirmLabel: 'Restaurar e sobrescrever', danger: true }))) return
    if (mode === 'dest' && !(await confirmar({ title: `Restaurar em ${dest}?`, message: `Os ${paths.length} item(ns) marcados vão para ${dest} (ex.: ${example}). Se já houver arquivos com o mesmo nome lá, eles são SUBSTITUÍDOS; o resto da pasta não é tocado.`, confirmLabel: 'Restaurar nesta pasta', danger: true }))) return
    setErr(''); setBusy(true)
    try {
      const before = await api<{ logs: RunLog[] }>('GET', `/api/runs/${run.id}`)
      since.current = Math.max(10000, ...before.logs.map((l) => l.seq))
      await api('POST', `/api/runs/${run.id}/restore`, { paths, original, target: mode === 'restores' ? target : '', dest: mode === 'dest' ? dest : '' })
      setLogs([])
    } catch (x) { setErr((x as ApiError).message); setBusy(false) }
  }
  useEffect(() => {
    if (logs === null || done) return
    const t = setInterval(async () => {
      try {
        const d = await api<{ logs: RunLog[] }>('GET', `/api/runs/${run.id}`)
        const fresh = d.logs.filter((l) => l.seq > since.current)
        setLogs(fresh)
        if (fresh.some((l) => l.text.startsWith('restauração concluída'))) { setDone('ok'); setBusy(false); onDone() }
        if (fresh.some((l) => l.level === 'error')) { setDone('failed'); setBusy(false) }
      } catch { /* tenta de novo */ }
    }, 2000)
    return () => clearInterval(t)
  }, [logs, done, run.id, onDone])
  return (
    <div className="space-y-4 text-sm">
      <p>{paths.length} item(ns) do backup <b>{run.backup_kind === 'incremental' ? 'incremental' : 'full'}</b> de <b>{run.started_at ? new Date(run.started_at).toLocaleString('pt-BR') : '—'}</b> <span className="text-muted">({run.output_name})</span>. Pastas marcadas incluem tudo o que há dentro delas.</p>
      <fieldset className="space-y-2" disabled={busy || done !== ''}>
        <label className={`flex items-start gap-2.5 rounded-lg border p-3 ${mode === 'restores' ? 'border-primary' : 'border-border'}`}>
          <input type="radio" name="destino" className="mt-1" checked={mode === 'restores'} onChange={() => setMode('restores')} />
          <span className="flex-1"><b className="block">Numa pasta de restauração (seguro)</b><span className="text-xs text-muted">Nada no servidor é alterado: os arquivos vão para <code>restores/&lt;nome&gt;</code> na pasta de backups do agente, com os caminhos completos.</span>
            {mode === 'restores' && <Input className="mt-2 font-mono" value={target} onChange={(e) => setTarget(e.target.value)} aria-label="Nome da pasta de restauração" />}</span>
        </label>
        <label className={`flex items-start gap-2.5 rounded-lg border p-3 ${mode === 'dest' ? 'border-accent' : 'border-border'} ${me.role !== 'admin' ? 'opacity-60' : ''}`}>
          <input type="radio" name="destino" className="mt-1" checked={mode === 'dest'} disabled={me.role !== 'admin'} onChange={() => { setMode('dest'); if (!dest) setPicking(true) }} />
          <span className="min-w-0 flex-1"><b className="block">Numa pasta do servidor (escolher)</b><span className="text-xs text-muted">{me.role === 'admin' ? 'Navegue nas pastas do servidor e escolha onde os itens entram. Arquivos com o mesmo nome lá são substituídos. Precisa do volume /:/host-rw no agente.' : 'Só administradores podem gravar em pastas do servidor.'}</span>
            {mode === 'dest' && <>
              <button type="button" aria-label="Pasta do servidor: escolher" onClick={() => setPicking(true)}
                className="mt-2 flex w-full min-w-0 items-center gap-2 rounded-lg border border-border bg-surface px-3 py-2 text-left hover:bg-bg">
                <FolderIcon /><span className={`truncate font-mono ${dest ? '' : 'text-muted'}`}>{dest || 'Escolha uma pasta…'}</span>
                <span className="ml-auto shrink-0 text-xs text-primary">{dest ? 'Trocar…' : 'Escolher…'}</span>
              </button>
              {dest && example && <span className="mt-1 block truncate text-xs text-muted" title={example}>Ex.: <code>{paths[0]}</code> → <code>{example}</code></span>}
            </>}
          </span>
        </label>
        <label className={`flex items-start gap-2.5 rounded-lg border p-3 ${original ? 'border-danger' : 'border-border'} ${me.role !== 'admin' ? 'opacity-60' : ''}`}>
          <input type="radio" name="destino" className="mt-1" checked={original} disabled={me.role !== 'admin'} onChange={() => setMode('original')} />
          <span><b className="block">No local original (sobrescreve)</b><span className="text-xs text-muted">{me.role === 'admin' ? 'Os arquivos voltam para onde estavam, substituindo os atuais. Precisa do volume /:/host-rw no agente.' : 'Só administradores podem sobrescrever arquivos do servidor.'}</span></span>
        </label>
      </fieldset>
      <ErrorBox message={err} />
      {logs !== null && (
        <div className="max-h-52 overflow-y-auto rounded-lg bg-bg p-3 font-mono text-xs">
          {logs.length === 0 && <span className="inline-flex items-center gap-2 text-muted"><Spinner /> Aguardando o agente…</span>}
          {logs.map((l) => <div key={l.seq} className={l.level === 'error' ? 'text-danger' : l.level === 'warn' ? 'text-accent' : ''}>{l.text}</div>)}
        </div>
      )}
      {done === 'ok' && <div className="rounded-lg border border-ok/40 bg-ok/10 px-3 py-2 text-ok">Restauração concluída.</div>}
      {done === '' && <Button variant={original ? 'danger' : 'primary'} onClick={go} disabled={busy || (mode === 'restores' && !target.trim()) || (mode === 'dest' && !dest)}>{busy ? 'Restaurando…' : original ? 'Restaurar e sobrescrever' : mode === 'dest' ? 'Restaurar nesta pasta' : 'Restaurar na pasta'}</Button>}
      {picking && <FolderPicker title={`Restaurar em qual pasta de ${run.agent_name}?`} intro="Navegue até a pasta que vai receber os itens (ou crie uma nova). Eles entram nela a partir da pasta onde estavam."
        rootLabel="Raiz" newHint="Pasta nova: será criada na restauração." initial={dest.replace(/^\//, '')} load={loadDisk} onClose={() => { setPicking(false); if (!dest) setMode('restores') }}
        onPick={(p) => { setDest('/' + p.replace(/^\/+/, '')); setPicking(false) }} />}
    </div>
  )
}

type DbFile = { name: string; database?: string; bytes: number; tables: number }
type DbContents = { engine: string; version: string; files: DbFile[] }
const ENGINE: Record<string, string> = { mysql: 'MySQL', mariadb: 'MariaDB', postgres: 'PostgreSQL', mongodb: 'MongoDB' }

/** Restaurar bancos de um backup de banco, no servidor do job: num banco novo (padrão) ou sobrescrevendo. */
function DbRestore({ me, run }: { me: User; run: Run }) {
  const [contents, setContents] = useState<DbContents | null>(null)
  const [err, setErr] = useState('')
  const [chosen, setChosen] = useState<Record<string, boolean>>({})
  const [targets, setTargets] = useState<Record<string, string>>({})
  const [overwrite, setOverwrite] = useState(false)
  const [busy, setBusy] = useState(false)
  const [logs, setLogs] = useState<RunLog[] | null>(null)
  const [done, setDone] = useState<'' | 'ok' | 'failed'>('')
  const since = useRef(0)
  const stamp = (run.started_at ?? '').slice(0, 10).replace(/-/g, '')
  useEffect(() => {
    api<DbContents>('GET', `/api/runs/${run.id}/databases`).then((c) => {
      setContents(c)
      const t: Record<string, string> = {}
      for (const f of c.files) if (f.database) t[f.database] = `${f.database}_restaurado_${stamp}`
      setTargets(t)
    }).catch((x) => setErr((x as ApiError).message))
  }, [run.id, stamp])
  const dbs = (contents?.files ?? []).filter((f) => f.database)
  const picked = dbs.filter((f) => chosen[f.database!])
  async function go() {
    const list = picked.map((f) => ({ source: f.database!, target: overwrite ? f.database! : (targets[f.database!] ?? '').trim() }))
    const msg = overwrite
      ? `Os bancos ${list.map((d) => d.source).join(', ')} serão APAGADOS no servidor e recriados como estavam neste backup. Tudo o que mudou neles depois do backup será perdido.`
      : `Os bancos serão criados no servidor do job: ${list.map((d) => `${d.source} → ${d.target}`).join(', ')}. Os originais não são alterados.`
    if (!(await confirmar({ title: overwrite ? 'Sobrescrever os bancos?' : 'Restaurar em bancos novos?', message: msg, confirmLabel: overwrite ? 'Apagar e restaurar' : 'Restaurar', danger: overwrite }))) return
    setErr(''); setBusy(true)
    try {
      const before = await api<{ logs: RunLog[] }>('GET', `/api/runs/${run.id}`)
      since.current = Math.max(10000, ...before.logs.map((l) => l.seq))
      await api('POST', `/api/runs/${run.id}/restore-db`, { databases: list, overwrite })
      setLogs([])
    } catch (x) { setErr((x as ApiError).message); setBusy(false) }
  }
  useEffect(() => {
    if (logs === null || done) return
    const t = setInterval(async () => {
      try {
        const d = await api<{ logs: RunLog[] }>('GET', `/api/runs/${run.id}`)
        const fresh = d.logs.filter((l) => l.seq > since.current)
        setLogs(fresh)
        if (fresh.some((l) => l.text.startsWith('restauração de bancos concluída'))) { setDone('ok'); setBusy(false) }
        else if (fresh.some((l) => l.level === 'error')) { setDone('failed'); setBusy(false) }
      } catch { /* tenta de novo */ }
    }, 2000)
    return () => clearInterval(t)
  }, [logs, done, run.id])
  return (
    <Card className="min-w-0 space-y-4 text-sm">
      <div>
        <h2 className="font-semibold">{fmtWhen(run.started_at)} <span className="ml-1 align-middle"><Badge tone="ok">Banco de dados</Badge></span></h2>
        <p className="text-xs text-muted">{run.output_name}{contents ? ` · ${ENGINE[contents.engine] ?? contents.engine} ${contents.version}` : ''}</p>
      </div>
      <ErrorBox message={err} />
      {!contents && !err && <span className="inline-flex items-center gap-2 text-muted"><Spinner /> Lendo o conteúdo do backup…</span>}
      {contents && (<>
        <p className="text-muted">A restauração roda no <b>mesmo servidor do job</b> (o contêiner ou o servidor de banco de onde o backup saiu), com as credenciais do job.</p>
        <div role="radiogroup" aria-label="Como restaurar" className="grid gap-2 sm:grid-cols-2">
          <label className={`flex items-start gap-2.5 rounded-lg border p-3 ${!overwrite ? 'border-primary' : 'border-border'}`}>
            <input type="radio" className="mt-1" checked={!overwrite} disabled={busy || done !== ''} onChange={() => setOverwrite(false)} />
            <span><b className="block">Em bancos novos (seguro)</b><span className="text-xs text-muted">Cria um banco com outro nome ao lado do original, para conferir ou copiar dados. Nada existente é alterado.</span></span>
          </label>
          <label className={`flex items-start gap-2.5 rounded-lg border p-3 ${overwrite ? 'border-danger' : 'border-border'} ${me.role !== 'admin' ? 'opacity-60' : ''}`}>
            <input type="radio" className="mt-1" checked={overwrite} disabled={me.role !== 'admin' || busy || done !== ''} onChange={() => setOverwrite(true)} />
            <span><b className="block">Sobrescrever os originais</b><span className="text-xs text-muted">{me.role === 'admin' ? 'Apaga o banco e o recria como estava no backup. O que mudou depois é perdido.' : 'Só administradores sobrescrevem bancos.'}</span></span>
          </label>
        </div>
        <table className="w-full">
          <thead className="border-b border-border text-left text-xs text-muted"><tr><th className="w-8 py-2" /><th className="py-2">Banco no backup</th><th className="py-2 text-right">Tamanho</th><th className="px-3 py-2 text-right">Tabelas</th><th className="py-2">Restaurar como</th></tr></thead>
          <tbody>
            {dbs.map((f) => (
              <tr key={f.database} className="border-b border-border last:border-0">
                <td className="py-2"><input type="checkbox" aria-label={`Restaurar ${f.database}`} checked={!!chosen[f.database!]} disabled={busy || done !== ''} onChange={(e) => setChosen({ ...chosen, [f.database!]: e.target.checked })} /></td>
                <td className="py-2 font-mono">{f.database}</td>
                <td className="py-2 text-right tabular-nums text-muted">{fmtBytes(f.bytes)}</td>
                <td className="px-3 py-2 text-right tabular-nums text-muted">{f.tables}</td>
                <td className="py-2">{overwrite ? <span className="font-mono text-danger">{f.database} (sobrescreve)</span>
                  : <Input className="font-mono" aria-label={`Nome do banco restaurado para ${f.database}`} value={targets[f.database!] ?? ''} disabled={busy || done !== ''} onChange={(e) => setTargets({ ...targets, [f.database!]: e.target.value })} />}</td>
              </tr>
            ))}
          </tbody>
        </table>
        {logs !== null && (
          <div className="max-h-56 overflow-y-auto rounded-lg bg-bg p-3 font-mono text-xs">
            {logs.length === 0 && <span className="inline-flex items-center gap-2 text-muted"><Spinner /> Aguardando o agente…</span>}
            {logs.map((l) => <div key={l.seq} className={l.level === 'error' ? 'text-danger' : l.level === 'warn' ? 'text-accent' : ''}>{l.text}</div>)}
          </div>
        )}
        {done === 'ok' && <div className="rounded-lg border border-ok/40 bg-ok/10 px-3 py-2 text-ok">Restauração concluída.</div>}
        {done === '' && <Button variant={overwrite ? 'danger' : 'primary'} disabled={busy || picked.length === 0} onClick={go}>{busy ? 'Restaurando…' : overwrite ? `Apagar e restaurar ${picked.length} banco(s)` : `Restaurar ${picked.length} banco(s)`}</Button>}
      </>)}
    </Card>
  )
}
