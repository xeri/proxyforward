import {CSSProperties, useEffect, useRef, useState} from 'react'
import {flushSync} from 'react-dom'
import {Shell} from './layout/Shell'
import {TitleBar} from './layout/TitleBar'
import {Sidebar} from './layout/Sidebar'
import {NAV_MAIN, NAV_SETTINGS, NavId} from './nav'
import {Overview} from './screens/Overview'
import {Traffic} from './screens/Traffic'
import {Players} from './screens/Players'
import {Analytics} from './screens/Analytics'
import {Activity} from './screens/Activity'
import {Settings} from './screens/Settings'
import {Tunnels} from './screens/Tunnels'
import {Wizard} from './screens/Wizard'
import {CommandPalette} from './components/CommandPalette'
import {Spinner} from './components/ui'
import {UIStatus, useTick} from './state'
import {prefersReduced} from './motion'

const supportsVT = typeof (document as Document & {startViewTransition?: unknown}).startViewTransition === 'function'

/* Contextual ambient glow: the backdrop's status blob (tokens.css --ambient)
   tints amber when the tunnel is degraded and red when it is down. Derived
   from the same signals the pills and pipeline use. */
type Ambient = 'ok' | 'warn' | 'bad'
function ambientOf(s: UIStatus | null): Ambient {
  if (!s || s.mode === 'wizard' || !s.role) return 'ok'
  if (s.engineFatal) return 'bad'
  // Link state first — the health score only means something over a live
  // link. A waiting gateway idles amber; an agent that lost its gateway is red.
  const up = s.role === 'agent' ? s.linkUp : s.agentConnected
  if (!up) return s.role === 'agent' ? 'bad' : 'warn'
  if (s.healthScore === 'bad') return 'bad'
  if (s.healthScore === 'warn') return 'warn'
  return 'ok'
}

export default function App() {
  const status = useTick()
  const [nav, setNav] = useState<NavId>('overview')

  // The whole app re-tints per role via CSS: [data-role] swaps the accent ramp.
  useEffect(() => {
    document.documentElement.dataset.role = status?.role || 'unset'
  }, [status?.role])

  // Ambient state → data-ambient. The first reading applies immediately;
  // after that a new state must hold for 3s of consecutive ticks before the
  // backdrop shifts, so 2 Hz flapping never strobes the room. The 1.2s
  // --ambient transition (base.css) does the actual cross-fade.
  const ambientRef = useRef<{applied: Ambient | null; pending: Ambient | null; timer: number}>(
    {applied: null, pending: null, timer: 0},
  )
  useEffect(() => {
    const next = ambientOf(status)
    const ref = ambientRef.current
    const apply = () => {
      document.documentElement.dataset.ambient = next
      ref.applied = next
      ref.pending = null
    }
    if (ref.applied === null) { apply(); return }
    if (next === ref.applied) {
      if (ref.pending !== null) { window.clearTimeout(ref.timer); ref.pending = null }
      return
    }
    if (ref.pending === next) return // countdown to this state is already running
    window.clearTimeout(ref.timer)
    ref.pending = next
    ref.timer = window.setTimeout(apply, 3000)
  }, [status])

  // Navigate inside a view transition: content morphs, chrome stays pinned.
  const go = (id: NavId) => {
    if (id === nav) return
    const doc = document as Document & {startViewTransition?: (cb: () => void) => {finished: Promise<void>}}
    if (!prefersReduced() && doc.startViewTransition) {
      document.documentElement.classList.add('pf-vt-nav')
      const vt = doc.startViewTransition(() => flushSync(() => setNav(id)))
      vt.finished.finally(() => document.documentElement.classList.remove('pf-vt-nav'))
    } else {
      setNav(id)
    }
  }

  // Ctrl+K opens the palette; Ctrl+1..6 jump straight to a screen.
  const [palette, setPalette] = useState(false)
  useEffect(() => {
    const items = [...NAV_MAIN, NAV_SETTINGS]
    const h = (e: KeyboardEvent) => {
      if (!e.ctrlKey || e.altKey || e.metaKey) return
      if (e.key.toLowerCase() === 'k') { e.preventDefault(); setPalette(o => !o); return }
      const hit = items.find(n => n.shortcut === e.key)
      if (hit) { e.preventDefault(); go(hit.id) }
    }
    window.addEventListener('keydown', h)
    return () => window.removeEventListener('keydown', h)
  })

  // The wizard holds the stage past backend cutover so its final act (the
  // live handshake) can play; it hands over via onDone with a view transition.
  const backendWizard = !!status && (status.mode === 'wizard' || status.role === '')
  const [wizardHold, setWizardHold] = useState(false)
  useEffect(() => {
    if (backendWizard) setWizardHold(true)
  }, [backendWizard])
  const finishWizard = () => {
    const doc = document as Document & {startViewTransition?: (cb: () => void) => void}
    if (!prefersReduced() && doc.startViewTransition) {
      // Glaze the handover: the ambient glow flares and a glare sweep crosses
      // the sheet while the wizard morphs into the console.
      const html = document.documentElement
      html.classList.add('pf-glaze')
      window.setTimeout(() => html.classList.remove('pf-glaze'), 1600)
      doc.startViewTransition(() => flushSync(() => setWizardHold(false)))
    } else {
      setWizardHold(false)
    }
  }

  if (!status) {
    return (
      <Shell titlebar={<TitleBar brand />}>
        <div className="flex h-full items-center justify-center text-[var(--text-3)]"><Spinner size={22} /></div>
      </Shell>
    )
  }

  if (backendWizard || wizardHold) {
    return (
      <Shell titlebar={<TitleBar brand />}>
        <Wizard status={status} onDone={finishWizard} />
      </Shell>
    )
  }

  const s = status
  return (
    <Shell
      sidebar={<Sidebar status={s} nav={nav} onNav={go} />}
      titlebar={<TitleBar status={s} nav={nav} onPalette={() => setPalette(true)} />}
    >
      {/* The wide adaptive canvas: screens lay out against this container's
          real width (@container queries), not the viewport, so the sidebar
          is automatically accounted for. Interim max-w-5xl clamps keep the
          not-yet-redesigned screens at their designed width; each redesign
          deletes its own clamp. */}
      <div
        className="@container mx-auto w-full max-w-[var(--content-max)] px-[var(--page-pad)] py-6"
        style={{viewTransitionName: 'pf-content'} as CSSProperties}
      >
        <div key={nav} className={supportsVT ? '' : 'pf-page'}>
          {nav === 'overview' && <Overview status={s} onNavigate={go} />}
          {nav === 'traffic' && <Traffic status={s} onNavigate={go} />}
          {nav === 'players' && <Players status={s} />}
          {nav === 'analytics' && <Analytics status={s} />}
          {nav === 'tunnels' && <Tunnels status={s} />}
          {nav === 'activity' && <Activity attached={s.mode === 'attached'} />}
          {nav === 'settings' && <Settings status={s} />}
        </div>
      </div>
      {palette && <CommandPalette ctx={{status: s, go}} onClose={() => setPalette(false)} />}
    </Shell>
  )
}
