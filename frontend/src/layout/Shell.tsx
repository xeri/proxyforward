import {ReactNode, useEffect, useRef} from 'react'
import {useMotion} from '../motion'

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

  // The pointer is a lamp: one delegated, rAF-throttled listener writes local
  // coordinates onto every card within the lamp's reach — not just the hovered
  // one — so the rim glow spills continuously across card gaps instead of
  // cutting off at each edge. The glow layers live inside .pf-card, so the
  // background between cards never lights up. Dormant under reduced motion;
  // re-arms live when the Animations preference flips (Shell never remounts).
  useEffect(() => {
    if (reduced) return
    const root = rootRef.current
    if (!root) return
    // Slightly past the widest glow gradient (240px) so cards dim just after
    // the light has visually left them.
    const REACH = 280
    let raf = 0
    let x = 0
    let y = 0
    const lit = new Set<HTMLElement>()
    const drop = (el: HTMLElement) => {
      el.style.removeProperty('--mx')
      el.style.removeProperty('--my')
      lit.delete(el)
    }
    const apply = () => {
      raf = 0
      for (const el of root.querySelectorAll<HTMLElement>('.pf-card')) {
        const r = el.getBoundingClientRect()
        if (x > r.left - REACH && x < r.right + REACH && y > r.top - REACH && y < r.bottom + REACH) {
          el.style.setProperty('--mx', `${x - r.left}px`)
          el.style.setProperty('--my', `${y - r.top}px`)
          lit.add(el)
        } else if (lit.has(el)) {
          drop(el)
        }
      }
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
        <aside className="pf-sheet relative z-10 row-span-2 min-h-0 border-r border-[var(--border)]">
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
      <main
        className="relative min-h-0 min-w-0 overflow-y-auto"
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
    </div>
  )
}
