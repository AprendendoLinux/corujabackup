import { useCallback, useEffect, useState } from 'react'
import { Agent, LinkState, PendingToken, User, api, ApiError, fmtDate } from '../api'
import { Actions, Badge, Button, Card, CopyBox, ErrorBox, Field, IconButton, Input, Modal, Spinner } from '../ui'
import { Wizard } from '../Wizard'
import { confirmar, perguntar } from '../Dialogs'

const DEFAULT_PORT = 23847

/** Explica, em linguagem simples, por que o Server ainda não conectou no agente. */
function LinkInfo({ link }: { link?: LinkState }) {
  if (!link) return <span className="text-xs text-muted">aguardando a primeira tentativa…</span>
  if (link.state === 'connecting') return <span className="inline-flex items-center gap-1.5 text-xs text-muted"><Spinner className="h-3 w-3" />conectando…</span>
  if (link.state === 'error') return <span className="text-xs text-danger">{link.error}</span>
  return null
}

function splitAddress(a: string): { host: string; port: string } {
  const m = a.match(/^\[(.+)\]:(\d+)$/) ?? a.match(/^(.+):(\d+)$/)
  return m ? { host: m[1], port: m[2] } : { host: a, port: String(DEFAULT_PORT) }
}
const joinAddress = (host: string, port: string | number) => (host.includes(':') ? `[${host}]:${port}` : `${host}:${port}`)

export default function Agents({ me }: { me: User }) {
  const [agents, setAgents] = useState<Agent[]>([])
  const [pending, setPending] = useState<PendingToken[]>([])
  const [err, setErr] = useState('')
  const [modal, setModal] = useState(false)
  const canEdit = me.role !== 'viewer'

  const load = useCallback(() => {
    api<{ agents: Agent[]; pending_tokens: PendingToken[] }>('GET', '/api/agents')
      .then((r) => { setAgents(r.agents); setPending(r.pending_tokens); setErr('') })
      .catch((x) => setErr((x as ApiError).message))
  }, [])
  const waiting = agents.some((a) => !a.online && !a.revoked_at) || pending.length > 0
  useEffect(() => { load() }, [load])
  useEffect(() => { const t = setInterval(load, waiting ? 3000 : 10000); return () => clearInterval(t) }, [load, waiting])

  async function act(fn: () => Promise<unknown>) {
    try { await fn(); load() } catch (x) { setErr((x as ApiError).message) }
  }
  const rename = async (a: Agent) => {
    const n = await perguntar({ title: 'Renomear agente', label: 'Novo nome', value: a.name, confirmLabel: 'Renomear' })
    if (n && n !== a.name) act(() => api('PATCH', `/api/agents/${a.id}`, { name: n }))
  }
  const readdress = async (a: Agent) => {
    const n = await perguntar({ title: `Endereço de ${a.name}`, label: 'IP ou nome e porta', value: a.address || `:${DEFAULT_PORT}`, confirmLabel: 'Salvar', icon: 'agentes',
      hint: 'Onde o Server conecta no agente, por exemplo 192.0.2.10:23847. A porta é a publicada na stack do agente.' })
    if (n && n !== a.address) act(() => api('PATCH', `/api/agents/${a.id}`, { address: n.trim() }))
  }
  const revoke = async (a: Agent) => await confirmar({ title: `Revogar "${a.name}"?`, message: 'O Server deixa de conectar nele, e ele recusa novas conexões. Para voltar, é preciso uma nova matrícula.', confirmLabel: 'Revogar', danger: true, icon: 'revogar' }) && act(() => api('POST', `/api/agents/${a.id}/revoke`))
  const remove = async (a: Agent) => await confirmar({ title: `Excluir "${a.name}"?`, message: 'O agente é removido definitivamente do painel.', danger: true }) && act(() => api('DELETE', `/api/agents/${a.id}`))

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between gap-3">
        <div>
          <h1 className="text-2xl font-semibold">Agentes</h1>
          <p className="text-sm text-muted">Cada agente executa os backups da máquina em que está instalado. O Server conecta nele pelo IP e pela porta cadastrados aqui.</p>
        </div>
        {canEdit && <Button onClick={() => setModal(true)}>+ Novo agente</Button>}
      </div>
      <ErrorBox message={err} />

      <Card className="overflow-x-auto p-0">
        <table className="w-full text-sm">
          <thead className="border-b border-border text-left text-muted">
            <tr><th className="p-3">Nome</th><th className="p-3">Estado</th><th className="p-3">Endereço</th><th className="p-3">Máquina</th><th className="p-3">Versão</th><th className="p-3">Visto por último</th><th className="p-3" /></tr>
          </thead>
          <tbody>
            {agents.length === 0 && <tr><td colSpan={7} className="p-6 text-center text-muted">Nenhum agente ainda. Clique em “Novo agente”.</td></tr>}
            {agents.map((a) => (
              <tr key={a.id} className="border-b border-border align-top last:border-0">
                <td className="p-3 font-medium">{a.name}{a.docker_available && <span title="Docker disponível" className="ml-2 text-xs text-muted">🐳</span>}</td>
                <td className="p-3">
                  {a.revoked_at ? <Badge tone="danger">Revogado</Badge> : a.online ? <Badge tone="ok">Online</Badge> : <Badge tone="off">Offline</Badge>}
                  {!a.online && !a.revoked_at && <div className="mt-1 max-w-64">{a.address ? <LinkInfo link={a.link} /> : <span className="text-xs text-danger">sem endereço: cadastre o IP e a porta</span>}</div>}
                </td>
                <td className="p-3 font-mono text-xs">{a.address || '—'}</td>
                <td className="p-3 text-muted">{a.hostname || '—'}<div className="text-xs">{a.os}/{a.arch}</div></td>
                <td className="p-3 text-muted">{a.agent_version || '—'}</td>
                <td className="p-3 text-muted">{fmtDate(a.last_seen_at)}</td>
                <td className="p-3 text-right whitespace-nowrap">
                  {canEdit && <Actions>
                    {!a.revoked_at && <IconButton icon="agentes" label="Alterar o endereço" onClick={() => readdress(a)} />}
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
          <h2 className="mb-2 font-semibold">Aguardando a primeira conexão</h2>
          <ul className="divide-y divide-border text-sm">
            {pending.map((t) => (
              <li key={t.id} className="flex items-center justify-between gap-3 py-2">
                <div className="min-w-0">
                  <b>{t.name}</b> <span className="font-mono text-xs text-muted">{t.address}</span> <span className="text-xs text-muted">· token vale até {fmtDate(t.expires_at)}</span>
                  <div>{t.address ? <LinkInfo link={t.link} /> : <span className="text-xs text-danger">token de uma versão anterior, sem endereço: cancele e cadastre o agente de novo</span>}</div>
                </div>
                {canEdit && <IconButton icon="fechar" tone="danger" label="Cancelar" onClick={async () => await confirmar({ title: `Cancelar "${t.name}"?`, message: 'O token deixa de valer e o Server para de tentar conectar.', confirmLabel: 'Cancelar o token', danger: true, icon: 'fechar' }) && act(() => api('DELETE', `/api/agents/tokens/${t.id}`))} />}
              </li>
            ))}
          </ul>
        </Card>
      )}

      {modal && (
        <Modal wide title="Novo agente" onClose={() => { setModal(false); load() }}>
          <AgentWizard onChange={load} onClose={() => { setModal(false); load() }} />
        </Modal>
      )}
    </div>
  )
}

type NewAgent = { token: string; name: string; expires_at: string; address: string }

/** Assistente: identificação e endereço → instalação (stack pronta) → conexão (o Server conecta e mostra o resultado). */
function AgentWizard({ onChange, onClose }: { onChange: () => void; onClose: () => void }) {
  const [step, setStep] = useState(0)
  const [name, setName] = useState('')
  const [host, setHost] = useState('')
  const [port, setPort] = useState(String(DEFAULT_PORT))
  const [ttl, setTtl] = useState(24)
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState(false)
  const [created, setCreated] = useState<NewAgent | null>(null)
  const [online, setOnline] = useState<Agent | null>(null)
  const [link, setLink] = useState<LinkState | undefined>()
  const STEPS = [
    { title: 'Identificação', hint: 'Como o agente vai aparecer e onde o Server encontra a máquina dele.' },
    { title: 'Instalação', hint: 'Escolha como instalar e suba o agente na máquina que será protegida.' },
    { title: 'Conexão', hint: 'O Server tenta conectar a cada poucos segundos. Assim que o agente estiver no ar, ele aparece aqui sozinho.' },
  ]
  useEffect(() => {
    if (step !== 2 || !created || online) return
    const look = () => api<{ agents: Agent[]; pending_tokens: PendingToken[] }>('GET', '/api/agents').then((r) => {
      const a = r.agents.find((x) => x.name === created.name)
      if (a?.online) { setOnline(a); onChange(); return }
      setLink(a?.link ?? r.pending_tokens.find((t) => t.name === created.name)?.link)
    }).catch(() => {})
    look()
    const t = setInterval(look, 2000)
    return () => clearInterval(t)
  }, [step, created, online, onChange])

  async function next() {
    setErr('')
    if (step === 0) {
      if (name.trim().length < 2) return setErr('Dê um nome ao agente (pelo menos 2 caracteres).')
      const h = host.trim()
      if (!h || /[\s/]/.test(h)) return setErr('Informe o IP ou o nome da máquina do agente (sem http:// e sem barras).')
      const p = Number(port)
      if (!Number.isInteger(p) || p < 1 || p > 65535) return setErr('Informe uma porta entre 1 e 65535.')
      if (!created) {
        setBusy(true)
        try { setCreated(await api<NewAgent>('POST', '/api/agents/tokens', { name: name.trim(), ttl_hours: ttl, address: joinAddress(h, p) })); onChange() }
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
        <div className="grid gap-3 sm:grid-cols-[1fr_9rem]">
          <Field label="IP ou nome da máquina" hint="Como o Server alcança a máquina do agente."><Input value={host} placeholder="192.0.2.10" onChange={(e) => setHost(e.target.value)} disabled={!!created} /></Field>
          <Field label="Porta" hint="Publicada na stack."><Input type="number" min={1} max={65535} value={port} onChange={(e) => setPort(e.target.value)} disabled={!!created} /></Field>
        </div>
        <Field label="Validade do token" hint="O token de matrícula só pode ser usado uma vez.">
          <select className="w-full rounded-lg border border-border bg-surface px-3 py-2" value={ttl} onChange={(e) => setTtl(+e.target.value)} disabled={!!created}>
            <option value={1}>1 hora</option><option value={24}>24 horas</option><option value={72}>3 dias</option><option value={168}>7 dias</option>
          </select>
        </Field>
      </>)}
      {step === 1 && created && <Created c={created} />}
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
            <b>Conectando em {created.address}…</b>
            {link?.state === 'error' && <p className="max-w-md rounded-lg bg-danger/10 px-3 py-2 text-sm text-danger">Última tentativa: {link.error}</p>}
            <p className="max-w-md text-sm text-muted">Confira se a stack está no ar (<code>docker logs corujabackup-agent</code>) e se a porta {splitAddress(created.address).port} da máquina do agente aceita conexões vindas do Server. Você pode fechar e voltar depois: o token vale até {fmtDate(created.expires_at)}.</p>
          </div>
        )
      )}
    </Wizard>
  )
}

type Method = 'compose' | 'run'

/** Passo de instalação: primeiro pergunta como instalar e só então mostra o comando daquele jeito. */
function Created({ c }: { c: NewAgent }) {
  const { port } = splitAddress(c.address)
  const [method, setMethod] = useState<Method | null>(null)
  const [docker, setDocker] = useState(false)
  const sock = docker ? '\n      - /var/run/docker.sock:/var/run/docker.sock   # jobs de banco em contêineres Docker' : ''
  const compose = `services:
  agent:
    image: ghcr.io/aprendendolinux/corujabackup-agent:latest
    container_name: corujabackup-agent
    restart: always
    ports:
      - "${port}:23847"
    environment:
      CORUJA_TOKEN: "${c.token}"
      CORUJA_HOST_ROOT: "/host"
    volumes:
      - /srv/coruja-agente:/var/lib/coruja-agent
      - /:/host:ro
      - /:/host-rw
      - /srv/coruja-backups:/backups${sock}`
  const run = [
    'docker run -d --name corujabackup-agent --restart always \\',
    `  -p ${port}:23847 \\`,
    `  -e CORUJA_TOKEN="${c.token}" \\`,
    '  -e CORUJA_HOST_ROOT=/host \\',
    '  -v /srv/coruja-agente:/var/lib/coruja-agent \\',
    '  -v /:/host:ro \\',
    '  -v /:/host-rw \\',
    '  -v /srv/coruja-backups:/backups \\',
    ...(docker ? ['  -v /var/run/docker.sock:/var/run/docker.sock \\'] : []),
    '  ghcr.io/aprendendolinux/corujabackup-agent:latest',
  ].join('\n')
  const options: { id: Method; title: string; text: string }[] = [
    { id: 'compose', title: 'docker-compose.yml', text: 'Para colar como stack no Portainer ou subir com docker compose up -d. Mais fácil de atualizar depois.' },
    { id: 'run', title: 'docker run', text: 'Um único comando no terminal da máquina, sem arquivo de configuração.' },
  ]
  return (
    <div className="space-y-4 text-sm">
      <p className="rounded-lg border border-accent/50 bg-accent/10 px-3 py-2">
        O token de matrícula já vai dentro do comando. <b>Ele não será mostrado de novo</b>, vale uma única vez e expira em {fmtDate(c.expires_at)}.
      </p>
      <div>
        <div className="mb-2 font-medium">Como você quer instalar o agente?</div>
        <div role="radiogroup" aria-label="Forma de instalação" className="grid gap-2 sm:grid-cols-2">
          {options.map((o) => (
            <label key={o.id} className={`flex cursor-pointer items-start gap-2.5 rounded-lg border p-3 hover:bg-bg ${method === o.id ? 'border-primary' : 'border-border'}`}>
              <input type="radio" name="metodo" className="mt-1" checked={method === o.id} onChange={() => setMethod(o.id)} />
              <span><b className="block font-mono">{o.title}</b><span className="text-xs text-muted">{o.text}</span></span>
            </label>
          ))}
        </div>
      </div>
      {method && (<>
        <label className="flex items-start gap-2.5">
          <input type="checkbox" className="mt-1" checked={docker} onChange={(e) => setDocker(e.target.checked)} />
          <span>Vou fazer backup de bancos que rodam em <b>contêineres Docker</b> nesta máquina<span className="block text-xs text-muted">Dá ao agente acesso ao Docker (docker.sock) para rodar o dump dentro do contêiner do banco.</span></span>
        </label>
        <div>
          <div className="mb-1 font-medium">{method === 'compose' ? 'Cole esta stack no Portainer (ou salve como docker-compose.yml e rode docker compose up -d)' : 'Rode este comando na máquina do agente'}</div>
          <CopyBox text={method === 'compose' ? compose : run} />
        </div>
        <ul className="list-disc space-y-1 pl-5 text-muted">
          <li>Libere a porta <b>{port}</b> desta máquina <b>só para o IP do Server</b>: é por ela que o Server conecta em <span className="font-mono">{c.address}</span>.</li>
          <li>O agente lê os arquivos em <code>/host</code> (somente leitura). <code>/host-rw</code> só é usado para gravar o backup local na pasta escolhida no job.</li>
          <li>Os backups locais ficam em <code>/srv/coruja-backups</code> (pode trocar a pasta antes de subir).</li>
        </ul>
        <details className="text-muted">
          <summary className="cursor-pointer select-none">Ver só o token</summary>
          <div className="mt-2"><CopyBox text={c.token} /></div>
        </details>
      </>)}
    </div>
  )
}
