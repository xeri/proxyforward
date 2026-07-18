import { Button, IconExternal, IconPlus, IconTrash } from 'frontend'

/** The four variants at md. Primary carries the accent fill + glow (one per
 * surface); ghost is the quiet workhorse; subtle sits inside cards; danger is
 * destructive-only. Copy is ported from the app's own screens. */
export function Variants() {
  return (
    <div style={{ display: 'flex', gap: 10, alignItems: 'center', flexWrap: 'wrap' }}>
      <Button onClick={() => {}}>Issue pairing code</Button>
      <Button variant="ghost" onClick={() => {}}>Done</Button>
      <Button variant="subtle" onClick={() => {}}>Copy</Button>
      <Button variant="danger" onClick={() => {}}>Revoke</Button>
    </div>
  )
}

/** size="sm" is the list-toolbar form; an icon rides inline before the label. */
export function SmallWithIcons() {
  return (
    <div style={{ display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap' }}>
      <Button size="sm" onClick={() => {}}><IconPlus size={14} /> Add tunnel</Button>
      <Button variant="ghost" size="sm" onClick={() => {}}><IconExternal size={14} /> Export diagnostics</Button>
      <Button variant="danger" size="sm" onClick={() => {}}><IconTrash size={14} /> Delete</Button>
    </div>
  )
}

export function Disabled() {
  return (
    <div style={{ display: 'flex', gap: 10, alignItems: 'center', flexWrap: 'wrap' }}>
      <Button disabled onClick={() => {}}>Issue code</Button>
      <Button variant="ghost" disabled onClick={() => {}}>Previous</Button>
      <Button variant="danger" disabled onClick={() => {}}>Revoke</Button>
    </div>
  )
}
