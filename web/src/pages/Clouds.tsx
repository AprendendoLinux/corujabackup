import { FormEvent, useCallback, useEffect, useRef, useState } from 'react'
import { CLOUD_LABEL, CloudAccount, DeviceInfo, User, api, ApiError, fmtBytes, fmtDate } from '../api'
import { Actions, Badge, Button, Card, CopyBox, ErrorBox, Field, IconButton, Input, Modal } from '../ui'
import { confirmar } from '../Dialogs'
import { CLOUD_ICON } from '../icons'
import NetDestination, { AgentSelect, NET_TYPES, useAgents } from './NetDestination'

// Catálogo de destinos. ok = disponível; soon = em construção (colorido); off = previsto (cinza).
type Prov = { id: string; label: string; hint: string; state: 'ok' | 'soon' | 'off' }
const PROVIDERS: { group: string; items: Prov[] }[] = [
  { group: 'Nuvem', items: [
    { id: 'gdrive', label: 'Google Drive', hint: 'Entre com a sua conta Google', state: 'ok' },
    { id: 'onedrive', label: 'OneDrive', hint: 'Microsoft pessoal ou corporativo', state: 'ok' },
    { id: 'dropbox', label: 'Dropbox', hint: 'Entre com a sua conta Dropbox', state: 'ok' },
    { id: 's3', label: 'Amazon S3', hint: 'AWS, MinIO, Wasabi…', state: 'off' },
    { id: 'b2', label: 'Backblaze B2', hint: 'Armazenamento econômico', state: 'off' },
    { id: 'box', label: 'Box', hint: 'Armazenamento corporativo', state: 'off' },
    { id: 'mega', label: 'MEGA', hint: 'Armazenamento cifrado', state: 'off' },
    { id: 'pcloud', label: 'pCloud', hint: 'Armazenamento europeu', state: 'off' },
  ] },
  { group: 'Servidores e rede', items: [
    { id: 'sftp', label: 'SFTP', hint: 'Servidor por SSH', state: 'ok' },
    { id: 'ftp', label: 'FTP', hint: 'FTPS ou FTP simples', state: 'ok' },
    { id: 'smb', label: 'Pasta de rede', hint: 'SMB / CIFS (Windows, NAS)', state: 'ok' },
    { id: 'local', label: 'Pasta local', hint: 'NFS, iSCSI ou disco montado', state: 'ok' },
  ] },
]
type Defs = { gdrive?: boolean; gdrive_web?: boolean; onedrive_web?: boolean; dropbox_web?: boolean; broker?: string }
const PROV_BY_ID = Object.fromEntries(PROVIDERS.flatMap((g) => g.items).map((p) => [p.id, p]))

function ProviderIcon({ id, size = 40 }: { id: string; size?: number }) {
  const I = CLOUD_ICON[id]
  return I ? I({ size }) : null
}

function ProviderGrid({ onPick }: { onPick: (id: string) => void }) {
  return (
    <div className="space-y-4">
      <p className="text-sm text-muted">Escolha onde guardar os backups.</p>
      {PROVIDERS.map((g) => (
        <section key={g.group} aria-label={g.group}>
          <h3 className="mb-2 text-xs font-semibold uppercase tracking-wide text-muted">{g.group}</h3>
          <div className="grid grid-cols-2 gap-2.5 sm:grid-cols-3 md:grid-cols-4">
            {g.items.map((p) => {
              const on = p.state === 'ok'
              return (
                <button key={p.id} type="button" disabled={!on} aria-disabled={!on} onClick={() => on && onPick(p.id)}
                  title={on ? p.label : 'Em breve'}
                  className={`group relative flex flex-col items-center gap-1.5 rounded-xl border px-2.5 pb-3 pt-4 text-center transition ${on ? 'border-border bg-surface hover:-translate-y-0.5 hover:border-primary hover:shadow-md focus-visible:outline focus-visible:outline-2 focus-visible:outline-primary' : 'cursor-not-allowed border-border/70 bg-bg'}`}>
                  <span className={on || p.state === 'soon' ? '' : 'opacity-60 grayscale'}><ProviderIcon id={p.id} size={40} /></span>
                  <span className={`text-sm font-semibold ${on ? '' : 'text-muted'}`}>{p.label}</span>
                  <span className="text-xs leading-snug text-muted">{p.hint}</span>
                  {!on && <span className="absolute right-2 top-2 rounded-full bg-border/60 px-2 py-0.5 text-[10px] font-medium uppercase tracking-wide text-muted">Em breve</span>}
                </button>
              )
            })}
          </div>
        </section>
      ))}
    </div>
  )
}

// Teste de um destino de rede já salvo: quem testa é um agente.
function TestDestination({ account }: { account: CloudAccount }) {
  const agents = useAgents()
  const [agent, setAgent] = useState(0)
  const [msg, setMsg] = useState('')
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState(false)
  async function run() {
    setMsg(''); setErr(''); setBusy(true)
    try { const r = await api<{ detail: string }>('POST', '/api/destinations/test', { agent_id: agent, id: account.id }); setMsg(`Funcionando: ${r.detail}.`) }
    catch (x) { setErr((x as ApiError).message) } finally { setBusy(false) }
  }
  return (
    <div className="space-y-3 text-sm">
      <p className="text-muted">Escolha o agente que vai acessar o storage ({account.account_label}).</p>
      <AgentSelect agents={agents} value={agent} onChange={setAgent} />
      {msg && <div className="rounded-lg border border-ok/40 bg-ok/10 px-3 py-2 text-ok">{msg}</div>}
      <ErrorBox message={err} />
      <Button onClick={run} disabled={busy || !agent}>{busy ? 'Testando…' : 'Testar'}</Button>
    </div>
  )
}

// Login por popup: abre a janela já no clique (senão o navegador bloqueia), pede o endereço ao Server, espera o código que
// a página de retorno entrega por postMessage (só aceita a origem do intermediário) e conclui no Server.
async function popupLogin(body: Record<string, unknown>, broker: string) {
  const pop = window.open('', 'coruja-oauth', 'width=520,height=700')
  if (!pop) throw new Error('O navegador bloqueou a janela. Libere pop-ups para este endereço e tente de novo.')
  try {
    const r = await api<{ url: string; state: string }>('POST', '/api/clouds/oauth/begin', { ...body, origin: window.location.origin })
    pop.location.href = r.url
    await new Promise<void>((resolve, reject) => {
      let settled = false
      let received = false // o código já chegou: a janela pode fechar, só falta o Server concluir
      const end = (fn: () => void) => { if (settled) return; settled = true; window.removeEventListener('message', onMsg); clearInterval(timer); fn() }
      const onMsg = (ev: MessageEvent) => {
        if (ev.origin !== broker || ev.data?.type !== 'coruja-oauth' || ev.data.state !== r.state) return
        if (received) return
        received = true
        clearInterval(timer)
        if (ev.data.error) return end(() => reject(new Error(ev.data.error === 'access_denied' ? 'Você não autorizou o acesso.' : `O provedor recusou: ${ev.data.error}`)))
        api('POST', '/api/clouds/oauth/finish', { state: r.state, code: ev.data.code }).then(() => end(resolve), (x) => end(() => reject(x)))
      }
      window.addEventListener('message', onMsg)
      const timer = window.setInterval(() => { if (pop.closed) setTimeout(() => { if (!received) end(() => reject(new Error('A janela foi fechada antes de terminar.'))) }, 1500) }, 500)
    })
  } finally { try { pop.close() } catch { /* já fechou */ } }
}

export default function Clouds({ me }: { me: User }) {
  const [accounts, setAccounts] = useState<CloudAccount[]>([])
  const [err, setErr] = useState('')
  const [info, setInfo] = useState('')
  const [defs, setDefs] = useState<Defs>({})
  const [wizard, setWizard] = useState<{ id?: number; device?: DeviceInfo; name?: string; type?: string } | null>(null)
  const [testing, setTesting] = useState<CloudAccount | null>(null)
  const isAdmin = me.role === 'admin'

  const load = useCallback(() => {
    api<{ accounts: CloudAccount[]; defaults?: Defs }>('GET', '/api/clouds').then((r) => { setAccounts(r.accounts); setDefs(r.defaults ?? {}); setErr('') }).catch((x) => setErr((x as ApiError).message))
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
    if (a.popup) {
      try { await popupLogin({ account_id: a.id, type: a.type }, defs.broker ?? ''); setErr(''); setInfo(`"${a.name}" reautorizada.`); load() } catch (x) { setInfo(''); setErr((x as Error).message) }
      return
    }
    try { const r = await api<{ device: DeviceInfo }>('POST', `/api/clouds/${a.id}/reauthorize`); setWizard({ id: a.id, device: r.device, name: a.name }) } catch (x) { setErr((x as ApiError).message) }
  }

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between gap-3">
        <div>
          <h1 className="text-2xl font-semibold">Storages</h1>
          <p className="text-sm text-muted">Onde os backups são guardados. Nas nuvens, as credenciais ficam só no Server; nos storages de rede, quem acessa é o agente.</p>
        </div>
        {isAdmin && <Button onClick={() => setWizard({})}>+ Novo storage</Button>}
      </div>
      <ErrorBox message={err} />
      {info && <div className="rounded-lg border border-ok/40 bg-ok/10 px-3 py-2 text-sm text-ok">{info}</div>}

      <div className="grid gap-4 md:grid-cols-2">
        {accounts.length === 0 && <Card className="md:col-span-2"><p className="text-center text-muted">Nenhum storage ainda. Clique em “Novo storage”.</p></Card>}
        {accounts.map((a) => {
          const pct = a.quota_total ? Math.min(100, Math.round((a.quota_used / a.quota_total) * 100)) : 0
          return (
            <Card key={a.id}>
              <div className="flex items-start justify-between gap-2">
                <div className="flex min-w-0 items-center gap-3">
                  <ProviderIcon id={a.type} size={36} />
                  <div className="min-w-0">
                    <div className="truncate font-semibold">{a.name}</div>
                    <div className="truncate text-sm text-muted">{CLOUD_LABEL[a.type]}{a.account_label && ` · ${a.account_label}`}</div>
                  </div>
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
              <div className="mt-3 flex flex-wrap items-center justify-between gap-2 text-sm">
                <span className="text-muted">{a.jobs} job(s) usando</span>
                <Actions>
                  {me.role !== 'viewer' && a.status !== 'pending' && !NET_TYPES.includes(a.type) && <IconButton icon="testar" label="Testar conexão" onClick={() => test(a)} />}
                  {isAdmin && NET_TYPES.includes(a.type) && <IconButton icon="testar" label="Testar conexão" onClick={() => setTesting(a)} />}
                  {isAdmin && !NET_TYPES.includes(a.type) && <IconButton icon="reautorizar" label="Reautorizar" onClick={() => reauth(a)} />}
                  {isAdmin && <IconButton icon="excluir" tone="danger" label="Excluir" onClick={async () => await confirmar({ title: `Excluir o storage "${a.name}"?`, message: 'Os arquivos já enviados não são apagados.', danger: true }) && act(() => api('DELETE', `/api/clouds/${a.id}`))} />}
                </Actions>
              </div>
            </Card>
          )
        })}
      </div>

      {testing && <Modal title={`Testar "${testing.name}"`} onClose={() => setTesting(null)}><TestDestination account={testing} /></Modal>}
      {wizard && <Modal wide={!wizard.type && !wizard.device} title={wizard.device ? 'Autorizar o acesso' : wizard.type ? `Conectar ${PROV_BY_ID[wizard.type]?.label ?? 'storage'}` : 'Novo storage'} onClose={() => { setWizard(null); load() }}>
        {wizard.device && wizard.id
          ? <Authorize id={wizard.id} name={wizard.name ?? ''} device={wizard.device} onDone={() => { setWizard(null); setInfo('Conta conectada com sucesso.'); load() }} />
          : !wizard.type ? <ProviderGrid onPick={(type) => setWizard({ type })} />
          : NET_TYPES.includes(wizard.type) ? (
            <div className="space-y-4">
              <button type="button" className="text-sm text-primary hover:underline" onClick={() => setWizard({})}>← Trocar o tipo de storage</button>
              <NetDestination type={wizard.type} onSaved={() => { setWizard(null); setInfo('Storage salvo.'); load() }} />
            </div>
          )
          : <NewAccount type={wizard.type} onBack={() => setWizard({})} defs={defs} onCreated={(id, device, name) => { setWizard({ id, device, name }); load() }} onConnected={() => { setWizard(null); setInfo('Conta conectada com sucesso.'); load() }} />}
      </Modal>}
    </div>
  )
}

function NewAccount({ type, onBack, onCreated, onConnected, defs }: { type: string; onBack: () => void; onCreated: (id: number, device: DeviceInfo, name: string) => void; onConnected: () => void; defs: Defs }) {
  const [f, setF] = useState({ type, name: '', client_id: '', client_secret: '', tenant: '' })
  const [mode, setMode] = useState<'auto' | 'code' | 'own'>('auto')
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState(false)
  const gd = f.type === 'gdrive'
  const od0 = f.type === 'onedrive'
  const dbx = f.type === 'dropbox' // só existe pelo popup
  const web = dbx || (((gd && !!defs.gdrive_web) || (od0 && !!defs.onedrive_web)) && mode === 'auto') // botão único: popup do provedor
  const who = dbx ? 'o Dropbox' : od0 ? 'a Microsoft' : 'o Google' // com artigo: "com a Microsoft", "com o Google"
  const std = gd && !web && !!defs.gdrive && mode !== 'own' // aplicativo padrão com código de dispositivo
  const noCreds = web || std // nada de ID/segredo para preencher
  async function viaPopup() {
    await popupLogin({ name: f.name, type: f.type }, defs.broker ?? '')
    onConnected()
  }
  async function submit(e: FormEvent) {
    e.preventDefault(); setErr(''); setBusy(true)
    try {
      if (web) { await viaPopup(); return }
      const r = await api<{ id: number; device: DeviceInfo }>('POST', '/api/clouds', std ? { ...f, client_id: '', client_secret: '' } : f)
      onCreated(r.id, r.device, f.name)
    } catch (x) { setErr((x as Error).message) } finally { setBusy(false) }
  }
  const sel = 'w-full rounded-lg border border-border bg-surface px-3 py-2'
  const od = f.type === 'onedrive'
  return (
    <form onSubmit={submit} className="space-y-4 text-sm">
      <div className="flex items-center gap-3 rounded-xl border border-border bg-bg px-4 py-3">
        <ProviderIcon id={type} size={40} />
        <div className="min-w-0 flex-1"><div className="font-semibold">{PROV_BY_ID[type]?.label}</div><div className="text-xs text-muted">{PROV_BY_ID[type]?.hint}</div></div>
        <button type="button" className="text-sm text-primary hover:underline" onClick={onBack}>← Trocar</button>
      </div>
      <Field label="Nome da conta" hint="Como aparece nos jobs. Ex.: Backups da empresa"><Input autoFocus value={f.name} onChange={(e) => setF({ ...f, name: e.target.value })} required /></Field>
      {web && (
        <div className="rounded-lg border border-border bg-bg p-3 text-muted">
          Uma janela {dbx ? 'do Dropbox' : od0 ? 'da Microsoft' : 'do Google'} vai abrir para você entrar e autorizar.{od0 ? '' : dbx ? ' O Coruja só enxerga a própria pasta dentro do seu Dropbox (Aplicativos › Coruja Backup).' : ' O Coruja só enxerga os arquivos que ele mesmo criar.'}{' '}
          {gd && defs.gdrive && <><button type="button" className="text-primary hover:underline" onClick={() => setMode('code')}>Prefiro usar um código</button>{' · '}</>}
          {!dbx && <button type="button" className="text-primary hover:underline" onClick={() => setMode('own')}>Usar o meu próprio aplicativo (avançado)</button>}
        </div>
      )}
      {std && (
        <div className="rounded-lg border border-border bg-bg p-3 text-muted">
          Vamos mostrar um <b>código</b> e um link do Google. Abra o link, digite o código e autorize a sua conta: não é preciso registrar aplicativo nem colar credenciais. O Coruja só enxerga os arquivos que ele mesmo criar.{' '}
          <button type="button" className="text-primary hover:underline" onClick={() => setMode('own')}>Usar o meu próprio aplicativo (avançado)</button>
        </div>
      )}
      {((gd && (defs.gdrive || defs.gdrive_web)) || (od0 && defs.onedrive_web)) && mode !== 'auto' && <button type="button" className="text-sm text-primary hover:underline" onClick={() => setMode('auto')}>← Voltar ao login com {who}</button>}
      {!noCreds && <Field label="Client ID (ID do aplicativo)"><Input className="font-mono" value={f.client_id} onChange={(e) => setF({ ...f, client_id: e.target.value })} required /></Field>}
      {noCreds ? null : od ? (
        <div className="grid gap-3 sm:grid-cols-2">
          <Field label="Locatário (tenant)" hint="common = contas pessoais e corporativas. Ou o ID/domínio do seu locatário."><Input placeholder="common" value={f.tenant} onChange={(e) => setF({ ...f, tenant: e.target.value })} /></Field>
          <Field label="Client Secret (opcional)" hint="Só se o aplicativo for “confidencial”."><Input type="password" autoComplete="off" value={f.client_secret} onChange={(e) => setF({ ...f, client_secret: e.target.value })} /></Field>
        </div>
      ) : (
        <Field label="Client Secret"><Input type="password" autoComplete="off" value={f.client_secret} onChange={(e) => setF({ ...f, client_secret: e.target.value })} required /></Field>
      )}
      {!noCreds && <details className="rounded-lg border border-border bg-bg p-3">
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
      <Button disabled={busy}>{web ? (busy ? `Aguardando ${who}…` : `Entrar com ${who}`) : 'Continuar'}</Button>
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
