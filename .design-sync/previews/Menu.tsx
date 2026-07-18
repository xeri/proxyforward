import { useRef } from 'react'
import { Button, Emblem, Menu, MenuItem } from 'frontend'

/** The role switcher's menu (RoleSwitcher.tsx) — the app's one real Menu: a
 * portalled float on menu glass anchored below its trigger, a quiet section
 * label, and Emblem-led rows. Rendered open (onClose is a no-op); the portal
 * escaping to document.body is expected in single-card mode. */
export function RolePicker() {
  const anchor = useRef<HTMLDivElement>(null)
  return (
    <div style={{ minHeight: 320 }}>
      <div ref={anchor} style={{ display: 'inline-block' }}>
        <Button variant="subtle" onClick={() => {}}>Running as gateway</Button>
      </div>
      <Menu open anchor={anchor} onClose={() => {}} minWidth={228}>
        <div
          style={{
            padding: '4px 8px',
            fontSize: 10,
            fontWeight: 600,
            textTransform: 'uppercase',
            letterSpacing: '0.08em',
            color: 'var(--text-3)',
          }}
        >
          This machine is
        </div>
        <MenuItem
          on
          lead={<Emblem role="gateway" size={22} />}
          title="Gateway"
          hint="Faces the internet — players connect here"
          onClick={() => {}}
        />
        <MenuItem
          lead={<Emblem role="agent" size={22} fixed />}
          title="Agent"
          hint="Hosts Minecraft — dials out"
          onClick={() => {}}
        />
      </Menu>
    </div>
  )
}
