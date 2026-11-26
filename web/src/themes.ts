// Temas: os embutidos da Coruja e os instalados por pacote (.zip). Todos têm modo claro e escuro; o administrador escolhe
// o tema e o modo em Aparência (Automático segue a preferência do sistema operacional de cada pessoa).

export type ThemeMode = 'auto' | 'light' | 'dark'

/** Temas embutidos: id do tema e o bloco de CSS (index.css) de cada modo. */
export const THEMES = [
  { id: 'coruja', label: 'Coruja', light: 'coruja', dark: 'noturno' },
  { id: 'floresta', label: 'Floresta', light: 'floresta', dark: 'floresta-escuro' },
] as const

export const MODES: { id: ThemeMode; label: string }[] = [
  { id: 'auto', label: 'Automático (segue o sistema de cada pessoa)' }, { id: 'light', label: 'Claro' }, { id: 'dark', label: 'Escuro' },
]

export type ThemePkg = { version?: string; author?: string; description?: string; logo_ui?: string; logo_doc?: string; asset_ver?: string; doc?: Record<string, string> }
export type CustomTheme = { id: string; label: string; vars: Record<string, string>; vars_dark?: Record<string, string>; pkg?: ThemePkg }

export const VAR_KEYS = ['bg', 'surface', 'text', 'muted', 'border', 'primary', 'primary_contrast', 'accent', 'ok', 'danger']

// Cores dos embutidos (só para as amostras na tela de Aparência; as de verdade estão em index.css).
export const BUILTIN_SWATCH: Record<string, { light: string[]; dark: string[] }> = {
  coruja: { light: ['#f6f5fb', '#3b2a6b', '#f5a524'], dark: ['#12101c', '#8b73e8', '#f5a524'] },
  floresta: { light: ['#f1f6f2', '#1f6b45', '#e0a526'], dark: ['#0f1712', '#4fbf86', '#e8b648'] },
}

const cssName = (k: string) => '--' + k.replace(/_/g, '-')
const darkQuery = () => window.matchMedia?.('(prefers-color-scheme: dark)')

export function effectiveMode(mode: ThemeMode): 'light' | 'dark' {
  if (mode === 'light' || mode === 'dark') return mode
  return darkQuery()?.matches ? 'dark' : 'light'
}

// Aplica um tema e um modo. Os instalados por pacote definem as variáveis CSS direto no <html>.
export function applyTheme(id: string, custom: CustomTheme[] = [], mode: ThemeMode = 'auto') {
  const ct = custom.find((t) => t.id === id)
  const bt = THEMES.find((t) => t.id === id)
  const ok = ct || bt ? id : 'coruja'
  const m = effectiveMode(mode)
  const root = document.documentElement
  VAR_KEYS.forEach((k) => root.style.removeProperty(cssName(k)))
  const builtin = THEMES.find((t) => t.id === ok) ?? THEMES[0]
  root.setAttribute('data-theme', ct ? (m === 'dark' ? 'noturno' : 'coruja') : builtin[m])
  root.setAttribute('data-theme-id', ok)
  root.setAttribute('data-mode', m)
  if (ct) {
    const vars = m === 'dark' && ct.vars_dark ? ct.vars_dark : ct.vars
    VAR_KEYS.forEach((k) => vars[k] && root.style.setProperty(cssName(k), vars[k]))
  }
  window.dispatchEvent(new Event('coruja-theme'))
  return ok
}

/** Avisa quando a preferência clara/escura do sistema muda (para o modo Automático). */
export function onSystemModeChange(fn: () => void) {
  const q = darkQuery()
  q?.addEventListener?.('change', fn)
  return () => q?.removeEventListener?.('change', fn)
}

// O tema é um só para todo o sistema, definido pelo administrador em Aparência. Versões antigas guardavam uma escolha
// por navegador, que podia sobrepor a do administrador; ela é descartada.
export function forgetLocalTheme() {
  try { localStorage.removeItem('coruja.theme') } catch { /* sem armazenamento: tudo bem */ }
}
