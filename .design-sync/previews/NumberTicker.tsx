import { useEffect, useState } from 'react'
import { NumberTicker, StatTile } from 'frontend'

const fmtBytes = (n: number) => {
  if (!n || n < 0) return '0 B'
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB']
  let i = 0, v = n
  while (v >= 1024 && i < units.length - 1) { v /= 1024; i++ }
  return `${v >= 100 || i === 0 ? Math.round(v) : v.toFixed(1)} ${units[i]}`
}
const fmtInt = (n: number) => Math.round(n).toLocaleString()

/** The three formats it carries in the app: bytes, plain counts, rates —
 * always tabular-nums so digits don't jitter as they glide. */
export function Formats() {
  return (
    <div style={{ display: 'grid', gap: 10, maxWidth: 360, fontFamily: "ui-monospace, 'Cascadia Mono', Consolas, monospace" }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', gap: 16 }}>
        <span style={{ color: 'var(--text-3)', fontSize: 12 }}>Session traffic</span>
        <span style={{ fontWeight: 600 }}><NumberTicker value={3_481_272_842} format={n => fmtBytes(Math.round(n))} /></span>
      </div>
      <div style={{ display: 'flex', justifyContent: 'space-between', gap: 16 }}>
        <span style={{ color: 'var(--text-3)', fontSize: 12 }}>Sessions</span>
        <span style={{ fontWeight: 600 }}><NumberTicker value={1287} format={fmtInt} /></span>
      </div>
      <div style={{ display: 'flex', justifyContent: 'space-between', gap: 16 }}>
        <span style={{ color: 'var(--text-3)', fontSize: 12 }}>Rate</span>
        <span style={{ fontWeight: 600, color: 'var(--dl)' }}><NumberTicker value={2_882_304} format={n => `${fmtBytes(n)}/s`} /></span>
      </div>
    </div>
  )
}

/** The Overview composition: tickers as StatTile values (live connections +
 * bytes moved), the numeral gliding when the tick lands. */
export function InStatTiles() {
  return (
    <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr 1fr', gap: 12, maxWidth: 560 }}>
      <StatTile label="Connections" value={<NumberTicker value={7} format={n => String(Math.round(n))} />} accent />
      <StatTile label="Session traffic" value={<NumberTicker value={912_745_089} format={n => fmtBytes(Math.round(n))} />} />
      <StatTile label="Unique players" value={<NumberTicker value={42} format={fmtInt} />} />
    </div>
  )
}

/** A live value: retargets every 1.5 s so a capture can land mid-glide.
 * Reduced-motion users get the instant final value instead. */
export function Live() {
  const [v, setV] = useState(1_294_336)
  useEffect(() => {
    const t = setInterval(() => setV(x => x + 180_000 + Math.round(Math.random() * 900_000)), 1500)
    return () => clearInterval(t)
  }, [])
  return (
    <div style={{ fontFamily: "ui-monospace, 'Cascadia Mono', Consolas, monospace" }}>
      <div style={{ fontSize: 11, color: 'var(--text-3)' }}>↓ Download</div>
      <div style={{ fontSize: 24, fontWeight: 600, color: 'var(--dl)' }}>
        <NumberTicker value={v} format={n => `${fmtBytes(n)}/s`} />
      </div>
    </div>
  )
}
