import type { ReactNode } from 'react'
import { Emblem, IconEdit, IconExternal, IconTrash, MenuItem } from 'frontend'

/** MenuItem rows only make sense on their parent's menu-glass surface, but the
 * real Menu portals to document.body and would escape this grid cell — so the
 * rows sit on a static div wearing the Menu's own material recipe (.pf-menu is
 * material-only; positioning stays with the consumer). */
function MenuSurface({ children }: { children: ReactNode }) {
  return (
    <div className="pf-menu" style={{ borderRadius: 'var(--r-md)', padding: 4, maxWidth: 280 }}>
      {children}
    </div>
  )
}

/** The RoleSwitcher rows: Emblem lead, title, quiet hint, and the check on the
 * current choice (`on` also tints the row with the accent). */
export function RoleRows() {
  return (
    <MenuSurface>
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
    </MenuSurface>
  )
}

/** Plain rows: icon lead + title only, a row with a hint, and a disabled row
 * (an action the current state rules out). */
export function States() {
  return (
    <MenuSurface>
      <MenuItem lead={<IconExternal size={15} />} title="Open details" onClick={() => {}} />
      <MenuItem
        lead={<IconEdit size={15} />}
        title="Rename agent"
        hint="Shown in the roster and on Overview"
        onClick={() => {}}
      />
      <MenuItem
        lead={<IconTrash size={15} />}
        title="Revoke identity"
        hint="Already revoked — nothing to revoke"
        disabled
        onClick={() => {}}
      />
    </MenuSurface>
  )
}
