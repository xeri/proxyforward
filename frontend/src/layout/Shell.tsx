import {ReactNode, useEffect, useRef} from 'react'
import {prefersReduced} from '../theme'

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

  // The pointer is a lamp: one delegated, rAF-throttled listener writes local
  // coordinates onto the hovered card; glass.css turns them into a traveling
  // rim glow and a faint surface bloom. Dormant under reduced motion.
  useEffect(() => {
    if (prefersReduced()) return
    const root = rootRef.current
    if (!root) return
    let raf = 0
    let card: HTMLElement | null = null
    let x = 0
    let y = 0
    const apply = () => {
      raf = 0
      if (!card) return
      const r = card.getBoundingClientRect()
      card.style.setProperty('--mx', `${x - r.left}px`)
      card.style.setProperty('--my', `${y - r.top}px`)
    }
    const drop = (el: HTMLElement | null) => {
      el?.style.removeProperty('--mx')
      el?.style.removeProperty('--my')
    }
    const onMove = (e: PointerEvent) => {
      const hit = (e.target as Element).closest?.('.pf-card') as HTMLElement | null
      if (hit !== card) {
        drop(card)
        card = hit
      }
      if (!card) return
      x = e.clientX
      y = e.clientY
      if (!raf) raf = requestAnimationFrame(apply)
    }
    const onLeave = () => {
      drop(card)
      card = null
    }
    root.addEventListener('pointermove', onMove)
    root.addEventListener('pointerleave', onLeave)
    return () => {
      root.removeEventListener('pointermove', onMove)
      root.removeEventListener('pointerleave', onLeave)
      if (raf) cancelAnimationFrame(raf)
      drop(card)
    }
  }, [])

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
