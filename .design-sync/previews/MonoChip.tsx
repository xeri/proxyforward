import { MonoChip } from 'frontend'

/** Ports, counts, and addresses riding inline with table text — the chips the
 * Tunnels and Agents tables actually mint. */
export function PortsAndAddresses() {
  return (
    <div style={{ display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap', maxWidth: 560 }}>
      <MonoChip>:25565</MonoChip>
      <MonoChip>:19132</MonoChip>
      <MonoChip>auto</MonoChip>
      <MonoChip>127.0.0.1:25565</MonoChip>
      <MonoChip>gen 7</MonoChip>
    </div>
  )
}

/** tone="accent" lifts the one chip that matters — the negotiated transport
 * beside the neutral default. */
export function AccentTone() {
  return (
    <div style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
      <MonoChip>yamux/tcp</MonoChip>
      <MonoChip tone="accent">quic</MonoChip>
    </div>
  )
}

/** Inline with prose-size text — the Players address list row. */
export function InlineWithText() {
  return (
    <div style={{ display: 'flex', justifyContent: 'space-between', gap: 12, maxWidth: 420, fontSize: 13 }}>
      <span style={{ fontFamily: 'var(--font-mono, ui-monospace, monospace)', color: 'var(--text)' }}>203.0.113.42</span>
      <span style={{ color: 'var(--text-3)' }}>
        <MonoChip>12×</MonoChip> <span style={{ marginLeft: 6 }}>2 h ago</span>
      </span>
    </div>
  )
}
