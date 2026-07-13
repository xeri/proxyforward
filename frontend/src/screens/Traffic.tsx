import {useEffect, useState} from 'react'
import {BandwidthPanel} from '../components/BandwidthChart'
import {Column, DataTable} from '../components/DataTable'
import {Badge, Card, CopyIcon, MonoChip, PageHeader} from '../components/ui'
import {IconConnections, IconLink, IconUsers} from '../components/icons'
import {usePeers} from '../history'
import {NavId} from '../nav'
import {openDossierOnMount} from '../players'
import {fmtBytes, fmtRate, fmtRtt, hasRtt, UIStatus} from '../state'

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

/** Traffic: one subject, one screen — the link itself, the bandwidth history,
 * live sessions, and every client ever seen. */
export function Traffic({status, onNavigate}: {status: UIStatus; onNavigate: (id: NavId) => void}) {
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
  const selfIP = stripPort(status.peerAddr ?? '')

  // Player and Ping columns are unconditional: RTT is protocol-agnostic, a
  // stable layout beats columns that pop in and out, and the Players wall's
  // MinecraftAware empty state is what teaches the feature.
  const sessionCols: Column<(typeof conns)[number]>[] = [
    {
      key: 'ip', header: 'Client IP', pin: true, mono: true,
      render: c => {
        const {ip} = splitAddr(c.clientAddr)
        return (
          <span className="inline-flex items-center gap-1.5">
            <span className="select-text">{ip}</span>
            <LanTag ip={ip} selfIP={selfIP} />
            <CopyIcon text={ip} title="Copy IP" />
          </span>
        )
      },
    },
    {key: 'port', header: 'Port', render: c => <MonoChip>{splitAddr(c.clientAddr).port || '—'}</MonoChip>},
    {
      key: 'player', header: 'Player',
      // Identified players link through to their dossier; a sniffed name
      // without a resolved UUID has no dossier to open and stays plain text.
      render: c => {
        const uuid = c.playerUuid
        return c.playerName
        ? (uuid
          ? (
            <button
              type="button"
              title={`Open ${c.playerName} in Players`}
              onClick={() => { openDossierOnMount(uuid); onNavigate('players') }}
              className="group inline-flex items-center gap-1.5"
            >
              <span className="inline-flex h-1.5 w-1.5 rounded-full" style={{background: 'var(--good)'}} />
              <span className="font-medium text-[var(--text)] underline-offset-2 transition-colors group-hover:text-[var(--accent)] group-hover:underline">{c.playerName}</span>
            </button>
          )
          : (
            <span className="inline-flex items-center gap-1.5">
              <span className="inline-flex h-1.5 w-1.5 rounded-full" style={{background: 'var(--good)'}} />
              <span className="font-medium text-[var(--text)]">{c.playerName}</span>
            </span>
          ))
        : <span className="text-[var(--text-3)]">—</span>
      },
    },
    {key: 'tunnel', header: 'Tunnel', render: c => <span className="text-[var(--text-2)]">{c.tunnelName || '—'}</span>},
    {key: 'dur', header: 'Duration', render: c => <span className="tabular-nums text-[var(--text-2)]">{fmtElapsed(Date.now() - c.startedAt)}</span>},
    {key: 'rx', header: 'Received', align: 'right', render: c => fmtBytes(c.bytesOut)},
    {key: 'tx', header: 'Sent', align: 'right', render: c => fmtBytes(c.bytesIn)},
    {
      key: 'rtt', header: 'Ping', align: 'right',
      render: c => hasRtt(c.rttMs)
        ? <span className="tabular-nums text-[var(--rtt)]">{fmtRtt(c.rttMs)}</span>
        : <span className="text-[var(--text-3)]">—</span>,
    },
  ]

  const clientCols: Column<ClientRow>[] = [
    {
      key: 'ip', header: 'Client', pin: true, mono: true,
      render: cl => (
        <span className="inline-flex items-center gap-1.5">
          <span className="select-text">{cl.ip}</span>
          <LanTag ip={cl.ip} selfIP={selfIP} />
          <CopyIcon text={cl.ip} title="Copy IP" />
          {cl.live && <Badge tone="good">live</Badge>}
        </span>
      ),
    },
    {key: 'rate', header: 'Rate', align: 'right', mono: true, render: cl => (
      <span className="text-[var(--text-2)]">{cl.live ? fmtRate(rates.get(cl.ip) ?? 0) : '—'}</span>
    )},
    {key: 'session', header: 'Session', align: 'right', render: cl => (
      <span className="text-[var(--text-2)]">{cl.live && cl.sessionStart ? fmtElapsed(Date.now() - cl.sessionStart) : '—'}</span>
    )},
    {key: 'dl', header: 'Total ↓', align: 'right', render: cl => fmtBytes(cl.totalOut)},
    {key: 'ul', header: 'Total ↑', align: 'right', render: cl => fmtBytes(cl.totalIn)},
    {key: 'conns', header: 'Conns', align: 'right', render: cl => <span className="text-[var(--text-2)]">{cl.conns}</span>},
    {key: 'first', header: 'First seen', align: 'right', render: cl => (
      <span className="text-xs text-[var(--text-3)]">{fmtRelative(cl.firstSeen)}</span>
    )},
    {key: 'last', header: 'Last seen', align: 'right', render: cl => (
      <span className="text-xs text-[var(--text-3)]">{cl.live ? 'now' : fmtRelative(cl.lastSeen)}</span>
    )},
  ]

  return (
    <div className="pf-stagger space-y-4">
      <PageHeader title="Traffic" subtitle="Every byte, session, and client through the tunnel." />

      <LinkStrip status={status} />

      <BandwidthPanel hero historyUnsupported={status.historyUnsupported} />

      <div className="grid grid-cols-1 gap-4 @min-[88rem]:grid-cols-2">
        <Card title="Live sessions" pad={false}
          action={<div className="pr-4"><Badge tone={conns.length ? 'good' : 'neutral'}>{status.connectionsTotal || conns.length} live</Badge></div>}>
          <DataTable
            columns={sessionCols} rows={conns} rowKey={c => c.id}
            empty={{
              icon: <IconConnections size={28} />,
              title: "No one's connected right now",
              hint: 'Sessions appear here the moment a player joins through the tunnel.',
            }}
          />
          {status.connectionsTruncated && (
            <div className="px-4 py-2 text-[11px] tabular-nums text-[var(--text-3)]">
              showing {conns.length} of {status.connectionsTotal} connections
            </div>
          )}
        </Card>

        <Card title="Every client" subtitle="Lifetime totals for every IP that has ever connected" pad={false}
          action={<div className="pr-4"><Badge tone="neutral">{clients.length}</Badge></div>}>
          <DataTable
            columns={clientCols} rows={clients} rowKey={cl => cl.ip}
            empty={{
              icon: <IconUsers size={28} />,
              title: 'No clients yet',
              hint: 'Lifetime per-IP stats build up here as players connect.',
            }}
          />
        </Card>
      </div>
    </div>
  )
}

/** LinkStrip: the tunnel link compressed to one slim band — verdict on the
 * left, the link's numbers on the right. */
function LinkStrip({status}: {status: UIStatus}) {
  const isAgent = status.role === 'agent'
  const up = isAgent ? status.linkUp : status.agentConnected
  const c = up ? 'var(--good)' : isAgent ? 'var(--bad)' : 'var(--warn)'
  const title = isAgent ? 'Gateway link' : 'Agent link'
  const headline = isAgent
    ? (up ? 'Connected to gateway' : 'Reconnecting…')
    : (up ? 'Agent connected' : 'Waiting for agent')

  return (
    <Card pad={false}>
      <div className="flex flex-col gap-4 p-4 @4xl:flex-row @4xl:items-center @4xl:justify-between">
        <div className="flex min-w-0 items-center gap-3.5">
          <div
            className="grid h-10 w-10 shrink-0 place-items-center rounded-[var(--r-md)] border transition-all duration-500"
            style={{
              color: c,
              borderColor: `color-mix(in srgb, ${c} 40%, var(--border))`,
              background: `color-mix(in srgb, ${c} 9%, transparent)`,
              boxShadow: `0 0 24px -6px color-mix(in srgb, ${c} 60%, transparent)`,
            }}
          >
            <IconLink size={19} />
          </div>
          <div className="min-w-0">
            <div className="text-[11px] font-semibold uppercase tracking-wider text-[var(--text-3)]">{title}</div>
            <div className="mt-0.5 flex items-center gap-2 text-base font-semibold leading-tight">
              <span className={`inline-flex h-2 w-2 rounded-full ${up ? 'pf-halo' : ''}`} style={{background: c, ['--halo' as string]: c}} />
              <span className="truncate">{headline}</span>
              {status.peerAddr && (
                <span className="ml-1 hidden min-w-0 items-center gap-1.5 text-[12.5px] font-normal text-[var(--text-3)] @2xl:inline-flex">
                  <span className="min-w-0 select-text truncate font-mono" title={status.peerAddr}>{status.peerAddr}</span>
                  <CopyIcon text={status.peerAddr} title="Copy peer address" />
                </span>
              )}
            </div>
          </div>
        </div>

        <div className="grid min-w-0 shrink-0 grid-cols-2 gap-x-7 gap-y-3 sm:grid-cols-4">
          <MiniStat label="Link uptime" value={status.linkUpSinceMs ? fmtElapsed(Date.now() - status.linkUpSinceMs) : '—'} />
          {isAgent
            ? <MiniStat label="Round trip" value={up ? `${status.rttMillis} ms` : '—'} />
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
      <div className="mt-0.5 truncate text-base font-semibold tabular-nums" title={value}>{value}</div>
      {sub && <div className="truncate text-[11px] tabular-nums text-[var(--text-3)]" title={sub}>{sub}</div>}
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

/** splitAddr separates "ip:port" into its parts. Bare IPv6 or portless
 * addresses return an empty port. */
function splitAddr(addr: string): {ip: string; port: string} {
  const i = addr.lastIndexOf(':')
  if (i > 0 && addr.indexOf(':') === i) return {ip: addr.slice(0, i), port: addr.slice(i + 1)}
  return {ip: addr, port: ''}
}

/** isPrivateIP reports loopback / RFC-1918 / link-local / unique-local
 * addresses — the ones worth flagging as LAN rather than public internet. */
function isPrivateIP(ip: string): boolean {
  if (ip === '::1' || ip.startsWith('127.') || ip.startsWith('localhost')) return true
  if (ip.startsWith('10.') || ip.startsWith('192.168.') || ip.startsWith('169.254.')) return true
  const m = ip.match(/^172\.(\d+)\./)
  if (m && +m[1] >= 16 && +m[1] <= 31) return true
  if (ip.startsWith('fe80:') || ip.startsWith('fc') || ip.startsWith('fd')) return true // IPv6 link/unique-local
  return false
}

/** LanTag flags an address that lives on the local network (private range, or
 * the same host as this machine's own tunnel peer). */
function LanTag({ip, selfIP}: {ip: string; selfIP: string}) {
  if (!(isPrivateIP(ip) || (selfIP && ip === selfIP))) return null
  return (
    <span className="rounded-[var(--r-sm)] border border-[color-mix(in_srgb,var(--accent)_35%,transparent)] bg-[color-mix(in_srgb,var(--accent)_14%,transparent)] px-1.5 py-0.5 text-[10px] font-semibold uppercase tracking-wide text-[var(--accent)]">
      LAN
    </span>
  )
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
