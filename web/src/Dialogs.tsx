// Janelas de confirmação e de pergunta no visual do sistema, no lugar de confirm() e prompt() do navegador.
// Uso: if (await confirmar({ title, message, danger: true })) ... ; const v = await perguntar({ title, label })
import { FormEvent, ReactNode, useEffect, useState } from 'react'
import Icon, { IconName } from './Icon'
import { Button, Input } from './ui'

type Ask = {
  kind: 'confirm' | 'prompt'
  title: string
  message?: ReactNode
  confirmLabel?: string
  danger?: boolean
  icon?: IconName
  label?: string
  value?: string
  type?: string
  hint?: string
  resolve: (v: string | boolean | null) => void
}

let push: ((a: Ask) => void) | null = null

export function confirmar(o: { title: string; message?: ReactNode; confirmLabel?: string; danger?: boolean; icon?: IconName }): Promise<boolean> {
  return new Promise((resolve) => {
    if (!push) return resolve(false)
    push({ kind: 'confirm', ...o, resolve: (v) => resolve(v === true) })
  })
}

export function perguntar(o: { title: string; message?: ReactNode; label: string; value?: string; type?: string; hint?: string; confirmLabel?: string; icon?: IconName }): Promise<string | null> {
  return new Promise((resolve) => {
    if (!push) return resolve(null)
    push({ kind: 'prompt', ...o, resolve: (v) => resolve(typeof v === 'string' ? v : null) })
  })
}

export default function DialogHost() {
  const [q, setQ] = useState<Ask[]>([])
  const [value, setValue] = useState('')
  useEffect(() => {
    push = (a) => setQ((c) => [...c, a])
    return () => { push = null }
  }, [])
  const cur = q[0]
  useEffect(() => { if (cur) setValue(cur.value ?? '') }, [cur])
  useEffect(() => {
    if (!cur) return
    const h = (e: KeyboardEvent) => { if (e.key === 'Escape') { e.preventDefault(); e.stopImmediatePropagation(); done(null) } }
    window.addEventListener('keydown', h, true)
    return () => window.removeEventListener('keydown', h, true)
  })
  if (!cur) return null
  function done(v: string | boolean | null) { cur.resolve(v); setQ((c) => c.slice(1)) }
  function submit(e: FormEvent) {
    e.preventDefault()
    if (cur.kind === 'prompt') { if (value.trim()) done(value) } else done(true)
  }
  const icon = cur.icon ?? (cur.danger ? 'excluir' : cur.kind === 'prompt' ? 'editar' : 'executar')
  return (
    <div className="fixed inset-0 z-[70] flex items-center justify-center bg-black/50 p-4 backdrop-blur-[2px]" onMouseDown={(e) => e.target === e.currentTarget && done(null)}>
      <form role="alertdialog" aria-modal="true" aria-labelledby="dlg-title" onSubmit={submit}
        className="w-full max-w-md overflow-hidden rounded-2xl border border-border bg-surface shadow-2xl">
        <div className="flex gap-4 p-6">
          <span className={`flex h-11 w-11 shrink-0 items-center justify-center rounded-full ${cur.danger ? 'bg-danger/15 text-danger' : 'bg-primary/10 text-primary'}`}>
            <Icon name={icon} size={22} />
          </span>
          <div className="min-w-0 flex-1 space-y-2">
            <h2 id="dlg-title" className="text-lg font-semibold">{cur.title}</h2>
            {cur.message && <div className="text-sm text-muted">{cur.message}</div>}
            {cur.kind === 'prompt' && (
              <label className="block pt-1 text-sm">
                <span className="mb-1 block font-medium">{cur.label}</span>
                <Input autoFocus type={cur.type ?? 'text'} value={value} onChange={(e) => setValue(e.target.value)} autoComplete={cur.type === 'password' ? 'new-password' : 'off'} />
                {cur.hint && <span className="mt-1 block text-xs text-muted">{cur.hint}</span>}
              </label>
            )}
          </div>
        </div>
        <div className="flex justify-end gap-2 border-t border-border bg-bg/60 px-6 py-3">
          <Button type="button" variant="ghost" onClick={() => done(null)}>Cancelar</Button>
          <Button autoFocus={cur.kind === 'confirm'} variant={cur.danger ? 'danger' : 'primary'} disabled={cur.kind === 'prompt' && !value.trim()}>{cur.confirmLabel ?? (cur.danger ? 'Excluir' : 'Confirmar')}</Button>
        </div>
      </form>
    </div>
  )
}
