import { Badge, Card, DataTable, MonoChip } from 'frontend'

const fmtBytes = (n: number) => {
  if (!n || n < 0) return '0 B'
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB']
  let i = 0, v = n
  while (v >= 1024 && i < units.length - 1) { v /= 1024; i++ }
  return `${v >= 100 || i === 0 ? Math.round(v) : v.toFixed(1)} ${units[i]}`
}

type Sess = {
  id: string; ip: string; port: string; player: string | null
  dur: string; rx: number; tx: number; rtt: number
}
const sessions: Sess[] = [
  { id: 'c1', ip: '203.0.113.42', port: '52814', player: 'Herobrine_7', dur: '42m 18s', rx: 812_402_688, tx: 64_225_280, rtt: 34 },
  { id: 'c2', ip: '198.51.100.7', port: '61102', player: 'EnderQueen', dur: '18m 03s', rx: 221_249_536, tx: 18_874_368, rtt: 87 },
  { id: 'c3', ip: '192.0.2.115', port: '49521', player: 'xX_Creeper_Xx', dur: '7m 44s', rx: 96_468_992, tx: 8_912_896, rtt: 152 },
  { id: 'c4', ip: '203.0.113.9', port: '55873', player: null, dur: '0m 12s', rx: 45_056, tx: 12_288, rtt: 0 },
]

/** The Traffic live-sessions composition: pinned mono identity column, chips,
 * a player dot, right-aligned tabular numerals, RTT in its series color and
 * "—" for the unknown sentinel. */
export function LiveSessions() {
  const cols = [
    {
      key: 'ip', header: 'Client IP', pin: true, mono: true,
      render: (c: Sess) => <span className="select-text">{c.ip}</span>,
    },
    { key: 'port', header: 'Port', render: (c: Sess) => <MonoChip>{c.port}</MonoChip> },
    {
      key: 'player', header: 'Player',
      render: (c: Sess) => c.player
        ? (
          <span style={{ display: 'inline-flex', alignItems: 'center', gap: 6 }}>
            <span style={{ display: 'inline-flex', width: 6, height: 6, borderRadius: 9999, background: 'var(--good)' }} />
            <span style={{ fontWeight: 500, color: 'var(--text)' }}>{c.player}</span>
          </span>
        )
        : <span style={{ color: 'var(--text-3)' }}>—</span>,
    },
    { key: 'dur', header: 'Duration', render: (c: Sess) => <span className="tabular-nums" style={{ color: 'var(--text-2)' }}>{c.dur}</span> },
    { key: 'rx', header: 'Received', align: 'right' as const, render: (c: Sess) => fmtBytes(c.rx) },
    {
      key: 'rtt', header: 'Ping', align: 'right' as const,
      render: (c: Sess) => c.rtt > 0
        ? <span className="tabular-nums" style={{ color: 'var(--rtt)' }}>{c.rtt} ms</span>
        : <span style={{ color: 'var(--text-3)' }}>—</span>,
    },
  ]
  return (
    <div style={{ maxWidth: 560 }}>
      <Card label="Live sessions" pad={false}
        action={<div style={{ paddingRight: 16 }}><Badge tone="good">4 live</Badge></div>}>
        <DataTable
          columns={cols}
          rows={sessions}
          rowKey={(c: Sess) => c.id}
          onRowClick={() => {}}
          empty={{ title: "No one's connected right now" }}
        />
      </Card>
    </div>
  )
}

/** Dense rows — the player-dossier session log treatment. */
export function Dense() {
  const cols = [
    { key: 'when', header: 'When', pin: true, render: (c: Sess) => <span style={{ color: 'var(--text-2)' }}>{c.dur} ago</span> },
    { key: 'dur', header: 'Duration', render: (c: Sess) => <span className="tabular-nums">{c.dur}</span> },
    { key: 'rx', header: '↓', align: 'right' as const, render: (c: Sess) => fmtBytes(c.rx) },
    { key: 'tx', header: '↑', align: 'right' as const, render: (c: Sess) => fmtBytes(c.tx) },
    {
      key: 'rtt', header: 'Ping', align: 'right' as const,
      render: (c: Sess) => c.rtt > 0 ? <span className="tabular-nums" style={{ color: 'var(--rtt)' }}>{c.rtt} ms</span> : '—',
    },
  ]
  return (
    <div style={{ maxWidth: 480 }}>
      <Card label="Sessions" subtitle="Most recent connections" pad={false}>
        <DataTable columns={cols} rows={sessions} rowKey={(c: Sess) => c.id} dense
          empty={{ title: 'No sessions recorded' }} />
      </Card>
    </div>
  )
}

/** The designed empty state — a table with no rows never looks broken. */
export function Empty() {
  const cols = [
    { key: 'ip', header: 'Client', pin: true, mono: true, render: () => null },
    { key: 'dl', header: 'Total ↓', align: 'right' as const, render: () => null },
    { key: 'ul', header: 'Total ↑', align: 'right' as const, render: () => null },
  ]
  return (
    <div style={{ maxWidth: 480 }}>
      <Card label="Every client" subtitle="Lifetime totals for every IP that has ever connected" pad={false}>
        <DataTable
          columns={cols}
          rows={[] as Sess[]}
          rowKey={(c: Sess) => c.id}
          empty={{
            title: 'No clients yet',
            hint: 'Lifetime per-IP stats build up here as players connect.',
          }}
        />
      </Card>
    </div>
  )
}
