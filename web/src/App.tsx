import { useEffect, useState } from 'react'
import Logo from './Logo'
import { THEMES, applyTheme, savedTheme } from './themes'

type Health = { status: string; database: boolean; version: string; uptime_seconds: number }
type Branding = { name: string; theme: string }

export default function App() {
  const [health, setHealth] = useState<Health | null>(null)
  const [error, setError] = useState(false)
  const [brand, setBrand] = useState<Branding>({ name: 'Coruja Backup', theme: 'coruja' })
  const [theme, setTheme] = useState('coruja')

  useEffect(() => {
    fetch('/api/branding').then((r) => r.json()).then((b: Branding) => {
      setBrand(b)
      setTheme(applyTheme(savedTheme() ?? b.theme))
    }).catch(() => setTheme(applyTheme(savedTheme() ?? 'coruja')))
  }, [])

  useEffect(() => {
    const load = () => fetch('/api/health').then((r) => r.json()).then((h: Health) => { setHealth(h); setError(false) }).catch(() => setError(true))
    load()
    const t = setInterval(load, 15000)
    return () => clearInterval(t)
  }, [])

  const version = health?.version ?? '0.0.1'
  return (
    <div className="min-h-screen flex flex-col">
      <header className="bg-surface border-b border-border">
        <div className="mx-auto max-w-5xl px-4 py-3 flex items-center gap-3">
          <Logo />
          <span className="text-lg font-semibold">{brand.name}</span>
          <label className="ml-auto flex items-center gap-2 text-sm text-muted">
            Tema
            <select
              className="rounded-md border border-border bg-surface px-2 py-1 text-text"
              value={theme}
              onChange={(e) => setTheme(applyTheme(e.target.value))}
            >
              {THEMES.map((t) => <option key={t.id} value={t.id}>{t.label}</option>)}
            </select>
          </label>
        </div>
      </header>

      <main className="mx-auto w-full max-w-5xl flex-1 px-4 py-10">
        <h1 className="text-2xl font-semibold">Bem-vindo ao {brand.name}</h1>
        <p className="mt-2 text-muted">Backup centralizado com agentes leves, nuvem e bancos de dados. A configuração será toda feita por aqui.</p>

        <section className="mt-8 grid gap-4 sm:grid-cols-3" aria-label="Estado do sistema">
          <Card title="Server" value={error ? 'Sem resposta' : health ? (health.status === 'ok' ? 'Online' : 'Degradado') : '…'} ok={!error && health?.status === 'ok'} />
          <Card title="Banco de dados" value={health ? (health.database ? 'Conectado' : 'Indisponível') : '…'} ok={!!health?.database} />
          <Card title="Agentes" value="Em breve" />
        </section>
      </main>

      <footer className="border-t border-border bg-surface py-4 text-center text-sm text-muted">
        {brand.name} - Versão {version} - Desenvolvido por{' '}
        <a className="text-primary underline" href="https://henrique.tec.br" target="_blank" rel="noopener noreferrer">Henrique Fagundes</a>
      </footer>
    </div>
  )
}

function Card({ title, value, ok }: { title: string; value: string; ok?: boolean }) {
  return (
    <div className="rounded-xl border border-border bg-surface p-4 shadow-sm">
      <div className="text-sm text-muted">{title}</div>
      <div className="mt-1 flex items-center gap-2 text-lg font-medium">
        {ok !== undefined && <span aria-hidden className={`inline-block h-2.5 w-2.5 rounded-full ${ok ? 'bg-ok' : 'bg-danger'}`} />}
        {value}
      </div>
    </div>
  )
}
