import { Emblem } from 'frontend'

const Label = ({ children }: { children: string }) => (
  <span style={{ fontSize: 11, color: 'var(--text-3)' }}>{children}</span>
)

/** Both role motifs in both tints: riding var(--accent) (the active-role wash)
 * and pinned to the fixed role swatches for peer/wizard surfaces. */
export function Roles() {
  return (
    <div style={{ display: 'flex', gap: 28, alignItems: 'end' }}>
      {(['agent', 'gateway'] as const).map(r => (
        <div key={r} style={{ display: 'grid', gap: 8, justifyItems: 'center' }}>
          <Emblem role={r} size={32} />
          <Label>{`${r} · accent`}</Label>
        </div>
      ))}
      {(['agent', 'gateway'] as const).map(r => (
        <div key={`${r}f`} style={{ display: 'grid', gap: 8, justifyItems: 'center' }}>
          <Emblem role={r} size={32} fixed />
          <Label>{`${r} · fixed`}</Label>
        </div>
      ))}
    </div>
  )
}

/** The size ramp the app uses (22 → 40) — one shared box per size, so a role
 * swap never shifts layout. */
export function Sizes() {
  return (
    <div style={{ display: 'flex', gap: 20, alignItems: 'end' }}>
      {[22, 26, 32, 38, 40].map(s => (
        <div key={s} style={{ display: 'grid', gap: 8, justifyItems: 'center' }}>
          <Emblem role="agent" size={s} fixed />
          <Label>{`${s}px`}</Label>
        </div>
      ))}
    </div>
  )
}

/** Glow marks a healthy, connected identity (the Agents roster / Overview
 * identity strip treatment). */
export function Glow() {
  return (
    <div style={{ display: 'flex', gap: 28, alignItems: 'end' }}>
      <div style={{ display: 'grid', gap: 8, justifyItems: 'center' }}>
        <Emblem role="agent" size={38} fixed glow />
        <Label>agent · glow</Label>
      </div>
      <div style={{ display: 'grid', gap: 8, justifyItems: 'center' }}>
        <Emblem role="gateway" size={38} fixed glow />
        <Label>gateway · glow</Label>
      </div>
      <div style={{ display: 'grid', gap: 8, justifyItems: 'center' }}>
        <Emblem role="gateway" size={38} glow />
        <Label>accent · glow</Label>
      </div>
    </div>
  )
}
