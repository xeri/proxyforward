import { Button, SectionHeader } from 'frontend'

/** In-card section titling — title with the count riding quietly beside it,
 * hint underneath. */
export function Canonical() {
  return (
    <div style={{ maxWidth: 560 }}>
      <SectionHeader
        title="Live sessions"
        count={6}
        hint="One row per player connection through this tunnel."
      />
    </div>
  )
}

/** The action slot carries the group's one tool. */
export function WithAction() {
  return (
    <div style={{ maxWidth: 560 }}>
      <SectionHeader
        title="Enrollment invites"
        count={2}
        hint="Live pairing codes this gateway will still honor."
        action={<Button size="sm" variant="ghost" onClick={() => {}}>Issue code</Button>}
      />
    </div>
  )
}

/** Heading grouped rows on the hairline rhythm — the shape it exists for. */
export function GroupedRows() {
  return (
    <div style={{ maxWidth: 560 }}>
      <SectionHeader title="Recent players" count={3} />
      <div style={{ marginTop: 8 }}>
        {[
          ['Herobrine_', 'joined 4 m ago', '23 ms'],
          ['stone_cutter', 'joined 18 m ago', '41 ms'],
          ['ender_pearl', 'left 1 h ago', '—'],
        ].map(([name, when, rtt]) => (
          <div
            key={name}
            style={{ display: 'flex', justifyContent: 'space-between', gap: 12, padding: '8px 0', borderTop: '1px solid var(--hairline)', fontSize: 13 }}
          >
            <span style={{ color: 'var(--text)' }}>{name}</span>
            <span style={{ color: 'var(--text-3)' }}>{when}</span>
            <span style={{ color: 'var(--text-2)', fontVariantNumeric: 'tabular-nums' }}>{rtt}</span>
          </div>
        ))}
      </div>
    </div>
  )
}
