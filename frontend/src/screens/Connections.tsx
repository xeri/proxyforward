import {useEffect, useState} from 'react'
import {Badge, Card, CopyIcon, EmptyState} from '../components/ui'
import {IconConnections, IconLink} from '../components/icons'
import {usePeers} from '../history'
import {fmtBytes, fmtRate, UIStatus} from '../state'

const MONO = "ui-monospace, 'Cascadia Mono', Consolas, monospace"

// Per-IP current-rate tracker: module-level so tab switches don't reset the
// baselines. Rates come from diffing live-connection byte totals between
// status ticks.
const ipRateState = new Map<string, {t: number; bytes: number; rate: number}>()

function currentRates(conns: {clientAddr: string; bytesIn: number; bytesOut: number}[]): Map<string, number> {
  const now = Date.now()
  const byIP = new Map<string, number>()
  for (const c of conns) {
    const ip = stripPort(c.clientAddr)
    byIP.set(ip, (byIP.get(ip) ?? 0) + c.bytesIn + c.bytesOut)
  }
  const rates = new Map<string, number>()
  for (const [ip, bytes] of byIP) {
    const prev = ipRateState.get(ip)
    if (!prev) {
      ipRateState.set(ip, {t: now, bytes, rate: 0})
      rates.set(ip, 0)
    } else if (now - prev.t >= 900) {
      const rate = Math.max(0, (bytes - prev.bytes) / ((now - prev.t) / 1000))
      ipRateState.set(ip, {t: now, bytes, rate})
      rates.set(ip, rate)
    } else {
      rates.set(ip, prev.rate)
    }
  }
  for (const ip of [...ipRateState.keys()]) {
    if (!byIP.has(ip)) ipRateState.delete(ip)
  }
  return rates
}

/** Connections: control-link hero, live sessions, and every client ever seen. */
export function Connections({status}: {status: UIStatus}) {
  const conns = [...(status.connections ?? [])].sort((a, b) => a.startedAt - b.startedAt)
  const peers = usePeers()

  // Re-render once a second so durations advance between 2 Hz status ticks.
  const [, tick] = useState(0)
  useEffect(() => {
    const t = setInterval(() => tick(x => x + 1), 1000)
    return () => clearInterval(t)
  }, [])

  const rates = currentRates(conns)
  const clients = mergeClients(peers, conns)

  return (
    <div className="pf-stagger space-y-5">
      <ControlLinkCard status={status} />

      <Card title="Active connections" pad={false}
        action={<div className="pr-4"><Badge tone={conns.length ? 'good' : 'neutral'}>{conns.length} live</Badge></div>}>
        {conns.length === 0 ? (
          <div className="px-4 pb-4">
            <EmptyState icon={<IconConnections size={28} />} title="No active connections"
              hint="Player sessions appear here in real time as they connect through the tunnel." />
          </div>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-y border-[var(--border)] text-left text-xs uppercase tracking-wide text-[var(--text-3)]">
                  <th className="px-4 py-2 font-medium">Client</th>
                  <th className="px-4 py-2 font-medium">Tunnel</th>
                  <th className="px-4 py-2 font-medium">Duration</th>
                  <th className="px-4 py-2 text-right font-medium">Received</th>
                  <th className="px-4 py-2 text-right font-medium">Sent</th>
                </tr>
              </thead>
              <tbody>
                {conns.map(c => (
                  <tr key={c.id} className="pf-fade border-b border-[var(--border)] transition-colors duration-200 last:border-0 hover:bg-[var(--panel-2)]/50">
                    <td className="px-4 py-2.5">
                      <span className="inline-flex items-center gap-1.5 font-mono text-[13px]">
                        {c.clientAddr}
                        <CopyIcon text={stripPort(c.clientAddr)} title="Copy IP" />
                      </span>
                    </td>
                    <td className="px-4 py-2.5 text-[var(--text-2)]">{c.tunnelName || '—'}</td>
                    <td className="px-4 py-2.5 tabular-nums text-[var(--text-2)]">{fmtElapsed(Date.now() - c.startedAt)}</td>
                    <td className="px-4 py-2.5 text-right tabular-nums">{fmtBytes(c.bytesOut)}</td>
                    <td className="px-4 py-2.5 text-right tabular-nums">{fmtBytes(c.bytesIn)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Card>

      <Card title="All clients" subtitle="Every IP that has ever connected, from the persistent stats store" pad={false}
        action={<div className="pr-4"><Badge tone="neutral">{clients.length}</Badge></div>}>
        {clients.length === 0 ? (
          <div className="px-4 pb-4">
            <EmptyState icon={<IconConnections size={28} />} title="No clients yet"
              hint="Lifetime per-IP stats build up here as players connect." />
          </div>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-y border-[var(--border)] text-left text-xs uppercase tracking-wide text-[var(--text-3)]">
                  <th className="px-4 py-2 font-medium">Client</th>
                  <th className="px-4 py-2 text-right font-medium">Rate</th>
                  <th className="px-4 py-2 text-right font-medium">Session</th>
                  <th className="px-4 py-2 text-right font-medium">Total ↓</th>
                  <th className="px-4 py-2 text-right font-medium">Total ↑</th>
                  <th className="px-4 py-2 text-right font-medium">Conns</th>
                  <th className="px-4 py-2 text-right font-medium">First seen</th>
                  <th className="px-4 py-2 text-right font-medium">Last seen</th>
                </tr>
              </thead>
              <tbody>
                {clients.map(cl => (
                  <tr key={cl.ip} className="border-b border-[var(--border)] transition-colors duration-200 last:border-0 hover:bg-[var(--panel-2)]/50">
                    <td className="px-4 py-2.5">
                      <span className="inline-flex items-center gap-1.5 font-mono text-[13px]">
                        {cl.ip}
                        <CopyIcon text={cl.ip} title="Copy IP" />
                        {cl.live && <Badge tone="good">live</Badge>}
                      </span>
                    </td>
                    <td className="px-4 py-2.5 text-right tabular-nums text-[var(--text-2)]" style={{fontFamily: MONO}}>
                      {cl.live ? fmtRate(rates.get(cl.ip) ?? 0) : '—'}
                    </td>
                    <td className="px-4 py-2.5 text-right tabular-nums text-[var(--text-2)]">
                      {cl.live && cl.sessionStart ? fmtElapsed(Date.now() - cl.sessionStart) : '—'}
                    </td>
                    <td className="px-4 py-2.5 text-right tabular-nums">{fmtBytes(cl.totalOut)}</td>
                    <td className="px-4 py-2.5 text-right tabular-nums">{fmtBytes(cl.totalIn)}</td>
                    <td className="px-4 py-2.5 text-right tabular-nums text-[var(--text-2)]">{cl.conns}</td>
                    <td className="px-4 py-2.5 text-right text-xs tabular-nums text-[var(--text-3)]">{fmtRelative(cl.firstSeen)}</td>
                    <td className="px-4 py-2.5 text-right text-xs tabular-nums text-[var(--text-3)]">{cl.live ? 'now' : fmtRelative(cl.lastSeen)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Card>
    </div>
  )
}

/** ControlLinkCard: the tunnel link itself — peer, uptime, RTT, raw bytes. */
function ControlLinkCard({status}: {status: UIStatus}) {
  const isAgent = status.role === 'agent'
  const up = isAgent ? status.linkUp : status.agentConnected
  const c = up ? 'var(--good)' : isAgent ? 'var(--bad)' : 'var(--warn)'
  const title = isAgent ? 'Gateway link' : 'Agent link'
  const headline = isAgent
    ? (up ? 'Connected to gateway' : 'Reconnecting…')
    : (up ? 'Agent connected' : 'Waiting for agent')

  return (
    <Card pad={false}>
      <div className="flex flex-col gap-5 p-5 md:flex-row md:items-center md:justify-between">
        <div className="flex items-center gap-4">
          <div
            className="grid h-12 w-12 shrink-0 place-items-center rounded-2xl border transition-all duration-500"
            style={{
              color: c,
              borderColor: `color-mix(in srgb, ${c} 40%, var(--border))`,
              background: `color-mix(in srgb, ${c} 9%, transparent)`,
              boxShadow: up ? `0 0 24px -6px color-mix(in srgb, ${c} 60%, transparent)` : 'none',
            }}
          >
            <IconLink size={22} />
          </div>
          <div className="min-w-0">
            <div className="text-[11px] font-semibold uppercase tracking-wider text-[var(--text-3)]">{title}</div>
            <div className="mt-0.5 flex items-center gap-2 text-lg font-semibold leading-tight">
              <span className={`inline-flex h-2 w-2 rounded-full ${up ? 'pf-halo' : ''}`} style={{background: c, ['--halo' as string]: c}} />
              {headline}
            </div>
            {status.peerAddr && (
              <div className="mt-1 flex items-center gap-1.5 text-[13px] text-[var(--text-2)]">
                <span className="truncate" style={{fontFamily: MONO}}>{status.peerAddr}</span>
                <CopyIcon text={status.peerAddr} title="Copy peer address" />
              </div>
            )}
          </div>
        </div>

        <div className="grid shrink-0 grid-cols-2 gap-x-8 gap-y-3 sm:grid-cols-4">
          <MiniStat label="Link uptime" value={status.linkUpSinceMs ? fmtElapsed(Date.now() - status.linkUpSinceMs) : '—'} />
          {isAgent
            ? <MiniStat label="Round-trip" value={up ? `${status.rttMillis} ms` : '—'} />
            : <MiniStat label="Sessions" value={String(status.linkSessions || 0)} />}
          {/* Link counters are read/write on the conn. "Download" is the
              server→player direction: the agent WRITES it to the gateway
              (out), the gateway READS it from the agent (in). */}
          <MiniStat
            label="Session traffic"
            value={fmtBytes(status.linkBytesIn + status.linkBytesOut)}
            sub={`↓ ${fmtBytes(isAgent ? status.linkBytesOut : status.linkBytesIn)} · ↑ ${fmtBytes(isAgent ? status.linkBytesIn : status.linkBytesOut)}`}
          />
          {isAgent
            ? <MiniStat label="Sessions" value={String(status.linkSessions || 0)} sub={allTimeSub(status)} />
            : <MiniStat label="All-time link" value={allTimeSub(status) ?? '—'} />}
        </div>
      </div>
    </Card>
  )
}

function allTimeSub(status: UIStatus): string | undefined {
  const total = status.allTimeBytesIn + status.allTimeBytesOut
  return total > 0 ? `all-time ${fmtBytes(total)}` : undefined
}

function MiniStat({label, value, sub}: {label: string; value: string; sub?: string}) {
  return (
    <div className="min-w-0">
      <div className="text-[11px] text-[var(--text-3)]">{label}</div>
      <div className="mt-0.5 truncate text-base font-semibold tabular-nums">{value}</div>
      {sub && <div className="truncate text-[11px] tabular-nums text-[var(--text-3)]">{sub}</div>}
    </div>
  )
}

// ---- client merge ----------------------------------------------------------

type ClientRow = {
  ip: string
  live: boolean
  sessionStart: number | null
  totalIn: number
  totalOut: number
  conns: number
  firstSeen: number
  lastSeen: number
}

/** mergeClients joins persisted peer records with live connections by IP:
 * live byte counts are added on top of persisted totals (a connection's bytes
 * fold into its peer record only when it closes). */
function mergeClients(
  peers: {ip: string; firstSeen: number; lastSeen: number; totalBytesIn: number; totalBytesOut: number; totalConns: number}[],
  conns: {clientAddr: string; startedAt: number; bytesIn: number; bytesOut: number}[],
): ClientRow[] {
  const rows = new Map<string, ClientRow>()
  for (const p of peers) {
    rows.set(p.ip, {
      ip: p.ip, live: false, sessionStart: null,
      totalIn: p.totalBytesIn, totalOut: p.totalBytesOut,
      conns: p.totalConns, firstSeen: p.firstSeen, lastSeen: p.lastSeen,
    })
  }
  for (const c of conns) {
    const ip = stripPort(c.clientAddr)
    let row = rows.get(ip)
    if (!row) {
      row = {ip, live: false, sessionStart: null, totalIn: 0, totalOut: 0, conns: 1, firstSeen: c.startedAt, lastSeen: c.startedAt}
      rows.set(ip, row)
    }
    row.live = true
    row.sessionStart = row.sessionStart === null ? c.startedAt : Math.min(row.sessionStart, c.startedAt)
    row.totalIn += c.bytesIn
    row.totalOut += c.bytesOut
    row.lastSeen = Date.now()
  }
  return [...rows.values()].sort((a, b) => {
    if (a.live !== b.live) return a.live ? -1 : 1
    return b.lastSeen - a.lastSeen
  })
}

function stripPort(addr: string): string {
  const i = addr.lastIndexOf(':')
  return i > 0 && addr.indexOf(':') === i ? addr.slice(0, i) : addr
}

function fmtElapsed(ms: number): string {
  const s = Math.max(0, Math.floor(ms / 1000))
  const h = Math.floor(s / 3600), m = Math.floor((s % 3600) / 60), sec = s % 60
  const pad = (n: number) => String(n).padStart(2, '0')
  if (h > 0) return `${h}:${pad(m)}:${pad(sec)}`
  return `${m}:${pad(sec)}`
}

function fmtRelative(t: number): string {
  if (!t) return '—'
  const s = Math.max(0, Math.floor((Date.now() - t) / 1000))
  if (s < 45) return 'just now'
  if (s < 3600) return `${Math.round(s / 60)}m ago`
  if (s < 86400) return `${Math.round(s / 3600)}h ago`
  if (s < 7 * 86400) return `${Math.round(s / 86400)}d ago`
  return `${Math.round(s / (7 * 86400))}w ago`
}
