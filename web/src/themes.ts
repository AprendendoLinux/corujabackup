export const THEMES = [
  { id: 'coruja', label: 'Coruja' },
  { id: 'noturno', label: 'Noturno' },
  { id: 'floresta', label: 'Floresta' },
] as const

export type ThemeId = (typeof THEMES)[number]['id']

export type ThemePkg = { version?: string; author?: string; description?: string; logo_ui?: string; logo_doc?: string; asset_ver?: string; doc?: Record<string, string> }
export type CustomTheme = { id: string; label: string; vars: Record<string, string>; pkg?: ThemePkg }

export const VAR_KEYS: { key: string; label: string }[] = [
  { key: 'bg', label: 'Fundo da página' }, { key: 'surface', label: 'Cartões e janelas' }, { key: 'text', label: 'Texto' },
  { key: 'muted', label: 'Texto secundário' }, { key: 'border', label: 'Bordas' }, { key: 'primary', label: 'Cor principal' },
  { key: 'primary_contrast', label: 'Texto sobre a cor principal' }, { key: 'accent', label: 'Destaque' }, { key: 'ok', label: 'Sucesso' }, { key: 'danger', label: 'Erro' },
]

// Espelha os temas embutidos de index.css (usado para "duplicar" um tema como ponto de partida).
export const BUILTIN_VARS: Record<string, Record<string, string>> = {
  coruja: { bg: '#f6f5fb', surface: '#ffffff', text: '#1e1b2e', muted: '#6b6784', border: '#e3e0f0', primary: '#3b2a6b', primary_contrast: '#ffffff', accent: '#f5a524', ok: '#1f9d55', danger: '#d64545' },
  noturno: { bg: '#12101c', surface: '#1c1930', text: '#ece9fa', muted: '#9a95b8', border: '#2e2a4a', primary: '#8b73e8', primary_contrast: '#12101c', accent: '#f5a524', ok: '#3ccf7b', danger: '#ff6b6b' },
  floresta: { bg: '#f1f6f2', surface: '#ffffff', text: '#16251b', muted: '#5c7364', border: '#d9e6dc', primary: '#1f6b45', primary_contrast: '#ffffff', accent: '#e0a526', ok: '#1f9d55', danger: '#c93c3c' },
}

const cssName = (k: string) => '--' + k.replace(/_/g, '-')

export function clearCustom() {
  const st = document.documentElement.style
  VAR_KEYS.forEach(({ key }) => st.removeProperty(cssName(key)))
}

// Aplica um tema embutido ou personalizado. Os personalizados definem as variáveis CSS direto no <html>.
export function applyTheme(id: string, custom: CustomTheme[] = []) {
  const ct = custom.find((t) => t.id === id)
  const builtin = THEMES.some((t) => t.id === id)
  const ok = ct || builtin ? id : 'coruja'
  clearCustom()
  document.documentElement.setAttribute('data-theme', ct ? 'coruja' : ok)
  document.documentElement.setAttribute('data-theme-id', ok)
  window.dispatchEvent(new Event('coruja-theme'))
  if (ct) VAR_KEYS.forEach(({ key }) => document.documentElement.style.setProperty(cssName(key), ct.vars[key]))
  try { localStorage.setItem('coruja.theme', ok) } catch { /* sem armazenamento: tudo bem */ }
  return ok
}

export function savedTheme(): string | null {
  try { return localStorage.getItem('coruja.theme') } catch { return null }
}
