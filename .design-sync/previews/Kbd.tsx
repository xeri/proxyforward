import { Kbd } from 'frontend'

/** The palette chord from the title bar — spaces split into one chip per key. */
export function Chord() {
  return <Kbd>Ctrl K</Kbd>
}

/** The CommandPalette footer row: chips inline with quiet caption text. */
export function PaletteFooter() {
  return (
    <div style={{ display: 'flex', gap: 14, alignItems: 'center', fontSize: 11, color: 'var(--text-3)' }}>
      <span style={{ display: 'inline-flex', alignItems: 'center', gap: 4 }}><Kbd>↑ ↓</Kbd> navigate</span>
      <span style={{ display: 'inline-flex', alignItems: 'center', gap: 4 }}><Kbd>Enter</Kbd> run</span>
      <span style={{ display: 'inline-flex', alignItems: 'center', gap: 4 }}><Kbd>Esc</Kbd> close</span>
    </div>
  )
}

/** Single-key navigation shortcuts as the sidebar shows them on hover. */
export function SidebarShortcuts() {
  return (
    <div style={{ display: 'grid', gap: 6, maxWidth: 200 }}>
      {[
        ['Overview', 'G O'],
        ['Traffic', 'G T'],
        ['Agents', 'G A'],
      ].map(([screen, keys]) => (
        <div key={screen} style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', fontSize: 13, color: 'var(--text-2)' }}>
          <span>{screen}</span>
          <Kbd>{keys}</Kbd>
        </div>
      ))}
    </div>
  )
}
