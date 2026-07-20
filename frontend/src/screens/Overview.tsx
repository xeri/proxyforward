import {ReactNode, useEffect, useRef, useState} from 'react'
import {
  ApproveGatewayProposal, IssuePairingCode, MeasureLatency,
  RejectGatewayProposal, RestartEngine,
} from '../../wailsjs/go/app/App'
import {PowerControls} from '../components/PowerControls'
import {powerStart, useServiceRunning} from '../power'
import {prefersReduced} from '../motion'
import {agent, app} from '../../wailsjs/go/models'
import {proposalAge, ProposalDiff, specsOf, useAgentConsent} from '../consent'
import {BandwidthPanel} from '../components/BandwidthChart'
import {NumberTicker} from '../components/NumberTicker'
import {
  Badge, Banner, Button, Card, Codebox, CopyButton, CopyIcon, ErrorBanner,
  IconButton, Overline, PageHeader, RoleWord, SignalCard, StatTile, StatusDot, TextInput,
} from '../components/ui'
import {Emblem} from '../components/Emblem'
import {IconActivity, IconEye, IconEyeOff, IconGlobe, IconLink, IconRefresh, IconServer} from '../components/icons'
import {NavId} from '../nav'
import {fmtBytes, fmtRtt, fmtUptime, hasRtt, isEngineOff, runStateOf, UIStatus} from '../state'

type Seg = 'good' | 'warn' | 'bad' | 'unknown'

const segColor: Record<Seg, string> = {
  good: 'var(--good)', warn: 'var(--warn)', bad: 'var(--bad)', unknown: 'var(--text-3)',
}

/** GatewayProposalBanner (agent role): the gateway operator has authored a change to
 * this machine's tunnels and it is waiting on the person sitting here. It applies
 * only on approval — that is the whole promise of config-consent — so the diff is
 * spelled out and both answers are one click. A rejection asks for a reason, which
 * the operator sees verbatim in their event log.
 *
 * A plain Banner in the existing alert stack: the pipeline below is this page's one
 * identity surface, and nothing else may answer the pointer. */
function GatewayProposalBanner({proposal, current, onDecided}: {
  proposal: agent.ProposalView
  current: {id: string; name?: string; publicPort?: number}[]
  onDecided: () => void
}) {
  const [reason, setReason] = useState('')
  const [rejecting, setRejecting] = useState(false)
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')

  const decide = (fn: () => Promise<void>) => {
    setBusy(true); setErr('')
    fn().then(() => { setRejecting(false); setReason(''); onDecided() })
      .catch(e => setErr(String(e?.message ?? e)))
      .finally(() => setBusy(false))
  }

  return (
    <Banner tone="warn">
      <div className="space-y-2.5">
        <div>
          <div className="font-medium">The gateway proposes a change to your tunnels</div>
          <div className="text-[12px] text-[var(--text-3)]">Sent {proposalAge(proposal.atMs)}. Nothing on this machine changes unless you approve it.</div>
        </div>
        <ProposalDiff current={current} proposed={specsOf(proposal.tunnels)} />
        {rejecting ? (
          <div className="space-y-2">
            <TextInput value={reason} onChange={setReason} size="sm" autoFocus
              placeholder="Why? The gateway operator sees this."
              onEnter={() => { if (!busy) decide(() => RejectGatewayProposal(proposal.proposalId, reason)) }} />
            <div className="flex gap-2">
              <Button size="sm" variant="danger" disabled={busy}
                onClick={() => decide(() => RejectGatewayProposal(proposal.proposalId, reason))}>Reject</Button>
              <Button size="sm" variant="ghost" disabled={busy} onClick={() => { setRejecting(false); setErr('') }}>Cancel</Button>
            </div>
          </div>
        ) : (
          <div className="flex gap-2">
            <Button size="sm" disabled={busy}
              onClick={() => decide(() => ApproveGatewayProposal(proposal.proposalId))}>Approve</Button>
            <Button size="sm" variant="subtle" disabled={busy} onClick={() => setRejecting(true)}>Reject…</Button>
          </div>
        )}
        {err && <div className="text-[12px] text-[var(--bad)]">{err}</div>}
      </div>
    </Banner>
  )
}

/** StoppedBanner: the honest "you turned this off" surface — warn tone, not the
 * crash's red, with the one action that undoes it. It carries Start (the header
 * control is hidden while off, so this owns it); powerStart routes to the
 * service when one is running. A plain Banner in the alert stack, never on the
 * pipeline (DESIGN rule 2). */
function StoppedBanner({isAgent, status}: {isAgent: boolean; status: UIStatus}) {
  const svcRunning = useServiceRunning(status)
  const [busy, setBusy] = useState(false)
  const start = () => {
    setBusy(true)
    powerStart(svcRunning).catch(() => {}).finally(() => setBusy(false))
  }
  return (
    <Banner
      tone="warn"
      action={<Button variant="primary" size="sm" onClick={start} disabled={busy}>Start</Button>}
    >
      {isAgent
        ? 'Tunnel stopped — players can’t reach your server until you start it again.'
        : 'Tunnel stopped — the public port is closed until you start it again.'}
    </Banner>
  )
}

/** Overview: the tunnel at a glance — alerts, the traffic pipeline, link
 * health with an inline latency probe, headline stats, both identities, the
 * role tool, and a one-hour bandwidth teaser that jumps to Traffic. */
export function Overview({status, onNavigate}: {status: UIStatus; onNavigate: (id: NavId) => void}) {
  const isAgent = status.role === 'agent'
  // Only the agent role can be asked to approve anything, so only it polls.
  const consent = useAgentConsent(isAgent)
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

  const pausedCount = tunnels.filter(t => t.paused).length

  // Deliberately off: the link is down because nothing is dialing, not because
  // a dial is failing. So the pipeline goes neutral grey ('unknown'), never the
  // down-link's red, its conduits stop, and its headlines say so (below) —
  // color is signal, and there is no fault to signal.
  const off = isEngineOff(status)

  // Segment states.
  const localState: Seg = off ? 'unknown'
    : !firstTunnel || !firstTunnel.localKnown ? 'unknown'
      : firstTunnel.localUp ? 'good' : 'bad'
  const linkState: Seg = off ? 'unknown'
    : isAgent ? (status.linkUp ? 'good' : 'bad')
      : (status.agentConnected ? 'good' : 'warn')
  const portState: Seg = off ? 'unknown'
    : isAgent
      ? (firstTunnel && firstTunnel.publicPort > 0 ? 'good' : 'unknown')
      : (tunnels.length > 0 || status.agentConnected ? 'good' : 'unknown')
  const flowing = linkState === 'good'

  // Moment: first-connect ignite. When the link transitions up, stamp
  // .pf-ignite on the pipeline for one left-to-right cascade (motion.css).
  // A mount with the link already up doesn't fire.
  const [ignite, setIgnite] = useState(false)
  const wasFlowing = useRef<boolean | null>(null)
  useEffect(() => {
    const was = wasFlowing.current
    wasFlowing.current = flowing
    if (was === false && flowing && !prefersReduced()) {
      setIgnite(true)
      const t = setTimeout(() => setIgnite(false), 1400)
      return () => clearTimeout(t)
    }
  }, [flowing])

  // Per-hop byte counters. Each role annotates the hops it can see:
  // agent  — local↔agent = its conntrack totals, agent↔gateway = raw link
  // gateway — agent↔gateway = raw link, gateway↔clients = its conntrack
  const appBytes = status.totalBytesIn + status.totalBytesOut
  const linkBytes = status.linkBytesIn + status.linkBytesOut
  const leftHop = isAgent ? appBytes : linkBytes
  const rightHop = isAgent ? linkBytes : appBytes

  return (
    <div className="pf-stagger space-y-12">
      <div>
        <PageHeader
          title="Overview"
          subtitle={isAgent ? 'The path from your server to your players.' : 'The public front door for your agent.'}
          action={runStateOf(status) !== 'stopped'
            ? <PowerControls status={status} players={conns.length} />
            : undefined}
        />

        {status.engineFatal && (
          <div className="mb-4">
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
        {isEngineOff(status) && (
          <div className="mb-4">
            <StoppedBanner isAgent={isAgent} status={status} />
          </div>
        )}
        {status.mode === 'attached' && (
          <div className="mb-4">
            <Banner tone="info">The engine runs as a Windows service — this window controls it over the pipe.</Banner>
          </div>
        )}
        {/* Paused ≠ crashed: a quiet warn line in the stack, no red, no ambient
            change, and the conduits keep reflecting the control link (which is
            up — that is the point of pause). The controls live where the
            tunnels do; this only says so and points there. */}
        {!isEngineOff(status) && pausedCount > 0 && (
          <div className="mb-4">
            <Banner
              tone="warn"
              action={<Button variant="ghost" size="sm" onClick={() => onNavigate(isAgent ? 'tunnels' : 'agents')}>
                {isAgent ? 'Open Tunnels' : 'Open Agents'}
              </Button>}
            >
              {pausedCount === 1
                ? 'A tunnel is paused — players connecting to it see the offline message.'
                : `${pausedCount} tunnels are paused — players connecting to them see the offline message.`}
            </Banner>
          </div>
        )}
        {consent.view?.incoming && (
          <div className="mb-4">
            <GatewayProposalBanner
              proposal={consent.view.incoming}
              current={(status.tunnels ?? []).map(t => ({id: t.id, name: t.name, publicPort: t.publicPort}))}
              onDecided={consent.refresh}
            />
          </div>
        )}

        {/* The identity surface: the traffic path as three machined stations
            on Signal Glass, flow streaming between them when live. It breaks
            the content grid once — running past the column edges — so the
            pipeline reads as the page's sculpture. The bleed GROWS with the
            container (--hero-bleed, tokens.css) rather than switching on at a
            breakpoint: the old binary @7xl variant snapped the gutters from
            28px to 8px the moment the window crossed ~1080px, which is what
            read as the card resizing and then clipping. A warn/bad link leaks
            tone-colored light from behind the glass. */}
        <div className="mx-[calc(-1*var(--hero-bleed))]">
        <Bleed
          color={linkState === 'bad' ? 'var(--bad)' : linkState === 'warn' ? 'var(--warn)' : null}
          strength="20%"
        >
        <SignalCard pad={false} className={ignite ? 'pf-ignite' : ''}>
        {/* Five children, in order: node / conduit / node / conduit / node —
            motion.css's ignite cascade addresses the stations by nth-child.
            The conduits take a range instead of a fixed 3rem so the stations
            get the slack at mid widths instead of truncating their headlines. */}
        <div className="grid grid-cols-1 gap-3 p-5 @3xl:grid-cols-[minmax(0,1fr)_minmax(2rem,4rem)_minmax(0,1fr)_minmax(2rem,4rem)_minmax(0,1fr)] @3xl:gap-0">
          <PipeNode
            icon={<IconServer size={20} />}
            title={isAgent ? 'Local server' : 'Agent link'}
            state={isAgent ? localState : linkState}
            headline={off ? 'Stopped'
              : isAgent
                ? (localState === 'good' ? 'Online' : localState === 'bad' ? 'Not reachable' : 'Checking…')
                : (status.agentConnected ? 'Connected' : 'Waiting')}
            detail={off ? 'Tunnel is turned off'
              : isAgent
                ? (firstTunnel?.localUp ? 'Minecraft is ready' : 'Start your Minecraft server')
                : (status.agentConnected ? 'Agent is dialed in' : 'No agent connected yet')}
          />
          <Conduit on={flowing && (isAgent ? localState !== 'bad' : true)} label={leftHop > 0 ? fmtBytes(leftHop) : undefined} />
          <PipeNode
            icon={<IconLink size={20} />}
            title="Tunnel link"
            state={linkState}
            pulse={!off}
            headline={off ? 'Stopped'
              : isAgent
                ? (status.linkUp ? 'Connected' : 'Reconnecting…')
                : (status.agentConnected ? 'Active' : 'Idle')}
            detail={off ? 'Not dialing'
              : isAgent
                ? (status.linkUp ? (hasRtt(status.rttMillis) ? `${fmtRtt(status.rttMillis)} round trip` : 'Measuring round trip…') : 'Retrying with backoff')
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
              : (tunnels.length ? `${tunnels.length} tunnel${tunnels.length === 1 ? '' : 's'}` : 'No ports')}
            detail={off ? 'Closed while stopped'
              : isAgent
                ? (firstTunnel && firstTunnel.publicPort > 0 ? 'Players connect here' : 'Waiting for the gateway')
                : (tunnels.length ? 'Open to players' : 'Waiting for tunnels')}
            extra={(
              <span className="font-mono text-[11px] tabular-nums text-[var(--text-3)]">
                {conns.length} live session{conns.length === 1 ? '' : 's'}
              </span>
            )}
          />
        </div>
      </SignalCard>
      </Bleed>
      </div>
      </div>

      {/* The data group: bandwidth teaser promoted beside link health and the
          headline numbers — all quiet, the pipeline keeps the stage. */}
      <div className="grid grid-cols-12 gap-[var(--grid-gap)]">
        <div className="col-span-12 @5xl:col-span-7">
          <BandwidthPanel compact historyUnsupported={status.historyUnsupported} onExpand={() => onNavigate('traffic')} />
        </div>
        <div className="col-span-12 space-y-[var(--grid-gap)] @5xl:col-span-5">
          <HealthPanel status={status} />
          <div className="grid grid-cols-2 gap-x-[var(--grid-gap)] gap-y-6 pt-2">
            <StatTile label="Link uptime" value={linkUptime} sub={uptimeSub || undefined} />
            <StatTile label="Round trip" value={(isAgent ? status.linkUp : status.agentConnected) && hasRtt(status.rttMillis) ? fmtRtt(status.rttMillis) : '—'} />
            <StatTile
              label="Live sessions" accent={conns.length > 0}
              value={<NumberTicker value={conns.length} format={n => String(Math.round(n))} />}
            />
            <StatTile
              label="Data moved"
              value={<NumberTicker value={appBytes} format={n => fmtBytes(Math.round(n))} />}
              sub={status.allTimeBytesIn + status.allTimeBytesOut > 0
                ? `all-time ${fmtBytes(status.allTimeBytesIn + status.allTimeBytesOut)}`
                : undefined}
            />
          </div>
        </div>
      </div>

      {/* Identity: who's on each end, wearing its role emblem, with the role
          tool — the one string the user actually hands out — as a strip. */}
      <div className="grid grid-cols-12 gap-[var(--grid-gap)]">
        <div className="col-span-12 @3xl:col-span-6">
          <IdentityCard
            role={isAgent ? 'agent' : 'gateway'} self
            sideLabel={<>This machine · <RoleWord role={isAgent ? 'agent' : 'gateway'}>{isAgent ? 'Agent' : 'Gateway'}</RoleWord></>}
            host={status.hostname} publicIp={status.publicIp} lanIps={status.localLanIps} online
          />
        </div>
        <div className="col-span-12 @3xl:col-span-6">
          <IdentityCard
            role={isAgent ? 'gateway' : 'agent'}
            sideLabel={<>Peer · <RoleWord role={isAgent ? 'gateway' : 'agent'}>{isAgent ? 'Gateway' : 'Agent'}</RoleWord></>}
            host={status.peerHostname} publicIp={status.peerPublicIp} lanIps={status.peerLanIps}
            online={isAgent ? status.linkUp : status.agentConnected}
          />
        </div>
        <div className="col-span-12">
          {isAgent
            ? <PlayerAddressStrip status={status} onNavigate={onNavigate} />
            : <GatewayPairingStrip />}
        </div>
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
      <div className="flex flex-col">
        <div className="flex flex-wrap items-center gap-x-6 gap-y-4 p-5">
          <div className="flex items-center gap-3">
            <span
              className="grid h-12 w-12 place-items-center rounded-[var(--r-lg)] border transition-all duration-500"
              style={{
                color: c,
                borderColor: `color-mix(in srgb, ${c} 40%, var(--border))`,
                // Catch-light as a padding-box band; an `inset 0 1px 0` specks
                // at the corners (glass.css, the rim primitive). backgroundImage,
                // not the `background` shorthand — React warns when a style
                // object updates a shorthand next to a longhand it subsumes
                // (backgroundClip), and this tile re-tints as health changes.
                backgroundImage: [
                  `linear-gradient(180deg, ${score !== 'unknown'
                    ? 'color-mix(in srgb, var(--bevel-top) 55%, white)'
                    : 'var(--bevel-top)'} 0 1px, transparent 1px)`,
                  `linear-gradient(color-mix(in srgb, ${c} 10%, transparent), color-mix(in srgb, ${c} 10%, transparent))`,
                ].join(', '),
                backgroundClip: 'padding-box, border-box',
                boxShadow: score !== 'unknown'
                  ? `0 0 24px -6px color-mix(in srgb, ${c} 60%, transparent)`
                  : undefined,
              }}
            >
              <IconActivity size={22} />
            </span>
            <div>
              <Overline>Link health</Overline>
              <div className="mt-0.5"><Badge tone={HEALTH_TONE[score]}>{HEALTH_LABEL[score]}</Badge></div>
            </div>
          </div>
          <div className="grid min-w-0 flex-1 grid-cols-3 gap-3">
            <HealthMetric label="Jitter" value={fmtMs(status.jitterMillis)} tone={status.jitterMillis < 0 ? 'unknown' : status.jitterMillis > 100 ? 'bad' : status.jitterMillis > 30 ? 'warn' : 'good'} />
            <HealthMetric label="Packet loss" value={fmtPct(status.packetLossPct)} tone={status.packetLossPct < 0 ? 'unknown' : status.packetLossPct > 5 ? 'bad' : status.packetLossPct > 1 ? 'warn' : 'good'} />
            <HealthMetric label="Round trip" value={linked && hasRtt(status.rttMillis) ? fmtRtt(status.rttMillis) : '—'} tone="neutral" />
          </div>
        </div>
        <LatencyProbe linked={linked} peer={isAgent ? 'gateway' : 'agent'} />
      </div>
    </Card>
  )
}

/** HealthMetric: tier-3 — type on whitespace with a hairline lead, no box.
 * Status tones stay on the numerals: the color IS the signal. */
function HealthMetric({label, value, tone}: {label: string; value: string; tone: Seg | 'neutral'}) {
  const c = tone === 'neutral' ? 'var(--text)' : segColor[tone]
  return (
    <div className="min-w-0 border-l border-[var(--hairline)] pl-3">
      <Overline>{label}</Overline>
      <div className="font-display mt-1 truncate text-[length:var(--fs-metric)] font-bold leading-[var(--lh-tight)] tabular-nums" style={{color: c}} title={value}>{value}</div>
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
          <div className="px-5 pt-2 pb-4">
            <div className="flex items-center gap-3">
              <Button size="sm" onClick={run} disabled={busy || !linked}>{busy ? 'Measuring…' : 'Measure now'}</Button>
              {!linked && <span className="text-xs text-[var(--text-3)]">{peer === 'agent' ? 'No agent connected' : 'Link is down'}</span>}
            </div>
            {res && (
              <div className="pf-fade mt-4">
                <div className="flex flex-wrap items-end gap-x-6 gap-y-2">
                  <div>
                    <Overline>One-way (estimated)</Overline>
                    <div className="font-display text-[length:var(--fs-metric)] font-bold leading-[var(--lh-tight)] tabular-nums text-[var(--text)]">{res.oneWayEstimateMs.toFixed(1)} ms</div>
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
  sideLabel: ReactNode; host?: string; publicIp?: string; lanIps?: string[]; online: boolean
}) {
  const lan = (lanIps ?? []).filter(Boolean)
  return (
    <Card pad={false} className="h-full">
      <div className="flex items-start gap-3.5 p-4">
        <Emblem role={role} size={38} fixed={!self} glow={online} />
        <div className="min-w-0 flex-1">
          <div className="flex items-baseline gap-2">
            <Overline>{sideLabel}</Overline>
            <span className="truncate text-[11px] text-[var(--text-3)]">{host || '—'}</span>
          </div>
          <div className="mt-2 flex items-center gap-1.5">
            <span className="select-text truncate font-mono text-[14px] font-semibold text-[var(--text)]">{publicIp || '—'}</span>
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
    <div className="pf-pipenode pf-control relative flex min-w-0 items-start gap-3 rounded-[var(--r-lg)] bg-[var(--input-bg)] p-3.5">
      <div
        className="grid h-11 w-11 shrink-0 place-items-center rounded-[var(--r-md)] border transition-all duration-500"
        style={{
          color: c,
          borderColor: `color-mix(in srgb, ${c} 40%, var(--border))`,
          background: `color-mix(in srgb, ${c} 9%, transparent)`,
          boxShadow: state !== 'unknown' ? `0 0 24px -6px color-mix(in srgb, ${c} 60%, transparent)` : 'none',
        }}
      >
        {icon}
      </div>
      <div className="min-w-0 flex-1">
        <Overline>{title}</Overline>
        <div className="font-display mt-0.5 truncate text-[length:var(--fs-metric)] font-bold leading-[var(--lh-tight)]">{headline}</div>
        <div className="mt-1.5"><StatusDot state={state} label={detail} pulse={pulse} /></div>
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

/** PlayerAddressStrip (agent): the address to hand to players — one compact
 * row, not a titled card around one string. Reachability testing lives on
 * each tunnel card. */
function PlayerAddressStrip({status, onNavigate}: {status: UIStatus; onNavigate: (id: NavId) => void}) {
  const first = (status.tunnels ?? [])[0]
  const host = (status.peerAddr || '').split(':')[0] || status.peerPublicIp || ''
  const bound = !!(first && first.publicPort > 0)
  const addr = bound && host
    ? (first.publicPort === 25565 ? host : `${host}:${first.publicPort}`)
    : ''
  return (
    <Card pad={false}>
      <div className="flex flex-wrap items-center gap-x-5 gap-y-2 px-5 py-3.5">
        <div className="w-44 shrink-0">
          <Overline>Player address</Overline>
          <div className="mt-0.5 text-[11px] text-[var(--text-3)]">
            <button className="font-medium hover:text-[var(--text)] hover:underline" onClick={() => onNavigate('tunnels')}>
              Test the player path →
            </button>
          </div>
        </div>
        <div className="min-w-56 flex-1">
          {addr ? (
            <div className="pf-fade"><Codebox text={addr} action={<CopyButton text={addr} label="Copy" />} /></div>
          ) : (
            <span className="text-sm text-[var(--text-3)]">Not bound yet — the address appears when the gateway opens your port.</span>
          )}
        </div>
      </div>
    </Card>
  )
}

/** GatewayPairingStrip: enrollment front and centre — but nothing standing until
 * asked. Both rows mint on click: the strict single-use code (the default), and
 * the one-click reusable "friend code" for the two-people-who-know-each-other
 * case. Minting is a real act now — every live code is a visible, revocable
 * invite row in the Agents roster — so a code must never be minted as a side
 * effect of merely looking at this screen. Codes rest masked (screen-share
 * safe); copy works without revealing. */
function GatewayPairingStrip() {
  return (
    <Card pad={false}>
      <div className="space-y-3 px-5 py-3.5">
        <PairingCodeRow
          label="Pairing code" hint="Single-use · enrolls one agent, expires in 10 min"
          cta="Generate code" reissuable
          fetch={() => IssuePairingCode(false, 600, [], [], '')} />
        <div className="border-t border-[var(--border)] pt-3">
          <PairingCodeRow
            label="Friend code" hint="Reusable · never expires · for people you trust"
            hintAfter="Reusable · manage or revoke it under Agents"
            cta="Mint friend code"
            fetch={() => IssuePairingCode(true, 0, [], [], 'friend')} />
        </div>
      </div>
    </Card>
  )
}

/** PairingCodeRow: a mint-on-click, masked, copyable pairing code. The `cta`
 * button is the only thing that mints — a mount must never issue a ticket,
 * because every ticket is now a live invite row in the Agents roster (a mount
 * effect here once ghost-minted an invite per Overview visit). `reissuable`
 * adds a refresh that mints a fresh code; `hintAfter` swaps the hint once a
 * code exists (e.g. to say where the standing code now lives). A click while
 * the engine is still starting retries briefly before reporting the error. */
function PairingCodeRow({label, hint, hintAfter, cta, fetch, reissuable}: {
  label: string; hint: string; hintAfter?: string; cta: string
  fetch: () => Promise<string>; reissuable?: boolean
}) {
  const [code, setCode] = useState('')
  const [err, setErr] = useState('')
  const [shown, setShown] = useState(false)
  const [gen, setGen] = useState(0) // 0 = not asked yet; each mint bumps it
  const fetchRef = useRef(fetch)
  fetchRef.current = fetch
  useEffect(() => {
    if (gen === 0) return // nothing minted until the user asks
    let cancelled = false
    setCode(''); setErr(''); setShown(false)
    const poll = (n: number) => {
      fetchRef.current().then(c => { if (!cancelled) setCode(c) })
        .catch(e => { if (!cancelled) { if (n < 12) setTimeout(() => poll(n + 1), 300); else setErr(String((e as Error)?.message ?? e)) } })
    }
    poll(0)
    return () => { cancelled = true }
  }, [gen])
  // The mask is real text — selectable, it drags out of the app as bullets. Copy
  // still hands over the real code.
  const box = code
    ? <div className="pf-fade">
        <Codebox
          text={shown ? code : '•'.repeat(28)} selectable={shown}
          action={
            <div className="flex items-center gap-1.5">
              {reissuable && <IconButton title="Issue a fresh code" onClick={() => setGen(g => g + 1)}><IconRefresh size={16} /></IconButton>}
              <IconButton title={shown ? 'Hide pairing code' : 'Reveal pairing code'} onClick={() => setShown(s => !s)}>
                {shown ? <IconEyeOff size={16} /> : <IconEye size={16} />}
              </IconButton>
              <CopyButton text={code} label="Copy" />
            </div>
          }
        />
      </div>
    : err
      ? <ErrorBanner message={err} onDismiss={() => { setErr(''); setGen(0) }} />
      : gen === 0
        ? <Button size="sm" variant="ghost" onClick={() => setGen(1)}>{cta}</Button>
        : <span className="text-sm text-[var(--text-3)]">Generating…</span>
  return (
    <div className="flex flex-wrap items-center gap-x-5 gap-y-2">
      <div className="w-44 shrink-0">
        <Overline>{label}</Overline>
        <div className="mt-0.5 text-[11px] text-[var(--text-3)]">{code && hintAfter ? hintAfter : hint}</div>
      </div>
      <div className="min-w-56 flex-1">{box}</div>
    </div>
  )
}
