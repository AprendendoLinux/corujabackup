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

export type Run = {
  id: number; uid: string; job_id: number; job_name: string; agent_id: number; agent_name: string; trigger: string
  scheduled_for: string | null; status: string; started_at: string | null; finished_at: string | null
  bytes_source: number; bytes_output: number; files: number; sha256: string; output_name: string; error: string; created_at: string
}
export type RunLog = { seq: number; at: string; level: string; text: string }
export type Job = {
  id: number; name: string; agent_id: number; kind: string; enabled: boolean; cron: string; timezone: string
  paths: string[]; excludes: string[]; dest_subdir: string; keep: number; version: number
  agent_name: string; agent_online: boolean; next_run_at: string | null; last_run: Run | null
}

export const CRON_PRESETS: { label: string; cron: string }[] = [
  { label: 'Todo dia às 00:10', cron: '10 0 * * *' },
  { label: 'Todo dia às 02:00', cron: '0 2 * * *' },
  { label: 'A cada 6 horas', cron: '0 */6 * * *' },
  { label: 'A cada hora', cron: '0 * * * *' },
  { label: 'Domingo às 03:00', cron: '0 3 * * 0' },
  { label: 'Dia 1 de cada mês às 03:00', cron: '0 3 1 * *' },
]
export const describeCron = (c: string) => CRON_PRESETS.find((p) => p.cron === c)?.label ?? `Personalizado (${c})`

export function fmtBytes(n: number) {
  if (!n) return '—'
  const u = ['B', 'KiB', 'MiB', 'GiB', 'TiB']
  let i = 0, v = n
  while (v >= 1024 && i < u.length - 1) { v /= 1024; i++ }
  return `${v.toFixed(i ? 1 : 0)} ${u[i]}`
}

export function fmtDuration(a: string | null, b: string | null) {
  if (!a || !b) return '—'
  const s = Math.max(0, Math.round((new Date(b).getTime() - new Date(a).getTime()) / 1000))
  if (s < 60) return `${s}s`
  const m = Math.floor(s / 60)
  return m < 60 ? `${m}min ${s % 60}s` : `${Math.floor(m / 60)}h ${m % 60}min`
}

export const STATUS: Record<string, { label: string; tone: 'ok' | 'off' | 'warn' | 'danger' }> = {
  ok: { label: 'Sucesso', tone: 'ok' }, warning: { label: 'Com avisos', tone: 'warn' }, failed: { label: 'Falhou', tone: 'danger' },
  canceled: { label: 'Cancelado', tone: 'off' }, missed: { label: 'Perdido', tone: 'danger' }, running: { label: 'Executando', tone: 'warn' }, queued: { label: 'Na fila', tone: 'off' },
}
