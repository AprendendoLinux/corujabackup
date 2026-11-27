// Roteador MikroTik de um job: endereço, acesso SSH, o que salvar e o teste (que mostra a impressão digital do roteador
// para confirmar no primeiro contato).
import { useState } from 'react'
import { api, ApiError } from '../api'
import { Button, ErrorBox, Field, Input } from '../ui'

export type MtState = {
  host: string; port: number; user: string; password: string; private_key: string; host_key: string
  export: boolean; export_sensitive: boolean; binary: boolean; backup_password: string
  password_set?: boolean; key_set?: boolean; backup_password_set?: boolean
}
export type MtView = Partial<MtState>

export function mtInitial(v?: MtView | null): MtState {
  return {
    host: v?.host ?? '', port: v?.port ?? 22, user: v?.user ?? '', password: '', private_key: '', host_key: v?.host_key ?? '',
    export: v?.export ?? true, export_sensitive: v?.export_sensitive ?? false, binary: v?.binary ?? true, backup_password: '',
    password_set: v?.password_set, key_set: v?.key_set, backup_password_set: v?.backup_password_set,
  }
}

export function mtPayload(m: MtState) {
  return { host: m.host.trim(), port: Number(m.port) || 22, user: m.user.trim(), password: m.password, private_key: m.private_key, host_key: m.host_key,
    export: m.export, export_sensitive: m.export_sensitive, binary: m.binary, backup_password: m.backup_password }
}

/** Mensagem do que falta (ou ''). */
export function mtCheck(m: MtState) {
  if (!m.host.trim()) return 'Informe o endereço do roteador.'
  if (!m.user.trim()) return 'Informe o usuário do roteador.'
  if (!m.password && !m.private_key && !m.password_set && !m.key_set) return 'Informe a senha (ou a chave privada) do usuário do roteador.'
  if (!m.export && !m.export_sensitive && !m.binary) return 'Escolha pelo menos um item para salvar.'
  if (m.binary && !m.backup_password && !m.backup_password_set) return 'Defina a senha do arquivo .backup.'
  if (m.binary && m.backup_password && m.backup_password.length < 8) return 'A senha do .backup precisa ter pelo menos 8 caracteres.'
  if (!m.host_key) return 'Teste a conexão e confirme a impressão digital do roteador.'
  return ''
}

type TestInfo = { identity: string; version: string; board: string; arch?: string; serial?: string; host_key: string }

export function MikrotikSection({ mt, setMt, agentId, jobId }: { mt: MtState; setMt: (m: MtState) => void; agentId: number; jobId?: number }) {
  const [useKey, setUseKey] = useState(!!mt.key_set && !mt.password_set)
  const [info, setInfo] = useState<TestInfo | null>(null)
  const [seen, setSeen] = useState('')
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState(false)
  const sel = 'w-full rounded-lg border border-border bg-surface px-3 py-2'
  // mudar o endereço ou o acesso invalida a confirmação da chave
  const set = (p: Partial<MtState>, resetKey = false) => { setMt({ ...mt, ...p, ...(resetKey ? { host_key: '' } : {}) }); if (resetKey) { setInfo(null); setSeen('') } }
  async function test() {
    setErr(''); setInfo(null); setBusy(true)
    try {
      const r = await api<TestInfo>('POST', `/api/agents/${agentId}/mikrotik-test`, { mikrotik: { ...mtPayload(mt), host_key: mt.host_key }, job_id: jobId ?? 0 })
      setInfo(r)
      if (r.host_key === mt.host_key) setSeen('') ; else setSeen(r.host_key)
    } catch (x) { setErr((x as ApiError).message) } finally { setBusy(false) }
  }
  const check = (label: string, k: 'export' | 'export_sensitive' | 'binary', hint: string) => (
    <label className="flex items-start gap-2.5 rounded-lg border border-border p-3">
      <input type="checkbox" className="mt-0.5" checked={mt[k]} onChange={(e) => set({ [k]: e.target.checked } as Partial<MtState>)} />
      <span><b className="block text-sm">{label}</b><span className="text-xs text-muted">{hint}</span></span>
    </label>
  )
  return (
    <div className="space-y-4 text-sm">
      <div className="rounded-lg border border-border bg-bg p-3 text-muted">
        O agente acessa o roteador por <b>SSH</b> (precisa alcançá-lo pela rede). Crie no roteador um usuário só para o backup:
        <code className="mt-1 block whitespace-pre-wrap break-all text-xs">/user group add name=coruja-backup policy=ssh,ftp,read,write,policy,test,sensitive{'\n'}/user add name=coruja group=coruja-backup password=SENHA-FORTE</code>
      </div>
      <div className="grid gap-3 sm:grid-cols-[1fr_7rem]">
        <Field label="Roteador" hint="Nome ou IP, como o agente o enxerga."><Input value={mt.host} onChange={(e) => set({ host: e.target.value }, true)} placeholder="192.168.88.1" /></Field>
        <Field label="Porta SSH"><Input type="number" min={1} max={65535} value={mt.port || ''} placeholder="22" onChange={(e) => set({ port: Number(e.target.value) }, true)} /></Field>
      </div>
      <div className="grid gap-3 sm:grid-cols-2">
        <Field label="Usuário"><Input autoComplete="off" value={mt.user} onChange={(e) => set({ user: e.target.value }, true)} placeholder="coruja" /></Field>
        {!useKey && <Field label="Senha" hint={mt.password_set ? 'Deixe em branco para manter a senha salva.' : undefined}><Input type="password" autoComplete="new-password" value={mt.password} onChange={(e) => set({ password: e.target.value })} /></Field>}
      </div>
      <label className="flex items-center gap-2"><input type="checkbox" checked={useKey} onChange={(e) => { setUseKey(e.target.checked); set({ password: '', private_key: '' }) }} /> Entrar com chave privada em vez de senha</label>
      {useKey && <Field label="Chave privada (sem frase-senha)" hint={mt.key_set ? 'Deixe em branco para manter a chave salva.' : 'Conteúdo do arquivo, começando por -----BEGIN.'}><textarea className={`${sel} h-28 font-mono text-xs`} value={mt.private_key} onChange={(e) => set({ private_key: e.target.value })} /></Field>}

      <div>
        <span className="mb-1.5 block text-sm font-medium">O que salvar</span>
        <div className="space-y-2">
          {check('Export da configuração (.rsc)', 'export', 'Texto legível, sem senhas. Bom para conferir mudanças e migrar para outro modelo.')}
          {check('Export com dados sensíveis (.rsc)', 'export_sensitive', 'Inclui senhas, chaves e segredos (show-sensitive). Restaura tudo, mas o arquivo passa a conter segredos.')}
          {check('Backup binário (.backup)', 'binary', 'Cópia completa do RouterOS, com usuários e certificados, cifrada com a senha abaixo. Só restaura em equipamento igual.')}
        </div>
      </div>
      {mt.binary && (
        <Field label="Senha do .backup" hint={mt.backup_password_set ? 'Deixe em branco para manter a senha salva. GUARDE esta senha: sem ela o .backup não restaura.' : 'Mínimo de 8 caracteres. GUARDE esta senha: sem ela o .backup não restaura.'}>
          <Input type="password" autoComplete="new-password" value={mt.backup_password} onChange={(e) => set({ backup_password: e.target.value })} />
        </Field>
      )}

      <div className="rounded-lg border border-border bg-bg p-3">
        <Button type="button" variant="ghost" onClick={test} disabled={busy || !agentId}>{busy ? 'Testando…' : 'Testar conexão com o roteador'}</Button>
        <p className="mt-2 text-xs text-muted">O teste conecta, lê a identificação e confere as permissões do export. Nada é gravado no roteador.</p>
        {info && (
          <div className="mt-3 rounded-lg border border-ok/40 bg-ok/10 px-3 py-2 text-ok">
            Conectado: <b>{info.identity}</b> · RouterOS {info.version} · {info.board}{info.serial ? ` · série ${info.serial}` : ''}
          </div>
        )}
        {seen && (
          <label className="mt-3 flex items-start gap-2">
            <input type="checkbox" className="mt-1" checked={mt.host_key === seen} onChange={(e) => set({ host_key: e.target.checked ? seen : '' })} />
            <span>Confirmo a impressão digital deste roteador: <code className="break-all">{seen}</code>. Ela fica fixada: se mudar, o backup é recusado.</span>
          </label>
        )}
        {mt.host_key && !seen && <p className="mt-2 text-xs text-muted">Impressão digital confirmada: <code className="break-all">{mt.host_key}</code></p>}
      </div>
      <ErrorBox message={err} />
    </div>
  )
}
