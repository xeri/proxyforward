import { Overline } from 'frontend'

/** The shared label recipe — 11px uppercase, tracked, muted. */
export function Canonical() {
  return <Overline>Link uptime</Overline>
}

/** Labels recede; values dominate — the overline-over-value pairing every
 * metric and identity block uses. */
export function LabellingValues() {
  return (
    <div style={{ display: 'flex', gap: 40, maxWidth: 560 }}>
      <div>
        <Overline>Public endpoint</Overline>
        <div style={{ marginTop: 4, fontFamily: 'var(--font-mono, ui-monospace, monospace)', fontSize: 14, color: 'var(--text)' }}>
          203.0.113.7:25565
        </div>
      </div>
      <div>
        <Overline>Round trip</Overline>
        <div style={{ marginTop: 4, fontSize: 14, fontWeight: 600, fontVariantNumeric: 'tabular-nums', color: 'var(--text)' }}>
          23 ms
        </div>
      </div>
      <div>
        <Overline>Transport</Overline>
        <div style={{ marginTop: 4, fontSize: 14, color: 'var(--text)' }}>QUIC</div>
      </div>
    </div>
  )
}
