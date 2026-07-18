import { useMemo } from 'react'
import { LineChart } from 'frontend'

const fmtBytes = (n: number) => {
  if (!n || n < 0) return '0 B'
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB']
  let i = 0, v = n
  while (v >= 1024 && i < units.length - 1) { v /= 1024; i++ }
  return `${v >= 100 || i === 0 ? Math.round(v) : v.toFixed(1)} ${units[i]}`
}

/** The player-dossier traffic history: two series (download filled, upload
 * bare) on a shared binary y-scale — 30-min buckets across 24 h. */
export function TrafficBinary() {
  const series = useMemo(() => {
    const now = Date.now()
    const n = 48, step = 1_800_000
    const dl = [], ul = []
    for (let i = 0; i < n; i++) {
      const t = now - (n - 1 - i) * step
      const wave = Math.sin(i / 5.1) * 0.5 + Math.sin(i / 2.3) * 0.25 + 0.75
      const v = Math.max(4_000_000, Math.round(wave * 620_000_000))
      dl.push({ t, v })
      ul.push({ t, v: Math.round(v / 11) })
    }
    return [
      { points: dl, cssVar: 'var(--dl)', label: '↓', fill: true, format: fmtBytes },
      { points: ul, cssVar: 'var(--ul)', label: '↑', format: fmtBytes },
    ]
  }, [])
  return (
    <div style={{ width: 560 }}>
      <LineChart series={series} height={200} scale="binary" formatY={fmtBytes}
        label="Herobrine_7's traffic history"
        emptyHint="No recorded traffic in this range." />
    </div>
  )
}

/** Latency with an unknown stretch: nulls break the line instead of inventing
 * a value — the gap is the honest render of an unsampled window. */
export function LatencyWithGap() {
  const series = useMemo(() => {
    const now = Date.now()
    const n = 60, step = 60_000
    const pts = []
    for (let i = 0; i < n; i++) {
      const t = now - (n - 1 - i) * step
      const v = i >= 24 && i < 34
        ? null
        : Math.round(34 + Math.sin(i / 4.2) * 9 + Math.sin(i / 1.7) * 4 + (i > 45 ? (i - 45) * 1.8 : 0))
      pts.push({ t, v })
    }
    return [{ points: pts, cssVar: 'var(--rtt)', label: 'rtt', fill: true, format: (v: number) => `${Math.round(v)} ms` }]
  }, [])
  return (
    <div style={{ width: 560 }}>
      <LineChart series={series} height={160} formatY={v => `${Math.round(v)} ms`}
        label="Session latency"
        emptyHint="No round-trip samples for this session." />
    </div>
  )
}

/** Stepped mode: a gauge that is constant between samples (connection count)
 * drawn as horizontal runs joined by vertical steps. */
export function SteppedGauge() {
  const series = useMemo(() => {
    const now = Date.now()
    const levels = [2, 2, 3, 5, 5, 4, 6, 6, 6, 8, 7, 7, 5, 5, 3, 4, 4, 2, 3, 3]
    const pts = levels.map((v, i) => ({ t: now - (levels.length - 1 - i) * 180_000, v }))
    return [{ points: pts, cssVar: 'var(--conn)', label: 'conns', format: (v: number) => String(Math.round(v)) }]
  }, [])
  return (
    <div style={{ width: 560 }}>
      <LineChart series={series} height={150} stepped
        formatY={v => String(Math.round(v))}
        label="Concurrent connections"
        emptyHint="No connection data in this range." />
    </div>
  )
}

/** The written empty state: the well, the hint, no fake axes. */
export function Empty() {
  return (
    <div style={{ width: 560 }}>
      <LineChart series={[]} height={150}
        emptyHint="No latency recorded in this range — needs a gateway build that reports per-connection RTT." />
    </div>
  )
}
