import { createContext, useContext, useEffect, useState } from 'react'
import type { Branding } from './api'

export const BrandCtx = createContext<{ brand: Branding; reload: () => void }>({
  brand: { name: 'Coruja Backup', theme: 'coruja', themes: [], has_logo: false }, reload: () => {},
})
export const useBrand = () => useContext(BrandCtx)

const activeTheme = () => document.documentElement.getAttribute('data-theme-id') ?? ''

// Logo: a do pacote do tema ativo, ou a personalizada (enviada na página Aparência), ou a coruja padrão.
export default function Logo({ size = 36 }: { size?: number }) {
  const { brand } = useBrand()
  const [tid, setTid] = useState(activeTheme)
  useEffect(() => {
    const f = () => setTid(activeTheme())
    window.addEventListener('coruja-theme', f)
    return () => window.removeEventListener('coruja-theme', f)
  }, [])
  const pk = brand.themes.find((t) => t.id === tid)?.pkg
  if (pk?.logo_ui) {
    return <img src={`/api/branding/themes/${tid}/logo?v=${pk.asset_ver ?? ''}`} alt={brand.name} height={size} style={{ height: size, maxWidth: size * 4, objectFit: 'contain' }} />
  }
  if (brand.has_logo) {
    return <img src={`/api/branding/logo?v=${brand.logo_version ?? ''}`} alt={brand.name} height={size} style={{ height: size, maxWidth: size * 4, objectFit: 'contain' }} />
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
