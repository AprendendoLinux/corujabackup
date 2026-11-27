// "Clicar e selecionar": árvore para marcar o que salvar (ou deixar de fora) e navegador para escolher uma pasta.
import { ReactNode, useCallback, useEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { api, ApiError } from './api'
import { Button, ErrorBox, Input, Modal } from './ui'

export type Entry = { name: string; dir: boolean; link?: boolean; size?: number }

const join = (parent: string, name: string) => (parent === '/' ? `/${name}` : parent ? `${parent}/${name}` : name)
const isUnder = (path: string, parent: string) => path !== parent && path.startsWith(parent === '/' ? '/' : `${parent}/`)

function ancestors(path: string): string[] {
  const out: string[] = []
  const parts = path.split('/').filter(Boolean)
  for (let i = 0; i < parts.length; i++) out.push('/' + parts.slice(0, i).join('/'))
  return out
}

export function size(n = 0) {
  if (n < 1024) return `${n} B`
  const u = ['KiB', 'MiB', 'GiB', 'TiB']
  let v = n / 1024, i = 0
  while (v >= 1024 && i < u.length - 1) { v /= 1024; i++ }
  return `${v.toFixed(1).replace('.', ',')} ${u[i]}`
}

const FolderIcon = () => (
  <svg viewBox="0 0 20 20" className="h-4 w-4 shrink-0 text-accent" fill="currentColor" aria-hidden="true"><path d="M2 5a2 2 0 0 1 2-2h3.6c.5 0 1 .2 1.4.6L10.4 5H16a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5z" /></svg>
)
const FileIcon = () => (
  <svg viewBox="0 0 20 20" className="h-4 w-4 shrink-0 text-muted" fill="none" stroke="currentColor" strokeWidth="1.5" aria-hidden="true"><path d="M5 2.75h6.5L15.25 6.5v10.75H5z" /><path d="M11.25 3v4h4" /></svg>
)

/** A janela do seletor vai para o <body>: assim não herda nada do formulário que a abriu. */
function Overlay({ title, onClose, children }: { title: string; onClose: () => void; children: ReactNode }) {
  return createPortal(<Modal title={title} onClose={onClose}>{children}</Modal>, document.body)
}

type Listing = { entries?: Entry[]; truncated?: boolean; error?: string }
type DirSize = { bytes: number; files: number; complete: boolean } | 'loading' | 'error'

/**
 * Tamanho das pastas, calculado pelo agente em segundo plano: no máximo dois pedidos por vez, os mais recentes (as
 * pastas que a pessoa acabou de abrir) primeiro. Agente antigo, sem a função: para de perguntar e não mostra nada.
 */
function useDirSizes(agentId: number) {
  const [sizes, setSizes] = useState<Record<string, DirSize>>({})
  const queue = useRef<string[]>([])
  const asked = useRef(new Set<string>())
  const busy = useRef(0)
  const off = useRef(false)
  const alive = useRef(true)
  useEffect(() => { alive.current = true; return () => { alive.current = false } }, [])
  const pump = useCallback(() => {
    while (!off.current && busy.current < 2 && queue.current.length > 0) {
      const p = queue.current.pop()!
      busy.current++
      setSizes((s) => ({ ...s, [p]: 'loading' }))
      api<{ bytes: number; files: number; complete: boolean }>('GET', `/api/agents/${agentId}/size?path=${encodeURIComponent(p)}`)
        .then((r) => { if (alive.current) setSizes((s) => ({ ...s, [p]: r })) })
        .catch((x) => {
          if ((x as ApiError).status === 422 && /versão antiga/.test((x as ApiError).message)) { off.current = true; queue.current = []; setSizes({}); return }
          if (alive.current) setSizes((s) => ({ ...s, [p]: 'error' }))
        })
        .finally(() => { busy.current--; if (alive.current) pump() })
    }
  }, [agentId])
  const want = useCallback((paths: string[]) => {
    const fresh = paths.filter((p) => !asked.current.has(p))
    if (fresh.length === 0 || off.current) return
    fresh.forEach((p) => asked.current.add(p))
    queue.current.push(...[...fresh].reverse()) // pop() pega do fim: assim a ordem da tela é respeitada
    pump()
  }, [pump])
  return { sizes, want }
}

function SizeTag({ s, file }: { s?: DirSize; file?: number }) {
  if (file !== undefined) return <span className="shrink-0 text-xs tabular-nums text-muted">{size(file)}</span>
  if (!s || s === 'error') return null
  if (s === 'loading') return <span className="shrink-0 text-xs text-muted">calculando…</span>
  return <span className="shrink-0 text-xs tabular-nums text-muted" title={`${s.files.toLocaleString('pt-BR')} arquivo(s)${s.complete ? '' : ' (contagem parcial: a pasta é grande demais para medir por completo)'}`}>{s.complete ? '' : 'mais de '}{size(s.bytes)}</span>
}

/** Árvore com caixas de marcar sobre o disco do agente. Marcar uma pasta inclui tudo o que há dentro dela. */
export function TreePicker({ title, intro, agentId, roots, initial, lockRoots, confirmLabel, onConfirm, onClose }: {
  title: string; intro: string; agentId: number; roots: string[]; initial: string[]; lockRoots?: boolean; confirmLabel: string
  onConfirm: (paths: string[]) => void; onClose: () => void
}) {
  const [selected, setSelected] = useState<Set<string>>(() => new Set(initial))
  const [open, setOpen] = useState<Set<string>>(() => new Set([...roots, ...initial.flatMap(ancestors).filter((a) => roots.some((r) => a === r || isUnder(a, r)))]))
  const [lists, setLists] = useState<Record<string, Listing>>({})
  const asked = useRef(new Set<string>())
  const { sizes, want } = useDirSizes(agentId)
  useEffect(() => {
    // a raiz da árvore e as subpastas de cada pasta aberta
    const dirs = [...roots]
    open.forEach((p) => lists[p]?.entries?.forEach((e) => { if (e.dir && !e.link) dirs.push(join(p, e.name)) }))
    want(dirs)
  }, [roots, open, lists, want])

  const fetchDir = useCallback((path: string) => {
    if (asked.current.has(path)) return
    asked.current.add(path)
    api<{ entries: Entry[]; truncated?: boolean }>('GET', `/api/agents/${agentId}/browse?area=host&path=${encodeURIComponent(path)}`)
      .then((r) => setLists((l) => ({ ...l, [path]: r })))
      .catch((x) => { asked.current.delete(path); setLists((l) => ({ ...l, [path]: { error: (x as ApiError).message } })) })
  }, [agentId])
  useEffect(() => { open.forEach(fetchDir) }, [open, fetchDir])

  function toggleOpen(path: string) {
    const next = new Set(open)
    if (next.has(path)) next.delete(path)
    else {
      next.add(path)
      if (lists[path]?.error) setLists((l) => { const c = { ...l }; delete c[path]; return c })
    }
    setOpen(next)
  }
  function toggle(path: string, on: boolean) {
    const next = new Set(selected)
    if (on) { next.forEach((p) => { if (isUnder(p, path)) next.delete(p) }); next.add(path) } else next.delete(path)
    setSelected(next)
  }

  function Node({ path, label, entry, depth }: { path: string; label: string; entry: Entry; depth: number }) {
    const covered = [...selected].find((s) => isUnder(path, s))
    const partial = !covered && !selected.has(path) && [...selected].some((s) => isUnder(s, path))
    const list = lists[path]
    // uma raiz da árvore de exclusões pode ser um arquivo: só se descobre ao tentar listá-la
    const isFile = !entry.dir || (depth === 0 && !!list?.error?.endsWith('não é uma pasta'))
    const expandable = !isFile && !entry.link
    const expanded = open.has(path)
    const locked = lockRoots && depth === 0
    return (
      <li>
        <div className="flex items-center gap-1.5 rounded-md py-1 pr-2 hover:bg-bg" style={{ paddingLeft: depth * 20 + 4 }}>
          {expandable ? (
            <button type="button" className="flex h-5 w-5 items-center justify-center rounded text-muted hover:text-text" aria-label={`${expanded ? 'Fechar' : 'Abrir'} ${path}`} aria-expanded={expanded} onClick={() => toggleOpen(path)}>
              <span className={`inline-block transition-transform ${expanded ? 'rotate-90' : ''}`}>▸</span>
            </button>
          ) : <span className="w-5" />}
          <label className={`flex min-w-0 flex-1 items-center gap-2 ${covered || locked ? 'cursor-default' : 'cursor-pointer'}`} title={covered ? `Já incluído pela pasta ${covered}` : path}>
            {locked ? <span className="w-4" /> : (
              <input type="checkbox" className="h-4 w-4 accent-primary" aria-label={path} checked={!!covered || selected.has(path)} disabled={!!covered}
                ref={(el) => { if (el) el.indeterminate = partial }} onChange={(e) => toggle(path, e.target.checked)} />
            )}
            {isFile ? <FileIcon /> : <FolderIcon />}
            <span className={`truncate ${covered ? 'text-muted' : ''}`}>{label}</span>
            {entry.link && <span className="shrink-0 text-xs text-muted">atalho (salva só o atalho)</span>}
          </label>
          {!entry.link && (isFile ? (entry.dir ? null : <SizeTag file={entry.size ?? 0} />) : <SizeTag s={sizes[path]} />)}
        </div>
        {expandable && expanded && (
          <ul>
            {!list && <li className="py-1 text-xs text-muted" style={{ paddingLeft: depth * 20 + 49 }}>Consultando o agente…</li>}
            {list?.error && <li className="py-1 pr-2 text-xs text-danger" style={{ paddingLeft: depth * 20 + 49 }}>{list.error}</li>}
            {list?.entries?.length === 0 && <li className="py-1 text-xs text-muted" style={{ paddingLeft: depth * 20 + 49 }}>pasta vazia</li>}
            {list?.entries?.map((e) => <li key={e.name}><ul>{Node({ path: join(path, e.name), label: e.name, entry: e, depth: depth + 1 })}</ul></li>)}
            {list?.truncated && <li className="py-1 text-xs text-muted" style={{ paddingLeft: depth * 20 + 49 }}>Há itens demais nesta pasta: só os primeiros aparecem.</li>}
          </ul>
        )}
      </li>
    )
  }

  const chosen = [...selected].sort()
  // total estimado do que está marcado: arquivos pelo tamanho da listagem, pastas pelo cálculo do agente
  let total = 0, pending = false, partial = false
  for (const p of chosen) {
    const cut = p.lastIndexOf('/')
    const e = lists[p.slice(0, cut) || '/']?.entries?.find((x) => x.name === p.slice(cut + 1))
    if (e && !e.dir) { total += e.size ?? 0; continue }
    const s = sizes[p]
    if (s && s !== 'loading' && s !== 'error') { total += s.bytes; if (!s.complete) partial = true } else pending = true
  }
  useEffect(() => { want(chosen.filter((p) => !lists[p.slice(0, p.lastIndexOf('/')) || '/']?.entries?.some((x) => x.name === p.slice(p.lastIndexOf('/') + 1) && !x.dir))) })
  return (
    <Overlay title={title} onClose={onClose}>
      <div className="space-y-3 text-sm">
        <p className="text-muted">{intro}</p>
        <div className="scroll-fino h-[46vh] overflow-auto rounded-lg border border-border p-1.5">
          {/* Node é chamado como função, e não como componente, para a árvore não se remontar a cada clique */}
          <ul>{roots.map((r) => <li key={r}><ul>{Node({ path: r, label: r === '/' ? '/ (tudo)' : r, entry: { name: r, dir: true }, depth: 0 })}</ul></li>)}</ul>
        </div>
        <div className="flex flex-wrap items-center justify-between gap-3">
          <span className="text-muted">{chosen.length === 0 ? 'Nada marcado.' : <>{chosen.length} item(ns) marcado(s){total > 0 || !pending ? <> · <b className="text-text">{partial ? 'mais de ' : '~'}{size(total)}</b></> : null}{pending ? ' · calculando…' : ''}</>}</span>
          <div className="flex gap-2">
            <Button type="button" variant="ghost" onClick={onClose}>Cancelar</Button>
            <Button type="button" onClick={() => onConfirm(chosen)}>{confirmLabel}</Button>
          </div>
        </div>
      </div>
    </Overlay>
  )
}

/** O que um navegador de pastas recebe de cada pasta: as subpastas e, no disco do agente, o espaço disponível. */
export type DiskInfo = { path: string; fs: string; total: number; free: number; used?: number; read_only?: boolean }
export type FolderList = { folders: string[]; free?: number; total?: number; used?: number; blocked?: string; disks?: DiskInfo[]; mount?: string; fs?: string }

/** Percentual em uso como o df: ocupado / (ocupado + livre), sem o espaço reservado ao root. */
export function usedPct(free: number, total: number, used?: number) {
  if (used !== undefined && used + free > 0) return Math.round((used / (used + free)) * 100)
  return total > 0 ? Math.round(((total - free) / total) * 100) : 0
}

/** Partições montadas na máquina do agente, com a barra de uso; clicar abre a partição. */
function Disks({ disks, onOpen }: { disks: DiskInfo[]; onOpen: (path: string) => void }) {
  return (
    <div>
      <span className="mb-1.5 block text-sm font-medium">Partições desta máquina</span>
      <ul className="grid gap-2 sm:grid-cols-2">
        {disks.map((d) => {
          const used = usedPct(d.free, d.total, d.used)
          const low = d.total > 0 && d.free / d.total < 0.1
          return (
            <li key={d.path}>
              <button type="button" onClick={() => onOpen(d.path.replace(/^\/+/, ''))} disabled={d.read_only}
                title={d.read_only ? 'Somente leitura: não recebe backups' : `Abrir ${d.path}`}
                className="w-full rounded-xl border border-border p-3 text-left transition hover:border-primary/60 disabled:cursor-not-allowed disabled:opacity-60">
                <div className="flex items-baseline justify-between gap-2">
                  <b className="truncate font-mono text-sm">{d.path}</b>
                  <span className={`text-xs font-semibold ${low ? 'text-danger' : 'text-muted'}`}>{used}%</span>
                </div>
                <div className="mt-1.5 h-2 overflow-hidden rounded-full bg-border" role="img" aria-label={`${d.path}: ${used}% em uso`}>
                  <div className={`h-full ${low ? 'bg-danger' : used > 80 ? 'bg-accent' : 'bg-primary'}`} style={{ width: `${used}%` }} />
                </div>
                <div className="mt-1 text-xs text-muted">{size(d.free)} livres de {size(d.total)} · {d.fs}{d.read_only ? ' · somente leitura' : ''}</div>
              </button>
            </li>
          )
        })}
      </ul>
    </div>
  )
}

export function SpaceBar({ free, total, usedBytes, mount, fs }: { free: number; total: number; usedBytes?: number; mount?: string; fs?: string }) {
  const used = Math.min(100, Math.max(0, usedPct(free, total, usedBytes)))
  const low = total > 0 && free / total < 0.1
  return (
    <div className="space-y-1">
      <div className="h-1.5 overflow-hidden rounded-full bg-border" role="img" aria-label={`${used}% do disco em uso`}>
        <div className={`h-full ${low ? 'bg-danger' : 'bg-primary'}`} style={{ width: `${used}%` }} />
      </div>
      <span className={`block text-xs ${low ? 'text-danger' : 'text-muted'}`}>{mount && <>Partição <b className="font-mono text-text">{mount}</b>{fs ? ` (${fs})` : ''} · </>}<b>{size(free)} livres</b> de {size(total)} ({used}% em uso){low ? ' · pouco espaço' : ''}</span>
    </div>
  )
}

const folderNameOK = (n: string, strict: boolean) => (strict ? /^[A-Za-z0-9._-]+$/.test(n) : !/[\\/:*?"<>|]/.test(n)) && n !== '.' && n !== '..' && n.length <= 200

/** Navegador para escolher UMA pasta. load("") lista a raiz; os caminhos são relativos a ela. */
export function FolderPicker({ title, intro, rootLabel, initial, strictNames, load, onPick, onClose }: {
  title: string; intro: string; rootLabel: string; initial: string; strictNames?: boolean
  load: (path: string) => Promise<string[] | FolderList>; onPick: (path: string) => void; onClose: () => void
}) {
  const [path, setPath] = useState(initial)
  const [folders, setFolders] = useState<string[] | null>(null)
  const [info, setInfo] = useState<FolderList | null>(null)
  const [err, setErr] = useState('')
  const [name, setName] = useState('')
  const [created, setCreated] = useState<string[]>([]) // pastas novas: ainda não existem no destino
  const loader = useRef(load)
  loader.current = load

  useEffect(() => {
    let live = true
    setFolders(null); setErr(''); setInfo(null)
    // uma pasta recém-criada também é consultada: o agente responde com o espaço do disco onde ela ficará
    loader.current(path)
      .then((r) => { if (!live) return; if (Array.isArray(r)) setFolders(r); else { setInfo(r); setFolders(r.folders) } })
      .catch((x) => { if (live) { if (!created.includes(path)) setErr((x as ApiError).message); setFolders([]) } })
    return () => { live = false }
  }, [path, created])

  const parts = path ? path.split('/') : []
  const isNew = created.includes(path)
  function addFolder() {
    const n = name.trim()
    if (!n) return
    if (!folderNameOK(n, !!strictNames)) { setErr(strictNames ? 'Use só letras sem acento, números, ponto, hífen e sublinhado.' : 'O nome não pode ter / \\ : * ? " < > |'); return }
    const full = join(path, n)
    if (!folders?.includes(n)) setCreated([...created, full])
    setName(''); setPath(full)
  }
  const crumb = 'rounded px-1.5 py-0.5 hover:bg-bg'

  return (
    <Overlay title={title} onClose={onClose}>
      <div className="space-y-3 text-sm">
        <p className="text-muted">{intro}</p>
        {!path && info?.disks && info.disks.length > 0 && <Disks disks={info.disks} onOpen={setPath} />}
        <nav aria-label="Caminho" className="flex flex-wrap items-center gap-0.5">
          <button type="button" className={`${crumb} text-primary`} onClick={() => setPath('')}>{rootLabel}</button>
          {parts.map((p, i) => (
            <span key={i} className="flex items-center gap-0.5"><span className="text-muted">/</span>
              <button type="button" className={`${crumb} ${i === parts.length - 1 ? 'font-semibold' : 'text-primary'}`} onClick={() => setPath(parts.slice(0, i + 1).join('/'))}>{p}</button>
            </span>
          ))}
        </nav>
        <ul className={`scroll-fino ${!path && info?.disks?.length ? "h-[22vh]" : "h-[34vh]"} overflow-auto rounded-lg border border-border p-1.5`}>
          {path && <li><button type="button" className="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-muted hover:bg-bg" onClick={() => setPath(parts.slice(0, -1).join('/'))}>↑ voltar</button></li>}
          {folders === null && <li className="px-2 py-1.5 text-muted">Consultando…</li>}
          {folders?.map((f) => (
            <li key={f}><button type="button" className="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left hover:bg-bg" onClick={() => setPath(join(path, f))}><FolderIcon /><span className="truncate">{f}</span></button></li>
          ))}
          {folders?.length === 0 && !err && <li className="px-2 py-1.5 text-muted">{isNew ? 'Pasta nova: será criada no primeiro backup.' : 'Nenhuma subpasta aqui.'}</li>}
        </ul>
        {info?.total ? <SpaceBar free={info.free ?? 0} total={info.total} usedBytes={info.used} mount={info.mount} fs={info.fs} /> : null}
        {info?.blocked && path && <p className="text-xs text-danger">{info.blocked}</p>}
        <ErrorBox message={err} />
        <div className="flex gap-2">
          <Input aria-label="Nome da nova pasta" placeholder="Nome de uma nova pasta" value={name} onChange={(e) => setName(e.target.value)}
            onKeyDown={(e) => { if (e.key === 'Enter') { e.preventDefault(); addFolder() } }} />
          <Button type="button" variant="ghost" className="whitespace-nowrap" onClick={addFolder} disabled={!name.trim()}>+ Criar pasta</Button>
        </div>
        <div className="flex flex-wrap items-center justify-between gap-3">
          <span className="min-w-0 truncate text-muted">{path ? <>Selecionada: <b className="text-text">{path}</b></> : 'Entre numa pasta ou crie uma nova.'}</span>
          <div className="flex gap-2">
            <Button type="button" variant="ghost" onClick={onClose}>Cancelar</Button>
            <Button type="button" onClick={() => onPick(path)} disabled={!path || !!info?.blocked || !!err || folders === null}>Usar esta pasta</Button>
          </div>
        </div>
      </div>
    </Overlay>
  )
}

/** Campo de pasta: mostra a escolha (ou o padrão automático) e abre o navegador. Não se digita o caminho. */
export function FolderField({ label, hint, value, autoText, disabledReason, resetLabel = 'Voltar para automática', prefix = '', onChange, picker }: {
  label: string; hint: string; value: string; autoText: string; disabledReason?: string; resetLabel?: string; prefix?: string
  onChange: (v: string) => void; picker: { title: string; intro: string; rootLabel: string; strictNames?: boolean; load: (path: string) => Promise<string[] | FolderList> }
}) {
  const [open, setOpen] = useState(false)
  return (
    <div className="text-sm">
      <span className="mb-1 block font-medium">{label}</span>
      <div className="flex gap-2">
        <button type="button" aria-label={`${label}: escolher`} disabled={!!disabledReason} onClick={() => setOpen(true)}
          className="flex min-w-0 flex-1 items-center gap-2 rounded-lg border border-border bg-surface px-3 py-2 text-left hover:bg-bg disabled:cursor-not-allowed disabled:opacity-60">
          <FolderIcon />
          <span className={`truncate ${value ? '' : 'text-muted'}`} title={value && prefix + value}>{value ? prefix + value : autoText}</span>
          <span className="ml-auto shrink-0 text-xs text-primary">Escolher…</span>
        </button>
        {value && <Button type="button" variant="ghost" aria-label={`${label}: voltar para automática`} title={resetLabel} onClick={() => onChange('')}>✕</Button>}
      </div>
      <span className="mt-1 block text-xs text-muted">{disabledReason ?? hint}</span>
      {open && <FolderPicker {...picker} initial={value} onClose={() => setOpen(false)} onPick={(v) => { onChange(v); setOpen(false) }} />}
    </div>
  )
}

/** O que um item da lista é no agente. "pattern" é um padrão de exclusão (*.tmp); "unknown", quando não deu para conferir. */
export type Kind = 'dir' | 'file' | 'link' | 'pattern' | 'missing' | 'unknown'

const isPattern = (p: string) => !p.startsWith('/') || /[*?]/.test(p)

/**
 * Descobre o que é cada caminho listando a pasta que o contém. Assim vale também para jobs já salvos e caminhos digitados.
 * Enquanto consulta, o item fica sem tipo.
 */
function useKinds(agentId: number, items: string[]): Record<string, Kind> {
  const [kinds, setKinds] = useState<Record<string, Kind>>({})
  const key = items.join('\n')
  useEffect(() => {
    let live = true
    const set = (p: string, k: Kind) => live && setKinds((c) => ({ ...c, [p]: k }))
    setKinds({})
    const parents = new Map<string, { path: string; name: string }[]>()
    for (const p of key ? key.split('\n') : []) {
      if (isPattern(p)) { set(p, 'pattern'); continue }
      const clean = p.replace(/\/+$/, '')
      if (!clean) { set(p, 'dir'); continue }
      const cut = clean.lastIndexOf('/')
      const parent = clean.slice(0, cut) || '/'
      parents.set(parent, [...(parents.get(parent) ?? []), { path: p, name: clean.slice(cut + 1) }])
    }
    parents.forEach((children, parent) => {
      api<{ entries: Entry[]; truncated?: boolean }>('GET', `/api/agents/${agentId}/browse?area=host&path=${encodeURIComponent(parent)}`)
        .then((r) => children.forEach((c) => {
          const e = r.entries.find((x) => x.name === c.name)
          set(c.path, e ? (e.link ? 'link' : e.dir ? 'dir' : 'file') : r.truncated ? 'unknown' : 'missing')
        }))
        .catch((x) => children.forEach((c) => set(c.path, /não existe|não é uma pasta/.test((x as ApiError).message) ? 'missing' : 'unknown')))
    })
    return () => { live = false }
  }, [agentId, key])
  return kinds
}

const KIND: Record<Kind, { label: string; title: string; tag: string }> = {
  dir: { label: 'Pasta', title: 'Pasta: entra tudo o que há dentro dela', tag: 'border-accent/60 bg-accent/15 text-text' },
  file: { label: 'Arquivo', title: 'Arquivo: só ele', tag: 'border-border bg-bg text-muted' },
  link: { label: 'Atalho', title: 'Atalho: entra só o atalho, não o conteúdo para onde ele aponta', tag: 'border-border bg-bg text-muted' },
  pattern: { label: 'Padrão', title: 'Padrão: vale para tudo o que combinar com ele', tag: 'border-primary/40 bg-primary/10 text-text' },
  missing: { label: 'Não encontrado', title: 'Este caminho não existe no agente agora', tag: 'border-danger/40 bg-danger/10 text-danger' },
  unknown: { label: 'Não conferido', title: 'Não foi possível consultar o agente (offline, versão antiga ou sem permissão)', tag: 'border-dashed border-border text-muted' },
}

const LinkIcon = () => (
  <svg viewBox="0 0 20 20" className="h-4 w-4 shrink-0 text-muted" fill="none" stroke="currentColor" strokeWidth="1.5" aria-hidden="true"><path d="M5 2.75h6.5L15.25 6.5v10.75H5z" /><path d="M7.5 14c0-2.5 1.5-4 4.500-4M10 8.500l2 1.500-2 1.500" /></svg>
)
const PatternIcon = () => <span className="w-4 shrink-0 text-center font-mono text-sm font-bold text-primary" aria-hidden="true">✱</span>

function KindIcon({ kind }: { kind?: Kind }) {
  if (kind === 'dir') return <FolderIcon />
  if (kind === 'file') return <FileIcon />
  if (kind === 'link') return <LinkIcon />
  if (kind === 'pattern') return <PatternIcon />
  return <span className="w-4 shrink-0 text-center text-muted" aria-hidden="true">{kind === 'missing' ? '!' : '·'}</span>
}

/** Lista de itens escolhidos, com remover, o botão que abre a árvore e (recolhida) a digitação, para casos especiais. */
export function PathList({ agentId, label, hint, items, empty, pickLabel, pickDisabled, typeLabel, placeholder, onPick, onChange }: {
  agentId: number; label: string; hint: string; items: string[]; empty: string; pickLabel: string; pickDisabled?: string
  typeLabel: string; placeholder: string; onPick: () => void; onChange: (items: string[]) => void
}) {
  const kinds = useKinds(agentId, items)
  const [typing, setTyping] = useState(false)
  const [text, setText] = useState('')
  function add() {
    const v = text.trim()
    if (v && !items.includes(v)) onChange([...items, v])
    setText('')
  }
  return (
    <div className="text-sm" role="group" aria-label={label}>
      <span className="mb-1 block font-medium">{label}</span>
      <div className="rounded-lg border border-border">
        {items.length === 0 ? <p className="px-3 py-2.5 text-muted">{empty}</p> : (
          <ul className="scroll-fino max-h-44 overflow-y-auto divide-y divide-border">
            {items.map((p) => (
              <li key={p} className="flex items-center gap-2 px-3 py-1.5" title={kinds[p] ? KIND[kinds[p]].title : undefined}>
                <KindIcon kind={kinds[p]} />
                <span data-path={p} className={`min-w-0 flex-1 break-all font-mono text-xs ${kinds[p] === 'dir' ? 'font-semibold' : ''}`}>{p}{kinds[p] === 'dir' && !p.endsWith('/') ? '/' : ''}</span>
                {kinds[p] && <span className={`shrink-0 rounded-full border px-2 py-0.5 text-[11px] leading-none ${KIND[kinds[p]].tag}`}>{KIND[kinds[p]].label}</span>}
                <button type="button" aria-label={`Remover ${p}`} className="text-muted hover:text-danger" onClick={() => onChange(items.filter((x) => x !== p))}>✕</button>
              </li>
            ))}
          </ul>
        )}
        <div className="flex flex-wrap items-center gap-x-3 gap-y-2 border-t border-border p-2">
          <Button type="button" variant="ghost" onClick={onPick} disabled={!!pickDisabled} title={pickDisabled}>{pickLabel}</Button>
          {!typing && <button type="button" className="text-xs text-primary hover:underline" onClick={() => setTyping(true)}>{typeLabel}</button>}
          {typing && (
            <div className="flex min-w-60 flex-1 gap-2">
              <Input autoFocus aria-label={typeLabel} className="font-mono text-xs" placeholder={placeholder} value={text} onChange={(e) => setText(e.target.value)}
                onBlur={add} onKeyDown={(e) => { if (e.key === 'Enter') { e.preventDefault(); add() } }} />
              <Button type="button" variant="ghost" onClick={add}>Adicionar</Button>
            </div>
          )}
        </div>
      </div>
      <span className="mt-1 block text-xs text-muted">{hint}</span>
    </div>
  )
}
