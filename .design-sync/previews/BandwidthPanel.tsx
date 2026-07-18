import { useMemo } from 'react'
import { BandwidthPanel } from 'frontend'

// Canned HistoryResult (frontend/src/history.ts → stats.HistoryResult).
// Default: the 1m range's shape — 240 × 250 ms buckets. `out` = download
// bytes per bucket, `o*` its rate OHLC; `in`/`i*` upload; `cc`/`rc` conn/RTT
// gauge closes (known); `p*`/`l*` stay at the -1 unknown sentinel.
function makeHistory(bucketMs = 250, n = 240, windowMs = 60_000) {
  const now = Date.now()
  const t0 = now - (n - 1) * bucketMs
  const buckets = []
  for (let i = 0; i < n; i++) {
    const t = t0 + i * bucketMs
    const wave = Math.sin(i / 17) * 0.45 + Math.sin(i / 6.3) * 0.28 + 1
    const burst = i > 150 && i < 185 ? 1.9 : 1
    const dnRate = Math.max(80_000, wave * burst * 2_400_000) // ~2.4 MiB/s base
    const upRate = Math.max(12_000, dnRate / 9 + Math.sin(i / 4.1) * 30_000)
    const jig = (r: number, k: number) => Math.max(1, r * (1 + Math.sin(i * k) * 0.08))
    const conn = 4 + Math.round(Math.sin(i / 40) * 2 + (burst > 1 ? 2 : 0))
    const rtt = 32 + Math.sin(i / 23) * 7 + (burst > 1 ? 9 : 0)
    buckets.push({
      t,
      out: Math.round(dnRate * bucketMs / 1000),
      in: Math.round(upRate * bucketMs / 1000),
      oo: jig(dnRate, 0.7), oh: jig(dnRate, 0.7) * 1.12, ol: jig(dnRate, 0.7) * 0.9, oc: dnRate,
      io: jig(upRate, 0.9), ih: jig(upRate, 0.9) * 1.1, il: jig(upRate, 0.9) * 0.92, ic: upRate,
      co: conn, ch: conn + 1, cl: Math.max(0, conn - 1), cc: conn,
      ro: rtt, rh: rtt + 4, rl: rtt - 3, rc: rtt,
      po: -1, ph: -1, pl: -1, pc: -1,
      lo: -1, lh: -1, ll: -1, lc: -1,
    })
  }
  return { windowMs, bucketMs, buckets }
}

/** The full instrument: range picker, line/candles toggle, legend chips,
 * min/avg/max stats, live ↓/↑ readout, and the dual-axis neon plot. */
export function FullCard() {
  const hist = useMemo(makeHistory, [])
  return (
    <div style={{ width: 560 }}>
      <BandwidthPanel useHistory={() => hist} vtName={null} />
    </div>
  )
}

/** The Overview teaser: live-rate headlines doubling as the legend over a
 * bare sparkline, with the jump-off to Traffic. */
export function CompactTeaser() {
  // Compact pins the 1h range, so the series must span the hour or it hugs
  // the right edge as a sliver: 240 × 15 s buckets.
  const hist = useMemo(() => makeHistory(15_000, 240, 3_600_000), [])
  return (
    <div style={{ width: 420 }}>
      <BandwidthPanel compact useHistory={() => hist} onExpand={() => {}} vtName={null} />
    </div>
  )
}

/** The honest unavailable state: an old daemon can't serve history, and the
 * panel says so instead of pretending an empty chart is "no traffic". */
export function Unavailable() {
  return (
    <div style={{ width: 560 }}>
      <BandwidthPanel historyUnsupported useHistory={() => null} vtName={null} />
    </div>
  )
}
