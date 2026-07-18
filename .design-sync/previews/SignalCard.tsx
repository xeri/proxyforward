import { Card, SignalCard, StatTile, StatusDot } from 'frontend'

/** The identity surface — Signal Glass with the caustic layer. One per
 * screen, only on surfaces that represent live network activity; here the
 * live-link headline from Traffic. */
export function Identity() {
  return (
    <div style={{ maxWidth: 560 }}>
      <SignalCard title="Live link" subtitle="QUIC to vps-fra1 · established 3 d ago">
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(3, minmax(0, 1fr))', gap: 16 }}>
          <StatTile label="Link uptime" value="3d 4h 12m" />
          <StatTile label="Round trip" value="23 ms" />
          <StatTile label="Live sessions" value={4} accent />
        </div>
      </SignalCard>
    </div>
  )
}

/** The two tiers honestly, side by side: Signal Glass answers the pointer and
 * carries the caustics; the quiet Card recedes behind its content. */
export function TierContrast() {
  return (
    <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 14, maxWidth: 560 }}>
      <SignalCard title="Tunnel">
        <div style={{ display: 'grid', gap: 8 }}>
          <StatusDot state="good" label="Link established" pulse />
          <div style={{ fontSize: 12, color: 'var(--text-3)' }}>Players connect at 203.0.113.7:25565</div>
        </div>
      </SignalCard>
      <Card label="This machine">
        <div style={{ display: 'grid', gap: 8 }}>
          <StatusDot state="good" label="Engine running" />
          <div style={{ fontSize: 12, color: 'var(--text-3)' }}>basement-pc · 192.168.1.40</div>
        </div>
      </Card>
    </div>
  )
}
