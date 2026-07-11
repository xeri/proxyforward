import {ReactNode, useEffect, useState} from 'react'
import {PairingCode, TestReachability} from '../../wailsjs/go/app/App'
import {BandwidthPanel} from '../components/BandwidthChart'
import {Button, Card, Codebox, CopyButton, CopyIcon, ErrorBanner, StatusDot} from '../components/ui'
import {IconBolt, IconGlobe, IconLink, IconServer} from '../components/icons'
import {fmtBytes, UIStatus} from '../state'

type Seg = 'good' | 'warn' | 'bad' | 'unknown'

const MONO = "ui-monospace, 'Cascadia Mono', Consolas, monospace"

/** Dashboard: pipeline hero (local server → tunnel → public port with live
 * traffic flow and per-hop counters), stat tiles with all-time sub-lines,
 * the bandwidth terminal, and the role-appropriate primary tool. */
export function Dashboard({status}: {status: UIStatus}) {
  const isAgent = status.role === 'agent'
  const tunnels = status.tunnels ?? []
  const conns = status.connections ?? []
  const firstTunnel = tunnels[0]

  // 1 Hz re-render so the uptime readouts tick between 2 Hz status snapshots.
  const [, tick] = useState(0)
  useEffect(() => {
    const t = setInterval(() => tick(x => x + 1), 1000)
    return () => clearInterval(t)
  }, [])
  const linkUptime = status.linkUpSinceMs ? fmtDur(Date.now() - status.linkUpSinceMs) : '—'
  const uptimeSub = [
    status.processStartMs ? `app ${fmtDur(Date.now() - status.processStartMs)}` : null,
    status.cumulativeUptimeMs ? `all-time ${fmtDur(status.cumulativeUptimeMs)}` : null,
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
    <div className="pf-stagger space-y-5">
      {status.engineFatal && <ErrorBanner message={`Engine stopped: ${status.engineFatal}`} />}

      {/* Pipeline hero: the traffic path, with flow streaming when live. */}
      <Card pad={false}>
        <div className="grid grid-cols-1 gap-4 p-5 md:grid-cols-[1fr_3.5rem_1fr_3.5rem_1fr] md:gap-0">
          <PipeNode
            icon={<IconServer size={22} />}
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
            icon={<IconLink size={22} />}
            title="Tunnel link"
            state={linkState}
            pulse
            headline={isAgent
              ? (status.linkUp ? 'Connected' : 'Reconnecting…')
              : (status.agentConnected ? 'Active' : 'Idle')}
            detail={isAgent
              ? (status.linkUp ? `${status.rttMillis} ms round-trip` : 'Retrying with backoff')
              : (status.agentConnected ? 'Relaying player traffic' : 'Listening for the agent')}
            extra={status.peerAddr ? (
              <span className="inline-flex max-w-full items-center gap-1 text-[11px] text-[var(--text-3)]">
                <span className="truncate" style={{fontFamily: MONO}}>{status.peerAddr}</span>
                <CopyIcon text={status.peerAddr} title="Copy peer address" />
              </span>
            ) : undefined}
          />
          <Conduit on={flowing && portState === 'good'} label={rightHop > 0 ? fmtBytes(rightHop) : undefined} />
          <PipeNode
            icon={<IconGlobe size={22} />}
            title="Public port"
            state={portState}
            headline={isAgent
              ? (firstTunnel && firstTunnel.publicPort > 0 ? `Port ${firstTunnel.publicPort}` : 'Not bound')
              : (tunnels.length ? `${tunnels.length} tunnel${tunnels.length === 1 ? '' : 's'}` : 'None')}
            detail={isAgent
              ? (firstTunnel && firstTunnel.publicPort > 0 ? 'Players can connect here' : 'Waiting for the gateway')
              : 'Ports opened by the agent'}
            extra={(
              <span className="text-[11px] tabular-nums text-[var(--text-3)]" style={{fontFamily: MONO}}>
                {conns.length} active conn{conns.length === 1 ? '' : 's'}
              </span>
            )}
          />
        </div>
      </Card>

      {/* Live stat tiles */}
      <div className="grid grid-cols-2 gap-4 md:grid-cols-4">
        <Stat label="Link uptime" value={linkUptime} sub={uptimeSub || undefined} />
        <Stat label={isAgent ? 'Round-trip' : 'Agent'} value={isAgent ? (status.linkUp ? `${status.rttMillis} ms` : '—') : (status.agentConnected ? 'Online' : 'Offline')} />
        <Stat label="Active connections" value={String(conns.length)} accent={conns.length > 0} />
        <Stat
          label="Transferred" value={fmtBytes(appBytes)}
          sub={status.allTimeBytesIn + status.allTimeBytesOut > 0
            ? `all-time ${fmtBytes(status.allTimeBytesIn + status.allTimeBytesOut)}`
            : undefined}
        />
      </div>

      {/* Bandwidth terminal: multi-range history, self-polling. */}
      <BandwidthPanel historyUnsupported={status.historyUnsupported} />

      {/* Role tool */}
      {isAgent
        ? <ReachabilityCard tunnelReady={!!(firstTunnel && firstTunnel.publicPort > 0)} tunnelID={firstTunnel?.id} port={firstTunnel?.publicPort} />
        : <GatewayPairingCard />}
    </div>
  )
}

const segColor: Record<Seg, string> = {
  good: 'var(--good)', warn: 'var(--warn)', bad: 'var(--bad)', unknown: 'var(--text-3)',
}

function PipeNode({icon, title, state, headline, detail, extra, pulse}: {
  icon: ReactNode; title: string; state: Seg; headline: string; detail: string; extra?: ReactNode; pulse?: boolean
}) {
  const c = segColor[state]
  return (
    <div className="flex min-w-0 flex-col items-center px-2 text-center">
      <div
        className="grid h-12 w-12 place-items-center rounded-2xl border transition-all duration-500"
        style={{
          color: c,
          borderColor: `color-mix(in srgb, ${c} 40%, var(--border))`,
          background: `color-mix(in srgb, ${c} 9%, transparent)`,
          boxShadow: state === 'good' ? `0 0 24px -6px color-mix(in srgb, ${c} 60%, transparent)` : 'none',
        }}
      >
        {icon}
      </div>
      <div className="mt-3 text-[11px] font-semibold uppercase tracking-wider text-[var(--text-3)]">{title}</div>
      <div className="mt-1 text-lg font-semibold leading-tight">{headline}</div>
      <div className="mt-1.5">
        <StatusDot state={state} label={detail} pulse={pulse} />
      </div>
      {extra && <div className="mt-1 flex max-w-full justify-center">{extra}</div>}
    </div>
  )
}

/** Conduit between two pipeline nodes: dashes stream across when live; the
 * label below carries that hop's lifetime byte count. */
function Conduit({on, label}: {on: boolean; label?: string}) {
  return (
    <div className="hidden flex-col items-center justify-start md:flex" aria-hidden>
      <div
        className="pf-conduit mt-[23px]"
        data-on={on}
        style={{['--flow-color' as string]: 'var(--accent-2)'}}
      />
      {label && (
        <span className="mt-1.5 whitespace-nowrap text-[10px] tabular-nums text-[var(--text-3)]" style={{fontFamily: MONO}}>
          {label}
        </span>
      )}
    </div>
  )
}

function Stat({label, value, sub, accent}: {label: string; value: string; sub?: string; accent?: boolean}) {
  return (
    <div className="pf-lift rounded-xl border border-[var(--border)] bg-[var(--panel)] p-3.5">
      <div className="text-xs text-[var(--text-3)]">{label}</div>
      <div
        className="mt-1 text-lg font-semibold tabular-nums"
        style={accent ? {
          background: 'linear-gradient(90deg, var(--accent), var(--accent-2))',
          WebkitBackgroundClip: 'text', backgroundClip: 'text', color: 'transparent',
        } : undefined}
      >{value}</div>
      {sub && <div className="mt-0.5 truncate text-[11px] tabular-nums text-[var(--text-3)]">{sub}</div>}
    </div>
  )
}

function ReachabilityCard({tunnelReady, tunnelID, port}: {
  tunnelReady: boolean; tunnelID?: string; port?: number
}) {
  const [result, setResult] = useState('')
  const [err, setErr] = useState('')
  const [testing, setTesting] = useState(false)
  const run = async () => {
    if (!tunnelID) return
    setTesting(true); setResult(''); setErr('')
    try { setResult(await TestReachability(tunnelID)) }
    catch (e) { setErr(String(e)) }
    finally { setTesting(false) }
  }
  return (
    <Card title="Public reachability" action={<IconBolt size={18} />}>
      <p className="text-sm text-[var(--text-2)]">
        Dials {port ? <span className="font-mono text-[var(--text)]">the gateway on port {port}</span> : 'the gateway'} across
        the real internet — the exact path a player takes (DNS → gateway firewall → router forward → tunnel → your server).
      </p>
      <div className="mt-3 flex items-center gap-3">
        <Button onClick={run} disabled={testing || !tunnelReady}>
          {testing ? 'Testing…' : 'Test now'}
        </Button>
        {!tunnelReady && <span className="text-xs text-[var(--text-3)]">Tunnel not bound yet</span>}
      </div>
      {result && <div className="pf-fade mt-3 rounded-lg border border-[color-mix(in_srgb,var(--good)_30%,transparent)] bg-[color-mix(in_srgb,var(--good)_10%,transparent)] px-3 py-2 text-sm text-[var(--good)]">{result}</div>}
      {err && <div className="mt-3"><ErrorBanner message={err} onDismiss={() => setErr('')} /></div>}
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
        Anyone with this code can connect an agent. Rotate it from Settings if it leaks.
      </p>
    </Card>
  )
}

function fmtDur(ms: number): string {
  const s = Math.max(0, Math.floor(ms / 1000))
  const d = Math.floor(s / 86400), h = Math.floor((s % 86400) / 3600), m = Math.floor((s % 3600) / 60), sec = s % 60
  if (d > 0) return `${d}d ${h}h`
  if (h > 0) return `${h}h ${m}m`
  if (m > 0) return `${m}m ${sec}s`
  return `${sec}s`
}
