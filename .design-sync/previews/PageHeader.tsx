import { Button, IconPlus, PageHeader } from 'frontend'

/** The canonical screen header — one display-size title per screen with a
 * quiet subtitle. Copy is Overview's agent-role header. */
export function Canonical() {
  return (
    <div style={{ maxWidth: 560 }}>
      <PageHeader title="Overview" subtitle="The path from your server to your players." />
    </div>
  )
}

/** The tool slot: Agents roster header with its issue-code action. */
export function WithAction() {
  return (
    <div style={{ maxWidth: 560 }}>
      <PageHeader
        title="Agents"
        subtitle="Every machine enrolled with this gateway."
        action={<Button size="sm" onClick={() => {}}><IconPlus size={14} /> Issue pairing code</Button>}
      />
    </div>
  )
}

/** subtitle is a ReactNode — the agent dossier passes the mono identity
 * fingerprint inline (Agents detail view). */
export function DossierSubtitle() {
  return (
    <div style={{ maxWidth: 560 }}>
      <PageHeader
        title="basement-pc"
        subtitle={
          <span style={{ display: 'inline-flex', alignItems: 'center', gap: 6 }}>
            <span style={{ fontFamily: 'var(--font-mono, ui-monospace, monospace)', fontSize: 11, color: 'var(--text-3)' }}>
              a1f0c9d2e83b47c6
            </span>
            <span>· enrolled 12 days ago</span>
          </span>
        }
        action={<Button size="sm" variant="ghost" onClick={() => {}}>Back to roster</Button>}
      />
    </div>
  )
}
