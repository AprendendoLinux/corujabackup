import { ReactNode, useEffect, useRef, useState } from 'react'

export function Button({ variant = 'primary', className = '', ...p }: React.ButtonHTMLAttributes<HTMLButtonElement> & { variant?: 'primary' | 'ghost' | 'danger' }) {
  const base = 'inline-flex items-center justify-center rounded-lg px-3.5 py-2 text-sm font-medium transition disabled:opacity-50 disabled:cursor-not-allowed focus:outline-none focus-visible:ring-2 focus-visible:ring-accent'
  const v = {
    primary: 'bg-primary text-primary-contrast hover:opacity-90',
    ghost: 'border border-border bg-surface text-text hover:bg-bg',
    danger: 'bg-danger text-white hover:opacity-90',
  }[variant]
  return <button className={`${base} ${v} ${className}`} {...p} />
}

export function Field({ label, hint, children }: { label: string; hint?: string; children: ReactNode }) {
  return (
    <label className="block text-sm">
      <span className="mb-1 block font-medium">{label}</span>
      {children}
      {hint && <span className="mt-1 block text-xs text-muted">{hint}</span>}
    </label>
  )
}

export const inputCls = 'w-full rounded-lg border border-border bg-surface px-3 py-2 text-text focus:outline-none focus:ring-2 focus:ring-accent'

export function Input({ className = '', ...p }: React.InputHTMLAttributes<HTMLInputElement>) {
  return <input className={`${inputCls} ${className}`} {...p} />
}

export function Card({ children, className = '' }: { children: ReactNode; className?: string }) {
  return <section className={`rounded-xl border border-border bg-surface p-5 shadow-sm ${className}`}>{children}</section>
}

export function ErrorBox({ message }: { message: string }) {
  if (!message) return null
  return <div role="alert" className="rounded-lg border border-danger/40 bg-danger/10 px-3 py-2 text-sm text-danger">{message}</div>
}

export function Badge({ tone, children }: { tone: 'ok' | 'off' | 'warn' | 'danger'; children: ReactNode }) {
  const c = { ok: 'bg-ok/15 text-ok', off: 'bg-border text-muted', warn: 'bg-accent/20 text-text', danger: 'bg-danger/15 text-danger' }[tone]
  return <span className={`inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium ${c}`}>{children}</span>
}

export function Modal({ title, onClose, children, wide }: { title: string; onClose: () => void; children: ReactNode; wide?: boolean }) {
  const dialog = useRef<HTMLDivElement>(null)
  useEffect(() => {
    // com uma janela sobre outra (seletor de pastas), o Esc fecha só a de cima
    const h = (e: KeyboardEvent) => {
      const all = document.querySelectorAll('[role=dialog]')
      if (e.key !== 'Escape' || e.defaultPrevented || all[all.length - 1] !== dialog.current) return
      e.preventDefault()
      onClose()
    }
    window.addEventListener('keydown', h)
    return () => window.removeEventListener('keydown', h)
  }, [onClose])
  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      {/* o título fica fixo e só o conteúdo rola, por dentro dos cantos arredondados */}
      <div ref={dialog} role="dialog" aria-modal="true" aria-label={title} className={`flex max-h-[94vh] w-full ${wide ? 'max-w-4xl' : 'max-w-2xl'} flex-col overflow-hidden rounded-xl border border-border bg-surface shadow-xl`}>
        <div className="flex items-center justify-between px-6 pb-3 pt-5">
          <h2 className="text-lg font-semibold">{title}</h2>
          <button aria-label="Fechar" className="text-muted hover:text-text" onClick={onClose}>✕</button>
        </div>
        <div className="scroll-fino mr-1.5 min-h-0 overflow-y-auto pb-5 pl-6 pr-4">{children}</div>
      </div>
    </div>
  )
}

export function CopyBox({ text }: { text: string }) {
  const pre = useRef<HTMLPreElement>(null)
  const [state, setState] = useState<'idle' | 'ok' | 'manual'>('idle')
  useEffect(() => {
    if (state === 'idle') return
    const t = setTimeout(() => setState('idle'), 2500)
    return () => clearTimeout(t)
  }, [state])

  // navigator.clipboard só existe em HTTPS ou localhost. Em http://IP:porta copiamos a partir da seleção do próprio texto;
  // se nem isso funcionar, o texto fica selecionado para o Ctrl+C.
  async function copy() {
    try {
      if (navigator.clipboard && window.isSecureContext) {
        await navigator.clipboard.writeText(text)
        setState('ok')
        return
      }
    } catch { /* sem permissão: tenta pela seleção */ }
    const selection = window.getSelection()
    if (!pre.current || !selection) return setState('manual')
    const range = document.createRange()
    range.selectNodeContents(pre.current)
    selection.removeAllRanges()
    selection.addRange(range)
    if (document.execCommand('copy')) {
      selection.removeAllRanges()
      setState('ok')
    } else {
      setState('manual')
    }
  }

  return (
    <div className="relative">
      <pre ref={pre} className="overflow-x-auto rounded-lg border border-border bg-bg p-3 pr-28 text-xs leading-relaxed">{text}</pre>
      <button type="button" aria-live="polite" className="absolute right-2 top-2 rounded-md border border-border bg-surface px-2 py-1 text-xs hover:bg-bg" onClick={copy}>
        {state === 'ok' ? 'Copiado ✓' : state === 'manual' ? 'Use Ctrl+C' : 'Copiar'}
      </button>
    </div>
  )
}
