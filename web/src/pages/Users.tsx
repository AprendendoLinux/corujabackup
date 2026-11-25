import { FormEvent, useCallback, useEffect, useState } from 'react'
import { ROLE_LABEL, User, api, ApiError, fmtDate } from '../api'
import { Badge, Button, Card, ErrorBox, Field, Input, Modal } from '../ui'

export default function Users({ me }: { me: User }) {
  const [users, setUsers] = useState<User[]>([])
  const [err, setErr] = useState('')
  const [modal, setModal] = useState(false)
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
                <td className="p-3"><b>{u.username}</b>{u.id === me.id && <span className="ml-2 text-xs text-muted">(você)</span>}<div className="text-xs text-muted">{u.display_name}</div></td>
                <td className="p-3">
                  <select aria-label={`Perfil de ${u.username}`} className="rounded-md border border-border bg-surface px-2 py-1" value={u.role}
                    onChange={(e) => act(() => api('PATCH', `/api/users/${u.id}`, { role: e.target.value }))}>
                    {Object.entries(ROLE_LABEL).map(([k, v]) => <option key={k} value={k}>{v}</option>)}
                  </select>
                </td>
                <td className="p-3">{u.totp_enabled ? <Badge tone="ok">Ativo</Badge> : <Badge tone="off">Desligado</Badge>}</td>
                <td className="p-3 text-muted">{fmtDate(u.last_login_at)} {u.disabled && <Badge tone="danger">Desativado</Badge>}</td>
                <td className="p-3 text-right whitespace-nowrap">
                  <button className="text-primary hover:underline" onClick={() => { const p = prompt(`Nova senha para ${u.username} (mín. 10 caracteres):`); if (p) act(() => api('PATCH', `/api/users/${u.id}`, { new_password: p })) }}>Redefinir senha</button>
                  {u.totp_enabled && <button className="ml-3 text-primary hover:underline" onClick={() => confirm(`Desativar o 2FA de ${u.username}?`) && act(() => api('PATCH', `/api/users/${u.id}`, { reset_2fa: true }))}>Resetar 2FA</button>}
                  <button className="ml-3 text-primary hover:underline" onClick={() => act(() => api('PATCH', `/api/users/${u.id}`, { disabled: !u.disabled }))}>{u.disabled ? 'Ativar' : 'Desativar'}</button>
                  {u.id !== me.id && <button className="ml-3 text-danger hover:underline" onClick={() => confirm(`Excluir ${u.username}?`) && act(() => api('DELETE', `/api/users/${u.id}`))}>Excluir</button>}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </Card>
      {modal && <Modal title="Novo usuário" onClose={() => setModal(false)}><NewUser onDone={() => { setModal(false); load() }} /></Modal>}
    </div>
  )
}

function NewUser({ onDone }: { onDone: () => void }) {
  const [f, setF] = useState({ username: '', display_name: '', password: '', role: 'viewer' })
  const [err, setErr] = useState('')
  async function submit(e: FormEvent) {
    e.preventDefault()
    try { await api('POST', '/api/users', f); onDone() } catch (x) { setErr((x as ApiError).message) }
  }
  return (
    <form onSubmit={submit} className="space-y-3">
      <Field label="Usuário"><Input autoFocus value={f.username} onChange={(e) => setF({ ...f, username: e.target.value })} required /></Field>
      <Field label="Nome de exibição"><Input value={f.display_name} onChange={(e) => setF({ ...f, display_name: e.target.value })} /></Field>
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
