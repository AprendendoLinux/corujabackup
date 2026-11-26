// Pasta do backup local: escolhida no disco do servidor do agente, com o espaço disponível.
import { useEffect, useRef, useState } from 'react'
import { api, ApiError } from './api'
import { FolderField, FolderList, SpaceBar } from './Picker'

type Disk = { entries: { name: string }[]; free?: number; total?: number; blocked?: string; exists?: boolean }

const browse = (agentId: number, path: string) => api<Disk>('GET', `/api/agents/${agentId}/browse?area=disk&path=${encodeURIComponent(path)}`)

/** A escolha da pasta e a medição do espaço chegaram na versão 0.0.14 do agente. */
export function agentHasDiskPicker(version = '') {
  const [a = 0, b = 0, c = 0] = version.split('-')[0].split('.').map(Number)
  return a > 0 || b > 0 || c >= 14
}

export function LocalDirField({ agentId, agentName, agentVersion, value, onChange, onUnavailable }: {
  agentId: number; agentName: string; agentVersion: string; value: string; onChange: (v: string) => void
  /** chamado quando a stack do agente não tem a montagem gravável: aí não se pode exigir a escolha */
  onUnavailable: () => void
}) {
  const versionOK = agentHasDiskPicker(agentVersion)
  const [noMount, setNoMount] = useState('')
  const supported = versionOK && !noMount
  const report = useRef(onUnavailable)
  report.current = onUnavailable
  useEffect(() => {
    let live = true
    if (versionOK) browse(agentId, '/').catch((x) => { const m = (x as ApiError).message; if (live && m.includes('/:/host-rw')) { setNoMount(m); report.current() } })
    return () => { live = false }
  }, [agentId, versionOK])
  const chosen = value.startsWith('/')
  const [space, setSpace] = useState<Disk | null>(null)
  const [err, setErr] = useState('')
  useEffect(() => {
    let live = true
    setSpace(null); setErr('')
    if (supported && chosen) browse(agentId, value).then((r) => live && setSpace(r)).catch((x) => live && setErr((x as ApiError).message))
    return () => { live = false }
  }, [agentId, value, supported, chosen])

  async function load(path: string): Promise<FolderList> {
    const r = await browse(agentId, '/' + path)
    return { folders: r.entries.map((e) => e.name), free: r.free, total: r.total, blocked: r.blocked }
  }
  const legacy = value && !chosen ? `pasta padrão do agente › ${value}` : ''
  return (
    <div className="space-y-2">
      <FolderField label="Pasta do backup local no servidor" value={chosen ? value.slice(1) : ''} autoText={legacy || (supported ? 'Escolha uma pasta do servidor…' : 'pasta padrão do agente')}
        prefix="/" resetLabel="Limpar a escolha" disabledReason={supported ? undefined : noMount ? `${noMount[0].toUpperCase()}${noMount.slice(1)}. Até lá, o backup local fica na pasta padrão dele.`
          : 'Para escolher a pasta e ver o espaço livre, atualize este agente para a versão 0.0.14 ou mais nova. Até lá, o backup local fica na pasta padrão dele.'}
        hint="Onde o arquivo do backup é gerado e onde ficam as cópias locais. A retenção só apaga os arquivos deste job."
        onChange={(v) => onChange(v ? '/' + v.replace(/^\/+/, '') : '')}
        picker={{ title: `Pasta do backup local em ${agentName}`, intro: 'Discos e pastas do servidor. O espaço mostrado é o do disco onde a pasta fica.', rootLabel: 'Servidor', load: (p) => load(p) }} />
      {space?.total ? <SpaceBar free={space.free ?? 0} total={space.total} /> : null}
      {space && !space.exists && <p className="text-xs text-muted">Esta pasta será criada no primeiro backup.</p>}
      {space?.blocked && <p className="text-xs text-danger">{space.blocked}</p>}
      {err && <p className="text-xs text-danger">{err}</p>}
    </div>
  )
}
