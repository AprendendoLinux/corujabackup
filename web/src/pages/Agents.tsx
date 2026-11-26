import { FormEvent, useCallback, useEffect, useState } from 'react'
import { Agent, PendingToken, User, api, ApiError, fmtDate } from '../api'
import { Actions, Badge, Button, Card, CopyBox, ErrorBox, Field, IconButton, Input, Modal, Spinner } from '../ui'
import { Wizard } from '../Wizard'
import { confirmar, perguntar } from '../Dialogs'

type Sys = { agent_port: string; agent_address: string; version: string }

export default function Agents({ me }: { me: User }) {
  const [agents, setAgents] = useState<Agent[]>([])
  const [pending, setPending] = useState<PendingToken[]>([])
  const [sys, setSys] = useState<Sys | null>(null)
  const [err, setErr] = useState('')
  const [modal, setModal] = useState(false)
  const canEdit = me.role !== 'viewer'

  const load = useCallback(() => {
    api<{ agents: Agent[]; pending_tokens: PendingToken[] }>('GET', '/api/agents')
      .then((r) => { setAgents(r.agents); setPending(r.pending_tokens); setErr('') })
      .catch((x) => setErr((x as ApiError).message))
  }, [])
  useEffect(() => { load(); const t = setInterval(load, 10000); return () => clearInterval(t) }, [load])
  useEffect(() => { api<Sys>('GET', '/api/system').then(setSys).catch(() => {}) }, [])

  async function act(fn: () => Promise<unknown>) {
    try { await fn(); load() } catch (x) { setErr((x as ApiError).message) }
  }
  const defaultAddress = `${window.location.hostname}:${sys?.agent_port ?? '8443'}`
  const address = sys?.agent_address || defaultAddress

  const rename = async (a: Agent) => {
    const n = await perguntar({ title: 'Renomear agente', label: 'Novo nome', value: a.name, confirmLabel: 'Renomear' })
    if (n && n !== a.name) act(() => api('PATCH', `/api/agents/${a.id}`, { name: n }))
  }
  const revoke = async (a: Agent) => await confirmar({ title: `Revogar "${a.name}"?`, message: 'Ele é desconectado na hora e só volta com uma nova matrícula.', confirmLabel: 'Revogar', danger: true, icon: 'revogar' }) && act(() => api('POST', `/api/agents/${a.id}/revoke`))
  const remove = async (a: Agent) => await confirmar({ title: `Excluir "${a.name}"?`, message: 'O agente é removido definitivamente do painel.', danger: true }) && act(() => api('DELETE', `/api/agents/${a.id}`))

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between gap-3">
        <div>
          <h1 className="text-2xl font-semibold">Agentes</h1>
          <p className="text-sm text-muted">Cada agente executa os backups da máquina em que está instalado e se conecta ao Server com IP:porta e um token de matrícula.</p>
        </div>
        {canEdit && <Button onClick={() => setModal(true)}>+ Novo agente</Button>}
      </div>
      <ErrorBox message={err} />

      {sys && <AgentAddress current={sys.agent_address} fallback={defaultAddress} canEdit={me.role === 'admin'} onSaved={(agent_address) => setSys({ ...sys, agent_address })} />}

      <Card className="overflow-x-auto p-0">
        <table className="w-full text-sm">
          <thead className="border-b border-border text-left text-muted">
            <tr><th className="p-3">Nome</th><th className="p-3">Estado</th><th className="p-3">Host</th><th className="p-3">Versão</th><th className="p-3">Visto por último</th><th className="p-3" /></tr>
          </thead>
          <tbody>
            {agents.length === 0 && <tr><td colSpan={6} className="p-6 text-center text-muted">Nenhum agente ainda. Clique em “Novo agente”.</td></tr>}
            {agents.map((a) => (
              <tr key={a.id} className="border-b border-border last:border-0">
                <td className="p-3 font-medium">{a.name}{a.docker_available && <span title="Docker disponível" className="ml-2 text-xs text-muted">🐳</span>}</td>
                <td className="p-3">{a.revoked_at ? <Badge tone="danger">Revogado</Badge> : a.online ? <Badge tone="ok">Online</Badge> : <Badge tone="off">Offline</Badge>}</td>
                <td className="p-3 text-muted">{a.hostname || '—'}<div className="text-xs">{a.os}/{a.arch} · {a.ip}</div></td>
                <td className="p-3 text-muted">{a.agent_version || '—'}</td>
                <td className="p-3 text-muted">{fmtDate(a.last_seen_at)}</td>
                <td className="p-3 text-right whitespace-nowrap">
                  {canEdit && <Actions>
                    <IconButton icon="editar" label="Renomear" onClick={() => rename(a)} />
                    {!a.revoked_at && <IconButton icon="revogar" label="Revogar" onClick={() => revoke(a)} />}
                    <IconButton icon="excluir" tone="danger" label="Excluir" onClick={() => remove(a)} />
                  </Actions>}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </Card>

      {pending.length > 0 && (
        <Card>
          <h2 className="mb-2 font-semibold">Tokens aguardando matrícula</h2>
          <ul className="divide-y divide-border text-sm">
            {pending.map((t) => (
              <li key={t.id} className="flex items-center justify-between py-2">
                <span><b>{t.name}</b> <span className="text-muted">· expira em {fmtDate(t.expires_at)}</span></span>
                {canEdit && <IconButton icon="fechar" tone="danger" label="Cancelar o token" onClick={() => act(() => api('DELETE', `/api/agents/tokens/${t.id}`))} />}
              </li>
            ))}
          </ul>
        </Card>
      )}

      {modal && (
        <Modal wide title="Novo agente" onClose={() => setModal(false)}>
          <AgentWizard addr={address} onChange={load} onClose={() => { setModal(false); load() }} />
        </Modal>
      )}
    </div>
  )
}

function AgentAddress({ current, fallback, canEdit, onSaved }: { current: string; fallback: string; canEdit: boolean; onSaved: (v: string) => void }) {
  const [editing, setEditing] = useState(false)
  const [value, setValue] = useState('')
  const [err, setErr] = useState('')
  async function save(e: FormEvent) {
    e.preventDefault(); setErr('')
    try { const r = await api<{ agent_address: string }>('PUT', '/api/system/agent-address', { address: value }); onSaved(r.agent_address); setEditing(false) }
    catch (x) { setErr((x as ApiError).message) }
  }
  return (
    <Card>
      <div className="flex flex-wrap items-center justify-between gap-3 text-sm">
        <div>
          <b>Endereço para os agentes:</b> <code className="font-mono">{current || fallback}</code>
          <div className="text-muted">{current ? 'Definido por você.' : 'Automático (endereço desta página + porta interna do canal). Se você publica o canal em outra porta ou outro nome, altere.'} É o que vai nos comandos de instalação.</div>
        </div>
        {canEdit && !editing && <Button variant="ghost" onClick={() => { setValue(current || fallback); setErr(''); setEditing(true) }}>Alterar</Button>}
      </div>
      {editing && (
        <form onSubmit={save} className="mt-3 space-y-2">
          <div className="flex flex-wrap gap-2">
            <div className="min-w-60 flex-1"><Input autoFocus aria-label="Endereço para os agentes" className="font-mono" value={value} placeholder="backup.exemplo.com:8443" onChange={(e) => setValue(e.target.value)} /></div>
            <Button>Salvar</Button>
            <Button type="button" variant="ghost" onClick={() => setEditing(false)}>Cancelar</Button>
          </div>
          <p className="text-xs text-muted">Formato endereço:porta. Deixe vazio para voltar ao automático. Isto só muda o texto dos comandos: a porta continua sendo publicada no Docker do Server.</p>
          <ErrorBox message={err} />
        </form>
      )}
    </Card>
  )
}

type NewAgent = { token: string; name: string; expires_at: string }

/** Assistente: identificação → instalação (stack pronta) → conexão (espera o agente aparecer). */
function AgentWizard({ addr, onChange, onClose }: { addr: string; onChange: () => void; onClose: () => void }) {
  const [step, setStep] = useState(0)
  const [name, setName] = useState('')
  const [ttl, setTtl] = useState(24)
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState(false)
  const [created, setCreated] = useState<NewAgent | null>(null)
  const [online, setOnline] = useState<Agent | null>(null)
  const STEPS = [
    { title: 'Identificação', hint: 'Como o agente vai aparecer nos jobs, nos e-mails e nos relatórios.' },
    { title: 'Instalação', hint: 'Suba o agente na máquina que será protegida, com a stack abaixo (Portainer ou docker compose).' },
    { title: 'Conexão', hint: 'Assim que o agente subir, ele aparece aqui sozinho.' },
  ]
  // na etapa de conexão, consulta a cada 3 s até o agente se apresentar
  useEffect(() => {
    if (step !== 2 || !created || online) return
    const look = () => api<{ agents: Agent[] }>('GET', '/api/agents').then((r) => {
      const a = r.agents.find((x) => x.name === created.name && x.online)
      if (a) { setOnline(a); onChange() }
    }).catch(() => {})
    look()
    const t = setInterval(look, 3000)
    return () => clearInterval(t)
  }, [step, created, online, onChange])

  async function next() {
    setErr('')
    if (step === 0) {
      if (name.trim().length < 2) return setErr('Dê um nome ao agente (pelo menos 2 caracteres).')
      if (!created) {
        setBusy(true)
        try { setCreated(await api<NewAgent>('POST', '/api/agents/tokens', { name: name.trim(), ttl_hours: ttl })); onChange() }
        catch (x) { return setErr((x as ApiError).message) } finally { setBusy(false) }
      }
    }
    setStep(step + 1)
  }
  return (
    <Wizard steps={STEPS} current={step} maxReached={step} onGo={(i) => setStep(i)} onBack={step === 1 ? undefined : step > 0 ? () => setStep(step - 1) : undefined}
      onNext={next} onFinish={onClose} nextLabel={step === 0 ? 'Gerar e avançar' : 'Avançar'} finishLabel={online ? 'Concluir' : 'Concluir depois'} busy={busy} err={err}>
      {step === 0 && (<>
        <Field label="Nome do agente" hint="Ex.: umami-server2. Use o nome da máquina para facilitar."><Input autoFocus value={name} onChange={(e) => setName(e.target.value)} disabled={!!created} /></Field>
        <Field label="Validade do token" hint="O token de matrícula só pode ser usado uma vez.">
          <select className="w-full rounded-lg border border-border bg-surface px-3 py-2" value={ttl} onChange={(e) => setTtl(+e.target.value)} disabled={!!created}>
            <option value={1}>1 hora</option><option value={24}>24 horas</option><option value={72}>3 dias</option><option value={168}>7 dias</option>
          </select>
        </Field>
      </>)}
      {step === 1 && created && <Created c={created} addr={addr} />}
      {step === 2 && created && (
        online ? (
          <div className="flex flex-col items-center gap-3 py-6 text-center">
            <span className="flex h-14 w-14 items-center justify-center rounded-full bg-ok/15 text-2xl text-ok">✓</span>
            <b className="text-lg">{online.name} está conectado!</b>
            <p className="text-sm text-muted">{online.hostname} · {online.os}/{online.arch} · versão {online.agent_version}{online.docker_available ? ' · Docker disponível' : ''}</p>
            <p className="text-sm text-muted">Agora crie um job para ele em <b>Jobs</b>.</p>
          </div>
        ) : (
          <div className="flex flex-col items-center gap-3 py-6 text-center">
            <Spinner className="h-10 w-10 text-primary" />
            <b>Aguardando o agente {created.name} se conectar…</b>
            <p className="max-w-md text-sm text-muted">Pode levar alguns segundos depois de subir a stack. Se demorar, confira o log com <code>docker logs corujabackup-agent</code> e se a porta {addr.split(':').pop()} está liberada para a saída dele. Você pode fechar e voltar depois: o token continua valendo até {fmtDate(created.expires_at)}.</p>
          </div>
        )
      )}
    </Wizard>
  )
}

function Created({ c, addr }: { c: { token: string; name: string; expires_at: string }; addr: string }) {
  const compose = `services:
  agent:
    image: ghcr.io/aprendendolinux/corujabackup-agent:latest
    container_name: corujabackup-agent
    restart: always
    environment:
      CORUJA_SERVER: "${addr}"
      CORUJA_TOKEN: "${c.token}"
      CORUJA_HOST_ROOT: "/host"
    volumes:
      - agent-data:/var/lib/coruja-agent
      - /:/host:ro                 # o que o agente pode salvar (somente leitura)
      - /:/host-rw                 # só para gravar o backup local na pasta escolhida no painel
      - /srv/coruja-backups:/backups   # pasta de backups padrão do agente
      # - /var/run/docker.sock:/var/run/docker.sock   # só para jobs de banco de dados em contêineres Docker
volumes:
  agent-data:`
  const run = `docker run -d --name corujabackup-agent --restart always \\
  -e CORUJA_SERVER="${addr}" -e CORUJA_TOKEN="${c.token}" -e CORUJA_HOST_ROOT=/host \\
  -v coruja-agent-data:/var/lib/coruja-agent -v /:/host:ro -v /:/host-rw -v /srv/coruja-backups:/backups \\
  ghcr.io/aprendendolinux/corujabackup-agent:latest`
  return (
    <div className="space-y-4 text-sm">
      <div className="rounded-lg border border-accent/50 bg-accent/10 p-3">
        <b>Guarde este token agora:</b> ele não será mostrado de novo. Vale uma única vez e expira em {fmtDate(c.expires_at)}.
      </div>
      <Field label="Token de matrícula"><CopyBox text={c.token} /></Field>
      <div>
        <div className="mb-1 font-medium">Opção 1 · docker run</div>
        <CopyBox text={run} />
      </div>
      <div>
        <div className="mb-1 font-medium">Opção 2 · docker-compose.yml</div>
        <CopyBox text={compose} />
      </div>
      <p className="text-muted">O agente se conecta de dentro para fora, em <b>{addr}</b> (porta do canal dos agentes). Libere essa porta para a saída do agente. A origem dos backups é lida em /host, <b>somente leitura</b>. A montagem /host-rw dá ao agente permissão de gravação em todo o sistema de arquivos dele, usada na pasta do backup local que você escolher no job.</p>
    </div>
  )
}
