import { FormEvent, useCallback, useEffect, useState } from 'react'
import { Agent, CloudAccount, CLOUD_LABEL, CRON_PRESETS, Job, User, api, ApiError, describeCron, fmtDate, STATUS } from '../api'
import { Badge, Button, Card, ErrorBox, Field, Input, Modal } from '../ui'

export default function Jobs({ me }: { me: User }) {
  const [jobs, setJobs] = useState<Job[]>([])
  const [agents, setAgents] = useState<Agent[]>([])
  const [clouds, setClouds] = useState<CloudAccount[]>([])
  const [err, setErr] = useState('')
  const [info, setInfo] = useState('')
  const [edit, setEdit] = useState<Job | 'new' | null>(null)
  const canEdit = me.role !== 'viewer'

  const load = useCallback(() => {
    api<{ jobs: Job[] }>('GET', '/api/jobs').then((r) => { setJobs(r.jobs); setErr('') }).catch((x) => setErr((x as ApiError).message))
    api<{ agents: Agent[] }>('GET', '/api/agents').then((r) => setAgents(r.agents)).catch(() => {})
    api<{ accounts: CloudAccount[] }>('GET', '/api/clouds').then((r) => setClouds(r.accounts)).catch(() => {})
  }, [])
  useEffect(() => { load(); const t = setInterval(load, 10000); return () => clearInterval(t) }, [load])

  async function act(fn: () => Promise<unknown>, ok?: string) {
    try { await fn(); setErr(''); if (ok) setInfo(ok); load() } catch (x) { setInfo(''); setErr((x as ApiError).message) }
  }

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between gap-3">
        <div>
          <h1 className="text-2xl font-semibold">Jobs de backup</h1>
          <p className="text-sm text-muted">O que salvar, quando e onde. O agente executa sozinho, mesmo se o Server estiver fora do ar.</p>
        </div>
        {canEdit && <Button onClick={() => setEdit('new')} disabled={agents.filter((a) => !a.revoked_at).length === 0} title={agents.length === 0 ? 'Cadastre um agente primeiro' : ''}>+ Novo job</Button>}
      </div>
      <ErrorBox message={err} />
      {info && <div className="rounded-lg border border-ok/40 bg-ok/10 px-3 py-2 text-sm text-ok">{info}</div>}

      <Card className="overflow-x-auto p-0">
        <table className="w-full text-sm">
          <thead className="border-b border-border text-left text-muted">
            <tr><th className="p-3">Job</th><th className="p-3">Agenda</th><th className="p-3">Próxima</th><th className="p-3">Última execução</th><th className="p-3" /></tr>
          </thead>
          <tbody>
            {jobs.length === 0 && <tr><td colSpan={5} className="p-6 text-center text-muted">Nenhum job ainda. Clique em “Novo job”.</td></tr>}
            {jobs.map((j) => {
              const s = j.last_run ? STATUS[j.last_run.status] : null
              return (
                <tr key={j.id} className="border-b border-border align-top last:border-0">
                  <td className="p-3">
                    <b>{j.name}</b> {!j.enabled && <Badge tone="off">Pausado</Badge>}
                    <div className="text-xs text-muted">{j.agent_name} {j.agent_online ? <span className="text-ok">● online</span> : <span>○ offline</span>} · {j.paths.length} origem(ns) · {j.cloud_account_id ? <span title={j.cloud_folder}>☁ {clouds.find((c) => c.id === j.cloud_account_id)?.name ?? 'nuvem'}</span> : 'só local'}</div>
                  </td>
                  <td className="p-3 text-muted">{describeCron(j.cron)}<div className="text-xs">{j.timezone}</div></td>
                  <td className="p-3 text-muted">{j.enabled ? fmtDate(j.next_run_at) : '—'}</td>
                  <td className="p-3">{s ? <><Badge tone={s.tone}>{s.label}</Badge><div className="mt-1 text-xs text-muted">{fmtDate(j.last_run!.started_at ?? j.last_run!.scheduled_for ?? j.last_run!.created_at)}</div></> : <span className="text-muted">nunca executou</span>}</td>
                  <td className="p-3 text-right whitespace-nowrap">
                    <a className="text-primary hover:underline" href={`#/historico?job=${j.id}`}>Histórico</a>
                    {canEdit && (<>
                      <button className="ml-3 text-primary hover:underline" onClick={() => confirm(`Executar "${j.name}" agora?`) && act(() => api('POST', `/api/jobs/${j.id}/run`), `Backup "${j.name}" enviado ao agente.`)}>Executar agora</button>
                      <button className="ml-3 text-primary hover:underline" onClick={() => setEdit(j)}>Editar</button>
                      <button className="ml-3 text-primary hover:underline" onClick={() => act(() => api('PATCH', `/api/jobs/${j.id}`, { enabled: !j.enabled }))}>{j.enabled ? 'Pausar' : 'Ativar'}</button>
                      <button className="ml-3 text-danger hover:underline" onClick={() => confirm(`Excluir o job "${j.name}" e todo o seu histórico? Os arquivos de backup já gerados no agente não são apagados.`) && act(() => api('DELETE', `/api/jobs/${j.id}`))}>Excluir</button>
                    </>)}
                  </td>
                </tr>
              )
            })}
          </tbody>
        </table>
      </Card>

      {edit && <Modal title={edit === 'new' ? 'Novo job' : `Editar "${edit.name}"`} onClose={() => setEdit(null)}>
        <JobForm job={edit === 'new' ? null : edit} agents={agents.filter((a) => !a.revoked_at)} clouds={clouds.filter((c) => c.status === 'connected')} onDone={() => { setEdit(null); load() }} />
      </Modal>}
    </div>
  )
}

function JobForm({ job, agents, clouds, onDone }: { job: Job | null; agents: Agent[]; clouds: CloudAccount[]; onDone: () => void }) {
  const isPreset = (c: string) => CRON_PRESETS.some((p) => p.cron === c)
  const [f, setF] = useState({
    name: job?.name ?? '', agent_id: job?.agent_id ?? agents[0]?.id ?? 0,
    paths: (job?.paths ?? ['/etc']).join('\n'), excludes: (job?.excludes ?? []).join('\n'),
    cron: job?.cron ?? '10 0 * * *', timezone: job?.timezone ?? 'America/Sao_Paulo',
    dest_subdir: job?.dest_subdir ?? '', keep: job?.keep ?? 7, enabled: job?.enabled ?? true,
    cloud_account_id: job?.cloud_account_id ?? 0, cloud_folder: job?.cloud_folder ?? '', cloud_keep: job?.cloud_keep ?? 7, cloud_keep_days: job?.cloud_keep_days ?? 0,
  })
  const [custom, setCustom] = useState(!isPreset(f.cron))
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState(false)
  const lines = (s: string) => s.split('\n').map((x) => x.trim()).filter(Boolean)
  const zones: string[] = (Intl as any).supportedValuesOf ? (Intl as any).supportedValuesOf('timeZone') : ['America/Sao_Paulo', 'UTC']

  async function submit(e: FormEvent) {
    e.preventDefault(); setErr(''); setBusy(true)
    const body: Record<string, unknown> = { name: f.name, agent_id: Number(f.agent_id), paths: lines(f.paths), excludes: lines(f.excludes), cron: f.cron, timezone: f.timezone, dest_subdir: f.dest_subdir, keep: Number(f.keep), enabled: f.enabled }
    if (f.cloud_account_id) Object.assign(body, { cloud_account_id: Number(f.cloud_account_id), cloud_folder: f.cloud_folder, cloud_keep: Number(f.cloud_keep), cloud_keep_days: Number(f.cloud_keep_days) })
    else Object.assign(body, { clear_cloud: true })
    try { job ? await api('PATCH', `/api/jobs/${job.id}`, body) : await api('POST', '/api/jobs', body); onDone() }
    catch (x) { setErr((x as ApiError).message) } finally { setBusy(false) }
  }
  const sel = 'w-full rounded-lg border border-border bg-surface px-3 py-2'
  return (
    <form onSubmit={submit} className="space-y-4">
      <div className="grid gap-3 sm:grid-cols-2">
        <Field label="Nome do job"><Input autoFocus value={f.name} onChange={(e) => setF({ ...f, name: e.target.value })} required /></Field>
        <Field label="Agente (servidor)">
          <select className={sel} value={f.agent_id} onChange={(e) => setF({ ...f, agent_id: Number(e.target.value) })}>
            {agents.map((a) => <option key={a.id} value={a.id}>{a.name}</option>)}
          </select>
        </Field>
      </div>
      <Field label="Pastas e arquivos a salvar" hint="Um caminho por linha, absoluto (ex.: /etc, /srv, /home). Dentro de contêiner, os caminhos são os do servidor, não os do contêiner.">
        <textarea className={`${sel} font-mono text-xs`} rows={4} value={f.paths} onChange={(e) => setF({ ...f, paths: e.target.value })} required />
      </Field>
      <Field label="Exclusões (opcional)" hint="Um padrão por linha. * vale para qualquer coisa. Sem / vale para o nome em qualquer pasta (*.tmp); com / vale para o caminho (*/data/cache/*).">
        <textarea className={`${sel} font-mono text-xs`} rows={3} value={f.excludes} onChange={(e) => setF({ ...f, excludes: e.target.value })} />
      </Field>
      <div className="grid gap-3 sm:grid-cols-2">
        <Field label="Quando">
          <select className={sel} value={custom ? 'custom' : f.cron} onChange={(e) => { if (e.target.value === 'custom') setCustom(true); else { setCustom(false); setF({ ...f, cron: e.target.value }) } }}>
            {CRON_PRESETS.map((p) => <option key={p.cron} value={p.cron}>{p.label}</option>)}
            <option value="custom">Personalizado…</option>
          </select>
        </Field>
        <Field label="Fuso horário">
          <input list="zones" className={sel} value={f.timezone} onChange={(e) => setF({ ...f, timezone: e.target.value })} required />
          <datalist id="zones">{zones.map((z) => <option key={z} value={z} />)}</datalist>
        </Field>
      </div>
      {custom && (
        <Field label="Expressão cron" hint="5 campos: minuto hora dia-do-mês mês dia-da-semana. Ex.: 30 1 * * 1-5 = dias úteis às 01:30.">
          <Input className="font-mono" value={f.cron} onChange={(e) => setF({ ...f, cron: e.target.value })} required />
        </Field>
      )}
      <div className="grid gap-3 sm:grid-cols-2">
        <Field label="Pasta de destino no agente" hint="Dentro da pasta de backups do agente. Vazio = nome do job.">
          <Input value={f.dest_subdir} placeholder="(automático)" onChange={(e) => setF({ ...f, dest_subdir: e.target.value })} />
        </Field>
        <Field label="Cópias locais a manter" hint={f.cloud_account_id ? '0 = apagar a cópia local depois que a nuvem confirmar o envio.' : 'A retenção só roda depois que o novo arquivo for verificado.'}>
          <Input type="number" min={f.cloud_account_id ? 0 : 1} max={365} value={f.keep} onChange={(e) => setF({ ...f, keep: Number(e.target.value) })} required />
        </Field>
      </div>
      <fieldset className="space-y-3 rounded-lg border border-border p-3">
        <legend className="px-1 text-sm font-medium">Enviar para a nuvem (opcional)</legend>
        <Field label="Conta de nuvem" hint={clouds.length === 0 ? 'Nenhuma conta conectada. Conecte uma na página Nuvem.' : undefined}>
          <select className={sel} value={f.cloud_account_id} onChange={(e) => setF({ ...f, cloud_account_id: Number(e.target.value), keep: Number(e.target.value) && f.keep === 7 ? 0 : f.keep })}>
            <option value={0}>Só cópia local</option>
            {clouds.map((c) => <option key={c.id} value={c.id}>{c.name} ({CLOUD_LABEL[c.type]})</option>)}
          </select>
        </Field>
        {f.cloud_account_id !== 0 && (<>
          <Field label="Pasta na nuvem" hint="Vazio = Coruja Backup/<servidor>/<job>. É criada automaticamente.">
            <Input value={f.cloud_folder} placeholder="(automática)" onChange={(e) => setF({ ...f, cloud_folder: e.target.value })} />
          </Field>
          <div className="grid gap-3 sm:grid-cols-2">
            <Field label="Cópias a manter na nuvem" hint="0 = não limitar por quantidade."><Input type="number" min={0} max={3650} value={f.cloud_keep} onChange={(e) => setF({ ...f, cloud_keep: Number(e.target.value) })} /></Field>
            <Field label="Dias a manter na nuvem" hint="0 = não limitar por dias. Pelo menos um dos dois é obrigatório."><Input type="number" min={0} max={3650} value={f.cloud_keep_days} onChange={(e) => setF({ ...f, cloud_keep_days: Number(e.target.value) })} /></Field>
          </div>
          <p className="text-xs text-muted">A retenção na nuvem só roda depois que o envio for conferido por hash, e o arquivo mais novo nunca é apagado.</p>
        </>)}
      </fieldset>
      <label className="flex items-center gap-2 text-sm"><input type="checkbox" checked={f.enabled} onChange={(e) => setF({ ...f, enabled: e.target.checked })} /> Job ativo</label>
      <ErrorBox message={err} />
      <Button disabled={busy}>{job ? 'Salvar alterações' : 'Criar job'}</Button>
    </form>
  )
}
