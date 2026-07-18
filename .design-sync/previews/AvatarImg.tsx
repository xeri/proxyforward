// NOTE: previews run without the Go asset server, so /pf/avatar/ 404s and
// every head resolves to the component's own inline offline-SVG fallback —
// that pixel head IS the honest fallback state, not a stand-in.
import { AvatarImg } from 'frontend'

/** The Players-wall card head: 84 px display of a 128 px render, pixelated,
 * inside the wall card's frame treatment. Renders the offline fallback here
 * (no asset server in previews). */
export function WallCard() {
  return (
    <div style={{ display: 'grid', gap: 8, justifyItems: 'center', width: 132, padding: 14, border: '1px solid var(--border)', borderRadius: 'var(--r-lg)', background: 'var(--panel)' }}>
      <span style={{ position: 'relative' }}>
        <AvatarImg
          id="offline:Herobrine_7"
          size={128}
          px={84}
          className="rounded-[var(--r-md)] border border-[var(--border)] bg-[var(--panel-2)]"
        />
        <span style={{ position: 'absolute', bottom: -4, right: -4, width: 12, height: 12, borderRadius: 9999, background: 'var(--good)', border: '2px solid var(--panel)' }} />
      </span>
      <span style={{ fontFamily: "ui-monospace, 'Cascadia Mono', Consolas, monospace", fontSize: 13, fontWeight: 600 }}>Herobrine_7</span>
    </div>
  )
}

/** The size ramp: 24 px roster chip → 48 px list row → 96 px dossier band.
 * All fall back to the offline pixel head without the asset server. */
export function Sizes() {
  return (
    <div style={{ display: 'flex', gap: 20, alignItems: 'end' }}>
      {[
        { px: 24, label: 'roster' },
        { px: 48, label: 'row' },
        { px: 96, label: 'dossier' },
      ].map(s => (
        <div key={s.px} style={{ display: 'grid', gap: 6, justifyItems: 'center' }}>
          <AvatarImg
            id={`offline:size${s.px}`}
            size={128}
            px={s.px}
            className="rounded-[var(--r-md)] border border-[var(--border)] bg-[var(--panel-2)]"
          />
          <span style={{ fontSize: 11, color: 'var(--text-3)' }}>{s.px}px · {s.label}</span>
        </div>
      ))}
    </div>
  )
}
