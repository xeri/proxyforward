import { StatusDot } from 'frontend'

/** The four states, each with its label — color is never the only signal.
 * Labels are the link-health verdicts from Agents/Overview. */
export function States() {
  return (
    <div style={{ display: 'grid', gap: 8 }}>
      <StatusDot state="good" label="Healthy" />
      <StatusDot state="warn" label="Fair" />
      <StatusDot state="bad" label="Poor" />
      <StatusDot state="unknown" label="Offline" />
    </div>
  )
}

/** pulse breathes a halo out of a good dot to mark a live surface (static
 * capture holds one frame of the breath). */
export function Pulsing() {
  return <StatusDot state="good" label="Link established" pulse />
}

/** The fleet rollup strip from the Agents roster header — verdict dot leading
 * quiet tabular stats. */
export function FleetRollup() {
  return (
    <div style={{ display: 'flex', gap: 14, alignItems: 'center', fontSize: 13 }}>
      <StatusDot state="good" label="Healthy" pulse />
      <span style={{ color: 'var(--text-3)', fontVariantNumeric: 'tabular-nums' }}>2 online</span>
      <span style={{ color: 'var(--text-3)', fontVariantNumeric: 'tabular-nums' }}>1 revoked</span>
      <span style={{ color: 'var(--text-3)', fontVariantNumeric: 'tabular-nums' }}>5 tunnels</span>
    </div>
  )
}
