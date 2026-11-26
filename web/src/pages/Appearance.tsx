import { ChangeEvent, useState } from 'react'
import { Branding, api, ApiError } from '../api'
import Logo, { useBrand } from '../Logo'
import { BUILTIN_VARS, CustomTheme, THEMES, VAR_KEYS, applyTheme } from '../themes'
import { Button, Card, ErrorBox, Field, Input } from '../ui'

export default function Appearance() {
  const { brand, reload } = useBrand()
  const [name, setName] = useState(brand.name)
  const [theme, setTheme] = useState(brand.theme)
  const [themes, setThemes] = useState<CustomTheme[]>(brand.themes)
  const [editing, setEditing] = useState<string | null>(null)
  const [msg, setMsg] = useState('')
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState(false)

  const cur = themes.find((t) => t.id === editing) ?? null
  const preview = (id: string, list = themes) => applyTheme(id, list)

  function addTheme(from: string) {
    const base = BUILTIN_VARS[from] ?? themes.find((t) => t.id === from)?.vars ?? BUILTIN_VARS.coruja
    let n = themes.length + 1, id = `tema-${n}`
    while (themes.some((t) => t.id === id)) id = `tema-${++n}`
    const t: CustomTheme = { id, label: `Meu tema ${n}`, vars: { ...base } }
    const list = [...themes, t]
    setThemes(list); setEditing(id); preview(id, list)
  }
  function patch(p: Partial<CustomTheme>, vars?: Record<string, string>) {
    if (!cur) return
    const next = { ...cur, ...p, vars: { ...cur.vars, ...(vars ?? {}) } }
    const list = themes.map((t) => (t.id === cur.id ? next : t))
    setThemes(list); preview(cur.id, list)
  }
  function remove(id: string) {
    const list = themes.filter((t) => t.id !== id)
    setThemes(list); if (editing === id) setEditing(null)
    const fallback = theme === id ? 'coruja' : theme
    setTheme(fallback); preview(fallback, list)
  }

  async function save() {
    setErr(''); setMsg(''); setBusy(true)
    try { await api('PUT', '/api/branding', { name, theme, themes }); setMsg('Aparência salva.'); reload() }
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
  async function removeLogo() {
    try { await api('DELETE', '/api/branding/logo'); setMsg('Logo removida (volta a coruja padrão).'); reload() } catch (x) { setErr((x as ApiError).message) }
  }

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold">Aparência</h1>
        <p className="text-sm text-muted">Nome, logo e cores do sistema (white-label). O rodapé padrão com a versão é mantido.</p>
      </div>
      <ErrorBox message={err} />
      {msg && <div className="rounded-lg border border-ok/40 bg-ok/10 px-3 py-2 text-sm text-ok">{msg}</div>}

      <Card>
        <h2 className="mb-3 font-semibold">Identidade</h2>
        <div className="grid gap-4 sm:grid-cols-2">
          <Field label="Nome do sistema" hint="Aparece no topo, no login, na aba do navegador e no rodapé."><Input value={name} maxLength={60} onChange={(e) => setName(e.target.value)} /></Field>
          <Field label="Tema padrão" hint="Cada pessoa pode escolher o seu no topo da página; este vale para quem ainda não escolheu.">
            <select className="w-full rounded-lg border border-border bg-surface px-3 py-2" value={theme} onChange={(e) => { setTheme(e.target.value); preview(e.target.value) }}>
              {THEMES.map((t) => <option key={t.id} value={t.id}>{t.label}</option>)}
              {themes.map((t) => <option key={t.id} value={t.id}>{t.label} (personalizado)</option>)}
            </select>
          </Field>
        </div>
        <div className="mt-4 flex flex-wrap items-center gap-4">
          <div className="flex h-16 min-w-32 items-center justify-center rounded-lg border border-border bg-bg px-4"><Logo size={44} /></div>
          <div className="space-y-1 text-sm">
            <label className="inline-flex cursor-pointer items-center rounded-lg border border-border bg-surface px-3.5 py-2 font-medium hover:bg-bg">
              {brand.has_logo ? 'Trocar logo' : 'Enviar logo'}
              <input type="file" accept="image/png,image/jpeg,image/gif,image/webp,image/svg+xml" className="sr-only" onChange={upload} />
            </label>
            {brand.has_logo && <button className="ml-3 text-danger hover:underline" onClick={removeLogo}>Remover</button>}
            <p className="text-xs text-muted">PNG, JPEG, GIF, WebP ou SVG (sem scripts), até 1 MiB. Altura recomendada: 40 a 80 px, fundo transparente.</p>
          </div>
        </div>
      </Card>

      <Card>
        <div className="mb-3 flex items-center justify-between gap-3">
          <h2 className="font-semibold">Temas personalizados</h2>
          {themes.length < 10 && (
            <select aria-label="Novo tema a partir de" className="rounded-md border border-border bg-surface px-2 py-1.5 text-sm" value="" onChange={(e) => e.target.value && addTheme(e.target.value)}>
              <option value="">+ Novo tema a partir de…</option>
              {THEMES.map((t) => <option key={t.id} value={t.id}>{t.label}</option>)}
              {themes.map((t) => <option key={t.id} value={t.id}>{t.label}</option>)}
            </select>
          )}
        </div>
        {themes.length === 0 && <p className="text-sm text-muted">Nenhum tema personalizado. Crie um a partir de um dos temas existentes e ajuste as cores; a página muda na hora para você ver o resultado.</p>}
        <ul className="divide-y divide-border text-sm">
          {themes.map((t) => (
            <li key={t.id} className="flex items-center gap-3 py-2">
              <span className="flex gap-1" aria-hidden>{['bg', 'primary', 'accent'].map((k) => <span key={k} className="inline-block h-4 w-4 rounded-full border border-border" style={{ background: t.vars[k] }} />)}</span>
              <b>{t.label}</b> {theme === t.id && <span className="text-xs text-muted">(padrão)</span>}
              <span className="ml-auto flex gap-3">
                <button className="text-primary hover:underline" onClick={() => { setEditing(t.id); preview(t.id) }}>Editar</button>
                <button className="text-danger hover:underline" onClick={() => confirm(`Excluir o tema "${t.label}"?`) && remove(t.id)}>Excluir</button>
              </span>
            </li>
          ))}
        </ul>
        {cur && (
          <div className="mt-4 space-y-3 rounded-lg border border-border bg-bg p-4">
            <Field label="Nome do tema"><Input value={cur.label} maxLength={40} onChange={(e) => patch({ label: e.target.value })} /></Field>
            <div className="grid gap-3 sm:grid-cols-2">
              {VAR_KEYS.map(({ key, label }) => (
                <label key={key} className="flex items-center justify-between gap-3 rounded-lg border border-border bg-surface px-3 py-2 text-sm">
                  <span>{label}</span>
                  <span className="flex items-center gap-2">
                    <code className="text-xs text-muted">{cur.vars[key]}</code>
                    <input type="color" aria-label={label} value={cur.vars[key]} onChange={(e) => patch({}, { [key]: e.target.value })} className="h-8 w-10 cursor-pointer rounded border border-border bg-transparent p-0" />
                  </span>
                </label>
              ))}
            </div>
            <div className="flex gap-2"><Button type="button" variant="ghost" onClick={() => { setTheme(cur.id); preview(cur.id) }}>Usar como padrão</Button><Button type="button" variant="ghost" onClick={() => setEditing(null)}>Fechar editor</Button></div>
          </div>
        )}
      </Card>

      <div className="flex gap-3">
        <Button onClick={save} disabled={busy}>Salvar aparência</Button>
        <Button variant="ghost" onClick={() => { reload(); preview(brand.theme, brand.themes); setName(brand.name); setTheme(brand.theme); setThemes(brand.themes); setEditing(null) }}>Descartar alterações</Button>
      </div>
    </div>
  )
}
