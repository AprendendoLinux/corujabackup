import { FormEvent, useState } from 'react'
import { User, api, ApiError } from './api'
import { Button, ErrorBox, Field, Input } from './ui'

/** Nome e sobrenome de um usuário (o próprio em Minha conta, ou qualquer um em Usuários). */
export default function NameForm({ user, url, onDone, compact }: { user: User; url: string; onDone: () => void; compact?: boolean }) {
  const split = (user.display_name || '').split(' ')
  const [first, setFirst] = useState(user.first_name ?? split[0] ?? '')
  const [last, setLast] = useState(user.last_name ?? split.slice(1).join(' '))
  const [err, setErr] = useState('')
  const [ok, setOk] = useState(false)
  const [busy, setBusy] = useState(false)
  const changed = first.trim() !== (user.first_name ?? '') || last.trim() !== (user.last_name ?? '')
  async function submit(e: FormEvent) {
    e.preventDefault()
    setErr(''); setOk(false); setBusy(true)
    try { await api('PATCH', url, { first_name: first, last_name: last }); setOk(true); onDone() }
    catch (x) { setErr((x as ApiError).message) } finally { setBusy(false) }
  }
  return (
    <form onSubmit={submit} className="space-y-3">
      <div className={`grid gap-3 sm:grid-cols-2 ${compact ? '' : 'max-w-2xl'}`}>
        <Field label="Nome"><Input autoComplete="given-name" maxLength={60} value={first} onChange={(e) => { setFirst(e.target.value); setOk(false) }} required /></Field>
        <Field label="Sobrenome"><Input autoComplete="family-name" maxLength={60} value={last} onChange={(e) => { setLast(e.target.value); setOk(false) }} /></Field>
      </div>
      <ErrorBox message={err} />
      <div className="flex items-center gap-3">
        <Button disabled={busy || !changed || !first.trim()}>{busy ? 'Salvando…' : 'Salvar nome'}</Button>
        {ok && !changed && <span className="text-sm text-muted">Nome atualizado.</span>}
      </div>
    </form>
  )
}
