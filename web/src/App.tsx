import { useCallback, useEffect, useState } from 'react'
import Logo, { BrandCtx, useFavicon } from './Logo'
import { applyTheme, forgetLocalTheme, onSystemModeChange } from './themes'
import { Agent, Branding, User, api, ApiError, ROLE_LABEL } from './api'
import { Button, Card } from './ui'
import Icon, { IconName } from './Icon'
import TipLayer from './TipLayer'
import Avatar from './Avatar'
import DialogHost from './Dialogs'
import { Login, Setup } from './pages/Auth'
import Agents from './pages/Agents'
import Jobs from './pages/Jobs'
import Clouds from './pages/Clouds'
import Runs from './pages/Runs'
import Users from './pages/Users'
import Audit from './pages/Audit'
import Account from './pages/Account'
import Appearance from './pages/Appearance'
import Security from './pages/Security'
import Notifications from './pages/Notifications'

type Me = { user: User; recovery_codes_left: number; version: string }
type Phase = 'loading' | 'setup' | 'login' | 'app'

function useRoute() {
  const get = () => (window.location.hash.replace(/^#\/?/, '') || 'painel')
  const [r, setR] = useState(get)
  useEffect(() => { const h = () => setR(get()); window.addEventListener('hashchange', h); return () => window.removeEventListener('hashchange', h) }, [])
  return r
}

type NavItem = { to: string; label: string; icon: IconName; admin?: boolean }
const NAV: { title: string; items: NavItem[] }[] = [
  { title: 'Operação', items: [
    { to: 'painel', label: 'Painel', icon: 'painel' }, { to: 'agentes', label: 'Agentes', icon: 'agentes' },
    { to: 'jobs', label: 'Jobs', icon: 'jobs' }, { to: 'nuvem', label: 'Destinos', icon: 'destinos' }, { to: 'historico', label: 'Histórico', icon: 'historico' },
  ] },
  { title: 'Administração', items: [
    { to: 'usuarios', label: 'Usuários', icon: 'usuarios', admin: true }, { to: 'notificacoes', label: 'Notificações', icon: 'notificacoes', admin: true },
    { to: 'seguranca', label: 'Segurança', icon: 'seguranca', admin: true }, { to: 'auditoria', label: 'Auditoria', icon: 'auditoria', admin: true },
    { to: 'aparencia', label: 'Aparência', icon: 'aparencia', admin: true },
  ] },
]

function readCollapsed() { try { return localStorage.getItem('coruja.menu') === 'recolhido' } catch { return false } }

export default function App() {
  const [brand, setBrand] = useState<Branding>({ name: 'Coruja Backup', theme: 'coruja', themes: [], has_logo: false })
  const [phase, setPhase] = useState<Phase>('loading')
  const [me, setMe] = useState<Me | null>(null)
  const [version, setVersion] = useState('0.0.16')
  const [drawer, setDrawer] = useState(false)
  const [collapsed, setCollapsed] = useState(readCollapsed)
  const route = useRoute()
  const page = route.split('?')[0]
  useFavicon(brand)

  const loadBrand = useCallback(() => {
    fetch('/api/branding').then((r) => r.json()).then((b: Branding) => {
      b.themes = b.themes ?? []
      setBrand(b); applyTheme(b.theme, b.themes, b.mode ?? 'auto')
    }).catch(() => applyTheme('coruja'))
  }, [])
  useEffect(() => {
    forgetLocalTheme()
    loadBrand()
    fetch('/api/version').then((r) => r.json()).then((v) => setVersion(v.version)).catch(() => {})
  }, [loadBrand])
  useEffect(() => { document.title = brand.name }, [brand])
  // no modo Automático, acompanha a troca de claro/escuro do sistema sem recarregar a página
  useEffect(() => onSystemModeChange(() => {
    if ((brand.mode ?? 'auto') === 'auto' && !window.location.hash.startsWith('#/aparencia')) applyTheme(brand.theme, brand.themes, 'auto')
  }), [brand])
  useEffect(() => { setDrawer(false) }, [route])
  useEffect(() => { try { localStorage.setItem('coruja.menu', collapsed ? 'recolhido' : 'aberto') } catch { /* tudo bem */ } }, [collapsed])

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

  const footer = <Footer brand={brand} version={version} />

  if (phase !== 'app' || !me) {
    return (
      <BrandCtx.Provider value={{ brand, reload: loadBrand }}>
        <div className="flex min-h-screen flex-col">
          <main className="flex flex-1 items-center justify-center px-4 py-10">
            {phase === 'loading' && <p className="text-center text-muted">Carregando…</p>}
            {phase === 'setup' && <Setup onDone={refresh} />}
            {phase === 'login' && <Login onDone={refresh} />}
          </main>
          {footer}
        </div>
        <TipLayer />
      <DialogHost />
      </BrandCtx.Provider>
    )
  }

  const isAdmin = me.user.role === 'admin'
  const sidebar = (mini: boolean) => (
    <div className="flex h-full flex-col">
      <div className={`flex items-center gap-3 border-b border-border py-4 ${mini ? 'justify-center px-2' : 'px-5'}`}>
        <a href="#/" className="flex min-w-0 items-center gap-3" aria-label={`${brand.name}: painel`}>
          <Logo size={mini ? 32 : 36} />
          {!mini && <span className="truncate text-base font-semibold">{brand.name}</span>}
        </a>
      </div>
      <nav className="scroll-fino flex-1 overflow-y-auto px-3 py-4" aria-label="Principal">
        {NAV.map((g) => {
          const items = g.items.filter((i) => !i.admin || isAdmin)
          if (items.length === 0) return null
          return (
            <div key={g.title} className="mb-5">
              {mini ? <div className="mx-auto mb-2 h-px w-6 bg-border" /> : <div className="mb-1.5 px-3 text-[11px] font-semibold uppercase tracking-wider text-muted">{g.title}</div>}
              <ul className="space-y-0.5">
                {items.map((i) => {
                  const active = page === i.to || (i.to === 'painel' && page === '')
                  return (
                    <li key={i.to}>
                      <a href={`#/${i.to}`} aria-current={active ? 'page' : undefined} data-tip={mini ? i.label : undefined} aria-label={mini ? i.label : undefined}
                        className={`flex items-center gap-3 rounded-xl py-2 text-sm font-medium transition ${mini ? 'justify-center px-2' : 'px-3'} ${
                          active ? 'bg-primary text-primary-contrast shadow-sm' : 'text-text hover:bg-primary/10 hover:text-primary'}`}>
                        <Icon name={i.icon} size={19} />{!mini && <span className="truncate">{i.label}</span>}
                      </a>
                    </li>
                  )
                })}
              </ul>
            </div>
          )
        })}
      </nav>
      <div className={`border-t border-border p-3 ${mini ? 'space-y-1' : ''}`}>
        <div className={`flex items-center gap-2 ${mini ? 'flex-col' : ''}`}>
          <a href="#/conta" data-tip={mini ? 'Minha conta' : undefined} aria-label="Minha conta"
            className={`flex min-w-0 flex-1 items-center gap-3 rounded-xl p-2 transition hover:bg-primary/10 ${page === 'conta' ? 'bg-primary/10' : ''}`}>
            <Avatar user={me.user} size={36} />
            {!mini && <span className="min-w-0"><span className="block truncate text-sm font-medium">{me.user.display_name || me.user.username}</span><span className="block text-xs text-muted">{ROLE_LABEL[me.user.role]}</span></span>}
          </a>
          <button type="button" onClick={logout} aria-label="Sair" data-tip="Sair" className="flex h-9 w-9 items-center justify-center rounded-lg text-muted transition hover:bg-danger/10 hover:text-danger"><Icon name="sair" /></button>
        </div>
      </div>
    </div>
  )

  return (
    <BrandCtx.Provider value={{ brand, reload: loadBrand }}>
      <div className="flex min-h-screen">
        {/* menu lateral fixo (telas grandes) */}
        <aside className={`sticky top-0 hidden h-screen shrink-0 border-r border-border bg-surface transition-[width] lg:block ${collapsed ? 'w-[76px]' : 'w-64'}`}>
          {sidebar(collapsed)}
          <button type="button" onClick={() => setCollapsed(!collapsed)} aria-label={collapsed ? 'Expandir o menu' : 'Recolher o menu'} data-tip={collapsed ? 'Expandir o menu' : 'Recolher o menu'}
            className="absolute -right-3 top-6 flex h-6 w-6 items-center justify-center rounded-full border border-border bg-surface text-muted shadow-sm hover:text-primary">
            <Icon name={collapsed ? 'expandir' : 'recolher'} size={14} />
          </button>
        </aside>
        {/* menu em gaveta (celular e telas pequenas) */}
        {drawer && (
          <div className="fixed inset-0 z-40 lg:hidden">
            <div className="absolute inset-0 bg-black/40" onClick={() => setDrawer(false)} />
            <aside className="absolute inset-y-0 left-0 w-72 max-w-[85vw] border-r border-border bg-surface shadow-xl">
              <button type="button" onClick={() => setDrawer(false)} aria-label="Fechar o menu" className="absolute right-3 top-4 flex h-8 w-8 items-center justify-center rounded-lg text-muted hover:bg-bg"><Icon name="fechar" /></button>
              {sidebar(false)}
            </aside>
          </div>
        )}
        <div className="flex min-w-0 flex-1 flex-col">
          <header className="sticky top-0 z-30 flex items-center gap-3 border-b border-border bg-surface/90 px-4 py-3 backdrop-blur lg:hidden">
            <button type="button" onClick={() => setDrawer(true)} aria-label="Abrir o menu" className="flex h-9 w-9 items-center justify-center rounded-lg hover:bg-bg"><Icon name="menu" /></button>
            <a href="#/" className="flex min-w-0 items-center gap-2"><Logo size={28} /><span className="truncate font-semibold">{brand.name}</span></a>
          </header>
          <main className="mx-auto w-full max-w-6xl flex-1 px-4 py-8 sm:px-8">
            {page === 'agentes' ? <Agents me={me.user} />
              : page === 'jobs' ? <Jobs me={me.user} />
              : page === 'nuvem' ? <Clouds me={me.user} />
              : page === 'historico' ? <Runs me={me.user} jobFilter={Number(new URLSearchParams(route.split('?')[1] ?? '').get('job') ?? 0)} />
              : page === 'usuarios' && isAdmin ? <Users me={me.user} />
              : page === 'auditoria' && isAdmin ? <Audit />
              : page === 'notificacoes' && isAdmin ? <Notifications />
              : page === 'seguranca' && isAdmin ? <Security />
              : page === 'aparencia' && isAdmin ? <Appearance />
              : page === 'conta' ? <Account me={me.user} recoveryLeft={me.recovery_codes_left} reload={refresh} />
              : <Dashboard name={brand.name} canReport={me.user.role !== 'viewer'} />}
          </main>
          {footer}
        </div>
      </div>
      <TipLayer />
      <DialogHost />
    </BrandCtx.Provider>
  )
}

/** Rodapé: nome e versão do sistema e os créditos configurados em Aparência. */
export function Footer({ brand, version }: { brand: Branding; version: string }) {
  const c = brand.credit ?? { label: 'Desenvolvido por', name: 'Henrique Fagundes', url: 'https://henrique.tec.br' }
  return (
    <footer className="border-t border-border px-4 py-4 text-center text-sm text-muted">
      {brand.name} - Versão {version}
      {c.name && <> - {c.label ? `${c.label} ` : ''}{c.url ? <a className="text-primary underline" href={c.url} target="_blank" rel="noopener noreferrer">{c.name}</a> : c.name}</>}
    </footer>
  )
}

type Health = { status: string; database: boolean }

function Dashboard({ name, canReport }: { name: string; canReport: boolean }) {
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
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h1 className="text-2xl font-semibold">Painel</h1>
          <p className="mt-1 text-muted">Visão geral do {name}.</p>
        </div>
        {canReport && <a href="/api/reports/daily.pdf" target="_blank" rel="noopener noreferrer"><Button variant="ghost" className="gap-2"><Icon name="pdf" />Relatório de hoje (PDF)</Button></a>}
      </div>
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
          <p className="mt-1 text-sm text-muted">Vá em <a className="text-primary underline" href="#/agentes">Agentes</a> e gere um token de matrícula para o primeiro agente.</p>
        </Card>
      )}
    </div>
  )
}

function Stat({ title, value, ok }: { title: string; value: string; ok?: boolean }) {
  return (
    <div className="rounded-2xl border border-border bg-surface p-5 shadow-sm">
      <div className="text-sm text-muted">{title}</div>
      <div className="mt-1 flex items-center gap-2 text-lg font-medium">
        {ok !== undefined && <span aria-hidden className={`inline-block h-2.5 w-2.5 rounded-full ${ok ? 'bg-ok' : 'bg-danger'}`} />}
        {value}
      </div>
    </div>
  )
}
