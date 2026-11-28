import { ChangeEvent, useEffect, useRef, useState } from 'react'
import { Credit, api, ApiError } from '../api'
import Logo, { useBrand } from '../Logo'
import { BUILTIN_SWATCH, CustomTheme, MODES, THEMES, ThemeMode, applyTheme, effectiveMode } from '../themes'
import { Button, Card, ErrorBox, Field, IconButton, Input } from '../ui'
import { confirmar } from '../Dialogs'
import Icon from '../Icon'

const DEFAULT_CREDIT: Credit = { label: 'Desenvolvido por', name: 'Henrique Fagundes', url: 'https://henrique.tec.br' }

export default function Appearance() {
  const { brand, reload } = useBrand()
  const [name, setName] = useState(brand.name)
  const [theme, setTheme] = useState(brand.theme)
  const [mode, setMode] = useState<ThemeMode>(brand.mode ?? 'auto')
  const [themes, setThemes] = useState<CustomTheme[]>(brand.themes)
  const [msg, setMsg] = useState('')
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState(false)
  const [credit, setCredit] = useState<Credit>(brand.credit ?? DEFAULT_CREDIT)
  // a pré-visualização vale só nesta página: ao sair sem salvar, volta o tema do sistema
  const saved = useRef(brand)
  saved.current = brand
  useEffect(() => () => { applyTheme(saved.current.theme, saved.current.themes, saved.current.mode ?? 'auto') }, [])
  useEffect(() => { applyTheme(theme, themes, mode) }, [theme, themes, mode])

  function remove(id: string) {
    setThemes(themes.filter((t) => t.id !== id))
    if (theme === id) setTheme('coruja')
  }
  async function save() {
    setErr(''); setMsg(''); setBusy(true)
    try { await api('PUT', '/api/branding', { name, theme, mode, themes: themes.map((t) => ({ id: t.id })), credit }); setMsg('Aparência salva. Vale para todos os usuários.'); reload() }
    catch (x) { setErr((x as ApiError).message) } finally { setBusy(false) }
  }
  async function upload(e: ChangeEvent<HTMLInputElement>) {
    const f = e.target.files?.[0]; e.target.value = ''
    if (!f) return
    setErr(''); setMsg('')
    try {
      const res = await fetch('/api/branding/logo', { method: 'POST', headers: { 'X-Coruja-CSRF': '1', 'Content-Type': f.type || 'application/octet-stream' }, body: f })
      if (!res.ok) throw new ApiError(res.status, (await res.json().catch(() => ({}))).error ?? 'Falha ao enviar a logo')
      setMsg('Logo atualizada.'); reload()
    } catch (x) { setErr((x as ApiError).message) }
  }
  async function importPkg(e: ChangeEvent<HTMLInputElement>) {
    const f = e.target.files?.[0]; e.target.value = ''
    if (!f) return
    setErr(''); setMsg('')
    try {
      const res = await fetch('/api/branding/themes/import', { method: 'POST', headers: { 'X-Coruja-CSRF': '1', 'Content-Type': 'application/zip' }, body: f })
      const j = await res.json().catch(() => ({}))
      if (!res.ok) throw new ApiError(res.status, j.error ?? 'Falha ao importar o pacote')
      const t = j.theme as CustomTheme
      setThemes(j.replaced ? themes.map((x) => (x.id === t.id ? t : x)) : [...themes, t])
      setTheme(t.id)
      setMsg(`Tema "${t.label}" ${j.replaced ? 'atualizado' : 'instalado'}. Ele já aparece selecionado: salve para aplicá-lo a todos.`); reload()
    } catch (x) { setErr((x as ApiError).message) }
  }
  async function removeLogo() {
    try { await api('DELETE', '/api/branding/logo'); setMsg('Logo removida (volta a coruja padrão).'); reload() } catch (x) { setErr((x as ApiError).message) }
  }

  const m = effectiveMode(mode)
  const swatches = (t: CustomTheme, which: 'light' | 'dark') => {
    const v = which === 'dark' && t.vars_dark ? t.vars_dark : t.vars
    return [v.bg, v.primary, v.accent]
  }
  const cards = [
    ...THEMES.map((t) => ({ id: t.id, label: t.label, tag: 'Coruja', sw: BUILTIN_SWATCH[t.id], pkg: false, desc: '' })),
    ...themes.map((t) => ({ id: t.id, label: t.label, tag: `pacote${t.pkg?.version ? ` ${t.pkg.version}` : ''}`, sw: { light: swatches(t, 'light'), dark: swatches(t, 'dark') }, pkg: true, desc: t.pkg?.description ?? '' })),
  ]

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold">Aparência</h1>
        <p className="text-sm text-muted">Nome, logo, tema e créditos do sistema (white-label). Valem para todos os usuários e também para os e-mails e o PDF.</p>
      </div>
      <ErrorBox message={err} />
      {msg && <div className="rounded-lg border border-ok/40 bg-ok/10 px-3 py-2 text-sm text-ok">{msg}</div>}

      <Card>
        <h2 className="mb-3 font-semibold">Identidade</h2>
        <div className="grid gap-4 sm:grid-cols-2">
          <Field label="Nome do sistema" hint="Aparece no menu, no login, na aba do navegador e no rodapé."><Input value={name} maxLength={60} onChange={(e) => setName(e.target.value)} /></Field>
          <div className="flex items-center gap-4">
            <div className="flex h-16 min-w-32 items-center justify-center rounded-xl border border-border bg-bg px-4"><Logo size={44} /></div>
            <div className="space-y-1 text-sm">
              <div className="flex items-center gap-1">
                <label className="inline-flex cursor-pointer items-center rounded-lg border border-border bg-surface px-3.5 py-2 font-medium hover:bg-bg">
                  {brand.has_logo ? 'Trocar logo' : 'Enviar logo'}
                  <input type="file" accept="image/png,image/jpeg,image/gif,image/webp,image/svg+xml" className="sr-only" onChange={upload} />
                </label>
                {brand.has_logo && <IconButton icon="excluir" tone="danger" label="Remover a logo" onClick={removeLogo} />}
              </div>
              <p className="text-xs text-muted">PNG, JPEG, GIF, WebP ou SVG, até 1 MiB. Temas de pacote podem trazer a própria logo.</p>
            </div>
          </div>
        </div>
      </Card>

      <Card>
        <div className="mb-4 flex flex-wrap items-start justify-between gap-3">
          <div>
            <h2 className="font-semibold">Tema</h2>
            <p className="text-sm text-muted">Escolha um tema da Coruja ou instale um pacote. Ao clicar, a página mostra a prévia; vale para todos depois de salvar.</p>
          </div>
          <label className="inline-flex cursor-pointer items-center gap-2 rounded-lg border border-border bg-surface px-3.5 py-2 text-sm font-medium hover:bg-bg">
            <Icon name="mais" size={16} /> Instalar pacote (.zip)
            <input type="file" accept=".zip,application/zip" className="sr-only" onChange={importPkg} />
          </label>
        </div>
        <div role="radiogroup" aria-label="Tema do sistema" className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
          {cards.map((c) => {
            const on = theme === c.id
            return (
              <div key={c.id} role="radio" aria-checked={on} tabIndex={0} title={c.desc || undefined}
                onClick={() => setTheme(c.id)} onKeyDown={(e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); setTheme(c.id) } }}
                className={`relative cursor-pointer rounded-xl border p-3 transition focus:outline-none focus-visible:ring-2 focus-visible:ring-accent ${on ? 'border-primary ring-2 ring-primary/40' : 'border-border hover:border-primary/50'}`}>
                <div className="mb-3 flex gap-1.5" aria-hidden="true">
                  {(['light', 'dark'] as const).map((k) => (
                    <div key={k} className={`flex h-10 flex-1 items-center justify-center gap-1 rounded-lg border border-border ${m === k ? '' : 'opacity-60'}`} style={{ background: c.sw[k][0] }}>
                      {c.sw[k].slice(1).map((x, i) => <span key={i} className="h-4 w-4 rounded-full" style={{ background: x }} />)}
                    </div>
                  ))}
                </div>
                <div className="flex items-center gap-2">
                  <span className={`flex h-4 w-4 shrink-0 items-center justify-center rounded-full border-2 ${on ? 'border-primary' : 'border-border'}`}>{on && <span className="h-2 w-2 rounded-full bg-primary" />}</span>
                  <b className="truncate text-sm">{c.label}</b>
                  <span className="rounded bg-bg px-1.5 text-[11px] text-muted">{c.tag}</span>
                  {c.pkg && <span className="ml-auto" onClick={(e) => e.stopPropagation()}>
                    <IconButton icon="excluir" tone="danger" label={`Excluir ${c.label}`} onClick={async () => await confirmar({ title: `Excluir o tema "${c.label}"?`, message: 'O pacote e as logos dele são removidos quando você salvar a aparência.', danger: true }) && remove(c.id)} />
                  </span>}
                </div>
              </div>
            )
          })}
        </div>
        <div className="mt-5">
          <span className="mb-1.5 block text-sm font-medium">Modo</span>
          <div role="radiogroup" aria-label="Modo claro ou escuro" className="inline-flex flex-wrap gap-1 rounded-xl border border-border bg-bg p-1">
            {MODES.map((o) => (
              <button key={o.id} type="button" role="radio" aria-checked={mode === o.id} onClick={() => setMode(o.id)}
                className={`rounded-lg px-3 py-1.5 text-sm font-medium transition ${mode === o.id ? 'bg-surface text-text shadow-sm' : 'text-muted hover:text-text'}`}>
                {o.id === 'auto' ? 'Automático' : o.label}
              </button>
            ))}
          </div>
          <p className="mt-1 text-xs text-muted">Automático segue o modo claro ou escuro do sistema de cada pessoa. E-mails e PDF usam sempre o modo claro.</p>
          {THEMES.find((t) => t.id === theme)?.dark_only && <p className="mt-1 text-xs font-medium text-accent">Este tema é sempre escuro: o modo escolhido aqui não muda a interface.</p>}
        </div>
      </Card>

      <Card>
        <h2 className="mb-1 font-semibold">Créditos do rodapé</h2>
        <p className="mb-3 text-sm text-muted">Aparecem depois do nome e da versão, no rodapé das páginas, dos e-mails e do PDF. Deixe o nome vazio para não mostrar créditos.</p>
        <div className="grid gap-3 sm:grid-cols-3">
          <Field label="Texto antes do nome"><Input value={credit.label} maxLength={40} placeholder="Desenvolvido por" onChange={(e) => setCredit({ ...credit, label: e.target.value })} /></Field>
          <Field label="Nome"><Input value={credit.name} maxLength={80} onChange={(e) => setCredit({ ...credit, name: e.target.value })} /></Field>
          <Field label="Link (opcional)" hint="https://… abre em nova aba"><Input type="url" value={credit.url} maxLength={200} placeholder="https://" onChange={(e) => setCredit({ ...credit, url: e.target.value })} /></Field>
        </div>
        <p className="mt-3 rounded-lg bg-bg px-3 py-2 text-sm text-muted">Prévia: {name} - Versão X.X.X{credit.name.trim() && <> - {credit.label.trim() && `${credit.label.trim()} `}{credit.url.trim() ? <span className="text-primary underline">{credit.name.trim()}</span> : credit.name.trim()}</>}</p>
        <button type="button" className="mt-2 text-xs text-primary hover:underline" onClick={() => setCredit(DEFAULT_CREDIT)}>Voltar ao padrão</button>
      </Card>

      <div className="flex gap-3">
        <Button onClick={save} disabled={busy}>Salvar aparência</Button>
        <Button variant="ghost" onClick={() => { setName(brand.name); setTheme(brand.theme); setMode(brand.mode ?? 'auto'); setThemes(brand.themes); setCredit(brand.credit ?? DEFAULT_CREDIT); setMsg(''); setErr('') }}>Descartar alterações</Button>
      </div>
    </div>
  )
}
