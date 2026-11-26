import { createContext, useContext, useEffect, useState } from 'react'
import type { Branding } from './api'

export const BrandCtx = createContext<{ brand: Branding; reload: () => void }>({
  brand: { name: 'Coruja Backup', theme: 'coruja', themes: [], has_logo: false }, reload: () => {},
})
export const useBrand = () => useContext(BrandCtx)

const activeTheme = () => document.documentElement.getAttribute('data-theme-id') ?? ''

// Endereço da logo em vigor: a do pacote do tema ativo, senão a enviada na página Aparência, senão nenhuma (coruja padrão).
export function logoURL(brand: Branding, themeId: string): string | null {
  const pk = brand.themes.find((t) => t.id === themeId)?.pkg
  if (pk?.logo_ui) return `/api/branding/themes/${themeId}/logo?v=${pk.asset_ver ?? ''}`
  if (brand.has_logo) return `/api/branding/logo?v=${brand.logo_version ?? ''}`
  return null
}

function setIcon(href: string, type: string) {
  let link = document.querySelector<HTMLLinkElement>('link[rel="icon"]')
  if (!link) { link = document.createElement('link'); link.rel = 'icon'; document.head.appendChild(link) }
  link.type = type
  link.href = href
}

// useFavicon mantém o ícone da aba igual à logo em vigor. A imagem é centralizada num quadrado (logos que não são
// quadradas não ficam distorcidas); sem logo própria, ou se ela não carregar, volta a coruja padrão.
export function useFavicon(brand: Branding) {
  useEffect(() => {
    let current = 0
    const update = () => {
      const mine = ++current
      const fallback = () => setIcon('/favicon.svg', 'image/svg+xml')
      const url = logoURL(brand, activeTheme() || brand.theme)
      if (!url) { fallback(); return }
      const img = new Image()
      img.onload = () => {
        if (mine !== current) return // o tema mudou enquanto a imagem carregava
        const side = 64, w = img.naturalWidth || side, h = img.naturalHeight || side, k = Math.min(side / w, side / h)
        const canvas = document.createElement('canvas')
        canvas.width = canvas.height = side
        const ctx = canvas.getContext('2d')
        if (!ctx) { fallback(); return }
        ctx.drawImage(img, (side - w * k) / 2, (side - h * k) / 2, w * k, h * k)
        try { setIcon(canvas.toDataURL('image/png'), 'image/png') } catch { fallback() }
      }
      img.onerror = () => { if (mine === current) fallback() }
      img.src = url
    }
    update()
    window.addEventListener('coruja-theme', update)
    return () => { current++; window.removeEventListener('coruja-theme', update) }
  }, [brand])
}

// Logo: a do pacote do tema ativo, ou a personalizada (enviada na página Aparência), ou a coruja padrão.
export default function Logo({ size = 36 }: { size?: number }) {
  const { brand } = useBrand()
  const [tid, setTid] = useState(activeTheme)
  useEffect(() => {
    const f = () => setTid(activeTheme())
    window.addEventListener('coruja-theme', f)
    return () => window.removeEventListener('coruja-theme', f)
  }, [])
  const src = logoURL(brand, tid)
  if (src) {
    return <img src={src} alt={brand.name} height={size} style={{ height: size, maxWidth: size * 4, objectFit: 'contain' }} />
  }
  return (
    <svg width={size} height={size} viewBox="0 0 64 64" role="img" aria-label={brand.name}>
      <rect width="64" height="64" rx="14" fill="var(--primary)" />
      <path d="M14 14l9 7h18l9-7v22c0 11-8 18-18 18S14 47 14 36z" fill="var(--accent)" />
      <circle cx="24" cy="31" r="7" fill="#fff" />
      <circle cx="40" cy="31" r="7" fill="#fff" />
      <circle cx="24" cy="31" r="3.2" fill="var(--primary)" />
      <circle cx="40" cy="31" r="3.2" fill="var(--primary)" />
      <path d="M29 38h6l-3 6z" fill="var(--primary)" />
    </svg>
  )
}
