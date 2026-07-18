import { Banner, Button } from 'frontend'

/** The four tones. info/good stay flat; warn/bad leak ambient light through
 * the glass (pf-bleed) so an alert reads before it is read. */
export function Tones() {
  return (
    <div style={{ display: 'grid', gap: 10, maxWidth: 560 }}>
      <Banner tone="info">Analytics runs locally — nothing leaves this machine.</Banner>
      <Banner tone="good">Enrollment complete — the agent is linked to this gateway.</Banner>
      <Banner tone="warn">The tunnel port is below 1024 — most systems need elevation to bind it.</Banner>
      <Banner tone="bad">The gateway rejected this identity — the pairing code may have been revoked.</Banner>
    </div>
  )
}

/** Structured content + an action, the consent-flow shape from Agents. */
export function WithAction() {
  return (
    <div style={{ maxWidth: 560 }}>
      <Banner
        tone="warn"
        action={<Button size="sm" variant="ghost" onClick={() => {}}>View agent</Button>}
        onDismiss={() => {}}
      >
        <div style={{ display: 'grid', gap: 2 }}>
          <div style={{ fontWeight: 500 }}>This agent proposes a config change</div>
          <div style={{ fontSize: 12, color: 'var(--text-3)' }}>Sent 2 minutes ago. Nothing changes until you approve it.</div>
        </div>
      </Banner>
    </div>
  )
}

/** Dismissible info ribbon. */
export function Dismissible() {
  return (
    <div style={{ maxWidth: 560 }}>
      <Banner tone="info" onDismiss={() => {}}>
        A new gateway build is available — restart to apply it.
      </Banner>
    </div>
  )
}
