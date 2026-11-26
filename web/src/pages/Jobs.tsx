import { ReactNode, useCallback, useEffect, useState } from 'react'
import { Agent, CloudAccount, CLOUD_LABEL, ContainerInfo, CRON_PRESETS, DbView, ENGINE_LABEL, ENGINE_PORT, Job, User, api, ApiError, describeCron, fmtDate, STATUS } from '../api'
import { Actions, Badge, Button, Card, ErrorBox, Field, IconButton, Input, Modal, Spinner } from '../ui'
import { confirmar } from '../Dialogs'
import { FolderField, PathList, TreePicker } from '../Picker'
import { LocalDirField, agentHasDiskPicker } from '../LocalDir'
import { Wizard } from '../Wizard'

export default function Jobs({ me }: { me: User }) {
  const [jobs, setJobs] = useState<Job[]>([])
  const [agents, setAgents] = useState<Agent[]>([])
  const [clouds, setClouds] = useState<CloudAccount[]>([])
  const [err, setErr] = useState('')
  const [info, setInfo] = useState('')
  const [edit, setEdit] = useState<Job | 'new' | null>(null)
  const canEdit = me.role !== 'viewer'

  // até o agente começar, o job recém-disparado não tem execução "em andamento": acompanha de perto por um tempo
  const [waitingUntil, setWaitingUntil] = useState(0)
  const running = jobs.some((j) => j.last_run?.status === 'running' || j.last_run?.status === 'queued')
  const fast = running || Date.now() < waitingUntil
  const load = useCallback(() => {
    api<{ jobs: Job[] }>('GET', '/api/jobs').then((r) => { setJobs(r.jobs); setErr('') }).catch((x) => setErr((x as ApiError).message))
    api<{ agents: Agent[] }>('GET', '/api/agents').then((r) => setAgents(r.agents)).catch(() => {})
    api<{ accounts: CloudAccount[] }>('GET', '/api/clouds').then((r) => setClouds(r.accounts)).catch(() => {})
  }, [])
  useEffect(() => { load() }, [load])
  useEffect(() => { const t = setInterval(load, fast ? 2000 : 10000); return () => clearInterval(t) }, [load, fast])

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
                    <div className="text-xs text-muted">{j.agent_name} {j.agent_online ? <span className="text-ok">● online</span> : <span>○ offline</span>} · {j.kind === 'database' ? `🗄 ${ENGINE_LABEL[j.db?.engine ?? ''] ?? 'Banco'}${j.db?.mode === 'docker' ? ` · ${j.db?.container}` : ` · ${j.db?.host}`}` : `${j.paths.length} origem(ns)`} · {j.cloud_account_id ? <span title={j.cloud_folder}>☁ {clouds.find((c) => c.id === j.cloud_account_id)?.name ?? 'nuvem'}</span> : 'só local'}</div>
                  </td>
                  <td className="p-3 text-muted">{describeCron(j.cron)}<div className="text-xs">{j.timezone}</div></td>
                  <td className="p-3 text-muted">{j.enabled ? fmtDate(j.next_run_at) : '—'}</td>
                  <td className="p-3">{j.last_run && (j.last_run.status === 'running' || j.last_run.status === 'queued') ? <LiveRun run={j.last_run} />
                    : s ? <><Badge tone={s.tone}>{s.label}</Badge><div className="mt-1 text-xs text-muted">{fmtDate(j.last_run!.started_at ?? j.last_run!.scheduled_for ?? j.last_run!.created_at)}</div></> : <span className="text-muted">nunca executou</span>}</td>
                  <td className="p-3 text-right whitespace-nowrap">
                    <Actions>
                      {canEdit && <IconButton icon="executar" label="Executar agora" onClick={async () => await confirmar({ title: 'Executar agora?', message: <>O backup <b>{j.name}</b> começa agora no agente <b>{j.agent_name}</b>.</>, confirmLabel: 'Executar', icon: 'executar' }) && act(async () => { await api('POST', `/api/jobs/${j.id}/run`); setWaitingUntil(Date.now() + 30000) }, `Backup "${j.name}" enviado ao agente.`)} />}
                      <IconButton icon="historico" label="Histórico" href={`#/historico?job=${j.id}`} />
                      {canEdit && (<>
                        <IconButton icon="editar" label="Editar" onClick={() => setEdit(j)} />
                        <IconButton icon={j.enabled ? 'pausar' : 'ativar'} label={j.enabled ? 'Pausar' : 'Ativar'} onClick={() => act(() => api('PATCH', `/api/jobs/${j.id}`, { enabled: !j.enabled }))} />
                        <IconButton icon="excluir" tone="danger" label="Excluir" onClick={async () => await confirmar({ title: `Excluir o job "${j.name}"?`, message: 'Todo o histórico dele também é apagado. Os arquivos de backup já gerados no agente e nos destinos não são apagados.', danger: true }) && act(() => api('DELETE', `/api/jobs/${j.id}`))} />
                      </>)}
                    </Actions>
                  </td>
                </tr>
              )
            })}
          </tbody>
        </table>
      </Card>

      {edit && <Modal wide title={edit === 'new' ? 'Novo job' : `Editar "${edit.name}"`} onClose={() => setEdit(null)}>
        <JobForm job={edit === 'new' ? null : edit} agents={agents.filter((a) => !a.revoked_at)} clouds={clouds.filter((c) => c.status === 'connected')} onDone={() => { setEdit(null); load() }} />
      </Modal>}
    </div>
  )
}

const PHASE: Record<string, string> = { archiving: 'Compactando', uploading: 'Enviando ao destino', verifying: 'Conferindo' }

/** Execução em andamento: indicador girando, fase e andamento, atualizados a cada 2 s. */
function LiveRun({ run }: { run: NonNullable<Job['last_run']> }) {
  const queued = run.status === 'queued'
  const pct = run.phase === 'uploading' && run.progress_pct > 0 ? run.progress_pct : null
  return (
    <a href={`#/historico?job=${run.job_id}`} className="block min-w-40" aria-live="polite">
      <span className="inline-flex items-center gap-1.5 rounded-full bg-primary/10 px-2 py-0.5 text-xs font-medium text-primary">
        <Spinner />
        {queued ? 'Na fila' : 'Executando'}
      </span>
      <div className="mt-1 text-xs text-muted">{queued ? 'aguardando o agente' : `${PHASE[run.phase] ?? 'Iniciando'}${pct !== null ? ` · ${pct}%` : ''}`}</div>
      {pct !== null && <div className="mt-1 h-1 overflow-hidden rounded-full bg-border"><div className="h-full bg-primary transition-[width]" style={{ width: `${pct}%` }} /></div>}
    </a>
  )
}

function JobForm({ job, agents, clouds, onDone }: { job: Job | null; agents: Agent[]; clouds: CloudAccount[]; onDone: () => void }) {
  const isPreset = (c: string) => CRON_PRESETS.some((p) => p.cron === c)
  const [f, setF] = useState({
    name: job?.name ?? '', agent_id: job?.agent_id ?? agents[0]?.id ?? 0,
    paths: job?.paths ?? [], excludes: job?.excludes ?? [],
    cron: job?.cron ?? '10 0 * * *', timezone: job?.timezone ?? 'America/Sao_Paulo',
    dest_subdir: job?.dest_subdir ?? '', keep: job?.keep ?? 7, enabled: job?.enabled ?? true,
    kind: job?.kind ?? 'files',
    cloud_account_id: job?.cloud_account_id ?? 0, cloud_folder: job?.cloud_folder ?? '', cloud_keep: job?.cloud_keep ?? 7, cloud_keep_days: job?.cloud_keep_days ?? 0,
  })
  const [db, setDb] = useState<DbState>({
    engine: job?.db?.engine ?? 'mysql', mode: job?.db?.mode ?? 'docker', container: job?.db?.container ?? '', host: job?.db?.host ?? '', port: job?.db?.port ?? 0,
    user: job?.db?.user ?? '', password: '', auth_db: job?.db?.auth_db ?? '', databases: (job?.db?.databases ?? []).join('\n'), exclude_databases: (job?.db?.exclude_databases ?? []).join('\n'),
    ssl_skip_verify: job?.db?.ssl_skip_verify ?? false, restore_test: job?.db?.restore_test ?? 'full', restore_image: job?.db?.restore_image ?? '',
  })
  const [custom, setCustom] = useState(!isPreset(f.cron))
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState(false)
  const [noWriteAgent, setNoWriteAgent] = useState(0) // agente sem a montagem gravável: usa a pasta padrão
  const [tree, setTree] = useState<'paths' | 'excludes' | null>(null)
  // uma exclusão que é um caminho exato aparece marcada na árvore; padrões (*.tmp, */cache/*) ficam só na lista
  const isExactPath = (p: string) => p.startsWith('/') && !/[*?]/.test(p)
  const agentName = agents.find((a) => a.id === Number(f.agent_id))?.name ?? 'agente'
  const agentVersion = agents.find((a) => a.id === Number(f.agent_id))?.agent_version ?? ''
  const dest = clouds.find((c) => c.id === f.cloud_account_id)
  const destIsCloud = !!dest && ['gdrive', 'onedrive', 'dropbox'].includes(dest.type)
  const zones: string[] = (Intl as any).supportedValuesOf ? (Intl as any).supportedValuesOf('timeZone') : ['America/Sao_Paulo', 'UTC']

  const [step, setStep] = useState(0)
  const [reached, setReached] = useState(job ? 4 : 0)
  const STEPS = [
    { title: 'Identificação', hint: 'Dê um nome ao job, escolha o agente onde ele roda e o que será salvo.' },
    { title: 'Origem', hint: f.kind === 'files' ? 'Marque as pastas e os arquivos do agente que entram no backup, e o que fica de fora.' : 'Onde está o banco de dados e quais bancos salvar.' },
    { title: 'Agenda', hint: 'Quando o backup roda e de que tipo ele é.' },
    { title: 'Destino', hint: 'Onde o arquivo é gerado no agente, quantas cópias ficam lá e para onde ele é enviado.' },
    { title: 'Revisão', hint: job ? 'Confira e salve as alterações.' : 'Confira tudo antes de criar o job.' },
  ]
  // mensagem de erro (ou '') das regras de cada etapa
  function check(i: number): string {
    if (i === 0) {
      if (f.name.trim().length < 2) return 'Dê um nome ao job (pelo menos 2 caracteres).'
      if (!f.agent_id) return 'Escolha o agente.'
    }
    if (i === 1) {
      if (f.kind === 'files' && f.paths.length === 0) return 'Selecione pelo menos uma pasta ou arquivo para salvar.'
      if (f.kind === 'database' && db.mode === 'docker' && !db.container.trim()) return 'Escolha o contêiner do banco.'
      if (f.kind === 'database' && db.mode === 'network' && !db.host.trim()) return 'Informe o endereço do servidor de banco.'
    }
    if (i === 2 && !f.cron.trim()) return 'Escolha quando o backup roda.'
    if (i === 3) {
      // job novo (ou que trocou de agente): a pasta local é escolhida no painel; jobs antigos podem continuar na pasta padrão
      const mustChoose = !job || job.agent_id !== Number(f.agent_id)
      if (mustChoose && agentHasDiskPicker(agentVersion) && noWriteAgent !== Number(f.agent_id) && !f.dest_subdir.startsWith('/')) return 'Escolha a pasta do backup local no agente.'
      if (f.cloud_account_id && !f.cloud_keep && !f.cloud_keep_days) return 'Defina a retenção no destino: quantidade de cópias e/ou número de dias.'
    }
    return ''
  }
  function go(i: number) { setErr(''); setStep(i) }
  function next() {
    const m = check(step)
    if (m) return setErr(m)
    setErr(''); setStep(step + 1); setReached(Math.max(reached, step + 1))
  }
  async function finish() {
    for (let i = 0; i < 4; i++) { const m = check(i); if (m) { setStep(i); setErr(m); return } }
    setErr(''); setBusy(true)
    const body: Record<string, unknown> = { name: f.name, agent_id: Number(f.agent_id), kind: f.kind, paths: f.paths, excludes: f.excludes, cron: f.cron, timezone: f.timezone, dest_subdir: f.dest_subdir, keep: Number(f.keep), enabled: f.enabled }
    if (f.cloud_account_id) Object.assign(body, { cloud_account_id: Number(f.cloud_account_id), cloud_folder: f.cloud_folder, cloud_keep: Number(f.cloud_keep), cloud_keep_days: Number(f.cloud_keep_days) })
    else Object.assign(body, { clear_cloud: true })
    if (f.kind === 'database') body.db = dbPayload(db)
    try { job ? await api('PATCH', `/api/jobs/${job.id}`, body) : await api('POST', '/api/jobs', body); onDone() }
    catch (x) { setErr((x as ApiError).message) } finally { setBusy(false) }
  }
  const sel = 'w-full rounded-lg border border-border bg-surface px-3 py-2'
  const cronLabel = CRON_PRESETS.find((p) => p.cron === f.cron)?.label ?? describeCron(f.cron)
  const review: [string, ReactNode][] = [
    ['Nome', f.name], ['Agente', agentName], ['Tipo', f.kind === 'files' ? 'Pastas e arquivos' : `Banco de dados · ${ENGINE_LABEL[db.engine] ?? db.engine}`],
    [f.kind === 'files' ? 'O que salvar' : 'Banco', f.kind === 'files' ? f.paths.join(', ') : db.mode === 'docker' ? `contêiner ${db.container}` : `${db.host}${db.port ? ':' + db.port : ''}`],
    ...(f.kind === 'files' && f.excludes.length ? [['Fica de fora', f.excludes.join(', ')] as [string, ReactNode]] : []),
    ['Agenda', `${cronLabel} · ${f.timezone}`], ['Tipo de backup', 'Completo (full) a cada execução'],
    ['Backup local', `${f.dest_subdir.startsWith('/') ? f.dest_subdir : 'pasta padrão do agente'} · ${f.keep === 0 ? 'apagado após o envio' : `mantém ${f.keep} cópia(s)`}`],
    ['Destino', dest ? `${dest.name} · ${f.cloud_folder || 'pasta automática'} · ${[f.cloud_keep ? `${f.cloud_keep} cópia(s)` : '', f.cloud_keep_days ? `${f.cloud_keep_days} dia(s)` : ''].filter(Boolean).join(' e ')}` : 'só a cópia local'],
    ['Situação', f.enabled ? 'Ativo' : 'Pausado'],
  ]
  return (
    <Wizard steps={STEPS} current={step} maxReached={reached} onGo={go} onBack={() => go(step - 1)} onNext={next} onFinish={finish}
      finishLabel={job ? 'Salvar alterações' : 'Criar job'} busy={busy} err={err} freeJump={!!job}>
      {step === 0 && (<>
        <div className="grid gap-3 sm:grid-cols-2">
          <Field label="Nome do job"><Input autoFocus value={f.name} onChange={(e) => setF({ ...f, name: e.target.value })} /></Field>
          <Field label="Agente">
            <select className={sel} value={f.agent_id} onChange={(e) => setF({ ...f, agent_id: Number(e.target.value), dest_subdir: job && job.agent_id === Number(e.target.value) ? job.dest_subdir : '' })}>
              {agents.map((a) => <option key={a.id} value={a.id}>{a.name}{a.online ? '' : ' (offline)'}</option>)}
            </select>
          </Field>
        </div>
        <div role="radiogroup" aria-label="O que salvar" className="grid gap-3 sm:grid-cols-2">
          {([['files', 'Pastas e arquivos', 'Qualquer pasta ou arquivo do agente, com exclusões.'], ['database', 'Banco de dados', 'MySQL, MariaDB, PostgreSQL ou MongoDB, com teste de restauração.']] as const).map(([k, t, d]) => (
            <button key={k} type="button" role="radio" aria-checked={f.kind === k} disabled={!!job && f.kind !== k} onClick={() => setF({ ...f, kind: k })}
              className={`rounded-xl border p-4 text-left transition disabled:cursor-not-allowed disabled:opacity-50 ${f.kind === k ? 'border-primary ring-2 ring-primary/30' : 'border-border hover:border-primary/50'}`}>
              <b className="block text-sm">{t}</b><span className="text-xs text-muted">{d}</span>
            </button>
          ))}
        </div>
        {job && <p className="text-xs text-muted">O tipo do job não pode ser alterado depois de criado.</p>}
      </>)}
      {step === 1 && (f.kind === 'files' ? (<>
        <PathList agentId={Number(f.agent_id)} label="Pastas e arquivos a salvar" items={f.paths} empty="Nada selecionado ainda." pickLabel="Selecionar pastas e arquivos…"
          typeLabel="digitar um caminho" placeholder="/srv/dados" onPick={() => setTree('paths')} onChange={(paths) => setF((v) => ({ ...v, paths }))}
          hint="Os caminhos são os do agente. Ele precisa estar online para navegar." />
        <PathList agentId={Number(f.agent_id)} label="Exclusões (opcional)" items={f.excludes} empty="Nada fica de fora." pickLabel="Selecionar o que fica de fora…"
          pickDisabled={f.paths.length === 0 ? 'Primeiro selecione o que salvar' : undefined} typeLabel="adicionar um padrão (ex.: *.tmp)" placeholder="*.tmp"
          onPick={() => setTree('excludes')} onChange={(excludes) => setF((v) => ({ ...v, excludes }))}
          hint="Num padrão, * vale para qualquer coisa. Sem / vale para o nome em qualquer pasta (*.tmp); com / vale para o caminho (*/data/cache/*)." />
        {tree === 'paths' && (
          <TreePicker title={`O que salvar em ${agentName}`} intro="Marque as pastas e os arquivos. Marcar uma pasta inclui tudo o que há dentro dela." agentId={Number(f.agent_id)}
            roots={['/']} initial={f.paths} confirmLabel="Usar a seleção" onClose={() => setTree(null)} onConfirm={(paths) => { setF((v) => ({ ...v, paths })); setTree(null) }} />
        )}
        {tree === 'excludes' && (
          <TreePicker title="O que fica de fora" intro="Dentro do que será salvo, marque o que NÃO deve entrar no backup." agentId={Number(f.agent_id)}
            roots={f.paths} lockRoots initial={f.excludes.filter(isExactPath)} confirmLabel="Excluir os marcados" onClose={() => setTree(null)}
            onConfirm={(picked) => { setF((v) => ({ ...v, excludes: [...v.excludes.filter((p) => !isExactPath(p)), ...picked] })); setTree(null) }} />
        )}
      </>) : <DbSection db={db} setDb={setDb} agentId={Number(f.agent_id)} job={job} />)}
      {step === 2 && (<>
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
            <Input className="font-mono" value={f.cron} onChange={(e) => setF({ ...f, cron: e.target.value })} />
          </Field>
        )}
        <div>
          <span className="mb-1.5 block text-sm font-medium">Tipo de backup</span>
          <div role="radiogroup" aria-label="Tipo de backup" className="grid gap-3 sm:grid-cols-2">
            <div role="radio" aria-checked="true" className="rounded-xl border border-primary p-4 ring-2 ring-primary/30">
              <b className="block text-sm">Completo (full)</b><span className="text-xs text-muted">Cada execução gera um arquivo com tudo. Restauração simples, a partir de um único arquivo.</span>
            </div>
            <div role="radio" aria-checked="false" aria-disabled="true" className="cursor-not-allowed rounded-xl border border-dashed border-border p-4 opacity-60">
              <b className="block text-sm">Full + incrementais <span className="ml-1 rounded bg-bg px-1.5 text-[11px] font-normal text-muted">em breve</span></b>
              <span className="text-xs text-muted">Um completo de tempos em tempos e, entre eles, só o que mudou. Menos espaço e envios mais rápidos.</span>
            </div>
          </div>
        </div>
        <label className="flex items-center gap-2 text-sm"><input type="checkbox" checked={f.enabled} onChange={(e) => setF({ ...f, enabled: e.target.checked })} /> Job ativo (roda sozinho na agenda)</label>
      </>)}
      {step === 3 && (<>
        <LocalDirField key={f.agent_id} agentId={Number(f.agent_id)} agentName={agentName} agentVersion={agentVersion} value={f.dest_subdir}
          onChange={(dest_subdir) => setF((v) => ({ ...v, dest_subdir }))} onUnavailable={() => setNoWriteAgent(Number(f.agent_id))} />
        <div className="grid gap-3 sm:grid-cols-2">
          <Field label="Cópias locais a manter" hint={f.cloud_account_id ? '0 = apagar a cópia local depois que o destino confirmar o envio.' : 'A retenção só roda depois que o novo arquivo for verificado.'}>
            <Input type="number" min={f.cloud_account_id ? 0 : 1} max={365} value={f.keep} onChange={(e) => setF({ ...f, keep: Number(e.target.value) })} required />
          </Field>
        </div>
        <fieldset className="space-y-3 rounded-xl border border-border p-4">
          <legend className="px-1 text-sm font-medium">Enviar para um destino (opcional)</legend>
          <Field label="Destino" hint={clouds.length === 0 ? 'Nenhum destino criado. Crie um na página Destinos.' : undefined}>
            <select className={sel} value={f.cloud_account_id} onChange={(e) => setF({ ...f, cloud_account_id: Number(e.target.value), cloud_folder: '', keep: Number(e.target.value) && f.keep === 7 ? 0 : f.keep })}>
              <option value={0}>Só cópia local</option>
              {clouds.map((c) => <option key={c.id} value={c.id}>{c.name} ({CLOUD_LABEL[c.type]})</option>)}
            </select>
          </Field>
          {f.cloud_account_id !== 0 && (<>
            <FolderField key={f.cloud_account_id} label="Pasta no destino" hint="Automática = Coruja Backup/<agente>/<job>. Pastas novas são criadas no primeiro backup." value={f.cloud_folder} autoText="(automática)"
              onChange={(cloud_folder) => setF((v) => ({ ...v, cloud_folder }))}
              picker={{ title: `Pasta em ${dest?.name ?? 'destino'}`, rootLabel: dest?.name ?? 'Destino',
                intro: dest?.type === 'gdrive' ? 'Por segurança, o Google Drive só mostra ao Coruja Backup as pastas criadas por ele.'
                  : dest?.type === 'dropbox' ? 'No Dropbox, o Coruja Backup só enxerga a própria pasta, em Aplicativos.'
                  : destIsCloud ? 'Pastas da sua conta.' : `Pastas do destino, consultadas pelo agente ${agentName}.`,
                load: async (path) => (await api<{ folders: string[] }>('GET', `/api/clouds/${f.cloud_account_id}/folders?agent_id=${f.agent_id}&path=${encodeURIComponent(path)}`)).folders }} />
            <div className="grid gap-3 sm:grid-cols-2">
              <Field label="Cópias a manter no destino" hint="0 = não limitar por quantidade."><Input type="number" min={0} max={3650} value={f.cloud_keep} onChange={(e) => setF({ ...f, cloud_keep: Number(e.target.value) })} /></Field>
              <Field label="Dias a manter no destino" hint="0 = não limitar por dias. Pelo menos um dos dois é obrigatório."><Input type="number" min={0} max={3650} value={f.cloud_keep_days} onChange={(e) => setF({ ...f, cloud_keep_days: Number(e.target.value) })} /></Field>
            </div>
            <p className="text-xs text-muted">A retenção no destino só roda depois que o envio for conferido, e o arquivo mais novo nunca é apagado.</p>
          </>)}
        </fieldset>
      </>)}
      {step === 4 && (
        <dl className="divide-y divide-border rounded-xl border border-border text-sm">
          {review.map(([k, v]) => (
            <div key={k} className="grid gap-1 px-4 py-2.5 sm:grid-cols-[10rem_1fr]">
              <dt className="text-muted">{k}</dt><dd className="break-words font-medium">{v}</dd>
            </div>
          ))}
        </dl>
      )}
    </Wizard>
  )
}

function dbPayload(d: { [k: string]: any }) {
  const lines = (s: string) => s.split('\n').map((x) => x.trim()).filter(Boolean)
  return { engine: d.engine, mode: d.mode, container: d.container, host: d.host, port: Number(d.port) || 0, user: d.user, password: d.password, auth_db: d.auth_db,
    databases: lines(d.databases), exclude_databases: lines(d.exclude_databases), ssl_skip_verify: d.ssl_skip_verify, restore_test: d.restore_test, restore_image: d.restore_image }
}

type DbState = { engine: string; mode: string; container: string; host: string; port: number; user: string; password: string; auth_db: string; databases: string; exclude_databases: string; ssl_skip_verify: boolean; restore_test: string; restore_image: string }

function DbSection({ db, setDb, agentId, job }: { db: DbState; setDb: (d: DbState) => void; agentId: number; job: Job | null }) {
  const [containers, setContainers] = useState<ContainerInfo[] | null>(null)
  const [note, setNote] = useState('')
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState(false)
  const sel = 'w-full rounded-lg border border-border bg-surface px-3 py-2'
  const upd = (p: Partial<DbState>) => setDb({ ...db, ...p })

  async function loadContainers() {
    setErr(''); setBusy(true)
    try { const r = await api<{ containers: ContainerInfo[] }>('GET', `/api/agents/${agentId}/containers`); setContainers(r.containers) }
    catch (x) { setErr((x as ApiError).message) } finally { setBusy(false) }
  }
  async function test() {
    setErr(''); setNote(''); setBusy(true)
    try {
      const r = await api<{ databases: string[]; version: string; where: string; user: string }>('POST', `/api/agents/${agentId}/db-test`, { db: dbPayload(db), job_id: job?.id ?? 0 })
      setNote(`Conexão ok em ${r.where} (usuário ${r.user || '—'}, versão ${r.version}). Bancos que serão salvos: ${r.databases.join(', ') || 'nenhum'}.`)
    } catch (x) { setErr((x as ApiError).message) } finally { setBusy(false) }
  }
  const isDocker = db.mode === 'docker'
  return (
    <fieldset className="space-y-3 rounded-lg border border-border p-3">
      <legend className="px-1 text-sm font-medium">Banco de dados</legend>
      <div className="grid gap-3 sm:grid-cols-2">
        <Field label="Tipo de banco">
          <select className={sel} value={db.engine} onChange={(e) => upd({ engine: e.target.value, port: 0 })}>
            {Object.entries(ENGINE_LABEL).map(([k, v]) => <option key={k} value={k}>{v}</option>)}
          </select>
        </Field>
        <Field label="Como o agente acessa" hint={isDocker ? 'Recomendado: executa o dump dentro do contêiner (usa as ferramentas na versão certa).' : 'Usa as ferramentas de cliente do agente; o servidor de banco precisa estar acessível pela rede.'}>
          <select className={sel} value={db.mode} onChange={(e) => upd({ mode: e.target.value })}>
            <option value="docker">Contêiner Docker (via docker.sock)</option>
            <option value="network">Rede (host e porta)</option>
          </select>
        </Field>
      </div>
      {isDocker ? (
        <Field label="Contêiner do banco" hint="Precisa do /var/run/docker.sock montado no agente.">
          <div className="flex gap-2">
            {containers ? (
              <select className={sel} value={db.container} onChange={(e) => { const c = containers.find((x) => x.name === e.target.value); upd({ container: e.target.value, engine: c?.engine || db.engine }) }}>
                <option value="">Selecione…</option>
                {containers.map((c) => <option key={c.name} value={c.name}>{c.name} — {c.image}{c.engine ? ` (${ENGINE_LABEL[c.engine]})` : ''}</option>)}
              </select>
            ) : <Input value={db.container} onChange={(e) => upd({ container: e.target.value })} placeholder="nome do contêiner" />}
            <Button type="button" variant="ghost" onClick={loadContainers} disabled={busy} className="whitespace-nowrap">Buscar no agente</Button>
          </div>
        </Field>
      ) : (
        <div className="grid gap-3 sm:grid-cols-3">
          <div className="sm:col-span-2"><Field label="Endereço do servidor de banco"><Input value={db.host} onChange={(e) => upd({ host: e.target.value })} placeholder="10.0.0.5 ou db.exemplo.com" /></Field></div>
          <Field label="Porta"><Input type="number" min={0} max={65535} value={db.port || ''} placeholder={String(ENGINE_PORT[db.engine])} onChange={(e) => upd({ port: Number(e.target.value) })} /></Field>
        </div>
      )}
      <div className="grid gap-3 sm:grid-cols-2">
        <Field label="Usuário" hint={isDocker ? 'Vazio = o usuário administrador configurado no próprio contêiner (ex.: root, postgres).' : undefined}><Input value={db.user} onChange={(e) => upd({ user: e.target.value })} autoComplete="off" /></Field>
        <Field label="Senha" hint={isDocker ? 'Vazia = usa a senha do ambiente do contêiner (MYSQL_ROOT_PASSWORD, POSTGRES_PASSWORD...).' : job?.db?.password_set ? 'Já cadastrada: deixe vazio para manter.' : undefined}>
          <Input type="password" autoComplete="new-password" value={db.password} placeholder={job?.db?.password_set ? '•••••••• (mantida)' : ''} onChange={(e) => upd({ password: e.target.value })} />
        </Field>
      </div>
      {db.engine === 'mongodb' && <Field label="Banco de autenticação" hint="Padrão: admin."><Input value={db.auth_db} onChange={(e) => upd({ auth_db: e.target.value })} placeholder="admin" /></Field>}
      {!isDocker && (db.engine === 'mysql' || db.engine === 'mariadb' || db.engine === 'postgres') && (
        <label className="flex items-center gap-2 text-sm"><input type="checkbox" checked={db.ssl_skip_verify} onChange={(e) => upd({ ssl_skip_verify: e.target.checked })} /> Exigir TLS sem validar o certificado (servidores com certificado autoassinado)</label>
      )}
      <div className="grid gap-3 sm:grid-cols-2">
        <Field label="Bancos a salvar" hint="Um por linha. Vazio = todos."><textarea className={`${sel} font-mono text-xs`} rows={3} value={db.databases} onChange={(e) => upd({ databases: e.target.value })} /></Field>
        <Field label="Bancos a ignorar" hint="Um por linha. Já ignorados: information_schema, performance_schema, sys (MySQL) e local, config (MongoDB)."><textarea className={`${sel} font-mono text-xs`} rows={3} value={db.exclude_databases} onChange={(e) => upd({ exclude_databases: e.target.value })} /></Field>
      </div>
      <Field label="Teste de restauração" hint={db.restore_test === 'full' ? 'Restaura o backup num servidor de banco descartável (sem rede, apagado em seguida) e compara o número de tabelas com a origem. Exige Docker no agente e usa alguns minutos e ~1 GiB de memória.' : db.restore_test === 'structural' ? 'Confere a integridade do arquivo e a estrutura do dump, sem restaurar.' : 'Nenhuma conferência além do próprio dump.'}>
        <select className={sel} value={db.restore_test} onChange={(e) => upd({ restore_test: e.target.value })}>
          <option value="full">Restauração real (recomendado)</option>
          <option value="structural">Só estrutura</option>
          <option value="none">Nenhum</option>
        </select>
      </Field>
      {db.restore_test === 'full' && <Field label="Imagem do servidor de banco descartável (opcional)" hint="Vazio = a mesma imagem do contêiner de origem (ou a versão equivalente, no modo rede)."><Input value={db.restore_image} onChange={(e) => upd({ restore_image: e.target.value })} placeholder="mysql:8.0" /></Field>}
      <div className="flex items-center gap-3">
        <Button type="button" variant="ghost" onClick={test} disabled={busy || !agentId}>Testar conexão</Button>
        {busy && <span className="text-sm text-muted">Consultando o agente…</span>}
      </div>
      <ErrorBox message={err} />
      {note && <div className="rounded-lg border border-ok/40 bg-ok/10 px-3 py-2 text-sm text-ok">{note}</div>}
    </fieldset>
  )
}
