import { useState } from 'react'
import { FormRow, Button, Badge, Switch } from 'frontend'

/** Label + hint left, action right — the force-stop row from Settings. */
export function WithButton() {
  return (
    <div style={{ maxWidth: 520 }}>
      <FormRow label="Force stop" hint="Cut the run immediately instead of waiting for a clean shutdown.">
        <Button variant="danger" size="sm" onClick={() => {}}>Force stop</Button>
      </FormRow>
    </div>
  )
}

/** Status + action cluster — the Windows service row from Settings. */
export function WithBadgeAndAction() {
  return (
    <div style={{ maxWidth: 520 }}>
      <FormRow label="Windows service" hint="Run headless in the background; the app attaches as a viewer.">
        <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
          <Badge tone="good">Running</Badge>
          <Button variant="ghost" size="sm" onClick={() => {}}>Stop</Button>
        </div>
      </FormRow>
    </div>
  )
}

/** A switch as the control — the log-file row. */
export function WithSwitch() {
  const [on, setOn] = useState(true)
  return (
    <div style={{ maxWidth: 520 }}>
      <FormRow label="Write to log file" hint="Rotated alongside the config in %AppData%\proxyforward.">
        <Switch checked={on} onChange={setOn} label="Write to log file" />
      </FormRow>
    </div>
  )
}
