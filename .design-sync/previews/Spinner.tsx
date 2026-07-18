import { Spinner } from 'frontend'

/** Spinners never stand alone in the app — they ride a labeled row. Top: the
 * connection pill's reconnect state (warn-colored, 12px). Bottom: the modal's
 * busy line while a pairing code mints. */
export function LabeledRows() {
  return (
    <div style={{ display: 'grid', gap: 14, maxWidth: 360 }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
        <span style={{ color: 'var(--warn)', display: 'inline-flex' }}><Spinner size={12} /></span>
        <span style={{ fontSize: 13, color: 'var(--text-2)' }}>Reconnecting — attempt 3</span>
      </div>
      <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
        <span style={{ color: 'var(--text-3)', display: 'inline-flex' }}><Spinner size={16} /></span>
        <span style={{ fontSize: 13, color: 'var(--text-3)' }}>Generating pairing code…</span>
      </div>
    </div>
  )
}

/** The three sizes in service: 12 in pills, 16 default, 22 for a screen that
 * hasn't loaded yet (App.tsx). */
export function Sizes() {
  const rows: [number, string][] = [
    [12, 'pill / inline'],
    [16, 'default'],
    [22, 'screen loading'],
  ]
  return (
    <div style={{ display: 'flex', alignItems: 'center', gap: 28 }}>
      {rows.map(([size, label]) => (
        <div key={size} style={{ display: 'grid', gap: 6, justifyItems: 'center' }}>
          <span style={{ color: 'var(--text-3)', display: 'inline-flex' }}><Spinner size={size} /></span>
          <span style={{ fontSize: 11, color: 'var(--text-3)' }}>{size}px — {label}</span>
        </div>
      ))}
    </div>
  )
}
