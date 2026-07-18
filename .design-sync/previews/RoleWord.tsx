import { RoleWord } from 'frontend'

/** The two role tokens — cyan for the agent, violet for the gateway. With no
 * children the role name itself is the label. */
export function BothRoles() {
  return (
    <div style={{ display: 'flex', gap: 12, alignItems: 'center' }}>
      <RoleWord role="agent" />
      <RoleWord role="gateway" />
    </div>
  )
}

/** Role mentions reading at a glance in prose — the role-switch confirmation
 * copy from RoleSwitcher. */
export function InProse() {
  return (
    <p style={{ maxWidth: 420, fontSize: 13, lineHeight: 1.6, color: 'var(--text-2)', margin: 0 }}>
      This machine stops being the <RoleWord role="agent">agent</RoleWord> and becomes
      the <RoleWord role="gateway">gateway</RoleWord>. The engine restarts,
      so <b style={{ color: 'var(--text)' }}>any live player sessions drop</b>.
    </p>
  )
}

/** The identity-card side labels from Overview — quiet caption text with the
 * role token closing it. */
export function InLabels() {
  return (
    <div style={{ display: 'grid', gap: 8, fontSize: 12, color: 'var(--text-3)' }}>
      <span>This machine · <RoleWord role="agent">Agent</RoleWord></span>
      <span>Peer · <RoleWord role="gateway">Gateway</RoleWord></span>
    </div>
  )
}
