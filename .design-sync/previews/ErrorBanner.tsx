import { ErrorBanner } from 'frontend'

/** The screens' catch-all failure surface: `catch (e) { setErr(String(e)) }`,
 * so the message is a raw Go error string. Dismissible, bad-tone bleed. */
export function IssueFailure() {
  return (
    <div style={{ maxWidth: 560 }}>
      <ErrorBanner
        message="issue pairing code: this engine is not running as a gateway"
        onDismiss={() => {}}
      />
    </div>
  )
}

/** A long wrapped dial error (the agent-side connect failure shape from
 * Overview) — the banner breaks words rather than overflowing. */
export function DialError() {
  return (
    <div style={{ maxWidth: 560 }}>
      <ErrorBanner
        message="connect gateway: dial tcp 203.0.113.7:8474: connectex: No connection could be made because the target machine actively refused it."
        onDismiss={() => {}}
      />
    </div>
  )
}
