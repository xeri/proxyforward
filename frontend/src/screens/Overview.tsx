import {ReactNode, useEffect, useState} from 'react'
import {MeasureLatency, PairingCode, RestartEngine} from '../../wailsjs/go/app/App'
import {app} from '../../wailsjs/go/models'
import {BandwidthPanel} from '../components/BandwidthChart'
import {NumberTicker} from '../components/NumberTicker'
import {
  Badge, Banner, Button, Card, Codebox, CopyButton, CopyIcon, ErrorBanner,
  PageHeader, StatTile, StatusDot,
} from '../components/ui'
import {Emblem} from '../components/Emblem'
import {IconActivity, IconClock, IconGauge, IconGlobe, IconLink, IconServer, IconUsers} from '../components/icons'
import {NavId} from '../nav'
import {fmtBytes, fmtUptime, UIStatus} from '../state'

type Seg = 'good' | 'warn' | 'bad' | 'unknown'

const segColor: Record<Seg, string> = {
  good: 'var(--good)', warn: 'var(--warn)', bad: 'var(--bad)', unknown: 'var(--text-3)',
}

/** Overview: the tunnel at a glance — alerts, the traffic pipeline, link
 * health with an inline latency probe, headline stats, both identities, the
 * role tool, and a one-hour bandwidth teaser that jumps to Traffic. */
export function Overview({status, onNavigate}: {status: UIStatus; onNavigate: (id: NavId) => void}) {
  const isAgent = status.role === 'agent'
  const tunnels = status.tunnels ?? []
  const conns = status.connections ?? []
  const firstTunnel = tunnels[0]

  // 1 Hz re-render so uptime readouts tick between 2 Hz snapshots.
  const [, tick] = useState(0)
  useEffect(() => {
    const t = setInterval(() => tick(x => x + 1), 1000)
    return () => clearInterval(t)
  }, [])
  const linkUptime = status.linkUpSinceMs ? fmtUptime(Date.now() - status.linkUpSinceMs) : '—'
  const uptimeSub = [
    status.processStartMs ? `app ${fmtUptime(Date.now() - status.processStartMs)}` : null,
    status.cumulativeUptimeMs ? `all-time ${fmtUptime(status.cumulativeUptimeMs)}` : null,
  ].filter(Boolean).join(' · ')

  // Segment states.
  const localState: Seg = !firstTunnel || !firstTunnel.localKnown ? 'unknown'
    : firstTunnel.localUp ? 'good' : 'bad'
  const linkState: Seg = isAgent ? (status.linkUp ? 'good' : 'bad')
    : (status.agentConnected ? 'good' : 'warn')
  const portState: Seg = isAgent
    ? (firstTunnel && firstTunnel.publicPort > 0 ? 'good' : 'unknown')
    : (tunnels.length > 0 || status.agentConnected ? 'good' : 'unknown')
  const flowing = linkState === 'good'

  // Per-hop byte counters. Each role annotates the hops it can see:
  // agent  — local↔agent = its conntrack totals, agent↔gateway = raw link
  // gateway — agent↔gateway = raw link, gateway↔clients = its conntrack
  const appBytes = status.totalBytesIn + status.totalBytesOut
  const linkBytes = status.linkBytesIn + status.linkBytesOut
  const leftHop = isAgent ? appBytes : linkBytes
  const rightHop = isAgent ? linkBytes : appBytes

  return (
    <div className="pf-stagger grid grid-cols-12 gap-[var(--grid-gap)]">
      <div className="col-span-12">
        <PageHeader
          title="Overview"
          subtitle={isAgent ? 'The path from your server to your players.' : 'The public front door for your agent.'}
        />
      </div>

      {status.engineFatal && (
        <div className="col-span-12">
          <Banner
            tone="bad"
            action={status.mode !== 'attached' ? (
              <Button variant="ghost" size="sm" onClick={() => RestartEngine().catch(() => {})}>Restart</Button>
            ) : undefined}
          >
            Engine stopped: {status.engineFatal}
          </Banner>
        </div>
      )}
      {status.mode === 'attached' && (
        <div className="col-span-12">
          <Banner tone="info">Running as a Windows service — this window is a viewer.</Banner>
        </div>
      )}

      {/* Pipeline hero: the traffic path as three machined stations, flow
          streaming between them when live. A warn/bad link leaks tone-colored
          light from behind the glass. */}
      <div className="col-span-12">
      <Bleed
        color={linkState === 'bad' ? 'var(--bad)' : linkState === 'warn' ? 'var(--warn)' : null}
        strength="20%"
      >
      <Card pad={false}>
        <div className="grid grid-cols-1 gap-3 p-5 @3xl:grid-cols-[1fr_3rem_1fr_3rem_1fr] @3xl:gap-0">
          <PipeNode
            icon={<IconServer size={20} />}
            title={isAgent ? 'Local server' : 'Agent link'}
            state={isAgent ? localState : linkState}
            headline={isAgent
              ? (localState === 'good' ? 'Online' : localState === 'bad' ? 'Not reachable' : 'Checking…')
              : (status.agentConnected ? 'Connected' : 'Waiting')}
            detail={isAgent
              ? (firstTunnel?.localUp ? 'Minecraft is ready' : 'Start your Minecraft server')
              : (status.agentConnected ? 'Agent is dialed in' : 'No agent connected yet')}
          />
          <Conduit on={flowing && (isAgent ? localState !== 'bad' : true)} label={leftHop > 0 ? fmtBytes(leftHop) : undefined} />
          <PipeNode
            icon={<IconLink size={20} />}
            title="Tunnel link"
            state={linkState}
            pulse
            headline={isAgent
              ? (status.linkUp ? 'Connected' : 'Reconnecting…')
              : (status.agentConnected ? 'Active' : 'Idle')}
            detail={isAgent
              ? (status.linkUp ? `${status.rttMillis} ms round trip` : 'Retrying with backoff')
              : (status.agentConnected ? 'Relaying player traffic' : 'Listening for the agent')}
            extra={status.peerAddr ? (
              <span className="inline-flex max-w-full items-center gap-1 text-[11px] text-[var(--text-3)]">
                <span className="select-text truncate font-mono">{status.peerAddr}</span>
                <CopyIcon text={status.peerAddr} title="Copy peer address" />
              </span>
            ) : undefined}
          />
          <Conduit on={flowing && portState === 'good'} label={rightHop > 0 ? fmtBytes(rightHop) : undefined} />
          <PipeNode
            icon={<IconGlobe size={20} />}
            title="Public port"
            state={portState}
            headline={isAgent
              ? (firstTunnel && firstTunnel.publicPort > 0 ? `Port ${firstTunnel.publicPort}` : 'Not bound')
              : (tunnels.length ? `${tunnels.length} tunnel${tunnels.length === 1 ? '' : 's'}` : 'None')}
            detail={isAgent
              ? (firstTunnel && firstTunnel.publicPort > 0 ? 'Players connect here' : 'Waiting for the gateway')
              : 'Ports opened'}
            extra={(
              <span className="font-mono text-[11px] tabular-nums text-[var(--text-3)]">
                {conns.length} live session{conns.length === 1 ? '' : 's'}
              </span>
            )}
          />
        </div>
      </Card>
      </Bleed>
      </div>

      {/* Link health beside the headline stats: verdict + signals left, the
          2×2 numbers right; both collapse to full width in a narrow window. */}
      <div className="col-span-12 @5xl:col-span-7">
        <HealthPanel status={status} />
      </div>
      <div className="col-span-12 grid grid-cols-2 content-start gap-[var(--grid-gap)] @5xl:col-span-5">
        <StatTile size="lg" icon={<IconClock size={15} />} label="Link uptime" value={linkUptime} sub={uptimeSub || undefined} />
        <StatTile size="lg" icon={<IconGauge size={15} />} label="Round trip" value={(isAgent ? status.linkUp : status.agentConnected) ? `${status.rttMillis} ms` : '—'} />
        <StatTile
          size="lg" icon={<IconUsers size={15} />} label="Live sessions" accent={conns.length > 0}
          value={<NumberTicker value={conns.length} format={n => String(Math.round(n))} />}
        />
        <StatTile
          size="lg" icon={<IconActivity size={15} />} label="Data moved"
          value={<NumberTicker value={appBytes} format={n => fmtBytes(Math.round(n))} />}
          sub={status.allTimeBytesIn + status.allTimeBytesOut > 0
            ? `all-time ${fmtBytes(status.allTimeBytesIn + status.allTimeBytesOut)}`
            : undefined}
        />
      </div>

      {/* Identity: who's on each end, wearing its role emblem. */}
      <div className="col-span-12 @3xl:col-span-6">
        <IdentityCard
          role={isAgent ? 'agent' : 'gateway'} self
          sideLabel={`This machine · ${isAgent ? 'Agent' : 'Gateway'}`}
          host={status.hostname} publicIp={status.publicIp} lanIps={status.localLanIps} online
        />
      </div>
      <div className="col-span-12 @3xl:col-span-6">
        <IdentityCard
          role={isAgent ? 'gateway' : 'agent'}
          sideLabel={`Peer · ${isAgent ? 'Gateway' : 'Agent'}`}
          host={status.peerHostname} publicIp={status.peerPublicIp} lanIps={status.peerLanIps}
          online={isAgent ? status.linkUp : status.agentConnected}
        />
      </div>

      {/* Role tool beside the bandwidth teaser. */}
      <div className="col-span-12 @5xl:col-span-7">
        <Bleed color="var(--accent)" strength="8%">
          {isAgent
            ? <PlayerAddressCard status={status} onNavigate={onNavigate} />
            : <GatewayPairingCard />}
        </Bleed>
      </div>
      <div className="col-span-12 @5xl:col-span-5">
        <BandwidthPanel compact historyUnsupported={status.historyUnsupported} onExpand={() => onNavigate('traffic')} />
      </div>
    </div>
  )
}

const HEALTH_LABEL: Record<Seg, string> = {good: 'Healthy', warn: 'Fair', bad: 'Poor', unknown: 'Unknown'}
const HEALTH_TONE: Record<Seg, 'good' | 'warn' | 'bad' | 'neutral'> = {good: 'good', warn: 'warn', bad: 'bad', unknown: 'neutral'}

const fmtMs = (v: number): string => (v < 0 ? '—' : `${v.toFixed(1)} ms`)
const fmtPct = (v: number): string => (v < 0 ? '—' : `${v.toFixed(v > 0 && v < 10 ? 1 : 0)}%`)

/** HealthPanel: the link-health rollup. The verdict sits beside jitter and
 * packet loss as explicit numbers, plus round trip. Thresholds mirror the
 * backend's score exactly. The latency probe expands inline underneath. */
function HealthPanel({status}: {status: UIStatus}) {
  const isAgent = status.role === 'agent'
  const linked = isAgent ? status.linkUp : status.agentConnected
  const score = (status.healthScore || 'unknown') as Seg
  const c = segColor[score]

  return (
    <Card pad={false}>
      <div className="grid grid-cols-1 gap-4 p-5 sm:grid-cols-[auto_1px_1fr] sm:items-center">
        <div className="flex items-center gap-3 sm:pr-1">
          <span
            className="grid h-12 w-12 place-items-center rounded-[var(--r-lg)] border transition-all duration-500"
            style={{
              color: c,
              borderColor: `color-mix(in srgb, ${c} 40%, var(--border))`,
              background: `color-mix(in srgb, ${c} 10%, transparent)`,
              boxShadow: score === 'good'
                ? `inset 0 1px 0 color-mix(in srgb, var(--bevel-top) 55%, white), 0 0 24px -6px color-mix(in srgb, ${c} 60%, transparent)`
                : 'inset 0 1px 0 var(--bevel-top)',
            }}
          >
            <IconActivity size={22} />
          </span>
          <div>
            <div className="text-[11px] font-semibold uppercase tracking-wider text-[var(--text-3)]">Link health</div>
            <div className="mt-0.5"><Badge tone={HEALTH_TONE[score]}>{HEALTH_LABEL[score]}</Badge></div>
          </div>
        </div>
        <div className="pf-sep-v hidden self-stretch sm:block" aria-hidden />
        <div className="grid grid-cols-3 gap-3">
          <HealthMetric label="Jitter" value={fmtMs(status.jitterMillis)} tone={status.jitterMillis < 0 ? 'unknown' : status.jitterMillis > 100 ? 'bad' : status.jitterMillis > 30 ? 'warn' : 'good'} />
          <HealthMetric label="Packet loss" value={fmtPct(status.packetLossPct)} tone={status.packetLossPct < 0 ? 'unknown' : status.packetLossPct > 5 ? 'bad' : status.packetLossPct > 1 ? 'warn' : 'good'} />
          <HealthMetric label="Round trip" value={linked ? `${status.rttMillis} ms` : '—'} tone="neutral" />
        </div>
      </div>
      <LatencyProbe linked={linked} peer={isAgent ? 'gateway' : 'agent'} />
    </Card>
  )
}

function HealthMetric({label, value, tone}: {label: string; value: string; tone: Seg | 'neutral'}) {
  const c = tone === 'neutral' ? 'var(--text)' : segColor[tone]
  return (
    <div className="rounded-[var(--r-md)] border border-[var(--border)] bg-[var(--input-bg)] p-3">
      <div className="text-[11px] text-[var(--text-3)]">{label}</div>
      <div className="mt-1 text-xl font-semibold tabular-nums" style={{color: c}}>{value}</div>
    </div>
  )
}

/** LatencyProbe: an expandable strip inside the health card. Headlines the
 * honest ½-RTT estimate; per-direction one-way values (clock-sync dependent)
 * sit underneath with a caveat. Works from either role. */
function LatencyProbe({linked, peer}: {linked: boolean; peer: 'gateway' | 'agent'}) {
  const [open, setOpen] = useState(false)
  const [res, setRes] = useState<app.LatencyResult | null>(null)
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState(false)
  const run = async () => {
    setBusy(true); setErr(''); setRes(null)
    try { setRes(await MeasureLatency()) }
    catch (e) { setErr(String(e)) }
    finally { setBusy(false) }
  }
  return (
    <div>
      <div className="pf-sep" aria-hidden />
      <button
        onClick={() => setOpen(o => !o)}
        aria-expanded={open}
        className="flex w-full items-center justify-between px-5 py-2.5 text-left text-sm text-[var(--text-2)] transition-colors hover:bg-[color-mix(in_srgb,var(--panel-2)_60%,transparent)] hover:text-[var(--text)]"
      >
        <span className="font-medium">Latency probe</span>
        <span className="text-xs text-[var(--text-3)]">{open ? 'Hide' : `Burst-test the live link to the ${peer}`}</span>
      </button>
      <div className="pf-expand" data-open={open}>
        <div>
          <div className="px-5 pb-4">
            <div className="flex items-center gap-3">
              <Button size="sm" onClick={run} disabled={busy || !linked}>{busy ? 'Measuring…' : 'Measure now'}</Button>
              {!linked && <span className="text-xs text-[var(--text-3)]">{peer === 'agent' ? 'No agent connected' : 'Link is down'}</span>}
            </div>
            {res && (
              <div className="pf-fade mt-4">
                <div className="flex flex-wrap items-end gap-x-6 gap-y-2">
                  <div>
                    <div className="text-[11px] text-[var(--text-3)]">One-way (estimated)</div>
                    <div className="text-2xl font-semibold tabular-nums text-[var(--accent)]">{res.oneWayEstimateMs.toFixed(1)} ms</div>
                  </div>
                  <div className="text-xs tabular-nums text-[var(--text-3)]">
                    round trip {res.rttAvgMs.toFixed(1)} ms ({res.rttMinMs.toFixed(1)}–{res.rttMaxMs.toFixed(1)}) · jitter {res.jitterMs.toFixed(1)} ms · {res.samples} probes
                  </div>
                </div>
                {res.haveOneWay && (
                  <div className="pf-well mt-3 px-3 py-2">
                    <div className="flex flex-wrap gap-x-6 gap-y-1 text-sm tabular-nums">
                      <span className="text-[var(--text-2)]">↑ upload <b className="text-[var(--text)]">{res.oneWayUpMs.toFixed(1)} ms</b></span>
                      <span className="text-[var(--text-2)]">↓ download <b className="text-[var(--text)]">{res.oneWayDownMs.toFixed(1)} ms</b></span>
                    </div>
                    {res.clockSyncCaveat && (
                      <div className="mt-1 text-[11px] text-[var(--text-3)]">
                        Per-direction values assume NTP-synced clocks on both machines; treat them as approximate.
                      </div>
                    )}
                  </div>
                )}
              </div>
            )}
            {err && <div className="mt-3"><ErrorBanner message={err} onDismiss={() => setErr('')} /></div>}
          </div>
        </div>
      </div>
    </div>
  )
}

/** IdentityCard: one end of the tunnel — role emblem, hostname, public IP
 * prominent, LAN quiet. `self` rides the live accent; the peer wears its
 * fixed role swatch, so both ends read in ecosystem colors at a glance. */
function IdentityCard({role, self = false, sideLabel, host, publicIp, lanIps, online}: {
  role: 'agent' | 'gateway'
  self?: boolean
  sideLabel: string; host?: string; publicIp?: string; lanIps?: string[]; online: boolean
}) {
  const lan = (lanIps ?? []).filter(Boolean)
  return (
    <Card pad={false} className="h-full">
      <div className="flex items-start gap-3.5 p-4">
        <Emblem role={role} size={38} fixed={!self} glow={online} />
        <div className="min-w-0 flex-1">
          <div className="text-[11px] font-semibold uppercase tracking-wider text-[var(--text-3)]">{sideLabel}</div>
          <div className="mt-1 flex items-center gap-2">
            <Badge tone="neutral"><IconServer size={12} /> {host || '—'}</Badge>
          </div>
          <div className="mt-2 flex items-center gap-1.5">
            <IconGlobe size={13} className="shrink-0 text-[var(--text-3)]" />
            <span className="select-text truncate font-mono text-sm font-semibold text-[var(--text)]">{publicIp || '—'}</span>
            {publicIp && <CopyIcon text={publicIp} title="Copy public address" />}
          </div>
          {lan.length > 0 && (
            <div className="mt-0.5 truncate font-mono text-[11px] text-[var(--text-3)]">
              LAN {lan.join(', ')}
            </div>
          )}
        </div>
        <StatusDot state={online ? 'good' : 'unknown'} label="" pulse={online} />
      </div>
    </Card>
  )
}

/** PipeNode: one station of the pipeline — a milled inset slab with a
 * state-lit icon chip and the station's verdict. */
function PipeNode({icon, title, state, headline, detail, extra, pulse}: {
  icon: ReactNode; title: string; state: Seg; headline: string; detail: string; extra?: ReactNode; pulse?: boolean
}) {
  const c = segColor[state]
  return (
    <div className="flex min-w-0 items-start gap-3 rounded-[var(--r-lg)] border border-[var(--border)] bg-[var(--input-bg)] p-3.5 shadow-[inset_0_1px_0_var(--hairline)]">
      <div
        className="grid h-11 w-11 shrink-0 place-items-center rounded-[var(--r-md)] border transition-all duration-500"
        style={{
          color: c,
          borderColor: `color-mix(in srgb, ${c} 40%, var(--border))`,
          background: `color-mix(in srgb, ${c} 9%, transparent)`,
          boxShadow: state === 'good' ? `0 0 24px -6px color-mix(in srgb, ${c} 60%, transparent)` : 'none',
        }}
      >
        {icon}
      </div>
      <div className="min-w-0 flex-1">
        <div className="text-[11px] font-semibold uppercase tracking-wider text-[var(--text-3)]">{title}</div>
        <div className="mt-0.5 truncate text-base font-semibold leading-tight">{headline}</div>
        <div className="mt-1"><StatusDot state={state} label={detail} pulse={pulse} /></div>
        {extra && <div className="mt-1 flex max-w-full">{extra}</div>}
      </div>
    </div>
  )
}

/** Bleed: ambient light escaping from behind a card (glass.css pf-bleed).
 * Renders a stable wrapper so entrance staggers don't reshuffle; the glow
 * only exists while `color` is set. */
function Bleed({color, strength = '14%', children}: {color: string | null; strength?: string; children: ReactNode}) {
  return (
    <div
      className={color ? 'pf-bleed relative' : undefined}
      style={color ? {['--bleed' as string]: color, ['--bleed-strength' as string]: strength} : undefined}
    >
      {children}
    </div>
  )
}

/** Conduit between two pipeline stations: dashes stream across when live;
 * the byte count for that hop rides just beneath the line. */
function Conduit({on, label}: {on: boolean; label?: string}) {
  return (
    <div className="hidden flex-col items-center justify-center gap-1.5 @3xl:flex" aria-hidden>
      <div
        className="pf-conduit"
        data-on={on}
        style={{['--flow-color' as string]: 'var(--accent)'}}
      />
      {label && (
        <span className="whitespace-nowrap font-mono text-[10px] tabular-nums text-[var(--text-3)]">
          {label}
        </span>
      )}
    </div>
  )
}

/** PlayerAddressCard (agent): the address to hand to players, front and
 * center. Reachability testing lives on each tunnel card. */
function PlayerAddressCard({status, onNavigate}: {status: UIStatus; onNavigate: (id: NavId) => void}) {
  const first = (status.tunnels ?? [])[0]
  const host = (status.peerAddr || '').split(':')[0] || status.peerPublicIp || ''
  const bound = !!(first && first.publicPort > 0)
  const addr = bound && host
    ? (first.publicPort === 25565 ? host : `${host}:${first.publicPort}`)
    : ''
  return (
    <Card
      title="Player address" subtitle="What your players type into Minecraft"
      action={addr ? <CopyButton text={addr} label="Copy address" /> : undefined}
    >
      {addr ? (
        <div className="pf-fade"><Codebox text={addr} /></div>
      ) : (
        <div className="text-sm text-[var(--text-3)]">Not bound yet — the address appears when the gateway opens your port.</div>
      )}
      <p className="mt-3 text-xs text-[var(--text-3)]">
        Not sure it works from the internet?{' '}
        <button className="font-medium text-[var(--accent)] hover:underline" onClick={() => onNavigate('tunnels')}>
          Test the player path
        </button>{' '}
        from the tunnel card.
      </p>
    </Card>
  )
}

function GatewayPairingCard() {
  const [code, setCode] = useState('')
  const [err, setErr] = useState('')
  useEffect(() => {
    let cancelled = false
    const poll = (n: number) => {
      PairingCode().then(c => { if (!cancelled) setCode(c) })
        .catch(e => { if (!cancelled) { if (n < 12) setTimeout(() => poll(n + 1), 300); else setErr(String(e)) } })
    }
    poll(0)
    return () => { cancelled = true }
  }, [])
  return (
    <Card title="Pairing code" subtitle="Paste this into the agent on your Minecraft machine"
      action={code ? <CopyButton text={code} /> : undefined}>
      {code
        ? <div className="pf-fade"><Codebox text={code} /></div>
        : err
          ? <ErrorBanner message={err} />
          : <div className="text-sm text-[var(--text-3)]">Generating…</div>}
      <p className="mt-3 text-xs text-[var(--text-3)]">
        Anyone with this code can connect an agent. Rotate it in Settings if it leaks.
      </p>
    </Card>
  )
}
