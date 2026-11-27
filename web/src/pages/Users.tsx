import { FormEvent, useCallback, useEffect, useState } from 'react'
import { ROLE_LABEL, User, api, ApiError, fmtDate, userName } from '../api'
import NameForm from '../NameForm'
import { Actions, Badge, Button, Card, ErrorBox, Field, IconButton, Input, Modal } from '../ui'
import Avatar from '../Avatar'
import { confirmar, perguntar } from '../Dialogs'

export default function Users({ me }: { me: User }) {
  const [users, setUsers] = useState<User[]>([])
  const [err, setErr] = useState('')
  const [modal, setModal] = useState(false)
  const [rename, setRename] = useState<User | null>(null)
  const load = useCallback(() => {
    api<{ users: User[] }>('GET', '/api/users').then((r) => setUsers(r.users)).catch((x) => setErr((x as ApiError).message))
  }, [])
  useEffect(load, [load])
  async function act(fn: () => Promise<unknown>) { try { await fn(); setErr(''); load() } catch (x) { setErr((x as ApiError).message) } }

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-semibold">Usuários</h1>
          <p className="text-sm text-muted">Quem acessa o Coruja Backup e com qual perfil.</p>
        </div>
        <Button onClick={() => setModal(true)}>+ Novo usuário</Button>
      </div>
      <ErrorBox message={err} />
      <Card className="overflow-x-auto p-0">
        <table className="w-full text-sm">
          <thead className="border-b border-border text-left text-muted"><tr><th className="p-3">Usuário</th><th className="p-3">Perfil</th><th className="p-3">2FA</th><th className="p-3">Último acesso</th><th className="p-3" /></tr></thead>
          <tbody>
            {users.map((u) => (
              <tr key={u.id} className="border-b border-border last:border-0">
                <td className="p-3"><div className="flex items-center gap-3"><Avatar user={u} size={34} /><div><b>{userName(u)}</b>{u.id === me.id && <span className="ml-2 text-xs text-muted">(você)</span>}{u.display_name && <div className="font-mono text-xs text-muted">{u.username}</div>}</div></div></td>
                <td className="p-3">
                  <select aria-label={`Perfil de ${u.username}`} className="rounded-md border border-border bg-surface px-2 py-1" value={u.role}
                    onChange={(e) => act(() => api('PATCH', `/api/users/${u.id}`, { role: e.target.value }))}>
                    {Object.entries(ROLE_LABEL).map(([k, v]) => <option key={k} value={k}>{v}</option>)}
                  </select>
                </td>
                <td className="p-3">{u.totp_enabled ? <Badge tone="ok">Ativo</Badge> : <Badge tone="off">Desligado</Badge>}</td>
                <td className="p-3 text-muted">{fmtDate(u.last_login_at)} {u.disabled && <Badge tone="danger">Desativado</Badge>}</td>
                <td className="p-3 text-right whitespace-nowrap">
                  <Actions>
                    <IconButton icon="editar" label="Editar nome" onClick={() => setRename(u)} />
                    <IconButton icon="senha" label="Redefinir senha" onClick={async () => { const p = await perguntar({ title: `Redefinir a senha de ${u.username}`, label: 'Nova senha', type: 'password', hint: 'Mínimo de 10 caracteres. As sessões abertas dele são encerradas.', confirmLabel: 'Redefinir', icon: 'senha' }); if (p) act(() => api('PATCH', `/api/users/${u.id}`, { new_password: p })) }} />
                    {u.totp_enabled && <IconButton icon="resetar2fa" label="Resetar 2FA" onClick={async () => await confirmar({ title: `Desativar o 2FA de ${u.username}?`, message: 'Ele passa a entrar só com a senha até ativar a verificação de novo.', confirmLabel: 'Desativar 2FA', danger: true, icon: 'resetar2fa' }) && act(() => api('PATCH', `/api/users/${u.id}`, { reset_2fa: true }))} />}
                    <IconButton icon="ligar" label={u.disabled ? 'Ativar' : 'Desativar'} onClick={() => act(() => api('PATCH', `/api/users/${u.id}`, { disabled: !u.disabled }))} />
                    {u.id !== me.id && <IconButton icon="excluir" tone="danger" label="Excluir" onClick={async () => await confirmar({ title: `Excluir ${u.username}?`, message: 'O acesso dele é removido definitivamente.', danger: true }) && act(() => api('DELETE', `/api/users/${u.id}`))} />}
                  </Actions>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </Card>
      {rename && <Modal title={`Nome de ${rename.username}`} onClose={() => setRename(null)}><NameForm compact user={rename} url={`/api/users/${rename.id}`} onDone={() => { setRename(null); load() }} /></Modal>}
      {modal && <Modal title="Novo usuário" onClose={() => setModal(false)}><NewUser onDone={() => { setModal(false); load() }} /></Modal>}
    </div>
  )
}

function NewUser({ onDone }: { onDone: () => void }) {
  const [f, setF] = useState({ username: '', first_name: '', last_name: '', password: '', role: 'viewer' })
  const [err, setErr] = useState('')
  async function submit(e: FormEvent) {
    e.preventDefault()
    try { await api('POST', '/api/users', f); onDone() } catch (x) { setErr((x as ApiError).message) }
  }
  return (
    <form onSubmit={submit} className="space-y-3">
      <Field label="Usuário"><Input autoFocus value={f.username} onChange={(e) => setF({ ...f, username: e.target.value })} required /></Field>
      <div className="grid gap-3 sm:grid-cols-2">
        <Field label="Nome"><Input maxLength={60} value={f.first_name} onChange={(e) => setF({ ...f, first_name: e.target.value })} required /></Field>
        <Field label="Sobrenome"><Input maxLength={60} value={f.last_name} onChange={(e) => setF({ ...f, last_name: e.target.value })} /></Field>
      </div>
      <Field label="Senha inicial" hint="Mínimo de 10 caracteres"><Input type="password" autoComplete="new-password" value={f.password} onChange={(e) => setF({ ...f, password: e.target.value })} required /></Field>
      <Field label="Perfil" hint="Administrador: tudo · Operador: gerencia agentes e backups · Somente leitura: só consulta">
        <select className="w-full rounded-lg border border-border bg-surface px-3 py-2" value={f.role} onChange={(e) => setF({ ...f, role: e.target.value })}>
          {Object.entries(ROLE_LABEL).map(([k, v]) => <option key={k} value={k}>{v}</option>)}
        </select>
      </Field>
      <ErrorBox message={err} />
      <Button>Criar usuário</Button>
    </form>
  )
}
