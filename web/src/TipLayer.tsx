// Mostra a dica (data-tip) de qualquer elemento ao passar o mouse ou focar com o teclado. Fica num nível acima de tudo,
// então não é cortada por tabelas com rolagem nem por janelas.
import { useEffect, useState } from 'react'

export default function TipLayer() {
  const [tip, setTip] = useState<{ text: string; x: number; y: number; below: boolean } | null>(null)
  useEffect(() => {
    const show = (e: Event) => {
      const el = (e.target as HTMLElement | null)?.closest?.('[data-tip]') as HTMLElement | null
      if (!el) return
      const r = el.getBoundingClientRect()
      const below = r.top < 40
      setTip({ text: el.dataset.tip ?? '', x: r.left + r.width / 2, y: below ? r.bottom + 6 : r.top - 6, below })
    }
    const hide = (e: Event) => {
      const el = (e.target as HTMLElement | null)?.closest?.('[data-tip]')
      const to = (e as FocusEvent).relatedTarget as HTMLElement | null
      if (el && (!to || !el.contains(to))) setTip(null)
    }
    const clear = () => setTip(null)
    document.addEventListener('mouseover', show)
    document.addEventListener('focusin', show)
    document.addEventListener('mouseout', hide)
    document.addEventListener('focusout', hide)
    document.addEventListener('click', clear)
    window.addEventListener('scroll', clear, true)
    return () => {
      document.removeEventListener('mouseover', show)
      document.removeEventListener('focusin', show)
      document.removeEventListener('mouseout', hide)
      document.removeEventListener('focusout', hide)
      document.removeEventListener('click', clear)
      window.removeEventListener('scroll', clear, true)
    }
  }, [])
  if (!tip) return null
  return <div role="tooltip" className="tip-layer" style={{ left: tip.x, top: tip.y, transform: `translate(-50%, ${tip.below ? '0' : '-100%'})` }}>{tip.text}</div>
}
