import type { CustomTheme, ThemeMode } from './themes'
export class ApiError extends Error {
  status: number
  code?: string
  constructor(status: number, message: string, code?: string) { super(message); this.status = status; this.code = code }
}

// Marcado quando a política de segurança exige que o usuário ative o 2FA antes de usar o resto do sistema.
export const flags = { needs2FA: false }

export async function api<T = any>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await fetch(path, {
    method,
    headers: { 'Content-Type': 'application/json', 'X-Coruja-CSRF': '1' },
    body: body === undefined ? undefined : JSON.stringify(body),
    credentials: 'same-origin',
  })
  let data: any = null
  try { data = await res.json() } catch { /* corpo vazio */ }
  if (!res.ok) {
    if (data?.code === '2fa_required') { flags.needs2FA = true; if (window.location.hash !== '#/conta') window.location.hash = '#/conta' }
    throw new ApiError(res.status, data?.error ?? `Erro ${res.status}`, data?.code)
  }
  return data as T
}

export type User = {
  id: number; username: string; display_name: string; first_name?: string; last_name?: string; role: 'admin' | 'operator' | 'viewer'
  totp_enabled: boolean; disabled: boolean; created_at: string; last_login_at: string | null; avatar_version?: string
}
/** Nome completo do usuário ou, sem nome, o login. */
export const userName = (u: Pick<User, 'username' | 'display_name'>) => u.display_name || u.username

export type Agent = {
  id: number; name: string; hostname: string; os: string; arch: string; agent_version: string
  docker_available: boolean; ip: string; created_at: string; last_seen_at: string | null
  revoked_at: string | null; online: boolean; address: string; link?: LinkState
}
/** Situação da conexão do Server com o endereço de um agente. */
export type LinkState = { address: string; state: 'connecting' | 'connected' | 'error'; error?: string; since: string; next_try?: string }
export type PendingToken = { id: number; name: string; created_at: string; expires_at: string; address: string; link?: LinkState }
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
  phase: string; progress_pct: number; cloud_status: string; cloud_path: string; cloud_hash: string; cloud_verified_by: string; backup_kind?: 'full' | 'incremental'; cancel_requested?: boolean
}
export type RunLog = { seq: number; at: string; level: string; text: string }
export type Job = {
  id: number; name: string; agent_id: number; kind: string; enabled: boolean; cron: string; timezone: string
  paths: string[]; excludes: string[]; dest_subdir: string; keep: number; version: number
  agent_name: string; agent_online: boolean; next_run_at: string | null; last_run: Run | null
  cloud_account_id: number | null; cloud_folder: string; cloud_keep: number; cloud_keep_days: number; cloud_direct?: boolean; backup_mode?: 'full' | 'incremental'; full_weekday?: number; mikrotik?: import('./pages/MikrotikForm').MtView | null
  db: DbView | null
}

export type DbView = {
  engine: string; mode: 'docker' | 'network'; container: string; host: string; port: number; user: string; password_set: boolean
  auth_db: string; databases: string[]; exclude_databases: string[]; ssl_skip_verify: boolean; restore_test: 'none' | 'structural' | 'full'; restore_image: string
}
export type ContainerInfo = { name: string; image: string; status: string; engine: string }
export const ENGINE_LABEL: Record<string, string> = { mysql: 'MySQL', mariadb: 'MariaDB', postgres: 'PostgreSQL', mongodb: 'MongoDB' }
export const ENGINE_PORT: Record<string, number> = { mysql: 3306, mariadb: 3306, postgres: 5432, mongodb: 27017 }

export type CloudAccount = {
  id: number; name: string; type: 'onedrive' | 'gdrive' | 'dropbox' | 'sftp' | 'ftp' | 'smb' | 'local'; popup?: boolean; status: 'pending' | 'connected' | 'error'; account_label: string
  quota_total: number; quota_used: number; quota_checked_at: string | null; last_error: string; created_at: string; jobs: number
}
export type DeviceInfo = { user_code: string; verification_uri: string; expires_at: string; interval: number }
export const CLOUD_LABEL = { onedrive: 'OneDrive', gdrive: 'Google Drive', dropbox: 'Dropbox', sftp: 'SFTP', ftp: 'FTP', smb: 'Pasta de rede (SMB)', nfs: 'NFS', local: 'Pasta local' } as const
export const PHASE: Record<string, string> = { archiving: 'Compactando', uploading: 'Enviando para a nuvem', verifying: 'Conferindo' }

export { describeSched as describeCron } from './Schedule'

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

export type Credit = { label: string; name: string; url: string }
export type Branding = { name: string; theme: string; mode?: ThemeMode; themes: CustomTheme[]; has_logo: boolean; logo_version?: string; credit?: Credit }

export type Channel = {
  id: number; name: string; type: 'email' | 'telegram'; enabled: boolean; created_at: string
  email?: { host: string; port: number; security: string; user: string; password_set: boolean; from: string; from_name: string; to: string[]; skip_verify: boolean }
  telegram?: { token_set: boolean; chat_ids: string[] }
}
export type Rule = {
  id: number; name: string; event: string; enabled: boolean; job_ids: number[]; channel_ids: number[]
  params: { time?: string; timezone?: string; offline_minutes?: number; attach_pdf?: boolean; only_problems?: boolean }
}
export type NotifLog = { id: number; rule_name: string; channel_name: string; kind: string; subject: string; status: string; attempts: number; error: string; created_at: string; sent_at: string | null }
export const EVENT_LABEL: Record<string, string> = {
  run_failed: 'Falha ou backup perdido', run_warning: 'Backup com avisos', run_ok: 'Backup concluído com sucesso',
  daily_report: 'Relatório diário consolidado', agent_offline: 'Agente offline',
}
