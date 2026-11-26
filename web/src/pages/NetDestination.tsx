import { FormEvent, useEffect, useState } from 'react'
import { Agent, api, ApiError } from '../api'
import { Button, ErrorBox, Field, Input } from '../ui'

export const NET_TYPES = ['sftp', 'ftp', 'smb', 'local']
const sel = 'w-full rounded-lg border border-border bg-surface px-3 py-2'

type Cfg = {
  type: string; host: string; port: number; user: string; password: string; private_key: string; host_key: string
  tls: string; skip_verify: boolean; share: string; domain: string; base_path: string; verify: string
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
    <select className={sel} value={value} onChange={(e) => onChange(Number(e.target.value))} aria-label="Agente que testa o destino">
      <option value={0}>Escolha um agente…</option>
      {agents.map((a) => <option key={a.id} value={a.id} disabled={!a.online}>{a.name}{a.online ? '' : ' (offline)'}</option>)}
    </select>
  )
}

export default function NetDestination({ type, onSaved }: { type: string; onSaved: () => void }) {
  const agents = useAgents()
  const [name, setName] = useState('')
  const [agent, setAgent] = useState(0)
  const [f, setF] = useState<Cfg>({ type, host: '', port: 0, user: '', password: '', private_key: '', host_key: '', tls: 'explicit', skip_verify: false, share: '', domain: '', base_path: '', verify: 'full' })
  const [useKey, setUseKey] = useState(false)
  const [tested, setTested] = useState<{ detail: string; host_key?: string } | null>(null)
  const [trusted, setTrusted] = useState(false)
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState('')
  useEffect(() => { const on = agents.find((a) => a.online); if (on && !agent) setAgent(on.id) }, [agents, agent])
  // qualquer mudança na configuração invalida o teste
  const set = (p: Partial<Cfg>) => { setF((v) => ({ ...v, host_key: '', ...p })); setTested(null); setTrusted(false) }
  const defPort = type === 'sftp' ? 22 : type === 'smb' ? 445 : f.tls === 'implicit' ? 990 : 21
  const cfg = () => ({ ...f, password: useKey ? '' : f.password, private_key: useKey ? f.private_key : '' })

  async function test() {
    setErr(''); setTested(null); setTrusted(false)
    if (!agent) { setErr('Escolha um agente online: é ele que vai acessar o destino.'); return }
    setBusy('test')
    try { setTested(await api<{ detail: string; host_key?: string }>('POST', '/api/destinations/test', { agent_id: agent, config: cfg() })) }
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
      <Field label="Nome do destino" hint="Como aparece nos jobs. Ex.: NAS da matriz"><Input autoFocus value={name} onChange={(e) => setName(e.target.value)} required /></Field>

      {type === 'local' && (
        <div className="rounded-lg border border-border bg-bg p-3 text-muted">
          Grava numa pasta do próprio agente, dentro da raiz de destinos (<code>/destinos</code>). Para NFS, iSCSI ou um disco externo, monte-o no servidor e
          adicione ao contêiner do agente um volume como <code>- /mnt/nas:/destinos/nas</code>.
        </div>
      )}
      {type !== 'local' && (
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
        <Field label="Pasta base (opcional)" hint={type === 'sftp' ? 'Relativa à pasta do usuário; comece com / para caminho absoluto.' : 'Pasta dentro do destino onde os backups ficam.'}><Input value={f.base_path} onChange={(e) => set({ base_path: e.target.value })} placeholder="backups" /></Field>
        <Field label="Conferência após o envio" hint="Completa relê o arquivo (dobra o tráfego).">
          <select className={sel} value={f.verify} onChange={(e) => set({ verify: e.target.value })}>
            <option value="full">Completa: relê e compara o SHA-256</option>
            <option value="size">Rápida: compara só o tamanho</option>
          </select>
        </Field>
      </div>

      <div className="rounded-lg border border-border bg-bg p-3">
        <Field label="Testar a partir do agente" hint="Quem acessa o destino é o agente. O teste grava, relê e apaga um arquivo pequeno."><AgentSelect agents={agents} value={agent} onChange={(v) => { setAgent(v); setTested(null) }} /></Field>
        {agents.length === 0 && <p className="mt-2 text-muted">Nenhum agente cadastrado ainda. Cadastre um em Agentes antes de criar destinos de rede.</p>}
        <div className="mt-3"><Button type="button" variant="ghost" onClick={test} disabled={!!busy}>{busy === 'test' ? 'Testando…' : 'Testar destino'}</Button></div>
        {tested && <div className="mt-3 rounded-lg border border-ok/40 bg-ok/10 px-3 py-2 text-ok">Teste aprovado: {tested.detail}.</div>}
        {tested?.host_key && (
          <label className="mt-3 flex items-start gap-2">
            <input type="checkbox" className="mt-1" checked={trusted} onChange={(e) => setTrusted(e.target.checked)} />
            <span>Confirmo a impressão digital deste servidor: <code className="break-all">{tested.host_key}</code>. Ela fica fixada: se mudar, o envio é recusado.</span>
          </label>
        )}
      </div>
      <ErrorBox message={err} />
      <Button disabled={!canSave || !!busy}>{busy === 'save' ? 'Salvando…' : 'Salvar destino'}</Button>
      {!tested && <p className="text-xs text-muted">O botão de salvar libera depois de um teste aprovado.</p>}
    </form>
  )
}
