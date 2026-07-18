import { StatTile } from 'frontend'

/** The Overview metric group: tier-3 headline numbers on the hairline left
 * rule, no boxes — the app's own 2-column rhythm. `accent` tints the
 * live-sessions numeral with the mode accent; "—" is the unknown sentinel,
 * never a fake zero. */
export function OverviewRow() {
  return (
    <div style={{ display: 'grid', gridTemplateColumns: 'repeat(2, minmax(0, 1fr))', columnGap: 20, rowGap: 24, maxWidth: 400 }}>
      <StatTile label="Link uptime" value="3d 4h 12m" sub="since Jul 15, 06:48" />
      <StatTile label="Round trip" value="23 ms" />
      <StatTile label="Live sessions" value={4} accent />
      <StatTile label="Data moved" value="4.2 GiB" sub="all-time 128 GiB" />
    </div>
  )
}

/** size="hero" — the one 36px figure a page may carry. */
export function Hero() {
  return (
    <div style={{ maxWidth: 560 }}>
      <StatTile label="Data moved" value="4.2 GiB" sub="all-time 128 GiB" size="hero" />
    </div>
  )
}

/** `tone` tints the numeral with a status color; the last tile shows the "—"
 * unknown sentinel untinted. */
export function Tones() {
  return (
    <div style={{ display: 'grid', gridTemplateColumns: 'repeat(4, minmax(0, 1fr))', gap: 16, maxWidth: 560 }}>
      <StatTile label="Tunnel uptime" value="99.8%" tone="good" />
      <StatTile label="Packet loss" value="2.1%" tone="warn" />
      <StatTile label="Local server" value="down" tone="bad" sub="dial refused :25565" />
      <StatTile label="Round trip" value="—" />
    </div>
  )
}
