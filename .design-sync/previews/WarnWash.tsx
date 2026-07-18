import { Badge, Button, FormRow, WarnWash } from 'frontend'

/** on={true} vs on={false} on the same row shape — the Settings System section
 * (Settings.tsx): the amber wash seeps into the glass exactly where the
 * problem row is, and the healthy row below stays flat. */
export function OnVsOff() {
  return (
    <div style={{ maxWidth: 560 }}>
      <WarnWash on>
        <FormRow label="Firewall rule" hint="Allows inbound player and agent connections.">
          <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
            <Badge tone="warn">Missing</Badge>
            <Button variant="ghost" size="sm" onClick={() => {}}>Add rule</Button>
          </div>
        </FormRow>
      </WarnWash>
      <WarnWash on={false}>
        <FormRow label="Windows service" hint="Run headless in the background; the app attaches as a viewer.">
          <Badge tone="good">Running</Badge>
        </FormRow>
      </WarnWash>
    </div>
  )
}

/** The wash localizes: three settings rows, only the one that needs eyes is
 * washed — its -mx bleed reaches past the row's text without touching the
 * neighbors. */
export function LocalizedToOneRow() {
  return (
    <div style={{ maxWidth: 560 }}>
      <FormRow label="Public address" hint="Embedded in pairing codes.">
        <span style={{ fontFamily: 'ui-monospace, Consolas, monospace', fontSize: 12, color: 'var(--text-2)' }}>
          play.example.net
        </span>
      </FormRow>
      <WarnWash on>
        <FormRow label="Windows service" hint="Run headless in the background; the app attaches as a viewer.">
          <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
            <Badge tone="warn">Stopped</Badge>
            <Button variant="ghost" size="sm" onClick={() => {}}>Start</Button>
          </div>
        </FormRow>
      </WarnWash>
      <FormRow label="Config folder" hint="Where this install keeps its identity and tunnels.">
        <Button variant="ghost" size="sm" onClick={() => {}}>Open</Button>
      </FormRow>
    </div>
  )
}
