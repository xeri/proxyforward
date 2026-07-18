import { Skeleton } from 'frontend'

/** One roster list row while the fleet loads (Agents.tsx RosterSkeleton):
 * emblem square, name + host lines, trailing status pill. Geometry matches
 * what it masks. */
export function RosterRow() {
  return (
    <div style={{ display: 'flex', alignItems: 'center', gap: 12, maxWidth: 480 }}>
      <Skeleton style={{ height: 38, width: 38, borderRadius: 'var(--r-md)' }} />
      <div style={{ flex: 1, display: 'grid', gap: 6 }}>
        <Skeleton style={{ height: 14, width: 140, borderRadius: 999 }} />
        <Skeleton style={{ height: 12, width: 190, borderRadius: 999 }} />
      </div>
      <Skeleton style={{ height: 16, width: 64, borderRadius: 999 }} />
    </div>
  )
}

/** The agent-card stat strip: three label/value pairs side by side. */
export function StatBlocks() {
  return (
    <div style={{ display: 'flex', gap: 28 }}>
      {[0, 1, 2].map(i => (
        <div key={i} style={{ display: 'grid', gap: 6 }}>
          <Skeleton style={{ height: 10, width: 48, borderRadius: 999 }} />
          <Skeleton style={{ height: 16, width: 56, borderRadius: 999 }} />
        </div>
      ))}
    </div>
  )
}

/** A whole tunnel card still loading (Tunnels.tsx): one block with the card's
 * own radius, shimmering as a unit. */
export function CardBlock() {
  return (
    <div style={{ maxWidth: 480 }}>
      <Skeleton style={{ height: 160, width: '100%', borderRadius: 'var(--r-lg)' }} />
    </div>
  )
}
