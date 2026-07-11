import {useEffect, useState} from 'react'
import {GetConfig, SaveTunnels} from '../../wailsjs/go/app/App'
import {config} from '../../wailsjs/go/models'
import {
  Badge, Button, Card, EmptyState, ErrorBanner, Field, IconButton, Modal,
  Select, TextInput, Toggle,
} from '../components/ui'
import {IconEdit, IconPlus, IconServer, IconTrash, IconTunnels} from '../components/icons'
import {UIStatus} from '../state'

type Tunnel = config.Tunnel

function newTunnelID(): string {
  const b = new Uint8Array(16)
  crypto.getRandomValues(b)
  return Array.from(b, x => x.toString(16).padStart(2, '0')).join('')
}

function blankTunnel(): Tunnel {
  return config.Tunnel.createFrom({
    ID: newTunnelID(), Name: 'Minecraft', Type: 'tcp',
    LocalAddr: '127.0.0.1:25565', PublicPort: 25565, Enabled: true,
    Options: {MinecraftAware: false, ProxyProtocolV2: false, OfflineMOTD: '', BandwidthLimitMbps: 0},
  })
}

/** Tunnels: the agent owns the definitions (add/edit/delete, hot-applied);
 * the gateway sees a read-only view of what the agent registered. */
export function Tunnels({status}: {status: UIStatus}) {
  const isAgent = status.role === 'agent'
  const [tunnels, setTunnels] = useState<Tunnel[]>([])
  const [loaded, setLoaded] = useState(false)
  const [editing, setEditing] = useState<Tunnel | null>(null)
  const [err, setErr] = useState('')

  const reload = () => GetConfig().then(c => { setTunnels(c.Agent?.Tunnels ?? []); setLoaded(true) }).catch(e => setErr(String(e)))
  useEffect(() => { reload() }, [])

  // Live state per tunnel, keyed by ID.
  const live = new Map((status.tunnels ?? []).map(t => [t.id, t]))

  const persist = async (next: Tunnel[]) => {
    setErr('')
    try {
      await SaveTunnels(next)
      setTunnels(next)
      setEditing(null)
    } catch (e) { setErr(String(e)) }
  }

  const onSave = (t: Tunnel) => {
    const idx = tunnels.findIndex(x => x.ID === t.ID)
    const next = idx >= 0 ? tunnels.map(x => x.ID === t.ID ? t : x) : [...tunnels, t]
    return persist(next)
  }
  const onDelete = (id: string) => persist(tunnels.filter(t => t.ID !== id))
  const onToggle = (id: string, enabled: boolean) =>
    persist(tunnels.map(t => t.ID === id ? config.Tunnel.createFrom({...t, Enabled: enabled}) : t))

  if (!isAgent) {
    const gwTunnels = status.tunnels ?? []
    return (
      <Card title="Registered tunnels" subtitle="Defined by the connected agent">
        {gwTunnels.length === 0
          ? <EmptyState icon={<IconTunnels size={28} />} title="No tunnels registered"
              hint="Tunnels appear here once an agent connects and registers its ports." />
          : <div className="pf-stagger space-y-2">
              {gwTunnels.map(t => (
                <div key={t.id} className="pf-lift flex items-center justify-between rounded-xl border border-[var(--border)] bg-[var(--panel-2)] px-4 py-3">
                  <div>
                    <div className="font-medium">{t.name}</div>
                    <div className="text-xs text-[var(--text-3)]">Public port {t.publicPort || '—'}</div>
                  </div>
                  <Badge tone={t.localKnown ? (t.localUp ? 'good' : 'bad') : 'neutral'}>
                    {t.localKnown ? (t.localUp ? 'Server up' : 'Server down') : 'Unknown'}
                  </Badge>
                </div>
              ))}
            </div>}
      </Card>
    )
  }

  return (
    <div className="pf-stagger space-y-4">
      {err && <ErrorBanner message={err} onDismiss={() => setErr('')} />}
      <div className="flex items-center justify-between">
        <p className="text-sm text-[var(--text-2)]">Each tunnel maps a local server to a public port on the gateway. Changes apply live.</p>
        <Button onClick={() => setEditing(blankTunnel())}><IconPlus size={16} /> Add tunnel</Button>
      </div>

      {loaded && tunnels.length === 0 && (
        <Card><EmptyState icon={<IconTunnels size={28} />} title="No tunnels yet"
          hint="Add one to expose a local Minecraft server through the gateway." /></Card>
      )}

      <div className="pf-stagger grid grid-cols-1 gap-3 lg:grid-cols-2">
        {tunnels.map(t => {
          const l = live.get(t.ID)
          const bound = l && l.publicPort > 0
          return (
            <div key={t.ID} className="pf-lift rounded-2xl border border-[var(--border)] bg-[var(--panel)] p-4">
              <div className="flex items-start justify-between">
                <div className="flex items-center gap-2.5">
                  <div
                    className="flex h-9 w-9 items-center justify-center rounded-xl text-white shadow-[0_2px_10px_-2px_color-mix(in_srgb,var(--accent)_45%,transparent)]"
                    style={{background: 'linear-gradient(135deg, var(--accent), color-mix(in srgb, var(--accent) 45%, var(--accent-2)))'}}
                  ><IconServer size={18} /></div>
                  <div>
                    <div className="font-semibold">{t.Name}</div>
                    <div className="font-mono text-xs text-[var(--text-3)]">{t.LocalAddr} → :{t.PublicPort}</div>
                  </div>
                </div>
                <div className="flex items-center gap-1">
                  <IconButton title="Edit" onClick={() => setEditing(config.Tunnel.createFrom({...t, Options: {...t.Options}}))}><IconEdit size={16} /></IconButton>
                  <IconButton title="Delete" variant="danger" onClick={() => onDelete(t.ID)}><IconTrash size={16} /></IconButton>
                </div>
              </div>

              <div className="mt-3 flex flex-wrap items-center gap-1.5">
                {!t.Enabled && <Badge tone="neutral">Disabled</Badge>}
                {t.Enabled && <Badge tone={bound ? 'good' : 'warn'}>{bound ? 'Bound' : 'Pending'}</Badge>}
                {l?.localKnown && <Badge tone={l.localUp ? 'good' : 'bad'}>{l.localUp ? 'Server up' : 'Server down'}</Badge>}
                {t.Options?.MinecraftAware && <Badge tone="accent">MC-aware</Badge>}
                {t.Options?.ProxyProtocolV2 && <Badge tone="accent">PROXY v2</Badge>}
                {t.Options?.OfflineMOTD && <Badge tone="neutral">Offline MOTD</Badge>}
                {(t.Options?.BandwidthLimitMbps ?? 0) > 0 && <Badge tone="neutral">{t.Options.BandwidthLimitMbps} Mbps cap</Badge>}
              </div>

              <div className="mt-3 border-t border-[var(--border)] pt-2">
                <Toggle checked={t.Enabled} onChange={v => onToggle(t.ID, v)} label="Enabled" />
              </div>
            </div>
          )
        })}
      </div>

      {editing && <TunnelEditor
        title={tunnels.some(t => t.ID === editing.ID) ? 'Edit tunnel' : 'Add tunnel'}
        initial={editing} onCancel={() => setEditing(null)} onSave={onSave} />}
    </div>
  )
}

function TunnelEditor({title, initial, onSave, onCancel}: {
  title: string; initial: Tunnel; onSave: (t: Tunnel) => Promise<void>; onCancel: () => void
}) {
  const [t, setT] = useState<Tunnel>(initial)
  const [saving, setSaving] = useState(false)
  const opt = t.Options ?? config.TunnelOptions.createFrom({})
  const set = (patch: Partial<Tunnel>) => setT(prev => config.Tunnel.createFrom({...prev, ...patch}))
  const setOpt = (patch: Partial<config.TunnelOptions>) => set({Options: config.TunnelOptions.createFrom({...opt, ...patch})})

  const save = async () => { setSaving(true); await onSave(t); setSaving(false) }

  return (
    <Modal title={title} onClose={onCancel} wide
      footer={<>
        <Button variant="ghost" onClick={onCancel}>Cancel</Button>
        <Button onClick={save} disabled={saving || !t.Name.trim() || !t.LocalAddr.trim()}>{saving ? 'Saving…' : 'Save'}</Button>
      </>}>
      <div className="space-y-4">
        <Field label="Name"><TextInput value={t.Name} onChange={v => set({Name: v})} autoFocus /></Field>
        <div className="grid grid-cols-2 gap-3">
          <Field label="Local Minecraft address" hint="Where the server actually runs on this machine.">
            <TextInput value={t.LocalAddr} onChange={v => set({LocalAddr: v})} mono />
          </Field>
          <Field label="Public port on the gateway" hint="What players connect to.">
            <TextInput value={String(t.PublicPort)} onChange={v => set({PublicPort: parseInt(v, 10) || 0})} mono />
          </Field>
        </div>

        <div className="rounded-xl border border-[var(--border)] bg-[var(--panel-2)] px-4 py-1 divide-y divide-[var(--border)]">
          <Toggle checked={opt.MinecraftAware} onChange={v => setOpt({MinecraftAware: v})}
            label="Minecraft-aware"
            hint="Poll the server for MOTD, player count and version; sniff usernames for the connections view." />
          <Toggle checked={opt.ProxyProtocolV2} onChange={v => setOpt({ProxyProtocolV2: v})}
            label="PROXY protocol v2"
            hint={<>Send the real client IP to the local server (Paper/Velocity). <b>Mutually exclusive</b> with BungeeCord/Velocity IP-forwarding — enabling both causes ghost errors.</>} />
        </div>

        <Field label="Offline MOTD" hint="Shown to players when the agent or server is down. Leave blank for a clean disconnect instead.">
          <TextInput value={opt.OfflineMOTD} onChange={v => setOpt({OfflineMOTD: v})} placeholder="Server is offline — back soon" />
        </Field>

        <div className="grid grid-cols-2 gap-3">
          <Field label="Bandwidth cap (Mbps)" hint="0 = unlimited. Protects the gateway's uplink.">
            <TextInput value={String(opt.BandwidthLimitMbps)} onChange={v => setOpt({BandwidthLimitMbps: parseInt(v, 10) || 0})} mono />
          </Field>
          <Field label="Protocol">
            <Select value="tcp" onChange={() => {}} options={[{value: 'tcp', label: 'TCP (Java Edition)'}]} />
          </Field>
        </div>
      </div>
    </Modal>
  )
}
