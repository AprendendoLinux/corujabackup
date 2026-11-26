import { useCallback, useEffect, useState } from 'react'
import { api, ApiError } from '../api'
import { Badge, Button, Card, CopyBox, ErrorBox, Field } from '../ui'

export default function Security() {
  const [policy, setPolicy] = useState({ require_2fa: 'none', session_hours: 12, metrics_enabled: false })
  const [token, setToken] = useState('')
  const [msg, setMsg] = useState('')
  const [err, setErr] = useState('')
  const load = useCallback(() => { api<typeof policy>('GET', '/api/security').then(setPolicy).catch((x) => setErr((x as ApiError).message)) }, [])
  useEffect(() => { load() }, [load])
  async function save() {
    setErr(''); setMsg('')
    try { await api('PUT', '/api/security', { require_2fa: policy.require_2fa, session_hours: Number(policy.session_hours) }); setMsg('Política salva.'); load() } catch (x) { setErr((x as ApiError).message) }
  }
  async function genToken() {
    setErr('')
    try { const r = await api<{ token: string }>('POST', '/api/security/metrics-token'); setToken(r.token); load() } catch (x) { setErr((x as ApiError).message) }
  }
  const host = window.location.host
  const sel = 'w-full rounded-lg border border-border bg-surface px-3 py-2'
  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold">Segurança</h1>
        <p className="text-sm text-muted">Política de acesso e integração com monitoramento.</p>
      </div>
      <ErrorBox message={err} />
      {msg && <div className="rounded-lg border border-ok/40 bg-ok/10 px-3 py-2 text-sm text-ok">{msg}</div>}
      <Card>
        <h2 className="mb-3 font-semibold">Acesso</h2>
        <div className="grid max-w-2xl gap-4 sm:grid-cols-2">
          <Field label="Exigir verificação em duas etapas (2FA)" hint="Quem ainda não ativou só consegue abrir “Minha conta” até ativar. Você precisa ter o seu 2FA ativo antes.">
            <select className={sel} value={policy.require_2fa} onChange={(e) => setPolicy({ ...policy, require_2fa: e.target.value })}>
              <option value="none">Ninguém (opcional)</option><option value="admins">Administradores</option><option value="all">Todos os usuários</option>
            </select>
          </Field>
          <Field label="Duração da sessão (horas)" hint="Renovada a cada uso. De 1 a 168 (7 dias).">
            <input type="number" min={1} max={168} className={sel} value={policy.session_hours} onChange={(e) => setPolicy({ ...policy, session_hours: Number(e.target.value) })} />
          </Field>
        </div>
        <Button className="mt-4" onClick={save}>Salvar política</Button>
      </Card>
      <Card>
        <div className="mb-2 flex items-center gap-2"><h2 className="font-semibold">Métricas para o Prometheus</h2>{policy.metrics_enabled ? <Badge tone="ok">Ativas</Badge> : <Badge tone="off">Desligadas</Badge>}</div>
        <p className="mb-3 text-sm text-muted">Expõe o estado dos agentes, dos jobs, das nuvens e das execuções em <code>/metrics</code>, protegido por um token. Sem token, o endereço nem existe.</p>
        <div className="flex gap-2"><Button variant="ghost" onClick={genToken}>{policy.metrics_enabled ? 'Gerar novo token (invalida o atual)' : 'Gerar token'}</Button>
          {policy.metrics_enabled && <Button variant="danger" onClick={() => confirm('Desligar as métricas?') && api('DELETE', '/api/security/metrics-token').then(() => { setToken(''); load() })}>Desligar</Button>}</div>
        {token && (
          <div className="mt-4 space-y-2 text-sm">
            <div className="rounded-lg border border-accent/50 bg-accent/10 p-3"><b>Guarde o token agora:</b> ele não será mostrado de novo.</div>
            <CopyBox text={token} />
            <div className="font-medium">prometheus.yml</div>
            <CopyBox text={`scrape_configs:\n  - job_name: coruja-backup\n    metrics_path: /metrics\n    authorization:\n      credentials: ${token}\n    static_configs:\n      - targets: ["${host}"]`} />
          </div>
        )}
      </Card>
    </div>
  )
}
