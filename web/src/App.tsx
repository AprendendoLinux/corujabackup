import { useCallback, useEffect, useState } from 'react'
import Logo, { BrandCtx } from './Logo'
import { THEMES, applyTheme, savedTheme } from './themes'
import { Agent, Branding, User, api, ApiError, ROLE_LABEL } from './api'
import { Card } from './ui'
import { Login, Setup } from './pages/Auth'
import Agents from './pages/Agents'
import Jobs from './pages/Jobs'
import Clouds from './pages/Clouds'
import Runs from './pages/Runs'
import Users from './pages/Users'
import Audit from './pages/Audit'
import Account from './pages/Account'
import Appearance from './pages/Appearance'

type Me = { user: User; recovery_codes_left: number; version: string }
type Phase = 'loading' | 'setup' | 'login' | 'app'

function useRoute() {
  const get = () => (window.location.hash.replace(/^#\/?/, '') || 'painel')
  const [r, setR] = useState(get)
  useEffect(() => { const h = () => setR(get()); window.addEventListener('hashchange', h); return () => window.removeEventListener('hashchange', h) }, [])
  return r
}

export default function App() {
  const [brand, setBrand] = useState<Branding>({ name: 'Coruja Backup', theme: 'coruja', themes: [], has_logo: false })
  const [theme, setTheme] = useState('coruja')
  const [phase, setPhase] = useState<Phase>('loading')
  const [me, setMe] = useState<Me | null>(null)
  const [version, setVersion] = useState('0.0.6')
  const route = useRoute()

  const loadBrand = useCallback(() => {
    fetch('/api/branding').then((r) => r.json()).then((b: Branding) => {
      b.themes = b.themes ?? []
      setBrand(b); setTheme(applyTheme(savedTheme() ?? b.theme, b.themes))
    }).catch(() => setTheme(applyTheme(savedTheme() ?? 'coruja')))
  }, [])
  useEffect(() => {
    loadBrand()
    fetch('/api/version').then((r) => r.json()).then((v) => setVersion(v.version)).catch(() => {})
  }, [loadBrand])
  useEffect(() => {
    document.title = brand.name
    const link = document.querySelector<HTMLLinkElement>('link[rel="icon"]')
    if (link) link.href = brand.has_logo ? `/api/branding/logo?v=${brand.logo_version ?? ''}` : '/favicon.svg'
  }, [brand])

  const refresh = useCallback(async () => {
    try {
      const m = await api<Me>('GET', '/api/me'); setMe(m); setPhase('app')
    } catch (x) {
      if ((x as ApiError).status !== 401) { setPhase('login'); return }
      try {
        const s = await api<{ needs_setup: boolean }>('GET', '/api/setup/status')
        setPhase(s.needs_setup ? 'setup' : 'login')
      } catch { setPhase('login') }
    }
  }, [])
  useEffect(() => { refresh() }, [refresh])

  async function logout() { try { await api('POST', '/api/logout') } catch { /* ignora */ } setMe(null); setPhase('login'); window.location.hash = '' }

  return (
    <BrandCtx.Provider value={{ brand, reload: loadBrand }}>
    <div className="flex min-h-screen flex-col">
      <header className="border-b border-border bg-surface">
        <div className="mx-auto flex max-w-6xl flex-wrap items-center gap-x-4 gap-y-2 px-4 py-3">
          <a href="#/" className="flex items-center gap-3"><Logo /><span className="text-lg font-semibold">{brand.name}</span></a>
          {phase === 'app' && me && (
            <nav className="ml-2 flex flex-wrap gap-1 text-sm" aria-label="Principal">
              <NavLink to="painel" cur={route}>Painel</NavLink>
              <NavLink to="agentes" cur={route}>Agentes</NavLink>
              <NavLink to="jobs" cur={route}>Jobs</NavLink>
              <NavLink to="nuvem" cur={route}>Nuvem</NavLink>
              <NavLink to="historico" cur={route}>Histórico</NavLink>
              {me.user.role === 'admin' && <NavLink to="usuarios" cur={route}>Usuários</NavLink>}
              {me.user.role === 'admin' && <NavLink to="auditoria" cur={route}>Auditoria</NavLink>}
              {me.user.role === 'admin' && <NavLink to="aparencia" cur={route}>Aparência</NavLink>}
            </nav>
          )}
          <div className="ml-auto flex items-center gap-3 text-sm">
            <select aria-label="Tema" className="rounded-md border border-border bg-surface px-2 py-1 text-text" value={theme} onChange={(e) => setTheme(applyTheme(e.target.value, brand.themes))}>
              {THEMES.map((t) => <option key={t.id} value={t.id}>{t.label}</option>)}
              {brand.themes.map((t) => <option key={t.id} value={t.id}>{t.label}</option>)}
            </select>
            {phase === 'app' && me && (
              <>
                <a href="#/conta" className="text-muted hover:text-text">{me.user.display_name || me.user.username} · {ROLE_LABEL[me.user.role]}</a>
                <button className="text-primary hover:underline" onClick={logout}>Sair</button>
              </>
            )}
          </div>
        </div>
      </header>

      <main className="mx-auto w-full max-w-6xl flex-1 px-4 py-8">
        {phase === 'loading' && <p className="text-muted">Carregando…</p>}
        {phase === 'setup' && <Setup onDone={refresh} />}
        {phase === 'login' && <Login onDone={refresh} />}
        {phase === 'app' && me && (
          route === 'agentes' ? <Agents me={me.user} />
          : route === 'jobs' ? <Jobs me={me.user} />
          : route === 'nuvem' ? <Clouds me={me.user} />
          : route.split('?')[0] === 'historico' ? <Runs me={me.user} jobFilter={Number(new URLSearchParams(route.split('?')[1] ?? '').get('job') ?? 0)} />
          : route === 'usuarios' && me.user.role === 'admin' ? <Users me={me.user} />
          : route === 'auditoria' && me.user.role === 'admin' ? <Audit />
          : route === 'aparencia' && me.user.role === 'admin' ? <Appearance />
          : route === 'conta' ? <Account me={me.user} recoveryLeft={me.recovery_codes_left} reload={refresh} />
          : <Dashboard name={brand.name} />
        )}
      </main>

      <footer className="border-t border-border bg-surface py-4 text-center text-sm text-muted">
        {brand.name} - Versão {version} - Desenvolvido por{' '}
        <a className="text-primary underline" href="https://henrique.tec.br" target="_blank" rel="noopener noreferrer">Henrique Fagundes</a>
      </footer>
    </div>
    </BrandCtx.Provider>
  )
}

function NavLink({ to, cur, children }: { to: string; cur: string; children: React.ReactNode }) {
  const active = cur.split('?')[0] === to
  return <a href={`#/${to}`} aria-current={active ? 'page' : undefined} className={`rounded-md px-3 py-1.5 ${active ? 'bg-primary text-primary-contrast' : 'text-text hover:bg-bg'}`}>{children}</a>
}

type Health = { status: string; database: boolean }

function Dashboard({ name }: { name: string }) {
  const [health, setHealth] = useState<Health | null>(null)
  const [agents, setAgents] = useState<Agent[] | null>(null)
  const [stats, setStats] = useState<{ last_24h: Record<string, number>; jobs: number } | null>(null)
  useEffect(() => {
    const load = () => {
      api<{ last_24h: Record<string, number>; jobs: number }>('GET', '/api/stats').then(setStats).catch(() => {})
      fetch('/api/health').then((r) => r.json()).then(setHealth).catch(() => setHealth(null))
      api<{ agents: Agent[] }>('GET', '/api/agents').then((r) => setAgents(r.agents)).catch(() => {})
    }
    load(); const t = setInterval(load, 10000); return () => clearInterval(t)
  }, [])
  const active = agents?.filter((a) => !a.revoked_at) ?? []
  const online = active.filter((a) => a.online).length
  return (
    <div>
      <h1 className="text-2xl font-semibold">Painel</h1>
      <p className="mt-1 text-muted">Visão geral do {name}.</p>
      <section className="mt-6 grid gap-4 sm:grid-cols-3" aria-label="Estado do sistema">
        <Stat title="Server" value={health ? (health.status === 'ok' ? 'Online' : 'Degradado') : 'Sem resposta'} ok={health?.status === 'ok'} />
        <Stat title="Banco de dados" value={health ? (health.database ? 'Conectado' : 'Indisponível') : '…'} ok={!!health?.database} />
        <Stat title="Agentes online" value={agents ? `${online} de ${active.length}` : '…'} ok={agents ? (active.length === 0 ? undefined : online === active.length) : undefined} />
      </section>
      {stats && (
        <section className="mt-4 grid gap-4 sm:grid-cols-4" aria-label="Backups nas últimas 24 horas">
          <Stat title="Jobs cadastrados" value={String(stats.jobs)} />
          <Stat title="Sucesso (24 h)" value={String(stats.last_24h.ok + stats.last_24h.warning)} ok={stats.last_24h.failed + stats.last_24h.missed === 0} />
          <Stat title="Falhas / perdidos (24 h)" value={String(stats.last_24h.failed + stats.last_24h.missed)} ok={stats.last_24h.failed + stats.last_24h.missed === 0} />
          <Stat title="Em execução" value={String(stats.last_24h.running)} />
        </section>
      )}
      {agents && active.length === 0 && (
        <Card className="mt-6">
          <b>Comece adicionando um agente.</b>
          <p className="mt-1 text-sm text-muted">Vá em <a className="text-primary underline" href="#/agentes">Agentes</a> e gere um token de matrícula para o primeiro servidor.</p>
        </Card>
      )}
    </div>
  )
}

function Stat({ title, value, ok }: { title: string; value: string; ok?: boolean }) {
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
