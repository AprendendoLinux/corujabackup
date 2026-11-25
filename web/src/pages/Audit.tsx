import { useEffect, useState } from 'react'
import { AuditEntry, api, ApiError, fmtDate } from '../api'
import { Card, ErrorBox } from '../ui'

const LABEL: Record<string, string> = {
  login: 'Login', 'login.failed': 'Login falhou', 'login.2fa_failed': '2FA falhou', setup: 'Configuração inicial',
  'password.changed': 'Senha alterada', '2fa.enabled': '2FA ativado', '2fa.disabled': '2FA desativado', '2fa.recovery_used': 'Código de recuperação usado',
  'user.created': 'Usuário criado', 'user.updated': 'Usuário alterado', 'user.deleted': 'Usuário excluído',
  'agent.token_created': 'Token de agente criado', 'agent.enrolled': 'Agente matriculado', 'agent.enroll_denied': 'Matrícula negada',
  'agent.renamed': 'Agente renomeado', 'cloud.created': 'Conta de nuvem criada', 'cloud.connected': 'Conta de nuvem conectada', 'cloud.deleted': 'Conta de nuvem excluída',
  'job.created': 'Job criado', 'job.updated': 'Job alterado', 'job.deleted': 'Job excluído', 'job.run_now': 'Backup executado manualmente', 'run.cancel': 'Backup cancelado', 'agent.revoked': 'Agente revogado', 'agent.deleted': 'Agente excluído',
}

export default function Audit() {
  const [rows, setRows] = useState<AuditEntry[]>([])
  const [err, setErr] = useState('')
  useEffect(() => { api<{ entries: AuditEntry[] }>('GET', '/api/audit?limit=200').then((r) => setRows(r.entries)).catch((x) => setErr((x as ApiError).message)) }, [])
  return (
    <div className="space-y-6">
      <h1 className="text-2xl font-semibold">Auditoria</h1>
      <ErrorBox message={err} />
      <Card className="overflow-x-auto p-0">
        <table className="w-full text-sm">
          <thead className="border-b border-border text-left text-muted"><tr><th className="p-3">Quando</th><th className="p-3">Quem</th><th className="p-3">Ação</th><th className="p-3">Detalhe</th><th className="p-3">IP</th></tr></thead>
          <tbody>
            {rows.map((r) => (
              <tr key={r.id} className="border-b border-border last:border-0">
                <td className="p-3 whitespace-nowrap text-muted">{fmtDate(r.at)}</td><td className="p-3">{r.username || '—'}</td>
                <td className="p-3">{LABEL[r.action] ?? r.action}</td><td className="p-3 text-muted">{r.detail}</td><td className="p-3 text-muted">{r.ip}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </Card>
    </div>
  )
}
