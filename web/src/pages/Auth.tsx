import { FormEvent, ReactNode, useEffect, useState } from 'react'
import { api, ApiError } from '../api'
import Icon, { IconName } from '../Icon'
import Logo, { useBrand } from '../Logo'
import { Button, ErrorBox, Field, Input, Spinner, inputCls } from '../ui'

const FEATURES: [IconName, string, string][] = [
  ['seguranca', 'Backups conferidos', 'Cada cópia é relida e conferida pelo SHA-256 antes de valer.'],
  ['destinos', 'Onde você precisar', 'Pasta local, SFTP, SMB, NFS, FTP e as principais nuvens.'],
  ['restaurar', 'Restauração em poucos cliques', 'Navegue pelas versões e traga de volta só o que precisa.'],
]

/** Logo num quadro claro: qualquer logo (colorida ou não) fica legível sobre a cor do tema. */
function LogoTile({ size = 44 }: { size?: number }) {
  return <span className="inline-flex items-center justify-center rounded-2xl bg-surface p-2.5 shadow-lg ring-1 ring-black/5"><Logo size={size} /></span>
}

/** Telas de entrada (login e configuração inicial): painel da marca à esquerda (no celular, uma faixa no topo) e o
 *  formulário à direita. Cores e logo vêm do tema ativo. */
function Shell({ title, subtitle, icon, footer, children }: { title: string; subtitle?: ReactNode; icon?: IconName; footer: ReactNode; children: ReactNode }) {
  const { brand } = useBrand()
  return (
    <div className="flex min-h-screen bg-bg">
      <aside className="auth-hero relative hidden w-[44%] max-w-[620px] shrink-0 flex-col justify-between overflow-hidden p-12 lg:flex">
        <div className="auth-dots pointer-events-none absolute inset-0" />
        <div className="relative flex items-center gap-3">
          <LogoTile size={36} />
          <span className="text-xl font-semibold tracking-tight">{brand.name}</span>
        </div>
        <div className="relative max-w-md">
          <h2 className="text-4xl font-semibold leading-tight tracking-tight">Seus dados protegidos e sempre conferidos.</h2>
          <p className="auth-soft mt-4 text-base">Backups agendados nos seus servidores, enviados para onde você escolher e prontos para voltar quando precisar.</p>
          <ul className="mt-10 space-y-5">
            {FEATURES.map(([icon, t, d]) => (
              <li key={t} className="flex gap-4">
                <span className="auth-chip flex h-10 w-10 shrink-0 items-center justify-center rounded-xl"><Icon name={icon} size={20} /></span>
                <span><b className="block font-semibold">{t}</b><span className="auth-soft text-sm">{d}</span></span>
              </li>
            ))}
          </ul>
        </div>
        <p className="auth-soft relative flex items-center gap-2 text-sm"><Icon name="senha" size={16} />Acesso protegido por senha e verificação em duas etapas.</p>
      </aside>

      <div className="flex min-w-0 flex-1 flex-col">
        <div className="auth-hero relative overflow-hidden px-6 pb-20 pt-10 text-center lg:hidden">
          <div className="auth-dots pointer-events-none absolute inset-0" />
          <div className="relative flex flex-col items-center gap-3">
            <LogoTile size={44} />
            <span className="text-2xl font-semibold tracking-tight">{brand.name}</span>
            <span className="auth-soft hidden text-sm sm:block">Seus dados protegidos e sempre conferidos.</span>
          </div>
        </div>
        <main className="flex flex-1 items-start justify-center px-4 pb-10 lg:items-center lg:px-8 lg:py-10">
          <div className="auth-surgir -mt-12 w-full max-w-[420px] lg:mt-0">
            <div className="rounded-2xl border border-border bg-surface p-6 shadow-xl shadow-black/5 sm:p-8">
              <div className={icon ? 'text-center' : ''}>
                {icon && <span className="mx-auto mb-4 flex h-14 w-14 items-center justify-center rounded-2xl bg-primary/10 text-primary"><Icon name={icon} size={28} /></span>}
                <h1 className="text-2xl font-semibold tracking-tight">{title}</h1>
                {subtitle && <p className="mt-1.5 text-sm text-muted">{subtitle}</p>}
              </div>
              <div className="mt-6">{children}</div>
            </div>
          </div>
        </main>
        {footer}
      </div>
    </div>
  )
}

/** Campo com ícone à esquerda (e um botão opcional à direita). */
function IconInput({ icon, end, className = '', ...p }: React.InputHTMLAttributes<HTMLInputElement> & { icon: IconName; end?: ReactNode }) {
  return (
    <span className="relative block">
      <span className="pointer-events-none absolute inset-y-0 left-3 flex items-center text-muted"><Icon name={icon} size={18} /></span>
      <input className={`${inputCls} h-11 pl-10 ${end ? 'pr-11' : ''} ${className}`} {...p} />
      {end && <span className="absolute inset-y-0 right-1.5 flex items-center">{end}</span>}
    </span>
  )
}

export function Setup({ onDone, footer }: { onDone: () => void; footer: ReactNode }) {
  const [f, setF] = useState({ username: '', first_name: '', last_name: '', password: '', confirm: '', setup_code: '' })
  const [needsCode, setNeedsCode] = useState(false)
  useEffect(() => { api<{ needs_code: boolean }>('GET', '/api/setup/status').then((r) => setNeedsCode(!!r.needs_code)).catch(() => {}) }, [])
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState(false)
  async function submit(e: FormEvent) {
    e.preventDefault()
    if (f.password !== f.confirm) return setErr('As senhas não conferem.')
    setBusy(true); setErr('')
    try { await api('POST', '/api/setup', { username: f.username, first_name: f.first_name, last_name: f.last_name, password: f.password, setup_code: f.setup_code }); onDone() }
    catch (x) { setErr((x as ApiError).message) } finally { setBusy(false) }
  }
  return (
    <Shell title="Configuração inicial" footer={footer} subtitle="Crie o primeiro administrador. Depois você cadastra agentes, agendamentos e destinos aqui mesmo.">
      <form onSubmit={submit} className="space-y-3">
        {needsCode && <Field label="Código de configuração" hint="Mostrado nos logs do Server a cada início: docker logs corujabackup-server (procure “CONFIGURAÇÃO INICIAL PENDENTE”). Evita que um estranho crie o administrador antes de você."><Input autoFocus autoComplete="off" value={f.setup_code} onChange={(e) => setF({ ...f, setup_code: e.target.value })} required /></Field>}
        <Field label="Usuário" hint="3 a 64 caracteres: letras minúsculas, números, ponto, hífen ou sublinhado"><Input autoFocus={!needsCode} value={f.username} onChange={(e) => setF({ ...f, username: e.target.value })} required /></Field>
        <div className="grid gap-3 sm:grid-cols-2">
          <Field label="Nome"><Input autoComplete="given-name" maxLength={60} value={f.first_name} onChange={(e) => setF({ ...f, first_name: e.target.value })} required /></Field>
          <Field label="Sobrenome"><Input autoComplete="family-name" maxLength={60} value={f.last_name} onChange={(e) => setF({ ...f, last_name: e.target.value })} /></Field>
        </div>
        <Field label="Senha" hint="Mínimo de 10 caracteres"><Input type="password" autoComplete="new-password" value={f.password} onChange={(e) => setF({ ...f, password: e.target.value })} required /></Field>
        <Field label="Confirme a senha"><Input type="password" autoComplete="new-password" value={f.confirm} onChange={(e) => setF({ ...f, confirm: e.target.value })} required /></Field>
        <ErrorBox message={err} />
        <Button className="h-11 w-full text-base" disabled={busy}>{busy ? <><Spinner className="h-4 w-4" />Criando…</> : 'Criar administrador'}</Button>
      </form>
    </Shell>
  )
}

export function Login({ onDone, footer }: { onDone: () => void; footer: ReactNode }) {
  const { brand } = useBrand()
  const [f, setF] = useState({ username: '', password: '' })
  const [code, setCode] = useState('')
  const [step, setStep] = useState<'pass' | '2fa'>('pass')
  const [show, setShow] = useState(false)
  const [caps, setCaps] = useState(false)
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
  const capsCheck = (e: React.KeyboardEvent) => setCaps(e.getModifierState?.('CapsLock') ?? false)
  if (step === '2fa') {
    return (
      <Shell title="Verificação em duas etapas" icon="seguranca" footer={footer} subtitle="Digite o código de 6 dígitos do seu aplicativo autenticador, ou um dos seus códigos de recuperação.">
        <form onSubmit={submit} className="space-y-4">
          <label className="block">
            <span className="sr-only">Código de verificação</span>
            <Input autoFocus inputMode="text" autoComplete="one-time-code" spellCheck={false} placeholder="000000" value={code} onChange={(e) => setCode(e.target.value)} required
              className="h-14 text-center font-mono text-2xl tracking-[0.35em] placeholder:text-border" />
          </label>
          <ErrorBox message={err} />
          <Button className="h-11 w-full text-base" disabled={busy}>{busy ? <><Spinner className="h-4 w-4" />Verificando…</> : 'Verificar'}</Button>
          <button type="button" onClick={() => { setStep('pass'); setCode(''); setErr('') }} className="mx-auto flex items-center gap-1.5 text-sm text-muted hover:text-text">
            <Icon name="voltar" size={16} />Voltar e entrar com outro usuário
          </button>
        </form>
      </Shell>
    )
  }
  return (
    <Shell title="Bem-vindo de volta" footer={footer} subtitle={<>Entre com seu usuário e senha para acessar o {brand.name}.</>}>
      <form onSubmit={submit} className="space-y-4">
        <Field label="Usuário">
          <IconInput icon="conta" autoFocus autoComplete="username" autoCapitalize="none" spellCheck={false} placeholder="seu.usuario" value={f.username} onChange={(e) => setF({ ...f, username: e.target.value })} required />
        </Field>
        <Field label="Senha">
          <IconInput icon="senha" type={show ? 'text' : 'password'} autoComplete="current-password" placeholder="Sua senha" value={f.password}
            onChange={(e) => setF({ ...f, password: e.target.value })} onKeyDown={capsCheck} onKeyUp={capsCheck} onBlur={() => setCaps(false)} required
            end={<button type="button" onClick={() => setShow(!show)} aria-label={show ? 'Ocultar senha' : 'Mostrar senha'} title={show ? 'Ocultar senha' : 'Mostrar senha'}
              className="rounded-md p-1.5 text-muted transition hover:bg-bg hover:text-text focus:outline-none focus-visible:ring-2 focus-visible:ring-accent"><Icon name={show ? 'ocultar' : 'ver'} size={18} /></button>} />
          {caps && <span className="mt-1.5 flex items-center gap-1.5 text-xs font-medium text-accent"><Icon name="seguranca" size={14} />Caps Lock ativado</span>}
        </Field>
        <ErrorBox message={err} />
        <Button className="h-11 w-full text-base" disabled={busy}>{busy ? <><Spinner className="h-4 w-4" />Entrando…</> : 'Entrar'}</Button>
      </form>
    </Shell>
  )
}

/** Troca obrigatória da senha padrão (admin/admin da primeira subida): nada do sistema abre antes disso. */
export function ForcePassword({ username, onDone, onLogout, footer }: { username: string; onDone: () => void; onLogout: () => void; footer: ReactNode }) {
  const [f, setF] = useState({ current: '', next: '', confirm: '' })
  const [show, setShow] = useState(false)
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState(false)
  async function submit(e: FormEvent) {
    e.preventDefault()
    if (f.next !== f.confirm) return setErr('As senhas novas não conferem.')
    setBusy(true); setErr('')
    try { await api('POST', '/api/me/password', { current: f.current, new: f.next }); onDone() }
    catch (x) { setErr((x as ApiError).message) } finally { setBusy(false) }
  }
  const eye = <button type="button" onClick={() => setShow(!show)} aria-label={show ? 'Ocultar senhas' : 'Mostrar senhas'} title={show ? 'Ocultar senhas' : 'Mostrar senhas'}
    className="rounded-md p-1.5 text-muted transition hover:bg-bg hover:text-text focus:outline-none focus-visible:ring-2 focus-visible:ring-accent"><Icon name={show ? 'ocultar' : 'ver'} size={18} /></button>
  return (
    <Shell title="Crie uma senha nova" icon="senha" footer={footer}
      subtitle={<>Você entrou com a senha padrão do usuário <b>{username}</b>. Por segurança, defina uma senha nova antes de continuar.</>}>
      <form onSubmit={submit} className="space-y-4">
        <Field label="Senha atual">
          <IconInput icon="senha" type={show ? 'text' : 'password'} autoComplete="current-password" autoFocus value={f.current} onChange={(e) => setF({ ...f, current: e.target.value })} required end={eye} />
        </Field>
        <Field label="Senha nova" hint="Mínimo de 10 caracteres, diferente da atual">
          <IconInput icon="senha" type={show ? 'text' : 'password'} autoComplete="new-password" minLength={10} value={f.next} onChange={(e) => setF({ ...f, next: e.target.value })} required />
        </Field>
        <Field label="Confirme a senha nova">
          <IconInput icon="senha" type={show ? 'text' : 'password'} autoComplete="new-password" minLength={10} value={f.confirm} onChange={(e) => setF({ ...f, confirm: e.target.value })} required />
        </Field>
        <ErrorBox message={err} />
        <Button className="h-11 w-full text-base" disabled={busy}>{busy ? <><Spinner className="h-4 w-4" />Salvando…</> : 'Salvar e continuar'}</Button>
        <button type="button" onClick={onLogout} className="mx-auto flex items-center gap-1.5 text-sm text-muted hover:text-text"><Icon name="sair" size={16} />Sair</button>
      </form>
    </Shell>
  )
}
