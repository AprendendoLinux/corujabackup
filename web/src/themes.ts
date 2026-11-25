export const THEMES = [
  { id: 'coruja', label: 'Coruja' },
  { id: 'noturno', label: 'Noturno' },
  { id: 'floresta', label: 'Floresta' },
] as const

export type ThemeId = (typeof THEMES)[number]['id']

export function applyTheme(id: string) {
  const ok = THEMES.some((t) => t.id === id) ? id : 'coruja'
  document.documentElement.setAttribute('data-theme', ok)
  try { localStorage.setItem('coruja.theme', ok) } catch { /* sem armazenamento: tudo bem */ }
  return ok as ThemeId
}

export function savedTheme(): string | null {
  try { return localStorage.getItem('coruja.theme') } catch { return null }
}
