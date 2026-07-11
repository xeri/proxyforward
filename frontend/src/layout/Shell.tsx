import {ReactNode, useEffect, useRef} from 'react'
import {prefersReduced} from '../theme'

// Content slides this many px beneath the sidebar's edge before clipping.
const UNDERLAP = 10

/**
 * The app frame for a frameless window. The sidebar spans full height and the
 * title bar sits beside it, sharing one continuous glass sheet (an L-shape
 * around the content). Without a sidebar (wizard) the bar spans the width.
 *
 * The scroll container spans the full window height and pads down by
 * --titlebar-h, so scrolled content passes under the translucent chrome and
 * comes out diffused — the sheet visibly frosts whatever slides beneath it.
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
        gridTemplateRows: 'var(--titlebar-h) 1fr',
        gridTemplateColumns: sidebar ? '224px 1fr' : '1fr',
      }}
    >
      {sidebar && (
        <aside className="pf-sheet relative z-10 row-span-2 min-h-0 border-r border-[var(--border)]">
          {sidebar}
        </aside>
      )}
      <header
        className="pf-sheet relative z-20 border-b border-[var(--border)]"
        style={{gridRow: 1, gridColumn: sidebar ? 2 : 1}}
      >
        {titlebar}
      </header>
      <main
        className="relative min-h-0 min-w-0 overflow-y-auto"
        style={{
          gridRow: '1 / span 2',
          gridColumn: sidebar ? 2 : 1,
          paddingTop: 'var(--titlebar-h)',
          scrollPaddingTop: 'calc(var(--titlebar-h) + 12px)',
          ...(sidebar ? {marginLeft: -UNDERLAP, paddingLeft: UNDERLAP} : undefined),
        }}
      >
        {children}
      </main>
    </div>
  )
}
