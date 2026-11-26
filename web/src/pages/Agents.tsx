import { FormEvent, useCallback, useEffect, useState } from 'react'
import { Agent, PendingToken, User, api, ApiError, fmtDate } from '../api'
import { Badge, Button, Card, CopyBox, ErrorBox, Field, Input, Modal } from '../ui'

type Sys = { agent_port: string; version: string }

export default function Agents({ me }: { me: User }) {
  const [agents, setAgents] = useState<Agent[]>([])
  const [pending, setPending] = useState<PendingToken[]>([])
  const [sys, setSys] = useState<Sys | null>(null)
  const [err, setErr] = useState('')
  const [modal, setModal] = useState(false)
  const [created, setCreated] = useState<{ token: string; name: string; expires_at: string } | null>(null)
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
  const rename = (a: Agent) => {
    const n = prompt('Novo nome do agente:', a.name)
    if (n && n !== a.name) act(() => api('PATCH', `/api/agents/${a.id}`, { name: n }))
  }
  const revoke = (a: Agent) => confirm(`Revogar "${a.name}"? Ele será desconectado e não poderá voltar sem nova matrícula.`) && act(() => api('POST', `/api/agents/${a.id}/revoke`))
  const remove = (a: Agent) => confirm(`Excluir "${a.name}" definitivamente?`) && act(() => api('DELETE', `/api/agents/${a.id}`))

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between gap-3">
        <div>
          <h1 className="text-2xl font-semibold">Agentes</h1>
          <p className="text-sm text-muted">Servidores que executam os backups. Cada um se conecta ao Server com IP:porta e um token de matrícula.</p>
        </div>
        {canEdit && <Button onClick={() => { setCreated(null); setModal(true) }}>+ Novo agente</Button>}
      </div>
      <ErrorBox message={err} />

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
                  {canEdit && (<>
                    <button className="text-primary hover:underline" onClick={() => rename(a)}>Renomear</button>
                    {!a.revoked_at && <button className="ml-3 text-primary hover:underline" onClick={() => revoke(a)}>Revogar</button>}
                    <button className="ml-3 text-danger hover:underline" onClick={() => remove(a)}>Excluir</button>
                  </>)}
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
                {canEdit && <button className="text-danger hover:underline" onClick={() => act(() => api('DELETE', `/api/agents/tokens/${t.id}`))}>Cancelar</button>}
              </li>
            ))}
          </ul>
        </Card>
      )}

      {modal && (
        <Modal title={created ? 'Agente pronto para instalar' : 'Novo agente'} onClose={() => setModal(false)}>
          {created ? <Created c={created} port={sys?.agent_port ?? '8443'} /> : <NewToken onCreated={(c) => { setCreated(c); load() }} />}
        </Modal>
      )}
    </div>
  )
}

function NewToken({ onCreated }: { onCreated: (c: { token: string; name: string; expires_at: string }) => void }) {
  const [name, setName] = useState('')
  const [ttl, setTtl] = useState(24)
  const [err, setErr] = useState('')
  async function submit(e: FormEvent) {
    e.preventDefault(); setErr('')
    try { onCreated(await api('POST', '/api/agents/tokens', { name, ttl_hours: ttl })) } catch (x) { setErr((x as ApiError).message) }
  }
  return (
    <form onSubmit={submit} className="space-y-3">
      <Field label="Nome do servidor" hint="Como ele aparecerá nos relatórios. Ex.: umami-server2"><Input autoFocus value={name} onChange={(e) => setName(e.target.value)} required /></Field>
      <Field label="Validade do token" hint="O token só pode ser usado uma vez">
        <select className="w-full rounded-lg border border-border bg-surface px-3 py-2" value={ttl} onChange={(e) => setTtl(+e.target.value)}>
          <option value={1}>1 hora</option><option value={24}>24 horas</option><option value={72}>3 dias</option><option value={168}>7 dias</option>
        </select>
      </Field>
      <ErrorBox message={err} />
      <Button>Gerar token</Button>
    </form>
  )
}

function Created({ c, port }: { c: { token: string; name: string; expires_at: string }; port: string }) {
  const host = window.location.hostname
  const addr = `${host}:${port}`
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
      - /srv/coruja-backups:/backups   # onde os arquivos de backup são gravados
      # - /var/run/docker.sock:/var/run/docker.sock   # só para jobs de banco de dados em contêineres Docker
volumes:
  agent-data:`
  const run = `docker run -d --name corujabackup-agent --restart always \\
  -e CORUJA_SERVER="${addr}" -e CORUJA_TOKEN="${c.token}" -e CORUJA_HOST_ROOT=/host \\
  -v coruja-agent-data:/var/lib/coruja-agent -v /:/host:ro -v /srv/coruja-backups:/backups \\
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
      <p className="text-muted">O agente se conecta de dentro para fora, em <b>{addr}</b> (porta do canal dos agentes). Libere essa porta para o servidor do agente. O servidor é montado <b>somente leitura</b> em /host; os backups são gravados em /srv/coruja-backups.</p>
    </div>
  )
}
