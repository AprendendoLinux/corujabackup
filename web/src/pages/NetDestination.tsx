import { FormEvent, useEffect, useState } from 'react'
import { Agent, api, ApiError, fmtBytes } from '../api'
import { Button, ErrorBox, Field, Input } from '../ui'
import { LocalDirField } from '../LocalDir'

export const NET_TYPES = ['sftp', 'ftp', 'smb', 'nfs', 'local']
const sel = 'w-full rounded-lg border border-border bg-surface px-3 py-2'

type Cfg = {
  type: string; host: string; port: number; user: string; password: string; private_key: string; host_key: string
  tls: string; skip_verify: boolean; share: string; domain: string; base_path: string; verify: string
  export: string; uid: string; gid: string; path: string
}

export function useAgents() {
  const [agents, setAgents] = useState<Agent[]>([])
  useEffect(() => {
    api<{ agents: Agent[] }>('GET', '/api/agents').then((r) => setAgents((r.agents ?? []).filter((a) => !a.revoked_at))).catch(() => setAgents([]))
  }, [])
  return agents
}

export function AgentSelect({ agents, value, onChange }: { agents: Agent[]; value: number; onChange: (v: number) => void }) {
  return (
    <select className={sel} value={value} onChange={(e) => onChange(Number(e.target.value))} aria-label="Agente que testa o storage">
      <option value={0}>Escolha um agente…</option>
      {agents.map((a) => <option key={a.id} value={a.id} disabled={!a.online}>{a.name}{a.online ? '' : ' (offline)'}</option>)}
    </select>
  )
}

export default function NetDestination({ type, onSaved }: { type: string; onSaved: () => void }) {
  const agents = useAgents()
  const [name, setName] = useState('')
  const [agent, setAgent] = useState(0)
  const [f, setF] = useState<Cfg>({ type, host: '', port: 0, user: '', password: '', private_key: '', host_key: '', tls: 'explicit', skip_verify: false, share: '', domain: '', base_path: '', verify: 'full', export: '', uid: '', gid: '', path: '' })
  const [useKey, setUseKey] = useState(false)
  const [tested, setTested] = useState<{ detail: string; host_key?: string; total?: number; free?: number } | null>(null)
  const [trusted, setTrusted] = useState(false)
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState('')
  useEffect(() => { const on = agents.find((a) => a.online); if (on && !agent) setAgent(on.id) }, [agents, agent])
  // qualquer mudança na configuração invalida o teste
  const set = (p: Partial<Cfg>) => { setF((v) => ({ ...v, host_key: '', ...p })); setTested(null); setTrusted(false) }
  const defPort = type === 'sftp' ? 22 : type === 'smb' ? 445 : f.tls === 'implicit' ? 990 : 21
  const num = (v: string) => (v.trim() === '' ? undefined : Number(v))
  const cfg = () => ({
    ...f, password: useKey ? '' : f.password, private_key: useKey ? f.private_key : '',
    uid: type === 'nfs' ? num(f.uid) : undefined, gid: type === 'nfs' ? num(f.gid) : undefined,
    path: type === 'local' ? f.path : '', base_path: type === 'local' ? '' : f.base_path,
  })
  const agentInfo = agents.find((a) => a.id === agent)

  async function test() {
    setErr(''); setTested(null); setTrusted(false)
    if (!agent) { setErr('Escolha um agente online: é ele que vai acessar o storage.'); return }
    if (type === 'local' && !f.path) { setErr('Escolha a pasta do storage.'); return }
    setBusy('test')
    try { setTested(await api<{ detail: string; host_key?: string; total?: number; free?: number }>('POST', '/api/destinations/test', { agent_id: agent, config: cfg() })) }
    catch (x) { setErr((x as ApiError).message) } finally { setBusy('') }
  }
  async function save(e: FormEvent) {
    e.preventDefault(); setErr(''); setBusy('save')
    try { await api('POST', '/api/destinations', { name, config: { ...cfg(), host_key: tested?.host_key ?? '' } }); onSaved() }
    catch (x) { setErr((x as ApiError).message) } finally { setBusy('') }
  }
  const canSave = !!tested && !!name.trim() && (type !== 'sftp' || trusted)

  return (
    <form onSubmit={save} className="space-y-4 text-sm">
      <Field label="Nome do storage" hint="Como aparece nos jobs. Ex.: NAS da matriz"><Input autoFocus value={name} onChange={(e) => setName(e.target.value)} required /></Field>

      {type === 'local' && (
        <div className="space-y-3 rounded-lg border border-border bg-bg p-3">
          <p className="text-muted">Grava numa pasta da máquina do agente: outro disco, um volume iSCSI ou um HD USB já montados. O agente precisa do volume <code>- /:/host-rw</code> na stack (o padrão).</p>
          <Field label="Agente onde fica a pasta" hint="Os jobs que usarem este storage gravam nesta mesma pasta, na máquina do agente de cada job."><AgentSelect agents={agents} value={agent} onChange={(v) => { setAgent(v); set({ path: '' }) }} /></Field>
          {agentInfo && <LocalDirField storage key={agentInfo.id} agentId={agentInfo.id} agentName={agentInfo.name} agentVersion={agentInfo.agent_version} value={f.path} onChange={(v) => set({ path: v })} onUnavailable={() => {}} />}
        </div>
      )}
      {type === 'nfs' && (
        <>
          <div className="rounded-lg border border-border bg-bg p-3 text-muted">
            O agente acessa a exportação direto pela rede (NFSv3), <b>sem montar nada</b> na máquina dele. No servidor NFS, libere o IP do agente em <code>/etc/exports</code>,
            por exemplo <code>/srv/backups 10.0.0.5(rw,sync,no_subtree_check)</code>, e rode <code>exportfs -ra</code>.
          </div>
          <div className="grid gap-3 sm:grid-cols-[1fr_9rem]">
            <Field label="Servidor" hint="Nome ou IP, como o agente o enxerga."><Input value={f.host} onChange={(e) => set({ host: e.target.value })} placeholder="nas.empresa.local" required /></Field>
            <Field label="Porta do NFS" hint="Vazio = automática."><Input type="number" min={1} max={65535} value={f.port || ''} placeholder="automática" onChange={(e) => set({ port: Number(e.target.value) })} /></Field>
          </div>
          <Field label="Exportação" hint="O caminho exportado no servidor, como aparece em showmount -e."><Input className="font-mono" value={f.export} onChange={(e) => set({ export: e.target.value })} placeholder="/srv/backups" required /></Field>
          <details className="rounded-lg border border-border bg-bg p-3">
            <summary className="cursor-pointer font-medium">Usuário que grava (avançado)</summary>
            <p className="mt-2 text-muted">O NFS identifica quem grava por números (UID/GID). Vazio = 0 (root). Com <code>root_squash</code>, o padrão do servidor, o root vira “nobody”: informe o UID/GID do dono da pasta exportada ou use <code>all_squash,anonuid=…,anongid=…</code> na exportação.</p>
            <div className="mt-2 grid gap-3 sm:grid-cols-2">
              <Field label="UID"><Input type="number" min={0} value={f.uid} placeholder="0" onChange={(e) => set({ uid: e.target.value })} /></Field>
              <Field label="GID"><Input type="number" min={0} value={f.gid} placeholder="0" onChange={(e) => set({ gid: e.target.value })} /></Field>
            </div>
          </details>
        </>
      )}
      {type !== 'local' && type !== 'nfs' && (
        <>
          {type === 'ftp' && (
            <Field label="Segurança" hint="FTPS cifra a senha e os arquivos. Sem TLS, tudo trafega em texto puro.">
              <select className={sel} value={f.tls} onChange={(e) => set({ tls: e.target.value, port: 0 })}>
                <option value="explicit">FTPS explícito (recomendado)</option>
                <option value="implicit">FTPS implícito (porta 990)</option>
                <option value="none">FTP sem criptografia</option>
              </select>
            </Field>
          )}
          {type === 'ftp' && f.tls === 'none' && <div className="rounded-lg border border-danger/40 bg-danger/10 px-3 py-2 text-danger">Atenção: sem TLS, quem estiver na rede consegue ler a senha e o conteúdo dos backups. Use só numa rede isolada e confiável.</div>}
          <div className="grid gap-3 sm:grid-cols-[1fr_7rem]">
            <Field label="Servidor" hint="Nome ou IP, como o agente o enxerga."><Input value={f.host} onChange={(e) => set({ host: e.target.value })} placeholder="nas.empresa.local" required /></Field>
            <Field label="Porta"><Input type="number" min={1} max={65535} value={f.port || ''} placeholder={String(defPort)} onChange={(e) => set({ port: Number(e.target.value) })} /></Field>
          </div>
          {type === 'smb' && (
            <div className="grid gap-3 sm:grid-cols-2">
              <Field label="Compartilhamento"><Input value={f.share} onChange={(e) => set({ share: e.target.value })} placeholder="backups" required /></Field>
              <Field label="Domínio (opcional)"><Input value={f.domain} onChange={(e) => set({ domain: e.target.value })} /></Field>
            </div>
          )}
          <div className="grid gap-3 sm:grid-cols-2">
            <Field label="Usuário"><Input autoComplete="off" value={f.user} onChange={(e) => set({ user: e.target.value })} required /></Field>
            {!useKey && <Field label="Senha"><Input type="password" autoComplete="new-password" value={f.password} onChange={(e) => set({ password: e.target.value })} /></Field>}
          </div>
          {type === 'sftp' && (
            <>
              <label className="flex items-center gap-2"><input type="checkbox" checked={useKey} onChange={(e) => { setUseKey(e.target.checked); set({}) }} /> Entrar com chave privada em vez de senha</label>
              {useKey && <Field label="Chave privada (sem frase-senha)" hint="Conteúdo do arquivo, começando por -----BEGIN."><textarea className={`${sel} h-28 font-mono text-xs`} value={f.private_key} onChange={(e) => set({ private_key: e.target.value })} /></Field>}
            </>
          )}
          {type === 'ftp' && f.tls !== 'none' && <label className="flex items-center gap-2"><input type="checkbox" checked={f.skip_verify} onChange={(e) => set({ skip_verify: e.target.checked })} /> Aceitar certificado autoassinado (não verifica a identidade do servidor)</label>}
        </>
      )}
      <div className="grid gap-3 sm:grid-cols-2">
        {type !== 'local' && <Field label="Pasta base (opcional)" hint={type === 'sftp' ? 'Relativa à pasta do usuário; comece com / para caminho absoluto.' : type === 'nfs' ? 'Pasta dentro da exportação onde os backups ficam.' : 'Pasta dentro do destino onde os backups ficam.'}><Input value={f.base_path} onChange={(e) => set({ base_path: e.target.value })} placeholder="backups" /></Field>}
        <Field label="Conferência após o envio" hint="Completa relê o arquivo (dobra o tráfego).">
          <select className={sel} value={f.verify} onChange={(e) => set({ verify: e.target.value })}>
            <option value="full">Completa: relê e compara o SHA-256</option>
            <option value="size">Rápida: compara só o tamanho</option>
          </select>
        </Field>
      </div>

      <div className="rounded-lg border border-border bg-bg p-3">
        {type !== 'local' && <Field label="Testar a partir do agente" hint="Quem acessa o storage é o agente. O teste grava, relê e apaga um arquivo pequeno."><AgentSelect agents={agents} value={agent} onChange={(v) => { setAgent(v); setTested(null) }} /></Field>}
        {type === 'local' && <p className="text-muted">O teste grava, relê e apaga um arquivo pequeno na pasta escolhida.</p>}
        {agents.length === 0 && <p className="mt-2 text-muted">Nenhum agente cadastrado ainda. Cadastre um em Agentes antes de criar um storages de rede.</p>}
        <div className="mt-3"><Button type="button" variant="ghost" onClick={test} disabled={!!busy}>{busy === 'test' ? 'Testando…' : 'Testar storage'}</Button></div>
        {tested && <div className="mt-3 rounded-lg border border-ok/40 bg-ok/10 px-3 py-2 text-ok">Teste aprovado: {tested.detail}.{tested.total ? ` Espaço livre: ${fmtBytes(tested.free ?? 0)} de ${fmtBytes(tested.total)}.` : ''}</div>}
        {tested?.host_key && (
          <label className="mt-3 flex items-start gap-2">
            <input type="checkbox" className="mt-1" checked={trusted} onChange={(e) => setTrusted(e.target.checked)} />
            <span>Confirmo a impressão digital deste servidor: <code className="break-all">{tested.host_key}</code>. Ela fica fixada: se mudar, o envio é recusado.</span>
          </label>
        )}
      </div>
      <ErrorBox message={err} />
      <Button disabled={!canSave || !!busy}>{busy === 'save' ? 'Salvando…' : 'Salvar storage'}</Button>
      {!tested && <p className="text-xs text-muted">O botão de salvar libera depois de um teste aprovado.</p>}
    </form>
  )
}
