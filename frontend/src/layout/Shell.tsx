import {ReactNode, useEffect, useRef} from 'react'
import {useMotion} from '../motion'
import {useRubberBand} from '../rubberband'

// Content slides this many px beneath the sidebar's edge before clipping.
// Inline px math — must stay a plain number; documented for token readers
// as the design system's "underlap" (no CSS var: used in style calc below).
const UNDERLAP = 10

/**
 * The app frame for a frameless window. The sidebar is a full-height pane of
 * heavy glass; the title bar floats beside it as a detached island inset from
 * the window edges. Without a sidebar (wizard) the island spans the width.
 *
 * The scroll container spans the full window height and pads down by
 * --chrome-top, so scrolled content passes beneath the island — through the
 * visible gutters around it — and comes out diffused: the floating glass
 * visibly frosts whatever slides underneath.
 */
export function Shell({sidebar, titlebar, children}: {
  sidebar?: ReactNode
  titlebar: ReactNode
  children: ReactNode
}) {
  const rootRef = useRef<HTMLDivElement>(null)
  const {reduced} = useMotion()

  // Scrollers give at their ends instead of stopping dead (rubberband.ts).
  useRubberBand()

  // The pointer is a lamp, and Signal Glass answers it: one delegated,
  // rAF-throttled listener writes local coordinates onto each .pf-signal
  // surface within reach (the identity surface — at most one or two per
  // screen) and stamps data-awake so the caustic drift runs. The light rests
  // ~5s after the pointer stops or leaves: idle UI is still UI. Dormant under
  // reduced motion; re-arms live when the Animations preference flips (Shell
  // never remounts).
  useEffect(() => {
    if (reduced) return
    const root = rootRef.current
    if (!root) return
    // Slightly past the widest glow gradient (240px) so surfaces dim just
    // after the light has visually left them.
    const REACH = 280
    const AWAKE_MS = 5000
    let raf = 0
    let doze: number | undefined
    let x = 0
    let y = 0
    const lit = new Set<HTMLElement>()
    const drop = (el: HTMLElement) => {
      el.style.removeProperty('--mx')
      el.style.removeProperty('--my')
      delete el.dataset.awake
      lit.delete(el)
    }
    const rest = () => {
      for (const el of [...lit]) delete el.dataset.awake
    }
    const apply = () => {
      raf = 0
      for (const el of root.querySelectorAll<HTMLElement>('.pf-signal')) {
        const r = el.getBoundingClientRect()
        if (x > r.left - REACH && x < r.right + REACH && y > r.top - REACH && y < r.bottom + REACH) {
          el.style.setProperty('--mx', `${x - r.left}px`)
          el.style.setProperty('--my', `${y - r.top}px`)
          el.dataset.awake = '1'
          lit.add(el)
        } else if (lit.has(el)) {
          drop(el)
        }
      }
      window.clearTimeout(doze)
      doze = window.setTimeout(rest, AWAKE_MS)
    }
    const onMove = (e: PointerEvent) => {
      x = e.clientX
      y = e.clientY
      if (!raf) raf = requestAnimationFrame(apply)
    }
    const onLeave = () => {
      for (const el of [...lit]) drop(el)
    }
    root.addEventListener('pointermove', onMove)
    root.addEventListener('pointerleave', onLeave)
    return () => {
      root.removeEventListener('pointermove', onMove)
      root.removeEventListener('pointerleave', onLeave)
      if (raf) cancelAnimationFrame(raf)
      window.clearTimeout(doze)
      onLeave()
    }
  }, [reduced])

  return (
    <div
      ref={rootRef}
      className="grid h-full"
      style={{
        gridTemplateRows: 'var(--chrome-top) 1fr',
        gridTemplateColumns: sidebar ? 'var(--sidebar-w) 1fr' : '1fr',
      }}
    >
      {sidebar && (
        <aside className="pf-sheet relative z-10 row-span-2 min-h-0">
          {sidebar}
        </aside>
      )}
      {/* The strip itself ignores the pointer so content scrolling through
          the gutters stays interactive; only the island is live. */}
      <header
        className="pointer-events-none relative z-20 flex"
        style={{
          gridRow: 1,
          gridColumn: sidebar ? 2 : 1,
          padding: 'var(--titlebar-inset) 12px 0',
        }}
      >
        <div
          className="pf-island pointer-events-auto min-w-0 flex-1 overflow-hidden"
          style={{height: 'var(--titlebar-h)'}}
        >
          {titlebar}
        </div>
      </header>
      {/* overscroll-none: the page scroller must never let WebView2 composite its own
          elastic overscroll under ours — two rubber bands, one gesture. rubberband.ts
          preventDefaults every delta it takes, so this is belt and braces, but it is
          the standard way these ship visibly broken. It changes no ownership: ownerFor
          breaks at the page before it reads overscroll-behavior. */}
      <main
        className="relative min-h-0 min-w-0 overflow-y-auto overscroll-y-none"
        style={{
          gridRow: '1 / span 2',
          gridColumn: sidebar ? 2 : 1,
          paddingTop: 'var(--chrome-top)',
          scrollPaddingTop: 'calc(var(--chrome-top) + 12px)',
          ...(sidebar ? {marginLeft: -UNDERLAP, paddingLeft: UNDERLAP} : undefined),
        }}
      >
        {children}
      </main>
      {/* Rubber-band edge light. Its own grid cell, so it overlays the content
          column exactly and — unlike a pseudo-element on <main> — never scrolls.
          Under the island (z-20), so the glass frosts the bloom passing beneath
          it. Inert until rubberband.ts stamps data-band on it. */}
      <div
        className="pf-band-glow relative z-[15]"
        data-band-glow
        aria-hidden
        style={{gridRow: '1 / span 2', gridColumn: sidebar ? 2 : 1}}
      />
    </div>
  )
}
