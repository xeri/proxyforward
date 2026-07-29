import {CSSProperties, useEffect, useRef, useState} from 'react'
import {flushSync} from 'react-dom'
import {Shell} from './layout/Shell'
import {TitleBar} from './layout/TitleBar'
import {Sidebar} from './layout/Sidebar'
import {mainNav, navFor, NavId} from './nav'
import {Overview} from './screens/Overview'
import {Agents} from './screens/Agents'
import {Traffic} from './screens/Traffic'
import {Players} from './screens/Players'
import {Analytics} from './screens/Analytics'
import {Activity} from './screens/Activity'
import {Settings} from './screens/Settings'
import {Tunnels} from './screens/Tunnels'
import {Wizard} from './screens/Wizard'
import {CloseDialog} from './components/CloseDialog'
import {CommandPalette} from './components/CommandPalette'
import {Spinner} from './components/ui'
import {announce, Announcer} from './components/Announcer'
import {CrashProbe, ErrorBoundary, ScreenFault, ShellFault} from './components/ErrorBoundary'
import {EngineSilent} from './components/EngineSilent'
import {isEngineOff, runStateOf, UIStatus, useTick, worstHealth} from './state'
import {prefersReduced} from './motion'
import {resetBands} from './rubberband'
import {TakePendingDeepLink} from '../wailsjs/go/app/App'
import {EventsOn} from '../wailsjs/runtime/runtime'

const supportsVT = typeof (document as Document & {startViewTransition?: unknown}).startViewTransition === 'function'

/* How long a new reading must hold before the app reacts to it. The tick is
   2 Hz, so anything that answers a single snapshot answers noise: the ambient
   glow would strobe the room and the announcer would talk over itself. One
   constant, because both consumers below are deliberately the same rule. */
const SETTLE_MS = 3000

/* Contextual ambient glow: the backdrop's status blob (tokens.css --ambient)
   tints amber when the tunnel is degraded and red when it is down. Derived
   from the same signals the pills and pipeline use. */
type Ambient = 'ok' | 'warn' | 'bad'
function ambientOf(s: UIStatus | null): Ambient {
  if (!s || s.mode === 'wizard' || !s.role) return 'ok'
  if (s.engineFatal) return 'bad'
  // Deliberately off is not a fault: the link is down because nothing is
  // dialing, not because a dial is failing. Never paint the room for a state
  // the operator chose — the stopped banner carries that message. (Checked
  // before the linkUp branch below, which would otherwise read the quiet link
  // as an outage.)
  if (isEngineOff(s)) return 'ok'
  // Link state first — the health score only means something over a live
  // link. A waiting gateway idles amber; an agent that lost its gateway is red.
  const up = s.role === 'agent' ? s.linkUp : s.agentConnected
  if (!up) return s.role === 'agent' ? 'bad' : 'warn'
  if (s.healthScore === 'bad') return 'bad'
  if (s.healthScore === 'warn') return 'warn'
  return 'ok'
}

/* The spoken counterpart to ambientOf: the same snapshot reduced to the one
   sentence worth saying out loud, plus whether it is urgent enough to interrupt
   a screen reader mid-word. Empty text means "nothing to announce" (setup, or
   no snapshot yet) — it settles like any other reading, so leaving the wizard
   announces whatever state follows it.
   The sentence IS the state's identity: the settle effect below compares text,
   so a changed player count or a changed fatal message is a transition, and an
   unchanged one is silence. */
type Spoken = {text: string; urgent: boolean}

function spokenOf(s: UIStatus | null): Spoken {
  if (!s || s.mode === 'wizard' || !s.role) return {text: '', urgent: false}
  if (s.engineFatal) return {text: `The engine stopped. ${s.engineFatal}`, urgent: true}
  if (runStateOf(s) === 'stopping') return {text: 'Stopping the tunnel run.', urgent: false}
  if (isEngineOff(s)) return {text: 'The tunnel run is stopped.', urgent: false}
  const isAgent = s.role === 'agent'
  const agents = s.agents ?? []
  // Count the fleet, not the rows we were sent — the list is clamped, exactly
  // as ConnectionPill reads it.
  const n = s.agentsTotal || agents.length
  // A gateway is never "down", it is waiting; an agent is never "waiting". Both
  // sentences, and the health rollup below, are ConnectionPill's rules said out
  // loud rather than a second mapping of the same fields.
  if (isAgent && !s.linkUp) return {text: 'The link to the gateway is down. Reconnecting.', urgent: false}
  if (!isAgent && n === 0) return {text: 'Waiting for agents.', urgent: false}
  const link = isAgent
    ? 'Connected to the gateway.'
    : n === 1 ? 'One agent online.' : `${n} agents online.`
  const tone = isAgent
    ? (s.healthScore === 'warn' || s.healthScore === 'bad' ? s.healthScore : 'good')
    : worstHealth(agents)
  const health = tone === 'bad' ? ' Link quality is poor.' : tone === 'warn' ? ' Link quality is fair.' : ''
  const p = s.connectionsTotal || s.connections?.length || 0
  const players = p === 0 ? ' No players connected.' : p === 1 ? ' One player connected.' : ` ${p} players connected.`
  return {text: link + health + players, urgent: false}
}

/**
 * The app's outermost frame. Two things live above the console, and both are
 * there because they must survive it: the live regions (a region announces only
 * what changes while it is already mounted) and the outer error boundary (if
 * the console throws, Shell is gone and the fallback has to draw its own frame).
 */
export default function App() {
  return (
    <>
      <Announcer />
      <ErrorBoundary fallback={f => <ShellFault fault={f} />}>
        <Console />
      </ErrorBoundary>
    </>
  )
}

function Console() {
  const tick = useTick()
  const status = tick.status
  const [nav, setNav] = useState<NavId>('overview')

  // The whole app re-tints per role via CSS: [data-role] swaps the accent ramp.
  useEffect(() => {
    document.documentElement.dataset.role = status?.role || 'unset'
  }, [status?.role])

  // Ambient state → data-ambient. The first reading applies immediately;
  // after that a new state must hold for SETTLE_MS of consecutive ticks before
  // the backdrop shifts, so 2 Hz flapping never strobes the room. The 1.2s
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
    ref.timer = window.setTimeout(apply, SETTLE_MS)
  }, [status])

  // Spoken state → the live regions, on exactly the ambient rule above: the
  // first reading is the baseline and is recorded *silently* (a cold start
  // narrating a link that has not finished dialing is noise, not information),
  // and after that a new sentence must hold for SETTLE_MS of consecutive ticks
  // before it is announced. So the announcer speaks transitions that lasted,
  // never ticks — at most one utterance per SETTLE_MS however hard the link flaps.
  const spokenRef = useRef<{applied: string | null; urgent: boolean; pending: string | null; timer: number}>(
    {applied: null, urgent: false, pending: null, timer: 0},
  )
  useEffect(() => {
    const next = spokenOf(status)
    const ref = spokenRef.current
    const apply = (speak: boolean) => {
      // One live alarm at a time: leaving an urgent state empties the assertive
      // region, so a resolved "the engine stopped" is not left sitting in the
      // accessibility tree behind a healthy console.
      if (ref.urgent && !next.urgent) announce('', true)
      if (speak) announce(next.text, next.urgent)
      ref.applied = next.text
      ref.urgent = next.urgent
      ref.pending = null
    }
    if (ref.applied === null) { apply(false); return }
    if (next.text === ref.applied) {
      if (ref.pending !== null) { window.clearTimeout(ref.timer); ref.pending = null }
      return
    }
    if (ref.pending === next.text) return // countdown to this sentence is already running
    window.clearTimeout(ref.timer)
    ref.pending = next.text
    ref.timer = window.setTimeout(() => apply(true), SETTLE_MS)
  }, [status])

  // Navigate inside a view transition: content morphs, chrome stays pinned.
  const go = (id: NavId) => {
    if (id === nav) return
    // A bounce still in flight would be captured into the pf-content snapshot.
    resetBands()
    const doc = document as Document & {startViewTransition?: (cb: () => void) => {finished: Promise<void>}}
    if (!prefersReduced() && doc.startViewTransition) {
      document.documentElement.classList.add('pf-vt-nav')
      const vt = doc.startViewTransition(() => flushSync(() => setNav(id)))
      vt.finished.finally(() => document.documentElement.classList.remove('pf-vt-nav'))
    } else {
      setNav(id)
    }
  }

  // A live role switch (RoleSwitcher) can hide the current screen — the agent
  // has no Agents rail. Fall back to Overview so the console never renders a
  // screen the role can't reach.
  useEffect(() => {
    const role = status?.role || ''
    if (nav !== 'settings' && !mainNav(role).some(n => n.id === nav)) setNav('overview')
  }, [status?.role, nav])

  // Ctrl+K opens the palette; Ctrl+1..n jump straight to a screen (the rail is
  // role-dependent, so the map is derived from the live role).
  const [palette, setPalette] = useState(false)
  useEffect(() => {
    const items = navFor(status?.role || '')
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

  // A clicked pxf:// pairing invite arrives from the OS via the backend: stashed for
  // a cold launch (drained once) or emitted on a warm one. Either way, open the wizard
  // straight onto the agent paste step with the code prefilled. The user still reviews
  // and confirms — a clicked link never auto-connects.
  const [deepLink, setDeepLink] = useState('')
  useEffect(() => {
    let done = false
    TakePendingDeepLink().then(url => { if (!done && url) { setDeepLink(url); setWizardHold(true) } }).catch(() => {})
    const off = EventsOn('pxf:deeplink', (url: string) => { setDeepLink(url); setWizardHold(true) })
    return () => { done = true; off() }
  }, [])

  const finishWizard = () => {
    setDeepLink('') // a later manual reopen starts at role selection, not a stale link
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

  // Nothing has arrived yet. A spinner is honest for the first few seconds and
  // a lie after that — it promises something is coming when the only thing
  // behind it may be a dead pipe. So the deadline turns it into a sentence with
  // a way out. CloseDialog rides along: BeforeClose (Go) swallows a close it
  // cannot honor silently and waits for that dialog, so without it the window
  // would be unclosable in exactly the state you most want to close it.
  if (!status) {
    return (
      <Shell titlebar={<TitleBar brand />}>
        {tick.overdue
          ? <EngineSilent reason={tick.error} onRetry={tick.retry} />
          : <div className="flex h-full items-center justify-center text-[var(--text-3)]"><Spinner size={22} /></div>}
        <CloseDialog />
      </Shell>
    )
  }

  if (backendWizard || wizardHold) {
    return (
      <Shell titlebar={<TitleBar brand />}>
        <Wizard status={status} onDone={finishWizard} deepLink={deepLink} />
        {/* Mounted here too: a re-pair overlay can cover a running engine, and
            a close during it must still get its fork. */}
        <CloseDialog />
      </Shell>
    )
  }

  const s = status
  return (
    <Shell
      // onPair reopens setup from the console: the sidebar's role switcher can
      // always become the gateway, but becoming the agent needs a pairing code
      // this machine may never have had — that route lands in the wizard's own
      // pairing flow rather than failing backend validation.
      sidebar={<Sidebar status={s} nav={nav} onNav={go} onPair={() => setWizardHold(true)} />}
      titlebar={<TitleBar status={s} nav={nav} onPalette={() => setPalette(true)} />}
    >
      <CrashProbe where="shell" />
      {/* The wide adaptive canvas: screens lay out against this container's
          real width (@container queries), not the viewport, so the sidebar
          is automatically accounted for. Interim max-w-5xl clamps keep the
          not-yet-redesigned screens at their designed width; each redesign
          deletes its own clamp. */}
      <div
        data-band-content
        className="@container mx-auto w-full max-w-[var(--content-max)] px-[var(--page-pad)] py-6"
        style={{viewTransitionName: 'pf-content'} as CSSProperties}
      >
        {/* The inner boundary: one screen dying must not take the chrome with
            it — Shell, the sidebar and the connection pill keep reporting the
            link while the canvas says what happened. Keyed by nav, so it is the
            existing screen-switch key that resets it: navigate away and the
            boundary is a new instance with no fault. */}
        <ErrorBoundary key={nav} fallback={f => <ScreenFault fault={f} />}>
          <div className={supportsVT ? '' : 'pf-page'}>
            <CrashProbe where="screen" />
            {nav === 'overview' && <Overview status={s} onNavigate={go} />}
            {nav === 'agents' && <Agents status={s} />}
            {nav === 'traffic' && <Traffic status={s} onNavigate={go} />}
            {nav === 'players' && <Players status={s} />}
            {nav === 'analytics' && <Analytics status={s} />}
            {nav === 'tunnels' && <Tunnels status={s} />}
            {nav === 'activity' && <Activity attached={s.mode === 'attached'} />}
            {nav === 'settings' && <Settings status={s} />}
          </div>
        </ErrorBoundary>
      </div>
      {palette && <CommandPalette ctx={{status: s, go}} onClose={() => setPalette(false)} />}
      <CloseDialog />
    </Shell>
  )
}
