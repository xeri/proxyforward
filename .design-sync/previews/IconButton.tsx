import { IconButton, IconRefresh, IconEye, IconEdit, IconTrash } from 'frontend'

/** The ghost toolbar row — pairing-code controls ported from Overview. */
export function GhostToolbar() {
  return (
    <div style={{ display: 'flex', gap: 4, alignItems: 'center' }}>
      <IconButton title="Issue a fresh code" onClick={() => {}}><IconRefresh size={16} /></IconButton>
      <IconButton title="Reveal pairing code" onClick={() => {}}><IconEye size={16} /></IconButton>
      <IconButton title="Edit tunnel" onClick={() => {}}><IconEdit size={16} /></IconButton>
    </div>
  )
}

/** Danger variant — the delete affordance from the tunnel list. */
export function Danger() {
  return (
    <div style={{ display: 'flex', gap: 4, alignItems: 'center' }}>
      <IconButton title="Delete" variant="danger" onClick={() => {}}><IconTrash size={16} /></IconButton>
    </div>
  )
}

export function Disabled() {
  return (
    <div style={{ display: 'flex', gap: 4, alignItems: 'center' }}>
      <IconButton title="Issue a fresh code" disabled onClick={() => {}}><IconRefresh size={16} /></IconButton>
      <IconButton title="Delete" variant="danger" disabled onClick={() => {}}><IconTrash size={16} /></IconButton>
    </div>
  )
}
