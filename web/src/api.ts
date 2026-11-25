export class ApiError extends Error {
  status: number
  constructor(status: number, message: string) { super(message); this.status = status }
}

export async function api<T = any>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await fetch(path, {
    method,
    headers: { 'Content-Type': 'application/json', 'X-Coruja-CSRF': '1' },
    body: body === undefined ? undefined : JSON.stringify(body),
    credentials: 'same-origin',
  })
  let data: any = null
  try { data = await res.json() } catch { /* corpo vazio */ }
  if (!res.ok) throw new ApiError(res.status, data?.error ?? `Erro ${res.status}`)
  return data as T
}

export type User = {
  id: number; username: string; display_name: string; role: 'admin' | 'operator' | 'viewer'
  totp_enabled: boolean; disabled: boolean; created_at: string; last_login_at: string | null
}
export type Agent = {
  id: number; name: string; hostname: string; os: string; arch: string; agent_version: string
  docker_available: boolean; ip: string; created_at: string; last_seen_at: string | null
  revoked_at: string | null; online: boolean
}
export type PendingToken = { id: number; name: string; created_at: string; expires_at: string }
export type AuditEntry = { id: number; at: string; username: string; action: string; detail: string; ip: string }

export const ROLE_LABEL = { admin: 'Administrador', operator: 'Operador', viewer: 'Somente leitura' } as const

export function fmtDate(s: string | null | undefined) {
  if (!s) return '—'
  return new Date(s).toLocaleString('pt-BR')
}
