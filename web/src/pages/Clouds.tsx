import { FormEvent, useCallback, useEffect, useRef, useState } from 'react'
import { CLOUD_LABEL, CloudAccount, DeviceInfo, User, api, ApiError, fmtBytes, fmtDate } from '../api'
import { Badge, Button, Card, CopyBox, ErrorBox, Field, Input, Modal } from '../ui'

export default function Clouds({ me }: { me: User }) {
  const [accounts, setAccounts] = useState<CloudAccount[]>([])
  const [err, setErr] = useState('')
  const [info, setInfo] = useState('')
  const [hasDefault, setHasDefault] = useState(false)
  const [wizard, setWizard] = useState<{ id?: number; device?: DeviceInfo; name?: string } | null>(null)
  const isAdmin = me.role === 'admin'

  const load = useCallback(() => {
    api<{ accounts: CloudAccount[]; defaults?: { gdrive?: boolean } }>('GET', '/api/clouds').then((r) => { setAccounts(r.accounts); setHasDefault(!!r.defaults?.gdrive); setErr('') }).catch((x) => setErr((x as ApiError).message))
  }, [])
  useEffect(() => { load() }, [load])

  async function act(fn: () => Promise<unknown>, ok?: string) {
    try { await fn(); setErr(''); if (ok) setInfo(ok); load() } catch (x) { setInfo(''); setErr((x as ApiError).message) }
  }
  async function test(a: CloudAccount) {
    try {
      const r = await api<{ ok: boolean; account?: string; error?: string }>('POST', `/api/clouds/${a.id}/test`)
      if (r.ok) { setErr(''); setInfo(`Conexão com "${a.name}" funcionando (${r.account}).`) } else { setInfo(''); setErr(r.error ?? 'Falha ao testar a conexão.') }
      load()
    } catch (x) { setErr((x as ApiError).message) }
  }
  async function reauth(a: CloudAccount) {
    try { const r = await api<{ device: DeviceInfo }>('POST', `/api/clouds/${a.id}/reauthorize`); setWizard({ id: a.id, device: r.device, name: a.name }) } catch (x) { setErr((x as ApiError).message) }
  }

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between gap-3">
        <div>
          <h1 className="text-2xl font-semibold">Nuvem</h1>
          <p className="text-sm text-muted">Contas onde os backups são guardados. As credenciais ficam só no Server (cifradas); o agente envia o arquivo direto para a nuvem.</p>
        </div>
        {isAdmin && <Button onClick={() => setWizard({})}>+ Conectar nuvem</Button>}
      </div>
      <ErrorBox message={err} />
      {info && <div className="rounded-lg border border-ok/40 bg-ok/10 px-3 py-2 text-sm text-ok">{info}</div>}

      <div className="grid gap-4 md:grid-cols-2">
        {accounts.length === 0 && <Card className="md:col-span-2"><p className="text-center text-muted">Nenhuma conta conectada ainda. Clique em “Conectar nuvem”.</p></Card>}
        {accounts.map((a) => {
          const pct = a.quota_total ? Math.min(100, Math.round((a.quota_used / a.quota_total) * 100)) : 0
          return (
            <Card key={a.id}>
              <div className="flex items-start justify-between gap-2">
                <div>
                  <div className="font-semibold">{a.name}</div>
                  <div className="text-sm text-muted">{CLOUD_LABEL[a.type]}{a.account_label && ` · ${a.account_label}`}</div>
                </div>
                {a.status === 'connected' ? <Badge tone="ok">Conectada</Badge> : a.status === 'error' ? <Badge tone="danger">Erro</Badge> : <Badge tone="warn">Aguardando autorização</Badge>}
              </div>
              {a.last_error && <p className="mt-2 text-sm text-danger">{a.last_error}</p>}
              {a.quota_total > 0 && (
                <div className="mt-3">
                  <div className="h-2 overflow-hidden rounded-full bg-border"><div className={`h-full ${pct > 90 ? 'bg-danger' : 'bg-primary'}`} style={{ width: `${pct}%` }} /></div>
                  <div className="mt-1 text-xs text-muted">{fmtBytes(a.quota_used)} de {fmtBytes(a.quota_total)} usados ({pct}%) · verificado {fmtDate(a.quota_checked_at)}</div>
                </div>
              )}
              <div className="mt-3 flex flex-wrap items-center gap-x-4 gap-y-1 text-sm">
                <span className="text-muted">{a.jobs} job(s) usando</span>
                {me.role !== 'viewer' && a.status !== 'pending' && <button className="text-primary hover:underline" onClick={() => test(a)}>Testar conexão</button>}
                {isAdmin && <button className="text-primary hover:underline" onClick={() => reauth(a)}>Reautorizar</button>}
                {isAdmin && <button className="text-danger hover:underline" onClick={() => confirm(`Excluir a conta "${a.name}"? Os arquivos já enviados à nuvem não são apagados.`) && act(() => api('DELETE', `/api/clouds/${a.id}`))}>Excluir</button>}
              </div>
            </Card>
          )
        })}
      </div>

      {wizard && <Modal title={wizard.device ? 'Autorizar o acesso' : 'Conectar nuvem'} onClose={() => { setWizard(null); load() }}>
        {wizard.device && wizard.id
          ? <Authorize id={wizard.id} name={wizard.name ?? ''} device={wizard.device} onDone={() => { setWizard(null); setInfo('Conta conectada com sucesso.'); load() }} />
          : <NewAccount hasDefault={hasDefault} onCreated={(id, device, name) => { setWizard({ id, device, name }); load() }} />}
      </Modal>}
    </div>
  )
}

function NewAccount({ onCreated, hasDefault }: { onCreated: (id: number, device: DeviceInfo, name: string) => void; hasDefault: boolean }) {
  const [f, setF] = useState({ type: 'onedrive', name: '', client_id: '', client_secret: '', tenant: '' })
  const [own, setOwn] = useState(false)
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState(false)
  async function submit(e: FormEvent) {
    e.preventDefault(); setErr(''); setBusy(true)
    try { const r = await api<{ id: number; device: DeviceInfo }>('POST', '/api/clouds', std ? { ...f, client_id: '', client_secret: '' } : f); onCreated(r.id, r.device, f.name) }
    catch (x) { setErr((x as ApiError).message) } finally { setBusy(false) }
  }
  const sel = 'w-full rounded-lg border border-border bg-surface px-3 py-2'
  const od = f.type === 'onedrive'
  const std = f.type === 'gdrive' && hasDefault && !own // usa o aplicativo padrão do Coruja: nada a preencher
  return (
    <form onSubmit={submit} className="space-y-4 text-sm">
      <div className="grid gap-3 sm:grid-cols-2">
        <Field label="Provedor"><select className={sel} value={f.type} onChange={(e) => setF({ ...f, type: e.target.value })}><option value="onedrive">OneDrive (Microsoft)</option><option value="gdrive">Google Drive</option></select></Field>
        <Field label="Nome da conta" hint="Como aparece nos jobs. Ex.: OneDrive da empresa"><Input autoFocus value={f.name} onChange={(e) => setF({ ...f, name: e.target.value })} required /></Field>
      </div>
      {std && (
        <div className="rounded-lg border border-border bg-bg p-3 text-muted">
          Vamos mostrar um <b>código</b> e um link do Google. Abra o link, digite o código e autorize a sua conta: não é preciso registrar aplicativo nem colar credenciais. O Coruja só enxerga os arquivos que ele mesmo criar.{' '}
          <button type="button" className="text-primary hover:underline" onClick={() => setOwn(true)}>Usar o meu próprio aplicativo (avançado)</button>
        </div>
      )}
      {f.type === 'gdrive' && hasDefault && own && <button type="button" className="text-sm text-primary hover:underline" onClick={() => setOwn(false)}>← Voltar ao aplicativo padrão</button>}
      {!std && <Field label="Client ID (ID do aplicativo)"><Input className="font-mono" value={f.client_id} onChange={(e) => setF({ ...f, client_id: e.target.value })} required /></Field>}
      {std ? null : od ? (
        <div className="grid gap-3 sm:grid-cols-2">
          <Field label="Locatário (tenant)" hint="common = contas pessoais e corporativas. Ou o ID/domínio do seu locatário."><Input placeholder="common" value={f.tenant} onChange={(e) => setF({ ...f, tenant: e.target.value })} /></Field>
          <Field label="Client Secret (opcional)" hint="Só se o aplicativo for “confidencial”."><Input type="password" autoComplete="off" value={f.client_secret} onChange={(e) => setF({ ...f, client_secret: e.target.value })} /></Field>
        </div>
      ) : (
        <Field label="Client Secret"><Input type="password" autoComplete="off" value={f.client_secret} onChange={(e) => setF({ ...f, client_secret: e.target.value })} required /></Field>
      )}
      {!std && <details className="rounded-lg border border-border bg-bg p-3">
        <summary className="cursor-pointer font-medium">Como obter o Client ID{od ? '' : ' e o Secret'}</summary>
        {od ? (
          <ol className="mt-2 list-decimal space-y-1 pl-5 text-muted">
            <li>No portal do Azure, abra <b>Microsoft Entra ID › Registros de aplicativo › Novo registro</b>.</li>
            <li>Tipos de conta: escolha o que combina com a sua conta (pessoal e/ou corporativa).</li>
            <li>Em <b>Autenticação</b>, ative <b>“Permitir fluxos de cliente público”</b> (necessário para o código de dispositivo).</li>
            <li>Em <b>Permissões de API</b> (Microsoft Graph, delegadas), adicione <b>Files.ReadWrite</b>, <b>User.Read</b> e <b>offline_access</b>.</li>
            <li>Copie o <b>ID do aplicativo (cliente)</b> para o campo acima.</li>
          </ol>
        ) : (
          <ol className="mt-2 list-decimal space-y-1 pl-5 text-muted">
            <li>No Google Cloud Console, crie um projeto e ative a <b>Google Drive API</b>.</li>
            <li>Em <b>Credenciais › Criar credenciais › ID do cliente OAuth</b>, escolha o tipo <b>“TVs e dispositivos de entrada limitada”</b>.</li>
            <li>Na tela de consentimento, adicione a sua conta como usuária de teste (ou publique o app).</li>
            <li>Copie o <b>ID do cliente</b> e a <b>chave secreta</b>. O acesso pedido é só <b>drive.file</b>: o Coruja Backup enxerga apenas os arquivos que ele mesmo cria.</li>
          </ol>
        )}
      </details>}
      <ErrorBox message={err} />
      <Button disabled={busy}>Continuar</Button>
    </form>
  )
}

function Authorize({ id, name, device, onDone }: { id: number; name: string; device: DeviceInfo; onDone: () => void }) {
  const [status, setStatus] = useState('pending')
  const [err, setErr] = useState('')
  const done = useRef(false)
  useEffect(() => {
    const t = setInterval(async () => {
      if (done.current) return
      try {
        const r = await api<{ status: string; error?: string }>('POST', `/api/clouds/${id}/poll`)
        if (r.error) setErr(r.error); else setErr('')
        setStatus(r.status)
        if (r.status === 'connected') { done.current = true; onDone() }
        if (r.status === 'expired' || r.status === 'denied') done.current = true
      } catch (x) { setErr((x as ApiError).message) }
    }, Math.max(3, device.interval) * 1000)
    return () => clearInterval(t)
  }, [id, device.interval, onDone])
  return (
    <div className="space-y-4 text-sm">
      {status === 'expired' && <ErrorBox message="O código expirou. Feche esta janela e use “Reautorizar” na conta para gerar outro." />}
      {status === 'denied' && <ErrorBox message="O acesso foi negado na página do provedor." />}
      <p>Para conectar <b>{name}</b>, abra a página abaixo <b>em qualquer navegador</b>, entre com a conta que vai guardar os backups e digite o código:</p>
      <Field label="1. Abra"><CopyBox text={device.verification_uri} /></Field>
      <Field label="2. Digite o código"><div className="rounded-lg border border-border bg-bg p-4 text-center font-mono text-2xl tracking-widest">{device.user_code}</div></Field>
      <p className="text-muted">Esta janela confere sozinha a cada poucos segundos e fecha quando a autorização terminar. O código vale até {fmtDate(device.expires_at)}.</p>
      <ErrorBox message={err} />
    </div>
  )
}
