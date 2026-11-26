import { FormEvent, useCallback, useEffect, useState } from 'react'
import { Channel, EVENT_LABEL, Job, NotifLog, Rule, api, ApiError, fmtDate } from '../api'
import { Badge, Button, Card, ErrorBox, Field, Input, Modal } from '../ui'

type Tab = 'canais' | 'regras' | 'historico'

export default function Notifications() {
  const [tab, setTab] = useState<Tab>('canais')
  const [channels, setChannels] = useState<Channel[]>([])
  const [rules, setRules] = useState<Rule[]>([])
  const [jobs, setJobs] = useState<Job[]>([])
  const [log, setLog] = useState<NotifLog[]>([])
  const [baseUrl, setBaseUrl] = useState('')
  const [err, setErr] = useState('')
  const [info, setInfo] = useState('')

  const load = useCallback(() => {
    api<{ channels: Channel[] }>('GET', '/api/notifications/channels').then((r) => setChannels(r.channels)).catch((x) => setErr((x as ApiError).message))
    api<{ rules: Rule[] }>('GET', '/api/notifications/rules').then((r) => setRules(r.rules)).catch(() => {})
    api<{ jobs: Job[] }>('GET', '/api/jobs').then((r) => setJobs(r.jobs)).catch(() => {})
    api<{ notifications: NotifLog[] }>('GET', '/api/notifications/log?limit=100').then((r) => setLog(r.notifications)).catch(() => {})
    api<{ base_url: string }>('GET', '/api/notifications/settings').then((r) => setBaseUrl(r.base_url)).catch(() => {})
  }, [])
  useEffect(() => { load(); const t = setInterval(load, 15000); return () => clearInterval(t) }, [load])
  async function act(fn: () => Promise<unknown>, ok?: string) {
    try { await fn(); setErr(''); if (ok) setInfo(ok); load() } catch (x) { setInfo(''); setErr((x as ApiError).message) }
  }

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold">Notificações e relatórios</h1>
        <p className="text-sm text-muted">Avisos por e-mail e Telegram, relatório diário consolidado em PDF A4 e alerta de servidor offline.</p>
      </div>
      <ErrorBox message={err} />
      {info && <div className="rounded-lg border border-ok/40 bg-ok/10 px-3 py-2 text-sm text-ok">{info}</div>}
      <div role="tablist" className="flex gap-1 border-b border-border">
        {([['canais', 'Canais'], ['regras', 'Regras'], ['historico', 'Histórico de envios']] as [Tab, string][]).map(([k, l]) => (
          <button key={k} role="tab" aria-selected={tab === k} onClick={() => setTab(k)} className={`-mb-px rounded-t-md border border-b-0 px-4 py-2 text-sm ${tab === k ? 'border-border bg-surface font-medium' : 'border-transparent text-muted hover:text-text'}`}>{l}</button>
        ))}
      </div>
      {tab === 'canais' && <Channels channels={channels} act={act} reload={load} setErr={setErr} setInfo={setInfo} />}
      {tab === 'regras' && <Rules rules={rules} channels={channels} jobs={jobs} act={act} reload={load} />}
      {tab === 'historico' && <History log={log} />}
      <Card>
        <h2 className="mb-2 font-semibold">Endereço público do sistema (opcional)</h2>
        <p className="mb-2 text-sm text-muted">Usado para colocar o link “Ver o log completo” nos e-mails e mensagens. Ex.: https://backup.suaempresa.com.br</p>
        <form className="flex max-w-xl gap-2" onSubmit={(e) => { e.preventDefault(); act(() => api('PUT', '/api/notifications/settings', { base_url: baseUrl }), 'Endereço salvo.') }}>
          <Input value={baseUrl} onChange={(e) => setBaseUrl(e.target.value)} placeholder="https://" /><Button>Salvar</Button>
        </form>
      </Card>
    </div>
  )
}

function Channels({ channels, act, reload, setErr, setInfo }: { channels: Channel[]; act: (f: () => Promise<unknown>, ok?: string) => void; reload: () => void; setErr: (s: string) => void; setInfo: (s: string) => void }) {
  const [edit, setEdit] = useState<Channel | 'new' | null>(null)
  const [testing, setTesting] = useState(0)
  async function test(c: Channel) {
    setTesting(c.id); setErr(''); setInfo('')
    try {
      const r = await api<{ ok: boolean; error?: string }>('POST', `/api/notifications/channels/${c.id}/test`)
      if (r.ok) setInfo(`Teste enviado por "${c.name}". Confira a caixa de entrada/o Telegram (vai um relatório de exemplo com PDF).`); else setErr(r.error ?? 'Falha no teste')
    } catch (x) { setErr((x as ApiError).message) } finally { setTesting(0) }
  }
  return (
    <div className="space-y-4">
      <div className="flex justify-end"><Button onClick={() => setEdit('new')}>+ Novo canal</Button></div>
      <div className="grid gap-4 md:grid-cols-2">
        {channels.length === 0 && <Card className="md:col-span-2"><p className="text-center text-muted">Nenhum canal ainda. Crie um canal de e-mail (SMTP) ou de Telegram para receber os avisos.</p></Card>}
        {channels.map((c) => (
          <Card key={c.id}>
            <div className="flex items-start justify-between gap-2">
              <div>
                <div className="font-semibold">{c.type === 'email' ? '✉️' : '✈️'} {c.name}</div>
                <div className="text-sm text-muted">
                  {c.type === 'email' ? `${c.email?.host}:${c.email?.port} · de ${c.email?.from} · para ${c.email?.to.join(', ')}` : `${c.telegram?.chat_ids.length} chat(s) · token ${c.telegram?.token_set ? 'cadastrado' : 'ausente'}`}
                </div>
              </div>
              {c.enabled ? <Badge tone="ok">Ativo</Badge> : <Badge tone="off">Desativado</Badge>}
            </div>
            <div className="mt-3 flex flex-wrap gap-x-4 gap-y-1 text-sm">
              <button className="text-primary hover:underline disabled:opacity-50" disabled={testing === c.id} onClick={() => test(c)}>{testing === c.id ? 'Enviando…' : 'Enviar teste'}</button>
              <button className="text-primary hover:underline" onClick={() => setEdit(c)}>Editar</button>
              <button className="text-primary hover:underline" onClick={() => act(() => api('PATCH', `/api/notifications/channels/${c.id}`, { enabled: !c.enabled }))}>{c.enabled ? 'Desativar' : 'Ativar'}</button>
              <button className="text-danger hover:underline" onClick={() => confirm(`Excluir o canal "${c.name}"? As regras que o usam deixam de enviar por ele.`) && act(() => api('DELETE', `/api/notifications/channels/${c.id}`))}>Excluir</button>
            </div>
          </Card>
        ))}
      </div>
      {edit && <Modal title={edit === 'new' ? 'Novo canal' : `Editar "${edit.name}"`} onClose={() => setEdit(null)}><ChannelForm ch={edit === 'new' ? null : edit} onDone={() => { setEdit(null); reload() }} /></Modal>}
    </div>
  )
}

function ChannelForm({ ch, onDone }: { ch: Channel | null; onDone: () => void }) {
  const [type, setType] = useState<'email' | 'telegram'>(ch?.type ?? 'email')
  const [name, setName] = useState(ch?.name ?? '')
  const [em, setEm] = useState({ host: ch?.email?.host ?? '', port: ch?.email?.port ?? 587, security: ch?.email?.security ?? 'starttls', user: ch?.email?.user ?? '', password: '', from: ch?.email?.from ?? '', from_name: ch?.email?.from_name ?? 'Coruja Backup', to: (ch?.email?.to ?? []).join('\n'), skip_verify: ch?.email?.skip_verify ?? false })
  const [tg, setTg] = useState({ token: '', chat_ids: (ch?.telegram?.chat_ids ?? []).join('\n') })
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState(false)
  const lines = (s: string) => s.split(/[\n,;]+/).map((x) => x.trim()).filter(Boolean)
  async function submit(e: FormEvent) {
    e.preventDefault(); setErr(''); setBusy(true)
    const body: Record<string, unknown> = { name, type }
    if (type === 'email') body.email = { ...em, port: Number(em.port), to: lines(em.to) }
    else body.telegram = { token: tg.token, chat_ids: lines(tg.chat_ids) }
    try { ch ? await api('PATCH', `/api/notifications/channels/${ch.id}`, body) : await api('POST', '/api/notifications/channels', body); onDone() }
    catch (x) { setErr((x as ApiError).message) } finally { setBusy(false) }
  }
  const sel = 'w-full rounded-lg border border-border bg-surface px-3 py-2'
  return (
    <form onSubmit={submit} className="space-y-3 text-sm">
      <div className="grid gap-3 sm:grid-cols-2">
        <Field label="Nome do canal"><Input autoFocus value={name} onChange={(e) => setName(e.target.value)} required /></Field>
        <Field label="Tipo"><select className={sel} value={type} disabled={!!ch} onChange={(e) => setType(e.target.value as 'email' | 'telegram')}><option value="email">E-mail (SMTP)</option><option value="telegram">Telegram</option></select></Field>
      </div>
      {type === 'email' ? (<>
        <div className="grid gap-3 sm:grid-cols-3">
          <div className="sm:col-span-2"><Field label="Servidor SMTP"><Input value={em.host} onChange={(e) => setEm({ ...em, host: e.target.value })} placeholder="smtp.suaempresa.com.br" required /></Field></div>
          <Field label="Porta"><Input type="number" value={em.port} onChange={(e) => setEm({ ...em, port: Number(e.target.value) })} /></Field>
        </div>
        <Field label="Segurança"><select className={sel} value={em.security} onChange={(e) => setEm({ ...em, security: e.target.value })}><option value="starttls">STARTTLS (porta 587)</option><option value="ssl">SSL/TLS direto (porta 465)</option><option value="none">Sem criptografia (não recomendado)</option></select></Field>
        <div className="grid gap-3 sm:grid-cols-2">
          <Field label="Usuário"><Input value={em.user} autoComplete="off" onChange={(e) => setEm({ ...em, user: e.target.value })} /></Field>
          <Field label="Senha" hint={ch?.email?.password_set ? 'Já cadastrada: deixe vazio para manter.' : undefined}><Input type="password" autoComplete="new-password" value={em.password} placeholder={ch?.email?.password_set ? '•••••••• (mantida)' : ''} onChange={(e) => setEm({ ...em, password: e.target.value })} /></Field>
        </div>
        <div className="grid gap-3 sm:grid-cols-2">
          <Field label="Remetente (e-mail)"><Input type="email" value={em.from} onChange={(e) => setEm({ ...em, from: e.target.value })} required /></Field>
          <Field label="Nome do remetente"><Input value={em.from_name} onChange={(e) => setEm({ ...em, from_name: e.target.value })} /></Field>
        </div>
        <Field label="Destinatários" hint="Um por linha (ou separados por vírgula)."><textarea className={`${sel} font-mono text-xs`} rows={3} value={em.to} onChange={(e) => setEm({ ...em, to: e.target.value })} required /></Field>
        <label className="flex items-center gap-2"><input type="checkbox" checked={em.skip_verify} onChange={(e) => setEm({ ...em, skip_verify: e.target.checked })} /> Não validar o certificado do servidor (certificado autoassinado)</label>
      </>) : (<>
        <Field label="Token do bot" hint={ch?.telegram?.token_set ? 'Já cadastrado: deixe vazio para manter.' : 'Crie o bot com o @BotFather e cole o token (ex.: 123456:ABC…).'}><Input type="password" autoComplete="off" value={tg.token} placeholder={ch?.telegram?.token_set ? '•••••••• (mantido)' : ''} onChange={(e) => setTg({ ...tg, token: e.target.value })} /></Field>
        <Field label="IDs dos chats" hint="Um por linha. Pessoa: número (ex.: 87646403). Grupo/canal: número negativo (ex.: -100123…) ou @canal. Para descobrir o seu, mande /start ao bot e consulte o @userinfobot. O bot precisa ter sido iniciado (ou adicionado ao grupo)."><textarea className={`${sel} font-mono text-xs`} rows={3} value={tg.chat_ids} onChange={(e) => setTg({ ...tg, chat_ids: e.target.value })} required /></Field>
      </>)}
      <ErrorBox message={err} />
      <Button disabled={busy}>{ch ? 'Salvar alterações' : 'Criar canal'}</Button>
    </form>
  )
}

function Rules({ rules, channels, jobs, act, reload }: { rules: Rule[]; channels: Channel[]; jobs: Job[]; act: (f: () => Promise<unknown>, ok?: string) => void; reload: () => void }) {
  const [edit, setEdit] = useState<Rule | 'new' | null>(null)
  const chName = (id: number) => channels.find((c) => c.id === id)?.name ?? `#${id}`
  const describe = (r: Rule) => {
    const p = r.params
    if (r.event === 'daily_report') return `todo dia às ${p.time} (${p.timezone})${p.attach_pdf ? ' · com PDF' : ''}${p.only_problems ? ' · só se houver problemas' : ''}`
    if (r.event === 'agent_offline') return `sem sinal há mais de ${p.offline_minutes} min`
    return r.job_ids.length === 0 ? 'todos os jobs' : r.job_ids.map((id) => jobs.find((j) => j.id === id)?.name ?? `#${id}`).join(', ')
  }
  async function quickSetup() {
    if (channels.length === 0) return
    const ids = channels.filter((c) => c.enabled).map((c) => c.id)
    await act(async () => {
      await api('POST', '/api/notifications/rules', { name: 'Falhas e backups perdidos', event: 'run_failed', channel_ids: ids })
      await api('POST', '/api/notifications/rules', { name: 'Relatório diário', event: 'daily_report', channel_ids: ids, params: { time: '07:00', timezone: 'America/Sao_Paulo', attach_pdf: true } })
      await api('POST', '/api/notifications/rules', { name: 'Agente offline', event: 'agent_offline', channel_ids: ids, params: { offline_minutes: 15 } })
    }, 'Regras recomendadas criadas: falhas, relatório diário às 07:00 (com PDF) e agente offline.')
  }
  return (
    <div className="space-y-4">
      <div className="flex flex-wrap justify-end gap-2">
        {rules.length === 0 && channels.length > 0 && <Button variant="ghost" onClick={quickSetup}>✨ Criar regras recomendadas</Button>}
        <Button onClick={() => setEdit('new')} disabled={channels.length === 0} title={channels.length === 0 ? 'Crie um canal primeiro' : ''}>+ Nova regra</Button>
      </div>
      <Card className="overflow-x-auto p-0">
        <table className="w-full text-sm">
          <thead className="border-b border-border text-left text-muted"><tr><th className="p-3">Regra</th><th className="p-3">Quando</th><th className="p-3">Enviar por</th><th className="p-3" /></tr></thead>
          <tbody>
            {rules.length === 0 && <tr><td colSpan={4} className="p-6 text-center text-muted">Nenhuma regra. {channels.length === 0 ? 'Crie um canal e depois as regras.' : 'Use “Criar regras recomendadas” para começar.'}</td></tr>}
            {rules.map((r) => (
              <tr key={r.id} className="border-b border-border align-top last:border-0">
                <td className="p-3"><b>{r.name}</b> {!r.enabled && <Badge tone="off">Pausada</Badge>}<div className="text-xs text-muted">{EVENT_LABEL[r.event]}</div></td>
                <td className="p-3 text-muted">{describe(r)}</td>
                <td className="p-3 text-muted">{r.channel_ids.map(chName).join(', ')}</td>
                <td className="p-3 text-right whitespace-nowrap">
                  {r.event === 'daily_report' && <button className="text-primary hover:underline" onClick={() => act(() => api('POST', `/api/notifications/rules/${r.id}/send-now`), 'Relatório enfileirado: chega em instantes (veja o histórico).')}>Enviar agora</button>}
                  <button className="ml-3 text-primary hover:underline" onClick={() => setEdit(r)}>Editar</button>
                  <button className="ml-3 text-primary hover:underline" onClick={() => act(() => api('PATCH', `/api/notifications/rules/${r.id}`, { ...r, enabled: !r.enabled }))}>{r.enabled ? 'Pausar' : 'Ativar'}</button>
                  <button className="ml-3 text-danger hover:underline" onClick={() => confirm(`Excluir a regra "${r.name}"?`) && act(() => api('DELETE', `/api/notifications/rules/${r.id}`))}>Excluir</button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </Card>
      {edit && <Modal title={edit === 'new' ? 'Nova regra' : `Editar "${edit.name}"`} onClose={() => setEdit(null)}><RuleForm rule={edit === 'new' ? null : edit} channels={channels} jobs={jobs} onDone={() => { setEdit(null); reload() }} /></Modal>}
    </div>
  )
}

function RuleForm({ rule, channels, jobs, onDone }: { rule: Rule | null; channels: Channel[]; jobs: Job[]; onDone: () => void }) {
  const [f, setF] = useState({ name: rule?.name ?? '', event: rule?.event ?? 'run_failed', enabled: rule?.enabled ?? true, job_ids: rule?.job_ids ?? [], channel_ids: rule?.channel_ids ?? [],
    time: rule?.params.time ?? '07:00', timezone: rule?.params.timezone ?? 'America/Sao_Paulo', offline: rule?.params.offline_minutes ?? 15, attach_pdf: rule?.params.attach_pdf ?? true, only_problems: rule?.params.only_problems ?? false })
  const [err, setErr] = useState('')
  const toggle = (arr: number[], id: number) => (arr.includes(id) ? arr.filter((x) => x !== id) : [...arr, id])
  const isRun = f.event.startsWith('run_')
  async function submit(e: FormEvent) {
    e.preventDefault(); setErr('')
    const params = f.event === 'daily_report' ? { time: f.time, timezone: f.timezone, attach_pdf: f.attach_pdf, only_problems: f.only_problems } : f.event === 'agent_offline' ? { offline_minutes: Number(f.offline) } : {}
    const body = { name: f.name, event: f.event, enabled: f.enabled, job_ids: isRun ? f.job_ids : [], channel_ids: f.channel_ids, params }
    try { rule ? await api('PATCH', `/api/notifications/rules/${rule.id}`, body) : await api('POST', '/api/notifications/rules', body); onDone() } catch (x) { setErr((x as ApiError).message) }
  }
  const sel = 'w-full rounded-lg border border-border bg-surface px-3 py-2'
  return (
    <form onSubmit={submit} className="space-y-3 text-sm">
      <div className="grid gap-3 sm:grid-cols-2">
        <Field label="Nome da regra"><Input autoFocus value={f.name} onChange={(e) => setF({ ...f, name: e.target.value })} required /></Field>
        <Field label="Quando avisar"><select className={sel} value={f.event} onChange={(e) => setF({ ...f, event: e.target.value })}>{Object.entries(EVENT_LABEL).map(([k, v]) => <option key={k} value={k}>{v}</option>)}</select></Field>
      </div>
      {f.event === 'daily_report' && (
        <div className="grid gap-3 sm:grid-cols-2">
          <Field label="Horário" hint="Resumo das últimas 24 h, no fuso abaixo."><Input type="time" value={f.time} onChange={(e) => setF({ ...f, time: e.target.value })} required /></Field>
          <Field label="Fuso horário"><Input value={f.timezone} onChange={(e) => setF({ ...f, timezone: e.target.value })} /></Field>
          <label className="flex items-center gap-2"><input type="checkbox" checked={f.attach_pdf} onChange={(e) => setF({ ...f, attach_pdf: e.target.checked })} /> Anexar o relatório em PDF (A4)</label>
          <label className="flex items-center gap-2"><input type="checkbox" checked={f.only_problems} onChange={(e) => setF({ ...f, only_problems: e.target.checked })} /> Enviar só se houver problemas</label>
        </div>
      )}
      {f.event === 'agent_offline' && <Field label="Minutos sem sinal do agente" hint="O agente envia um sinal a cada 15 s. Um alerta por queda."><Input type="number" min={1} max={1440} value={f.offline} onChange={(e) => setF({ ...f, offline: Number(e.target.value) })} /></Field>}
      {isRun && (
        <Field label="Jobs" hint="Nenhum marcado = todos os jobs.">
          <div className="max-h-36 space-y-1 overflow-auto rounded-lg border border-border p-2">
            {jobs.length === 0 && <span className="text-muted">Nenhum job cadastrado.</span>}
            {jobs.map((j) => <label key={j.id} className="flex items-center gap-2"><input type="checkbox" checked={f.job_ids.includes(j.id)} onChange={() => setF({ ...f, job_ids: toggle(f.job_ids, j.id) })} /> {j.name} <span className="text-xs text-muted">({j.agent_name})</span></label>)}
          </div>
        </Field>
      )}
      <Field label="Enviar por">
        <div className="space-y-1 rounded-lg border border-border p-2">
          {channels.map((c) => <label key={c.id} className="flex items-center gap-2"><input type="checkbox" checked={f.channel_ids.includes(c.id)} onChange={() => setF({ ...f, channel_ids: toggle(f.channel_ids, c.id) })} /> {c.type === 'email' ? '✉️' : '✈️'} {c.name}{!c.enabled && <span className="text-xs text-muted"> (desativado)</span>}</label>)}
        </div>
      </Field>
      <ErrorBox message={err} />
      <Button>{rule ? 'Salvar alterações' : 'Criar regra'}</Button>
    </form>
  )
}

function History({ log }: { log: NotifLog[] }) {
  const tone = (s: string) => (s === 'sent' ? 'ok' : s === 'failed' ? 'danger' : 'warn') as 'ok' | 'danger' | 'warn'
  const label: Record<string, string> = { sent: 'Enviado', failed: 'Falhou', pending: 'Pendente' }
  return (
    <Card className="overflow-x-auto p-0">
      <table className="w-full text-sm">
        <thead className="border-b border-border text-left text-muted"><tr><th className="p-3">Quando</th><th className="p-3">Regra / canal</th><th className="p-3">Assunto</th><th className="p-3">Estado</th></tr></thead>
        <tbody>
          {log.length === 0 && <tr><td colSpan={4} className="p-6 text-center text-muted">Nenhum envio ainda.</td></tr>}
          {log.map((n) => (
            <tr key={n.id} className="border-b border-border align-top last:border-0">
              <td className="p-3 whitespace-nowrap text-muted">{fmtDate(n.sent_at ?? n.created_at)}</td>
              <td className="p-3">{n.rule_name || '—'}<div className="text-xs text-muted">{n.channel_name || '—'}</div></td>
              <td className="p-3">{n.subject || <span className="text-muted">aguardando envio</span>}</td>
              <td className="p-3"><Badge tone={tone(n.status)}>{label[n.status] ?? n.status}</Badge>{n.attempts > 1 && <span className="ml-1 text-xs text-muted">{n.attempts} tentativas</span>}{n.error && <div className="mt-1 max-w-xs text-xs text-danger">{n.error}</div>}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </Card>
  )
}
