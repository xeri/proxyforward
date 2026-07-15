import {useEffect, useState} from 'react'
import {GetConfig, SaveTunnels, TestReachability} from '../../wailsjs/go/app/App'
import {config} from '../../wailsjs/go/models'
import {Column, DataTable} from '../components/DataTable'
import {
  Badge, Button, Card, Disclosure, EmptyState, ErrorBanner, Field, IconButton,
  Modal, MonoChip, PageHeader, Select, Skeleton, TextInput, Toggle,
} from '../components/ui'
import {IconBolt, IconEdit, IconPlus, IconServer, IconTrash, IconTunnels} from '../components/icons'
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
 * the gateway sees a read-only view of what the agent registered. Each bound
 * tunnel can test its own player path across the real internet. */
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
    const cols: Column<(typeof gwTunnels)[number]>[] = [
      {key: 'name', header: 'Tunnel', pin: true, render: t => <span className="font-medium">{t.name}</span>},
      {key: 'port', header: 'Public port', render: t => <MonoChip>{t.publicPort > 0 ? t.publicPort : '—'}</MonoChip>},
      {key: 'state', header: 'Server', align: 'right', render: t => (
        <Badge tone={t.localKnown ? (t.localUp ? 'good' : 'bad') : 'neutral'}>
          {t.localKnown ? (t.localUp ? 'Server up' : 'Server down') : 'Unknown'}
        </Badge>
      )},
    ]
    return (
      <div className="pf-stagger space-y-4">
        <PageHeader title="Tunnels" subtitle="Registered by the connected agent." />
        <Card title="Registered tunnels" pad={false}
          action={gwTunnels.length > 0 ? <div className="pr-4"><Badge tone="neutral">{gwTunnels.length}</Badge></div> : undefined}>
          <DataTable
            columns={cols} rows={gwTunnels} rowKey={t => t.id}
            empty={{
              icon: <IconTunnels size={28} />,
              title: 'No tunnels registered',
              hint: 'Tunnels appear here once an agent connects and registers its ports.',
            }}
          />
        </Card>
      </div>
    )
  }

  return (
    <div className="pf-stagger space-y-4">
      <PageHeader
        title="Tunnels"
        subtitle="Map local servers to public ports. Changes apply live."
        action={<Button onClick={() => setEditing(blankTunnel())}><IconPlus size={16} /> Add tunnel</Button>}
      />
      {err && <ErrorBanner message={err} onDismiss={() => setErr('')} />}

      {!loaded && (
        <div className="grid grid-cols-1 gap-[var(--grid-gap)] @min-[56rem]:grid-cols-2 @min-[84rem]:grid-cols-3" aria-busy="true">
          <Skeleton className="h-40 rounded-[var(--r-lg)]" />
          <Skeleton className="h-40 rounded-[var(--r-lg)]" />
        </div>
      )}

      {loaded && tunnels.length === 0 && (
        <Card><EmptyState icon={<IconTunnels size={28} />} title="No tunnels yet"
          hint="Add one to publish a local Minecraft server through the gateway."
          action={<Button onClick={() => setEditing(blankTunnel())}><IconPlus size={16} /> Add tunnel</Button>} /></Card>
      )}

      <div className="pf-stagger grid grid-cols-1 gap-[var(--grid-gap)] @min-[56rem]:grid-cols-2 @min-[84rem]:grid-cols-3">
        {tunnels.map(t => {
          const l = live.get(t.ID)
          const bound = !!(l && l.publicPort > 0)
          const advanced: [string, string][] = [
            ...(t.Options?.MinecraftAware ? [['Minecraft-aware', 'On'] as [string, string]] : []),
            ...(t.Options?.ProxyProtocolV2 ? [['PROXY protocol v2', 'On'] as [string, string]] : []),
            ...(t.Options?.OfflineMOTD ? [['Offline MOTD', t.Options.OfflineMOTD] as [string, string]] : []),
            ...((t.Options?.BandwidthLimitMbps ?? 0) > 0 ? [['Bandwidth cap', `${t.Options.BandwidthLimitMbps} Mbps`] as [string, string]] : []),
          ]
          return (
            <div key={t.ID} className="pf-card pf-lift flex flex-col p-4">
              <div className="flex items-start justify-between">
                <div className="flex min-w-0 items-center gap-2.5">
                  <div className="pf-control relative flex h-9 w-9 shrink-0 items-center justify-center rounded-[var(--r-md)] bg-[var(--input-bg)] text-[var(--text-2)]">
                    <IconServer size={18} />
                  </div>
                  <div className="min-w-0">
                    <div className="truncate font-semibold">{t.Name}</div>
                    <div className="select-text truncate font-mono text-xs text-[var(--text-3)]">{t.LocalAddr} → :{t.PublicPort}</div>
                  </div>
                </div>
                <div className="flex shrink-0 items-center gap-1">
                  <IconButton title="Edit" onClick={() => setEditing(config.Tunnel.createFrom({...t, Options: {...t.Options}}))}><IconEdit size={16} /></IconButton>
                  <DeleteButton onDelete={() => onDelete(t.ID)} />
                </div>
              </div>

              <div className="mt-3 flex flex-wrap items-center gap-1.5">
                {!t.Enabled && <Badge tone="neutral">Disabled</Badge>}
                {t.Enabled && <Badge tone={bound ? 'good' : 'warn'}>{bound ? 'Bound' : 'Pending'}</Badge>}
                {l?.localKnown && <Badge tone={l.localUp ? 'good' : 'bad'}>{l.localUp ? 'Server up' : 'Server down'}</Badge>}
              </div>

              {advanced.length > 0 && (
                <div className="mt-3">
                  <Disclosure label={<span className="text-[13px]">Advanced</span>} hint={`${advanced.length} option${advanced.length === 1 ? '' : 's'} set`}>
                    <dl className="space-y-1.5">
                      {advanced.map(([k, v]) => (
                        <div key={k} className="flex items-baseline justify-between gap-3 text-xs">
                          <dt className="text-[var(--text-3)]">{k}</dt>
                          <dd className="truncate text-right font-medium text-[var(--text-2)]" title={v}>{v}</dd>
                        </div>
                      ))}
                    </dl>
                  </Disclosure>
                </div>
              )}

              <div className="mt-3 flex flex-1 items-end justify-between gap-3 border-t border-[var(--border)] pt-2">
                <Toggle checked={t.Enabled} onChange={v => onToggle(t.ID, v)} label="Enabled" />
                <TestPath tunnelID={t.ID} bound={bound} port={l?.publicPort} />
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

/** TestPath: dials this tunnel's public port across the real internet — the
 * exact route a player takes (DNS → gateway → forward → tunnel → server). */
function TestPath({tunnelID, bound, port}: {tunnelID: string; bound: boolean; port?: number}) {
  const [state, setState] = useState<'idle' | 'busy' | 'ok' | 'fail'>('idle')
  const [msg, setMsg] = useState('')
  const run = async () => {
    setState('busy'); setMsg('')
    try {
      const res = await TestReachability(tunnelID)
      setState('ok'); setMsg(res)
    } catch (e) {
      setState('fail'); setMsg(String(e))
    }
  }
  return (
    <div className="flex min-w-0 flex-col items-end gap-1">
      <Button
        variant="ghost" size="sm" onClick={run} disabled={!bound || state === 'busy'}
        title={bound ? `Dial the gateway on port ${port} — the exact path a player takes` : 'Tunnel not bound yet'}
      >
        <IconBolt size={13} /> {state === 'busy' ? 'Testing…' : 'Test player path'}
      </Button>
      {msg && (
        <span
          className={`pf-fade max-w-64 truncate text-right text-[11px] ${state === 'ok' ? 'text-[var(--good)]' : 'text-[var(--bad)]'}`}
          title={msg}
        >{msg}</span>
      )}
    </div>
  )
}

/** DeleteButton: two-step delete — the first click arms it, the second
 * confirms; it disarms itself after a moment. Deleting applies live. */
function DeleteButton({onDelete}: {onDelete: () => void}) {
  const [armed, setArmed] = useState(false)
  useEffect(() => {
    if (!armed) return
    const t = setTimeout(() => setArmed(false), 2500)
    return () => clearTimeout(t)
  }, [armed])
  if (armed) {
    return (
      <Button variant="danger" size="sm" onClick={onDelete} title="Click again to delete this tunnel">
        <IconTrash size={13} /> Delete?
      </Button>
    )
  }
  return (
    <IconButton title="Delete" variant="danger" onClick={() => setArmed(true)}><IconTrash size={16} /></IconButton>
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
          <Field label="Local server address" hint="Where the server actually runs on this machine.">
            <TextInput value={t.LocalAddr} onChange={v => set({LocalAddr: v})} mono />
          </Field>
          <Field label="Public port" hint="What players connect to on the gateway.">
            <TextInput value={String(t.PublicPort)} onChange={v => set({PublicPort: parseInt(v, 10) || 0})} mono />
          </Field>
        </div>

        {/* Advanced options stay folded unless one is already set — the
            default add-tunnel flow is three fields and Save. */}
        <Disclosure
          label="Advanced options"
          hint="Protocol awareness, real client IPs, offline behavior, bandwidth"
          defaultOpen={opt.MinecraftAware || opt.ProxyProtocolV2 || !!opt.OfflineMOTD || (opt.BandwidthLimitMbps ?? 0) > 0}
        >
          <div className="space-y-4">
            <div className="divide-y divide-[var(--border)] rounded-[var(--r-md)] border border-[var(--border)] bg-[var(--panel-2)] px-4 py-1">
              <Toggle checked={opt.MinecraftAware} onChange={v => setOpt({MinecraftAware: v})}
                label="Minecraft-aware"
                hint="Poll the server for MOTD, player count and version; sniff usernames for the traffic view." />
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
        </Disclosure>
      </div>
    </Modal>
  )
}
