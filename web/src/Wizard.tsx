// Assistente em etapas (Voltar · Avançar · Concluir). Cada etapa valida o que é dela antes de avançar.
import { FormEvent, ReactNode } from 'react'
import { Button, ErrorBox } from './ui'

export type Step = { title: string; hint?: string }

export function Wizard({ steps, current, maxReached, onGo, onBack, onNext, onFinish, nextLabel = 'Avançar', finishLabel = 'Concluir', busy, err, freeJump, extra, children }: {
  steps: Step[]
  current: number
  /** etapa mais adiante já alcançada (as anteriores a ela podem ser revisitadas pelo cabeçalho) */
  maxReached: number
  onGo: (i: number) => void
  onBack?: () => void
  onNext: () => void
  onFinish: () => void
  nextLabel?: string
  finishLabel?: string
  busy?: boolean
  err?: string
  /** na edição, qualquer etapa pode ser aberta e salva a qualquer momento */
  freeJump?: boolean
  extra?: ReactNode
  children: ReactNode
}) {
  const last = current === steps.length - 1
  function submit(e: FormEvent) {
    e.preventDefault()
    if (busy) return
    if (last) onFinish(); else onNext()
  }
  return (
    <form onSubmit={submit} className="flex flex-col gap-5">
      <ol className="flex items-start gap-1" aria-label="Etapas">
        {steps.map((s, i) => {
          const done = i < current
          const on = i === current
          const reachable = freeJump || i <= maxReached
          return (
            <li key={s.title} className="relative min-w-0 flex-1">
              <button type="button" disabled={!reachable || on} onClick={() => onGo(i)} aria-current={on ? 'step' : undefined}
                className="group relative z-10 flex w-full min-w-0 flex-col items-center gap-1.5 text-center disabled:cursor-default">
                <span className={`flex h-8 w-8 items-center bg-surface justify-center rounded-full border-2 text-sm font-semibold transition ${
                  on ? 'border-primary !bg-primary text-primary-contrast' : done ? 'border-primary text-primary' : reachable ? 'border-border text-text group-hover:border-primary' : 'border-border text-muted'}`}>
                  {done && !freeJump ? '✓' : i + 1}
                </span>
                <span className={`w-full truncate text-xs ${on ? 'font-semibold text-text' : 'text-muted'}`}>{s.title}</span>
              </button>
              {i < steps.length - 1 && <span className={`absolute left-[calc(50%+1.25rem)] right-[calc(-50%+1.25rem)] top-4 h-0.5 rounded ${i < current ? 'bg-primary' : 'bg-border'}`} aria-hidden="true" />}
            </li>
          )
        })}
      </ol>
      {steps[current].hint && <p className="-mt-1 text-sm text-muted">{steps[current].hint}</p>}
      <div className="space-y-4">{children}</div>
      <ErrorBox message={err ?? ''} />
      <div className="flex flex-wrap items-center justify-between gap-2 border-t border-border pt-4">
        <div>{current > 0 && onBack && <Button type="button" variant="ghost" onClick={onBack} disabled={busy}>Voltar</Button>}</div>
        <div className="flex flex-wrap gap-2">
          {extra}
          {freeJump && !last && <Button type="button" variant="ghost" onClick={onFinish} disabled={busy}>{finishLabel}</Button>}
          <Button disabled={busy}>{busy ? 'Aguarde…' : last ? finishLabel : nextLabel}</Button>
        </div>
      </div>
    </form>
  )
}
