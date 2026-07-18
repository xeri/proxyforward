import { useState } from 'react'
import {
  Button, Codebox, CopyButton, Field, Modal, RoleWord, SegmentedControl,
} from 'frontend'

const CODE =
  'pxf://play.example.com:8474/v1/pair/tkt_9f3ce8a1b7124d06a5c2#sha256:9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08'

/** The Issue-pairing-code dialog from Agents.tsx in its issued-code state:
 * intro copy, the lifetime toggle, the minted code with its copy affordance,
 * and a ghost Done in the footer. Mounts open by design; onClose is a no-op. */
export function IssuePairingCode() {
  const [mode, setMode] = useState<'single' | 'reusable'>('single')
  return (
    <Modal
      title="Issue pairing code"
      onClose={() => {}}
      wide
      footer={<Button variant="ghost" onClick={() => {}}>Done</Button>}
    >
      <div style={{ display: 'grid', gap: 16 }}>
        <p style={{ fontSize: 13, lineHeight: 1.6, color: 'var(--text-2)', margin: 0 }}>
          Share this code with a machine. Every agent that enrolls with it joins this
          gateway's roster and proves a durable identity you can scope and revoke.
        </p>
        <Field
          label="Type"
          hint={mode === 'reusable'
            ? 'Enrolls multiple agents; lives until you revoke it.'
            : 'Enrolls one agent, then expires. Valid for 10 minutes.'}
        >
          <SegmentedControl<'single' | 'reusable'>
            value={mode}
            onChange={setMode}
            options={[
              { value: 'single', label: 'Single-use' },
              { value: 'reusable', label: 'Reusable' },
            ]}
          />
        </Field>
        <div style={{ display: 'grid', gap: 8 }}>
          <Codebox text={CODE} action={<CopyButton text={CODE} />} />
          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 12 }}>
            <span style={{ fontSize: 12, color: 'var(--text-3)', minWidth: 0 }}>
              Single-use — expires once one agent enrolls, or after 10 minutes.
            </span>
            <Button variant="ghost" size="sm" onClick={() => {}}>New code</Button>
          </div>
        </div>
      </div>
    </Modal>
  )
}

/** The role-switch confirmation from RoleSwitcher.tsx — a destructive-ish
 * decision with Cancel + primary commit in the footer. */
export function ConfirmSwitch() {
  return (
    <Modal
      title="Switch to gateway?"
      onClose={() => {}}
      footer={
        <>
          <Button variant="ghost" onClick={() => {}}>Cancel</Button>
          <Button onClick={() => {}}>Become the gateway</Button>
        </>
      }
    >
      <div style={{ display: 'grid', gap: 12, fontSize: 13, lineHeight: 1.6, color: 'var(--text-2)' }}>
        <p style={{ margin: 0 }}>
          This machine stops being the <RoleWord role="agent">agent</RoleWord> and becomes
          the <RoleWord role="gateway">gateway</RoleWord>. The engine restarts, so{' '}
          <b style={{ color: 'var(--text)' }}>any live player sessions drop</b>.
        </p>
        <p style={{ margin: 0 }}>
          Players will connect to this machine and it will listen for an agent. Its
          certificate is kept in the config folder, so any pairing code it has already
          handed out keeps working.
        </p>
      </div>
    </Modal>
  )
}
