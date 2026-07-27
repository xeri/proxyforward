import {useEffect, useState} from 'react'
import {GetConfig, SaveTunnels, TestReachability} from '../../wailsjs/go/app/App'
import {config} from '../../wailsjs/go/models'
import {Column, DataTable} from '../components/DataTable'
import {PauseControl, PausedBadge} from '../components/PauseControl'
import {proposalAge, ProposalDiff, specsOf, useAgentConsent} from '../consent'
import {
  Badge, Banner, Button, Card, Disclosure, EmptyState, ErrorBanner, Field, IconButton,
  Modal, MonoChip, PageHeader, Select, Skeleton, TextInput, Toggle,
} from '../components/ui'
import {IconBolt, IconEdit, IconPlus, IconServer, IconTrash, IconTunnels} from '../components/icons'
import {BANDWIDTH_SCOPES, HEALTH_PROBES, probeLabel, PROTOCOLS, scopeLabel, UIStatus} from '../state'

type Tunnel = config.Tunnel

// newTunnelID renders a typed tnl_<slug> id from the tunnel name, suffixing -2, -3…
// until it's free — the frontend mirror of link.TunnelID (and its slug: lowercase,
// [a-z0-9] only, other runs collapse to one hyphen, ends trimmed; empty → "tunnel").
// Existing hex ids are never touched — the local config dir is disposable, no migration.
function newTunnelID(name: string, taken: (id: string) => boolean): string {
  const slug = (name || '').toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '')
  const base = 'tnl_' + (slug || 'tunnel')
  let cand = base
  for (let n = 2; taken(cand); n++) cand = `${base}-${n}`
  return cand
}

function blankTunnel(existing: Tunnel[]): Tunnel {
  const taken = (id: string) => existing.some(t => t.ID === id)
  return config.Tunnel.createFrom({
    ID: newTunnelID('Minecraft', taken), Name: 'Minecraft', Type: 'tcp',
    LocalAddr: '127.0.0.1:25565', PublicPort: 25565, Enabled: true,
    Options: {MinecraftAware: false, ProxyProtocolV2: false, OfflineMOTD: '', BandwidthLimitMbps: 0, BandwidthLimitScope: 'combined'},
  })
}

/** healthSummary describes a tunnel's health check in one line, or '' when it is
 * entirely default — the card's Advanced list only names what was actually
 * configured, so a default check adds no row. */
function healthSummary(o?: config.TunnelOptions): string {
  if (!o) return ''
  const parts: string[] = []
  if (o.HealthProbe === 'mc-status') parts.push('Server ping')
  if (o.HealthIntervalSec) parts.push(`every ${o.HealthIntervalSec}s`)
  if (o.HealthTimeoutSec) parts.push(`${o.HealthTimeoutSec}s timeout`)
  if (o.HealthMaxFailed > 1) parts.push(`${o.HealthMaxFailed} failures to trip`)
  return parts.join(' · ')
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
  // Only the agent authors tunnels, so only it can be waiting on an approval.
  const consent = useAgentConsent(isAgent)

  const reload = () => GetConfig().then(c => { setTunnels(c.Agent?.Tunnels ?? []); setLoaded(true) }).catch(e => setErr(String(e)))
  useEffect(() => { reload() }, [])

  // Live state per tunnel, keyed by ID.
  const live = new Map((status.tunnels ?? []).map(t => [t.id, t]))
  // The set the gateway can see — a proposal only ever carries the enabled tunnels,
  // so the diff must compare against those, not local drafts.
  const enabledNow = tunnels.filter(t => t.Enabled)

  // Under config-consent a save is a *submission*: the gateway has to approve before
  // anything here moves, so painting `next` as applied would be a lie — the screen
  // would show a port that isn't serving. Re-read the truth instead and let the
  // pending banner explain the wait. This is also right for an edit the gateway
  // cannot see (a local address), which applies at once and reload() picks up.
  const persist = async (next: Tunnel[]) => {
    setErr('')
    try {
      await SaveTunnels(next)
      if (consent.view?.consentActive) {
        await reload()
        consent.refresh()
      } else {
        setTunnels(next)
      }
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
    // The list is clamped so the status frame fits (ipc.MaxStatusTunnels), and on
    // a gateway it is the whole fleet's — so count the fleet, not the rows. 0 from
    // a daemon too old to say, which is also one too old to clamp.
    const gwTotal = status.tunnelsTotal || gwTunnels.length
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
        <PageHeader eyebrow="passage" title="Tunnels" subtitle="Registered by the connected agent." />
        <Card title="Registered tunnels" pad={false}
          action={gwTotal > 0 ? <div className="pr-4"><Badge tone="neutral">{gwTotal}</Badge></div> : undefined}>
          <DataTable
            columns={cols} rows={gwTunnels} rowKey={t => t.id}
            empty={{
              icon: <IconTunnels size={28} />,
              title: 'No tunnels registered',
              hint: 'Tunnels appear here once an agent connects and registers its ports.',
            }}
          />
          {status.tunnelsTruncated && (
            <div className="px-4 py-2 text-sub tabular-nums text-[var(--text-3)]">
              showing {gwTunnels.length} of {gwTotal} tunnels
            </div>
          )}
        </Card>
      </div>
    )
  }

  return (
    <div className="pf-stagger space-y-4">
      <PageHeader
        eyebrow="passage"
        title="Tunnels"
        subtitle={consent.view?.consentActive
          ? 'Map local servers to public ports. Changes go to the gateway for approval.'
          : 'Map local servers to public ports. Changes apply live.'}
        action={<Button onClick={() => setEditing(blankTunnel(tunnels))}><IconPlus size={16} /> Add tunnel</Button>}
      />
      {err && <ErrorBanner message={err} onDismiss={() => setErr('')} />}

      {/* A submitted edit is invisible without this: the list still shows the old
          truth (correctly), so the wait has to be stated or the save reads as a no-op. */}
      {consent.view?.outgoing && (
        <Banner tone="info">
          <div className="space-y-2">
            <div>
              <div className="font-medium">Waiting for the gateway to approve your change</div>
              <div className="text-body text-[var(--text-3)]">
                Sent {proposalAge(consent.view.outgoing.atMs)}. Your tunnels keep running as they are until it is accepted.
                {status.peerHeadless && ' The gateway runs headless — nobody may be watching for this until someone opens its app.'}
              </div>
            </div>
            <ProposalDiff
              current={enabledNow.map(t => ({id: t.ID, name: t.Name, publicPort: t.PublicPort}))}
              proposed={specsOf(consent.view.outgoing.tunnels)}
            />
          </div>
        </Banner>
      )}
      {/* A declined edit changed nothing, so without the reason the save just
          vanished. The gateway operator's words, verbatim. */}
      {consent.view?.lastDecline && !consent.view.outgoing && (
        <Banner tone="warn">
          <div className="font-medium">The gateway declined your last change</div>
          <div className="text-body text-[var(--text-3)]">{consent.view.lastDecline}</div>
        </Banner>
      )}

      {!loaded && (
        <div className="grid grid-cols-1 gap-[var(--grid-gap)] @min-[56rem]:grid-cols-2 @min-[84rem]:grid-cols-3" aria-busy="true">
          <Skeleton className="h-40 rounded-[var(--r-lg)]" />
          <Skeleton className="h-40 rounded-[var(--r-lg)]" />
        </div>
      )}

      {loaded && tunnels.length === 0 && (
        <Card><EmptyState icon={<IconTunnels size={28} />} title="No tunnels yet"
          hint="Add one to publish a local Minecraft server through the gateway."
          action={<Button onClick={() => setEditing(blankTunnel(tunnels))}><IconPlus size={16} /> Add tunnel</Button>} /></Card>
      )}

      <div className="pf-stagger grid grid-cols-1 gap-[var(--grid-gap)] @min-[56rem]:grid-cols-2 @min-[84rem]:grid-cols-3">
        {tunnels.map(t => {
          const l = live.get(t.ID)
          const bound = !!(l && l.publicPort > 0)
          const advanced: [string, string][] = [
            ...(t.Type === 'udp' ? [['Protocol', 'UDP'] as [string, string]] : []),
            ...(t.Options?.MinecraftAware ? [['Minecraft-aware', 'On'] as [string, string]] : []),
            ...(t.Options?.ProxyProtocolV2 ? [['PROXY protocol v2', 'On'] as [string, string]] : []),
            ...(t.Options?.OfflineMOTD ? [['Offline MOTD', t.Options.OfflineMOTD] as [string, string]] : []),
            ...((t.Options?.BandwidthLimitMbps ?? 0) > 0
              ? [['Bandwidth cap', `${t.Options.BandwidthLimitMbps} Mbps${
                  (t.Options.BandwidthLimitScope || 'combined') !== 'combined' ? ` · ${scopeLabel(t.Options.BandwidthLimitScope)}` : ''
                }`] as [string, string]]
              : []),
            ...(healthSummary(t.Options) ? [['Health check', healthSummary(t.Options)] as [string, string]] : []),
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
                {l?.paused && <PausedBadge pausedBy={l.pausedBy} peer="the gateway" />}
                {l?.localKnown && <Badge tone={l.localUp ? 'good' : 'bad'}>{l.localUp ? 'Server up' : 'Server down'}</Badge>}
              </div>

              {/* What the server itself said, from the mc-status probe. Metadata
                  on whitespace, not a nested card. Absent for a tcp-probed or
                  down tunnel — nothing is claimed when nothing was heard. */}
              {l?.serverStatusKnown && (
                <div className="mt-2.5 min-w-0 space-y-1">
                  <div className="flex items-baseline gap-2">
                    <span className="text-caption lowercase tracking-[var(--tracking-label)] text-[var(--text-3)]">players</span>
                    <span className="text-emphasis font-medium tabular-nums text-[var(--text-1)]">
                      {l.serverPlayersOnline >= 0 && l.serverPlayersMax >= 0
                        ? `${l.serverPlayersOnline} / ${l.serverPlayersMax}`
                        : '—'}
                    </span>
                    {l.serverVersion && <span className="truncate text-sub text-[var(--text-3)]">{l.serverVersion}</span>}
                  </div>
                  {l.serverMotd && (
                    <div className="truncate text-sub text-[var(--text-3)]" title={l.serverMotd}>{l.serverMotd}</div>
                  )}
                </div>
              )}

              {advanced.length > 0 && (
                <div className="mt-3">
                  <Disclosure label={<span className="text-emphasis">Advanced</span>} hint={`${advanced.length} option${advanced.length === 1 ? '' : 's'} set`}>
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
                <div className="flex items-center gap-1.5">
                  {t.Enabled && bound && (
                    <PauseControl
                      tunnelId={t.ID}
                      what="this tunnel"
                      paused={!!l?.paused}
                      pausedBy={l?.pausedBy ?? ''}
                      players={(status.connections ?? []).filter(c => c.tunnelName === t.Name).length}
                    />
                  )}
                  <TestPath tunnelID={t.ID} bound={bound} port={l?.publicPort} />
                </div>
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
          className={`pf-fade max-w-64 truncate text-right text-sub ${state === 'ok' ? 'text-[var(--good)]' : 'text-[var(--bad)]'}`}
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
  const proto = t.Type || 'tcp'
  const isUDP = proto === 'udp'
  // Switching to UDP clears the three TCP-only options rather than leaving them
  // set-but-ignored: the agent's config validation rejects each of those
  // combinations, so a tunnel carrying them would fail on save with an error
  // about a checkbox the user can no longer see.
  const setProto = (v: string) => {
    if (v === 'udp') {
      set({Type: v, Options: config.TunnelOptions.createFrom({...opt, MinecraftAware: false, ProxyProtocolV2: false, OfflineMOTD: ''})})
      return
    }
    set({Type: v})
  }

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
          defaultOpen={isUDP || opt.MinecraftAware || opt.ProxyProtocolV2 || !!opt.OfflineMOTD || (opt.BandwidthLimitMbps ?? 0) > 0}
        >
          <div className="space-y-4">
            {/* Protocol leads the section because it governs everything under it:
                the three options below are TCP-only, and picking UDP turns them
                off rather than leaving controls that would fail validation on
                save. */}
            <Field label="Protocol" hint={isUDP
              ? 'Datagrams, for Bedrock/Geyser and voice-chat mods. TCP and UDP hold a port number independently, so this may reuse the number a Java tunnel already uses.'
              : 'A byte stream, for Java Edition.'}>
              <Select value={proto} onChange={v => setProto(v)} options={PROTOCOLS} />
            </Field>

            <div className="divide-y divide-[var(--border)] rounded-[var(--r-md)] border border-[var(--border)] bg-[var(--panel-2)] px-4 py-1">
              <Toggle checked={opt.MinecraftAware} onChange={v => setOpt({MinecraftAware: v})}
                label="Minecraft-aware" disabled={isUDP}
                hint={isUDP
                  ? 'TCP only — the Java login handshake this reads is a TCP stream.'
                  : 'Sniff player usernames from the login handshake for the traffic and players views.'} />
              <Toggle checked={opt.ProxyProtocolV2} onChange={v => setOpt({ProxyProtocolV2: v})}
                label="PROXY protocol v2" disabled={isUDP}
                hint={isUDP
                  ? 'TCP only — the v2 header is a preamble on a stream, and a datagram flow has none.'
                  : <>Send the real client IP to the local server (Paper/Velocity). <b>Mutually exclusive</b> with BungeeCord/Velocity IP-forwarding — enabling both causes ghost errors.</>} />
            </div>

            <Field label="Offline MOTD" hint={isUDP
              ? 'TCP only — status pings are a TCP protocol, so there is no ping here to answer.'
              : 'Shown to players when the agent or server is down. Leave blank for a clean disconnect instead.'}>
              <TextInput value={opt.OfflineMOTD} onChange={v => setOpt({OfflineMOTD: v})}
                placeholder="Server is offline — back soon" disabled={isUDP} />
            </Field>

            <div className="grid grid-cols-2 gap-3">
              <Field label="Bandwidth cap (Mbps)" hint="0 = unlimited. Protects the gateway's uplink.">
                <TextInput value={String(opt.BandwidthLimitMbps)} onChange={v => setOpt({BandwidthLimitMbps: parseInt(v, 10) || 0})} mono />
              </Field>
              <Field label="Cap scope" hint={
                (opt.BandwidthLimitMbps ?? 0) > 0
                  ? 'What the cap applies to: both directions together, each direction, or each connection.'
                  : 'Set a cap first.'
              }>
                <Select
                  value={opt.BandwidthLimitScope || 'combined'}
                  onChange={v => setOpt({BandwidthLimitScope: v})}
                  options={BANDWIDTH_SCOPES}
                  disabled={(opt.BandwidthLimitMbps ?? 0) <= 0}
                />
              </Field>
            </div>

            <HealthFields opt={opt} setOpt={setOpt} isUDP={isUDP} />
          </div>
        </Disclosure>
      </div>
    </Modal>
  )
}

/** HealthFields: how this tunnel's local server is checked. Folded away by
 * default — the shipped behaviour needs no configuration — and every field
 * placeholder shows the default it falls back to, so an empty box is never
 * ambiguous about what will happen. */
function HealthFields({opt, setOpt, isUDP}: {
  opt: config.TunnelOptions
  setOpt: (patch: Partial<config.TunnelOptions>) => void
  isUDP: boolean
}) {
  const num = (v: string) => Math.max(0, parseInt(v, 10) || 0)
  return (
    <Disclosure
      label="Health check"
      hint={isUDP ? 'Not available on UDP' : probeLabel(opt.HealthProbe)}
      defaultOpen={!isUDP && (!!opt.HealthProbe || !!opt.HealthIntervalSec || !!opt.HealthTimeoutSec || !!opt.HealthMaxFailed)}
    >
      {isUDP ? (
        <p className="text-body text-[var(--text-3)]">
          A UDP port cannot be probed — connecting to a dead one succeeds exactly as a live one does,
          so this tunnel's server health reads <span className="font-medium text-[var(--text-2)]">unknown</span> rather
          than a guess. Players are served either way.
        </p>
      ) : (
        <div className="space-y-4">
          <Field label="Check" hint={opt.HealthProbe === 'mc-status'
            ? 'Asks for the server list ping, so a process that accepts connections but never answers reads as down — and its MOTD and player count show on the card.'
            : 'Opens a connection and closes it. Cheap, but anything listening on the port passes.'}>
            <Select value={opt.HealthProbe || 'tcp'} onChange={v => setOpt({HealthProbe: v})} options={HEALTH_PROBES} />
          </Field>
          <div className="grid grid-cols-3 gap-3">
            <Field label="Every (s)" hint="Default 5">
              <TextInput value={opt.HealthIntervalSec ? String(opt.HealthIntervalSec) : ''} placeholder="5"
                onChange={v => setOpt({HealthIntervalSec: num(v)})} mono />
            </Field>
            <Field label="Timeout (s)" hint="Default 3">
              <TextInput value={opt.HealthTimeoutSec ? String(opt.HealthTimeoutSec) : ''} placeholder="3"
                onChange={v => setOpt({HealthTimeoutSec: num(v)})} mono />
            </Field>
            <Field label="Failures to trip" hint="Default 1">
              <TextInput value={opt.HealthMaxFailed ? String(opt.HealthMaxFailed) : ''} placeholder="1"
                onChange={v => setOpt({HealthMaxFailed: num(v)})} mono />
            </Field>
          </div>
          <p className="text-caption text-[var(--text-3)]">
            Consecutive failures — one success clears the count and brings the tunnel straight back up.
            Raise it if the server stutters under load.
          </p>
        </div>
      )}
    </Disclosure>
  )
}
