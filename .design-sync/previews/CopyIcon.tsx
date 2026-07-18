import { CopyIcon } from 'frontend'

const mono = { fontFamily: 'ui-monospace, Consolas, monospace' } as const

/** The table-row affordance (Traffic.tsx): a client address with its quiet
 * copy icon riding after — visible at rest, brightening on hover. */
export function AddressRow() {
  return (
    <div style={{ display: 'grid', gap: 10, maxWidth: 360 }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
        <span style={{ ...mono, fontSize: 12, color: 'var(--text-2)' }}>203.0.113.54:52144</span>
        <CopyIcon text="203.0.113.54:52144" title="Copy IP" />
      </div>
      <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
        <span style={{ ...mono, fontSize: 12, color: 'var(--text-2)' }}>198.51.100.23:61833</span>
        <CopyIcon text="198.51.100.23:61833" title="Copy IP" />
      </div>
    </div>
  )
}

/** The agent-detail subtitle (Agents.tsx): the full agent ID in quiet mono
 * with the copy icon inline. */
export function AgentIdSubtitle() {
  return (
    <span style={{ display: 'inline-flex', alignItems: 'center', gap: 6 }}>
      <span style={{ ...mono, fontSize: 11, color: 'var(--text-3)' }}>a3f19c2e8b4d7f01</span>
      <CopyIcon text="a3f19c2e8b4d7f01" title="Copy agent ID" />
    </span>
  )
}
