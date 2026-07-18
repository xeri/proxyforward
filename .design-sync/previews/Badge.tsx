import { Badge, StatusDot } from 'frontend'

/** The five tones with the labels the app actually mints — neutral recedes,
 * status tones carry their color as signal, accent marks the default route. */
export function Tones() {
  return (
    <div style={{ display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap' }}>
      <Badge tone="neutral">Proposed</Badge>
      <Badge tone="good">Bound</Badge>
      <Badge tone="warn">Pending</Badge>
      <Badge tone="bad">Revoked</Badge>
      <Badge tone="accent">Default</Badge>
    </div>
  )
}

/** The Agents roster marks: a health dot with the review/update flags stacked
 * beside it. */
export function RosterMarks() {
  return (
    <div style={{ display: 'grid', gap: 10, maxWidth: 420 }}>
      <span style={{ display: 'inline-flex', alignItems: 'center', gap: 8 }}>
        <StatusDot state="good" label="Healthy" pulse />
        <Badge tone="warn">Review</Badge>
      </span>
      <span style={{ display: 'inline-flex', alignItems: 'center', gap: 8 }}>
        <StatusDot state="unknown" label="Offline" />
        <Badge tone="good">New</Badge>
        <Badge tone="warn">Update</Badge>
      </span>
    </div>
  )
}

/** As quiet counters in card-header action slots. */
export function AsCount() {
  return (
    <div style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
      <Badge tone="neutral">12</Badge>
      <Badge tone="good">4 live</Badge>
      <Badge tone="warn">Single-use</Badge>
    </div>
  )
}
