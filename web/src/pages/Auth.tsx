import { FormEvent, useEffect, useState } from 'react'
import { api, ApiError } from '../api'
import Logo from '../Logo'
import { Button, Card, ErrorBox, Field, Input } from '../ui'

function Shell({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div className="flex min-h-[70vh] items-center justify-center px-4">
      <Card className="w-full max-w-sm">
        <div className="mb-5 flex flex-col items-center gap-2 text-center">
          <Logo size={48} />
          <h1 className="text-xl font-semibold">{title}</h1>
        </div>
        {children}
      </Card>
    </div>
  )
}

export function Setup({ onDone }: { onDone: () => void }) {
  const [f, setF] = useState({ username: '', display_name: '', password: '', confirm: '', setup_code: '' })
  const [needsCode, setNeedsCode] = useState(false)
  useEffect(() => { api<{ needs_code: boolean }>('GET', '/api/setup/status').then((r) => setNeedsCode(!!r.needs_code)).catch(() => {}) }, [])
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState(false)
  async function submit(e: FormEvent) {
    e.preventDefault()
    if (f.password !== f.confirm) return setErr('As senhas não conferem.')
    setBusy(true); setErr('')
    try { await api('POST', '/api/setup', { username: f.username, display_name: f.display_name, password: f.password, setup_code: f.setup_code }); onDone() }
    catch (x) { setErr((x as ApiError).message) } finally { setBusy(false) }
  }
  return (
    <Shell title="Configuração inicial">
      <p className="mb-4 text-sm text-muted">Crie o primeiro administrador. Depois você cadastra servidores, agendamentos e nuvem aqui mesmo.</p>
      <form onSubmit={submit} className="space-y-3">
        {needsCode && <Field label="Código de configuração" hint="Mostrado nos logs do Server a cada início: docker logs corujabackup-server (procure “CONFIGURAÇÃO INICIAL PENDENTE”). Evita que um estranho crie o administrador antes de você."><Input autoFocus autoComplete="off" value={f.setup_code} onChange={(e) => setF({ ...f, setup_code: e.target.value })} required /></Field>}
        <Field label="Usuário" hint="3 a 64 caracteres: letras minúsculas, números, ponto, hífen ou sublinhado"><Input autoFocus={!needsCode} value={f.username} onChange={(e) => setF({ ...f, username: e.target.value })} required /></Field>
        <Field label="Nome de exibição"><Input value={f.display_name} onChange={(e) => setF({ ...f, display_name: e.target.value })} /></Field>
        <Field label="Senha" hint="Mínimo de 10 caracteres"><Input type="password" autoComplete="new-password" value={f.password} onChange={(e) => setF({ ...f, password: e.target.value })} required /></Field>
        <Field label="Confirme a senha"><Input type="password" autoComplete="new-password" value={f.confirm} onChange={(e) => setF({ ...f, confirm: e.target.value })} required /></Field>
        <ErrorBox message={err} />
        <Button className="w-full" disabled={busy}>Criar administrador</Button>
      </form>
    </Shell>
  )
}

export function Login({ onDone }: { onDone: () => void }) {
  const [f, setF] = useState({ username: '', password: '' })
  const [code, setCode] = useState('')
  const [step, setStep] = useState<'pass' | '2fa'>('pass')
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState(false)
  async function submit(e: FormEvent) {
    e.preventDefault(); setBusy(true); setErr('')
    try {
      if (step === 'pass') {
        const r = await api<{ needs_2fa: boolean }>('POST', '/api/login', f)
        if (r.needs_2fa) setStep('2fa'); else onDone()
      } else {
        await api('POST', '/api/login/2fa', { code }); onDone()
      }
    } catch (x) {
      const ex = x as ApiError
      setErr(ex.message)
      if (step === '2fa' && ex.status === 401 && /novamente/i.test(ex.message)) setStep('pass')
    } finally { setBusy(false) }
  }
  return (
    <Shell title="Entrar">
      <form onSubmit={submit} className="space-y-3">
        {step === 'pass' ? (
          <>
            <Field label="Usuário"><Input autoFocus autoComplete="username" value={f.username} onChange={(e) => setF({ ...f, username: e.target.value })} required /></Field>
            <Field label="Senha"><Input type="password" autoComplete="current-password" value={f.password} onChange={(e) => setF({ ...f, password: e.target.value })} required /></Field>
          </>
        ) : (
          <Field label="Código de verificação" hint="Do aplicativo autenticador, ou um código de recuperação">
            <Input autoFocus inputMode="text" autoComplete="one-time-code" value={code} onChange={(e) => setCode(e.target.value)} required />
          </Field>
        )}
        <ErrorBox message={err} />
        <Button className="w-full" disabled={busy}>{step === 'pass' ? 'Entrar' : 'Verificar'}</Button>
      </form>
    </Shell>
  )
}
