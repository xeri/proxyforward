import { Badge, Disclosure } from 'frontend'

const OPTIONS: [string, string][] = [
  ['Minecraft-aware', 'On'],
  ['PROXY protocol v2', 'On'],
  ['Bandwidth cap', '80 Mbps'],
]

function OptionList() {
  return (
    <dl style={{ display: 'grid', gap: 6, margin: 0 }}>
      {OPTIONS.map(([k, v]) => (
        <div key={k} style={{ display: 'flex', justifyContent: 'space-between', gap: 12, fontSize: 12 }}>
          <dt style={{ color: 'var(--text-3)' }}>{k}</dt>
          <dd style={{ margin: 0, fontWeight: 500, color: 'var(--text-2)' }}>{v}</dd>
        </div>
      ))}
    </dl>
  )
}

/** Closed at rest — the tunnel card's Advanced section (Tunnels.tsx): the hint
 * says how much is folded away so a closed section never hides silently. */
export function Closed() {
  return (
    <div style={{ maxWidth: 420 }}>
      <Disclosure label={<span style={{ fontSize: 13 }}>Advanced</span>} hint="3 options set">
        <OptionList />
      </Disclosure>
    </div>
  )
}

/** The same section open (defaultOpen) — key/value rows inside. */
export function OpenAdvanced() {
  return (
    <div style={{ maxWidth: 420 }}>
      <Disclosure label={<span style={{ fontSize: 13 }}>Advanced</span>} hint="3 options set" defaultOpen>
        <OptionList />
      </Disclosure>
    </div>
  )
}

/** The Agents-screen event log shape: a wordier label/hint pair and richer row
 * content (time, badge, message). */
export function GatewayEvents() {
  const events = [
    { at: '12:04', tone: 'warn' as const, tag: 'conflict', msg: 'Port 25566 was taken — steve-pc reassigned to :25567' },
    { at: '11:48', tone: 'good' as const, tag: 'reclaim', msg: 'basement-pc reclaimed :25565 after its restart' },
  ]
  return (
    <div style={{ maxWidth: 560 }}>
      <Disclosure label="Gateway events" hint="Conflicts and automatic fixes, newest first" defaultOpen>
        <div style={{ display: 'grid', gap: 8 }}>
          {events.map(e => (
            <div key={e.at} style={{ display: 'flex', alignItems: 'center', gap: 10, fontSize: 12 }}>
              <span style={{ color: 'var(--text-3)', fontVariantNumeric: 'tabular-nums' }}>{e.at}</span>
              <Badge tone={e.tone}>{e.tag}</Badge>
              <span style={{ color: 'var(--text-2)', minWidth: 0 }}>{e.msg}</span>
            </div>
          ))}
        </div>
      </Disclosure>
    </div>
  )
}
