import { FormEvent, useEffect, useState } from 'react'
import QRCode from 'qrcode'
import { User, api, ApiError, flags, fmtDate } from '../api'
import { Badge, Button, Card, CopyBox, ErrorBox, Field, Input } from '../ui'

export default function Account({ me, recoveryLeft, reload }: { me: User; recoveryLeft: number; reload: () => void }) {
  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold">Minha conta</h1>
        <p className="text-sm text-muted">{me.display_name || me.username} · {me.username}</p>
      </div>
      {flags.needs2FA && !me.totp_enabled && <div className="rounded-lg border border-accent/60 bg-accent/10 px-3 py-2 text-sm"><b>A política de segurança exige a verificação em duas etapas.</b> Ative o 2FA abaixo para continuar usando o sistema.</div>}
      <Password />
      <TwoFA me={me} recoveryLeft={recoveryLeft} reload={() => { flags.needs2FA = false; reload() }} />
      <Sessions />
    </div>
  )
}

function Password() {
  const [f, setF] = useState({ current: '', next: '', confirm: '' })
  const [msg, setMsg] = useState(''); const [err, setErr] = useState('')
  async function submit(e: FormEvent) {
    e.preventDefault(); setErr(''); setMsg('')
    if (f.next !== f.confirm) return setErr('As senhas não conferem.')
    try { await api('POST', '/api/me/password', { current: f.current, new: f.next }); setMsg('Senha alterada. As outras sessões foram encerradas.'); setF({ current: '', next: '', confirm: '' }) }
    catch (x) { setErr((x as ApiError).message) }
  }
  return (
    <Card>
      <h2 className="mb-3 font-semibold">Alterar senha</h2>
      <form onSubmit={submit} className="grid max-w-md gap-3">
        <Field label="Senha atual"><Input type="password" autoComplete="current-password" value={f.current} onChange={(e) => setF({ ...f, current: e.target.value })} required /></Field>
        <Field label="Nova senha" hint="Mínimo de 10 caracteres"><Input type="password" autoComplete="new-password" value={f.next} onChange={(e) => setF({ ...f, next: e.target.value })} required /></Field>
        <Field label="Confirme a nova senha"><Input type="password" autoComplete="new-password" value={f.confirm} onChange={(e) => setF({ ...f, confirm: e.target.value })} required /></Field>
        <ErrorBox message={err} />
        {msg && <div className="rounded-lg border border-ok/40 bg-ok/10 px-3 py-2 text-sm text-ok">{msg}</div>}
        <Button className="w-fit">Alterar senha</Button>
      </form>
    </Card>
  )
}

function TwoFA({ me, recoveryLeft, reload }: { me: User; recoveryLeft: number; reload: () => void }) {
  const [setup, setSetup] = useState<{ secret: string; uri: string } | null>(null)
  const [qr, setQr] = useState('')
  const [code, setCode] = useState('')
  const [codes, setCodes] = useState<string[] | null>(null)
  const [pw, setPw] = useState('')
  const [err, setErr] = useState('')
  const [off, setOff] = useState(false)
  useEffect(() => { if (setup) QRCode.toDataURL(setup.uri, { margin: 1, width: 192 }).then(setQr) }, [setup])

  async function begin() { setErr(''); try { setSetup(await api('POST', '/api/me/2fa/begin')) } catch (x) { setErr((x as ApiError).message) } }
  async function enable(e: FormEvent) {
    e.preventDefault(); setErr('')
    try { const r = await api<{ recovery_codes: string[] }>('POST', '/api/me/2fa/enable', { code }); setCodes(r.recovery_codes); setSetup(null); setCode(''); reload() }
    catch (x) { setErr((x as ApiError).message) }
  }
  async function disable(e: FormEvent) {
    e.preventDefault(); setErr('')
    try { await api('POST', '/api/me/2fa/disable', { password: pw, code }); setOff(false); setPw(''); setCode(''); reload() } catch (x) { setErr((x as ApiError).message) }
  }

  return (
    <Card>
      <div className="mb-2 flex items-center gap-2"><h2 className="font-semibold">Verificação em duas etapas (2FA)</h2>{me.totp_enabled ? <Badge tone="ok">Ativa</Badge> : <Badge tone="off">Desligada</Badge>}</div>
      <p className="mb-3 text-sm text-muted">Use um aplicativo autenticador (Google Authenticator, Authy, 1Password…). Recomendado para administradores.</p>
      <ErrorBox message={err} />

      {codes && (
        <div className="my-3 space-y-2 rounded-lg border border-accent/50 bg-accent/10 p-3 text-sm">
          <b>Códigos de recuperação</b> — guarde em local seguro. Cada um funciona uma vez e eles não serão mostrados de novo.
          <CopyBox text={codes.join('\n')} />
        </div>
      )}

      {!me.totp_enabled && !setup && <Button onClick={begin}>Ativar 2FA</Button>}
      {setup && (
        <form onSubmit={enable} className="grid max-w-md gap-3 text-sm">
          <p>1. Escaneie o QR code no aplicativo (ou digite a chave manualmente).</p>
          {qr && <img src={qr} alt="QR code do 2FA" width={192} height={192} className="rounded-lg border border-border bg-white p-1" />}
          <code className="break-all rounded bg-bg px-2 py-1 text-xs">{setup.secret}</code>
          <Field label="2. Digite o código de 6 dígitos"><Input inputMode="numeric" autoComplete="one-time-code" maxLength={6} value={code} onChange={(e) => setCode(e.target.value)} required /></Field>
          <div className="flex gap-2"><Button>Confirmar e ativar</Button><Button type="button" variant="ghost" onClick={() => setSetup(null)}>Cancelar</Button></div>
        </form>
      )}
      {me.totp_enabled && !off && (
        <div className="flex items-center gap-4 text-sm">
          <span className="text-muted">{recoveryLeft} código(s) de recuperação restante(s)</span>
          <Button variant="ghost" onClick={() => setOff(true)}>Desativar 2FA</Button>
        </div>
      )}
      {off && (
        <form onSubmit={disable} className="grid max-w-md gap-3">
          <Field label="Senha"><Input type="password" value={pw} onChange={(e) => setPw(e.target.value)} required /></Field>
          <Field label="Código do aplicativo (ou de recuperação)"><Input value={code} onChange={(e) => setCode(e.target.value)} required /></Field>
          <div className="flex gap-2"><Button variant="danger">Desativar</Button><Button type="button" variant="ghost" onClick={() => setOff(false)}>Cancelar</Button></div>
        </form>
      )}
    </Card>
  )
}

type Sess = { id: string; created_at: string; last_seen_at: string; ip: string; user_agent: string; current: boolean }

function Sessions() {
  const [list, setList] = useState<Sess[]>([])
  const [err, setErr] = useState('')
  const load = () => api<{ sessions: Sess[] }>('GET', '/api/me/sessions').then((r) => setList(r.sessions)).catch((x) => setErr((x as ApiError).message))
  useEffect(() => { load() }, [])
  const act = (fn: () => Promise<unknown>) => fn().then(() => { setErr(''); load() }).catch((x) => setErr((x as ApiError).message))
  return (
    <Card>
      <div className="mb-2 flex items-center justify-between"><h2 className="font-semibold">Sessões ativas</h2>
        {list.length > 1 && <Button variant="ghost" onClick={() => act(() => api('POST', '/api/me/sessions/revoke-others'))}>Encerrar as outras</Button>}</div>
      <ErrorBox message={err} />
      <ul className="divide-y divide-border text-sm">
        {list.map((s) => (
          <li key={s.id} className="flex flex-wrap items-center justify-between gap-2 py-2">
            <span><b>{s.ip}</b> {s.current && <Badge tone="ok">esta sessão</Badge>}<div className="max-w-md truncate text-xs text-muted">{s.user_agent} · último uso {fmtDate(s.last_seen_at)}</div></span>
            {!s.current && <button className="text-danger hover:underline" onClick={() => act(() => api('DELETE', `/api/me/sessions/${s.id}`))}>Encerrar</button>}
          </li>
        ))}
      </ul>
    </Card>
  )
}
