import type { User } from './api'

/** Foto do usuário ou, sem foto, a inicial do nome num círculo. */
export default function Avatar({ user, size = 36 }: { user: Pick<User, 'id' | 'username' | 'display_name' | 'avatar_version'>; size?: number }) {
  const name = user.display_name || user.username
  if (user.avatar_version) {
    return <img src={`/api/users/${user.id}/avatar?v=${user.avatar_version}`} alt="" width={size} height={size} className="shrink-0 rounded-full object-cover ring-1 ring-border" style={{ width: size, height: size }} />
  }
  return (
    <span aria-hidden="true" className="flex shrink-0 items-center justify-center rounded-full bg-accent/25 font-semibold text-text" style={{ width: size, height: size, fontSize: size * 0.4 }}>
      {name.slice(0, 1).toUpperCase()}
    </span>
  )
}
