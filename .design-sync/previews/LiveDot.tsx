import { LiveDot } from 'frontend'

/** The bandwidth chart's live readout (BandwidthChart.tsx): the dot marks the
 * surface as self-updating, beside the current rates in the series colors. */
export function ChartReadout() {
  return (
    <div
      style={{
        display: 'flex',
        alignItems: 'center',
        gap: 16,
        fontFamily: 'ui-monospace, Consolas, monospace',
        fontSize: 12,
        fontVariantNumeric: 'tabular-nums',
      }}
    >
      <LiveDot />
      <span style={{ color: 'var(--dl)' }}>↓ 4.2 MB/s</span>
      <span style={{ color: 'var(--ul)' }}>↑ 812 kB/s</span>
    </div>
  )
}

/** Beside a table heading (Players.tsx): the count is data, the dot says the
 * list updates itself. */
export function TableHeading() {
  return (
    <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
      <span style={{ fontSize: 14, fontWeight: 600, letterSpacing: '-0.01em', color: 'var(--text)' }}>
        Players
      </span>
      <span style={{ fontSize: 14, color: 'var(--text-3)', fontVariantNumeric: 'tabular-nums' }}>3</span>
      <LiveDot />
    </div>
  )
}
