import {useEffect} from 'react'
import {useMotion} from './motion'

/**
 * Rubber-band overscroll. Push a scroller past its end and the content keeps
 * giving — but exponentially less the harder you push; let go and it springs
 * back through one overshoot. Interaction feedback, in the same family as
 * .pf-press and .pf-lift (DESIGN.md rule 3): it only ever answers input, and it
 * never plays on its own.
 *
 * One delegated listener set on `document`, mounted once from Shell — the
 * pointer-wake idiom (Shell.tsx). Under reduced motion nothing is attached at
 * all and every scroller reverts to a hard clamp: motion.css's kill switch only
 * zeroes animation/transition durations, so it could not stop a rAF loop
 * writing an inline transform.
 *
 * Native scrolling is never intercepted. We take over only for the delta that
 * *no* scroller in the chain can consume — which is exactly where the hard stop
 * used to be.
 */

/* Physics. JS-only px math that nothing in CSS reads, so these are plain
   documented consts, not tokens — the UNDERLAP precedent (Shell.tsx). */
const FALLOFF_C = 0.55       // UIScrollView's constant: the shape of the give
const MAX_PULL_RATIO = 0.35  // asymptote as a fraction of the scroller's height…
const MAX_PULL_PX = 160      // …clamped, so a tall page can't pull half a screen
const MIN_PULL_PX = 32       // …and a short well still has somewhere to go
const HOLD_OMEGA = 34        // rad/s, ζ=1 while a notched wheel drives it (see denseSrc)
const TRACK_OMEGA = 80       // …and while a trackpad does
const TOUCH_OMEGA = 120      // a finger gets near-direct tracking
const RELEASE_OMEGA = 14     // rad/s on release (a notched mouse / finger: one graceful overshoot)
const RELEASE_OMEGA_FLING = 26 // …and after a trackpad flick, which wants a snappier snap-back
const RELEASE_ZETA = 0.55    // < 1 → exactly one visible overshoot. This is the bounce.
const IDLE_MS = 90           // wheel has no "release" event; this is the gesture end
const KEY_IMPULSE = 900      // px/s kick when a key hits a wall
const REST_PX = 0.5          // settled: offset below this (sub-pixel — the tail of an
const REST_VEL = 8           // …and velocity below this   underdamped spring is long,
                             //   and it must not hold will-change for a second after
                             //   it is visually done). The zero-crossing of the
                             //   overshoot is far too fast to trip REST_VEL.
const LINE_PX = 16           // deltaMode 1 (lines) → px
const LEAK_TAU = 0.1         // s — the momentum leak; see pull() and tick()
const DEFLATE_REL = 0.9      // a leaking (trackpad) band bounces once it recedes this far below its
                             //   peak — the flick is spent; don't ride the OS momentum tail down (tick)
const FLING_MIN_PX = 4       // …but only if the pull was real; a sub-4px give just releases on idle
const NOTCH_PX = 30          // a wheel step is never smaller than this…
const NOTCH_MS = 25          // …nor closer together than this

type Band = {
  scroller: HTMLElement
  content: HTMLElement
  glow: HTMLElement | null // the edge-light overlay, stamped directly (see paint)
  offset: number    // px, signed: + = content pushed down (pulling past the top)
  vel: number       // px/s
  target: number    // where the spring is heading; 0 once released
  raw: number       // unconsumed input accumulated over this gesture
  edge: number      // +1 pushing down (bottom edge), -1 pushing up (top edge)
  max: number       // the falloff asymptote for this scroller
  held: boolean     // input is still arriving
  omega: number
  relOmega: number  // release-spring ω: snappier for a trackpad flick than a mouse
  leak: boolean     // `raw` decays while held — a trackpad fling; see tick()
  peakOut: number   // max outward pull this gesture, for the deflate-release trigger
  inRate: number    // EMA of |input| — the live input strength
  peakRate: number  // its running max — the flick's strength; gates the inertia guard
  autoRelease: boolean // wheel/key release on idle; touch waits for touchend
  lastInput: number
  last: number      // previous frame timestamp
}

const bands = new Map<HTMLElement, Band>()
let raf = 0
let wheelBand: Band | null = null
let touchBand: Band | null = null
let touchId = -1
let touchY = 0
let touchFrom: Element | null = null
let pageEl: HTMLElement | null = null

/** The app's one page scroller (Shell's <main>). */
function page(): HTMLElement | null {
  if (!pageEl?.isConnected) pageEl = document.querySelector('main')
  return pageEl
}

/* ---------- geometry ---------- */

function scrolls(cs: CSSStyleDeclaration): boolean {
  const o = cs.overflowY
  return o === 'auto' || o === 'scroll' || o === 'overlay'
}

/** Room left in the delta's direction. +1 = down. */
function room(el: HTMLElement, dir: number): number {
  return dir > 0 ? el.scrollHeight - el.clientHeight - el.scrollTop : el.scrollTop
}

/**
 * Native scroll-chaining semantics, with a bounce where the hard stop used to
 * be: the first container with room keeps the gesture (we do nothing), an
 * overscroll-behavior: contain container absorbs it (it bounces), and if nothing
 * absorbs, the page hits the wall. Returns null when native scrolling handles it.
 */
function ownerFor(from: Element | null, dir: number): HTMLElement | null {
  const pg = page()
  for (let el: Element | null = from; el && el !== document.body; el = el.parentElement) {
    if (el === pg) break
    if (!(el instanceof HTMLElement)) continue // an SVG icon is a common wheel target
    const cs = getComputedStyle(el)
    if (scrolls(cs) && el.scrollHeight - el.clientHeight > 1) {
      if (room(el, dir) > 1) return null // native scroll consumes it, untouched
      const ob = cs.overscrollBehaviorY
      if (ob === 'contain' || ob === 'none') return el // it absorbs → it bounces
      // otherwise it chains outward, like the platform does
    }
    // A fixed overlay is its own world: a wheel on a modal scrim must never
    // reach the page behind it. Checked after the scroll test — the Select
    // listbox is both fixed and scrollable.
    if (cs.position === 'fixed') return null
  }
  // Nothing absorbed it. The page bounces — even when it has no overflow at all
  // (Activity, the wizard): a short page still answers a push.
  return pg && room(pg, dir) <= 1 ? pg : null
}

/** The element we translate: never the scroller itself — a well has a border and
 *  a recessed background, and sliding the box would open a gap under it. */
function contentOf(scroller: HTMLElement): HTMLElement | null {
  return scroller.querySelector<HTMLElement>(':scope > [data-band-content]')
    ?? (scroller.firstElementChild as HTMLElement | null)
}

/** The edge-light overlay for this scroller, if any: a sibling (marked
 *  data-band-glow by the consumer — Shell, Activity) that neither scrolls nor
 *  rides the band transform. We stamp --band-t / data-band straight onto it,
 *  never onto the scroller: the scroller's subtree is the whole screen and
 *  --band-t is an inherited custom property, so stamping it there would restyle
 *  every descendant on every frame. The glow's subtree is two pseudo-elements. */
function glowOf(scroller: HTMLElement): HTMLElement | null {
  return scroller.parentElement?.querySelector<HTMLElement>(':scope > [data-band-glow]') ?? null
}

/* ---------- the give ---------- */

/** Apple's rational falloff: asymptotic, so it gives exponentially less the
 *  harder you push and never passes `max`. */
function falloff(raw: number, max: number): number {
  const r = Math.abs(raw)
  return (r * max * FALLOFF_C) / (max + FALLOFF_C * r)
}

/** Its inverse — used to seed `raw` when a gesture catches a bounce mid-flight,
 *  so grabbing it doesn't snap. */
function unfalloff(pull: number, max: number): number {
  const p = Math.min(pull, max * 0.98)
  return (p * max) / (FALLOFF_C * (max - p))
}

/* ---------- band lifecycle ---------- */

function bandFor(scroller: HTMLElement): Band | null {
  const existing = bands.get(scroller)
  if (existing) return existing
  const content = contentOf(scroller)
  if (!content) return null
  const now = performance.now()
  const b: Band = {
    scroller, content, glow: glowOf(scroller),
    offset: 0, vel: 0, target: 0, raw: 0, edge: 1,
    max: Math.max(MIN_PULL_PX, Math.min(MAX_PULL_RATIO * scroller.clientHeight, MAX_PULL_PX)),
    held: true, omega: HOLD_OMEGA, relOmega: RELEASE_OMEGA, leak: false,
    peakOut: 0, inRate: 0, peakRate: 0, autoRelease: true, lastInput: now, last: now,
  }
  // Only while live: a permanent will-change would make the content a containing
  // block for position:fixed descendants even at rest.
  content.style.willChange = 'transform'
  bands.set(scroller, b)
  return b
}

/** Start a gesture on a band, carrying any bounce already in flight. */
function claim(b: Band, edge: number, autoRelease: boolean) {
  b.edge = edge
  b.autoRelease = autoRelease
  b.held = true
  b.leak = false // the wheel re-arms it per gesture; a finger never leaks
  b.peakOut = 0
  b.inRate = 0
  b.peakRate = 0
  // Seed from the current offset when it's on the side we're pushing, so
  // catching a bounce continues it instead of yanking it back to zero.
  const pull = -b.offset * edge
  b.raw = pull > 0 ? edge * unfalloff(pull, b.max) : 0
}

function pull(b: Band, dy: number): boolean {
  const raw = b.raw + dy
  if (raw * b.edge <= 0) {
    // Pulled all the way back through the edge: hand control straight back to
    // native scrolling, this frame.
    b.raw = 0
    b.target = 0
    b.held = false
    b.autoRelease = true
    pump()
    return false
  }
  b.raw = raw
  b.held = true
  b.inRate += 0.3 * (Math.abs(dy) - b.inRate) // EMA of input strength
  if (b.inRate > b.peakRate) b.peakRate = b.inRate // the flick's peak — the tail decays below it
  b.lastInput = performance.now()
  b.target = -b.edge * falloff(raw, b.max)
  pump()
  return true
}

function paint(b: Band) {
  const px = b.offset
  b.content.style.transform = `translate3d(0, ${px.toFixed(2)}px, 0)`
  // The edge light. Stamped straight onto the glow overlay — never onto the
  // scroller, whose subtree is the whole screen: --band-t is an inherited custom
  // property, so writing it on the scroller restyles every descendant on every
  // frame (a full-page style recalc per frame). The glow's subtree is two pseudos.
  // The light belongs to the edge being *pressed*, and only while it is pressed:
  // keyed off b.edge, not the sign of the offset, so the release overshoot —
  // which carries the content through zero and out the other side — never flashes
  // the bloom on the opposite edge. Its outward travel is all that lights it.
  if (b.glow) {
    const out = Math.max(0, -px * b.edge)
    b.glow.style.setProperty('--band-t', Math.min(1, out / b.max).toFixed(3))
    b.glow.dataset.band = b.edge > 0 ? 'bottom' : 'top'
  }
  // The Select menu is portaled to <body>, so it does not ride the transform;
  // it re-anchors off a capture-phase document scroll listener (ui.tsx). No
  // native scroll event fires during a band, so it would otherwise detach.
  b.scroller.dispatchEvent(new Event('scroll'))
}

function rest(b: Band) {
  b.content.style.transform = ''
  b.content.style.willChange = ''
  if (b.glow) {
    b.glow.style.removeProperty('--band-t')
    delete b.glow.dataset.band
  }
  bands.delete(b.scroller)
  if (wheelBand === b) wheelBand = null
  if (touchBand === b) touchBand = null
  if (b.scroller.isConnected) b.scroller.dispatchEvent(new Event('scroll'))
}

/* ---------- the spring ---------- */

/**
 * Exact propagator for the damped oscillator, ζ ≤ 1. `target` is constant across a
 * frame, so over dt the system is linear and time-invariant and has a closed form —
 * which is unconditionally stable at any dt and any ω.
 *
 * This is why there is no fixed sub-step and no stability bound to respect when
 * tuning a constant. The first-order integrator this replaced needed both: it was
 * stable only while ω·h < 2(√(1+ζ²) − ζ) — 0.83 at ζ=1, NOT the 2ζω·h < 2 that the
 * determinant alone suggests — so raising TOUCH_OMEGA far enough would have made it
 * diverge slowly while still "passing" the looser test. It also bled ~5% of the
 * overshoot's amplitude to numerical damping. Both failure modes are now unreachable,
 * and the feel is bit-identical at 60 / 120 / 165 Hz.
 */
function propagate(b: Band, w: number, zeta: number, dt: number) {
  const x = b.offset - b.target // displacement from where the spring is heading
  const v = b.vel
  const decay = Math.exp(-zeta * w * dt)
  if (zeta < 1) {
    const wd = w * Math.sqrt(1 - zeta * zeta) // the damped frequency
    const c = Math.cos(wd * dt)
    const s = Math.sin(wd * dt)
    b.offset = b.target + decay * (x * c + ((v + zeta * w * x) / wd) * s)
    b.vel = decay * (v * c - ((w * w * x + zeta * w * v) / wd) * s)
  } else { // critically damped: the ωd → 0 limit of the above
    b.offset = b.target + decay * (x + (v + w * x) * dt)
    b.vel = decay * (v - w * dt * (v + w * x))
  }
}

function tick(now: number) {
  raf = 0
  for (const b of [...bands.values()]) {
    if (!b.content.isConnected) { bands.delete(b.scroller); continue }
    const dt = Math.max(0, (now - b.last) / 1000) || 0 // exact at any dt; no clamp needed
    b.last = now
    // Wheel and keys have no release event; idle input is the end of the gesture.
    if (b.held && b.autoRelease && now - b.lastInput > IDLE_MS) { b.held = false; b.target = 0 }

    // The momentum leak. A trackpad fling keeps emitting wheel events for up to a
    // second and a half after the fingers are gone, so `raw` — an integral — would
    // saturate the falloff and *pin* the band at `max` for the whole tail: a spring
    // held at full stretch by nothing. The user's model says it must recoil. So while
    // a fling drives the band, raw decays: raw' = input − raw/τ, whose steady state
    // is rate·τ. The pull now tracks the tail's *velocity* and melts with it, which is
    // the invariant iOS actually keeps (excursion ∝ velocity at the boundary).
    // Only for a fling — a mouse notch has no tail to leak, and a finger holding a
    // stretch (touch: no events while it is still) must keep it.
    if (b.held && b.leak) {
      b.raw *= Math.exp(-dt / LEAK_TAU)
      b.target = -b.edge * falloff(b.raw, b.max)
      // Hand off to the bounce the moment the flick is spent, instead of riding the
      // OS momentum tail down. That tail keeps arriving for up to ~1.5 s after the
      // fingers lift; held (ζ=1), the band would deflate to zero with no bounce and
      // sit pinned at the edge the whole time. So once the pull recedes past its peak
      // — input can no longer sustain it, i.e. the fingers are gone — release into the
      // underdamped spring from near the peak, and let onWheel swallow the leftover
      // tail (inertiaGuard) so it can't yank the band back out.
      const out = -b.offset * b.edge
      if (out > b.peakOut) b.peakOut = out
      else if (b.peakOut > FLING_MIN_PX && out < DEFLATE_REL * b.peakOut) {
        b.held = false
        b.target = 0
        inertiaEdge = b.edge
        inertiaRate = b.peakRate
        inertiaGuardUntil = now + IDLE_MS
      }
    }

    const w = b.held ? b.omega : b.relOmega
    const zeta = b.held ? 1 : RELEASE_ZETA // critical while held; underdamped on release
    propagate(b, w, zeta, dt)

    if (!b.held && Math.abs(b.offset) < REST_PX && Math.abs(b.vel) < REST_VEL) { rest(b); continue }
    paint(b)
  }
  if (bands.size) raf = requestAnimationFrame(tick)
}

function pump() {
  if (!raf && bands.size) raf = requestAnimationFrame(tick)
}

/** Snap every band to rest. Called on nav so a mid-bounce transform is never
 *  baked into the pf-content View Transition snapshot. */
export function resetBands() {
  for (const b of [...bands.values()]) rest(b)
  wheelBand = null
  touchBand = null
  touchId = -1
  if (raf) { cancelAnimationFrame(raf); raf = 0 }
}

/* ---------- input ---------- */

function deltaOf(e: WheelEvent): number {
  if (e.deltaMode === 1) return e.deltaY * LINE_PX
  if (e.deltaMode === 2) return e.deltaY * (page()?.clientHeight ?? 800)
  return e.deltaY
}

/**
 * Two instruments, one API. A notched mouse emits sparse whole-pixel steps: that is a
 * *rate* signal, and HOLD_OMEGA's 59 ms of glide is exactly what turns each 100px step
 * into motion. A trackpad emits dense fractional deltas: that is a *position* signal,
 * and running 59 ms behind a position signal is the definition of spongy. Same
 * constant, opposite verdicts — so tell them apart instead of compromising, on the
 * three ways they differ (step size, whole pixels, cadence). A free-spinning high-res
 * wheel classifies as a trackpad, and the tighter tracking is right for it too.
 */
let wheelAt = 0
let denseSrc = false
// A spent fling (tick released it early) leaves its inertial tail still arriving.
// Swallow that tail here so it can't re-grab the now-bouncing band; see onWheel.
let inertiaGuardUntil = 0
let inertiaEdge = 0
let inertiaRate = 0

function onWheel(e: WheelEvent) {
  if (e.ctrlKey) return // pinch-zoom / browser zoom
  const dy = deltaOf(e)
  if (!dy || Math.abs(e.deltaX) > Math.abs(dy)) return // horizontal wheel isn't ours

  const now = performance.now()
  const gap = now - wheelAt
  wheelAt = now
  denseSrc = !(Math.abs(dy) >= NOTCH_PX && Number.isInteger(dy) && gap >= NOTCH_MS)

  // Swallow a spent fling's momentum tail — dense, same-edge, and decayed below the
  // flick's own strength — so each leftover inertial event doesn't yank the bouncing
  // band back to the wall. A genuine new push (dy back up near the flick's rate) falls
  // through and re-bands, so an early release that fired a touch too soon self-heals.
  if (now < inertiaGuardUntil && denseSrc && Math.sign(dy) === inertiaEdge && Math.abs(dy) < 0.8 * inertiaRate) {
    inertiaGuardUntil = now + IDLE_MS
    e.preventDefault()
    return
  }

  // Latch: once a band owns the burst it keeps it, so a fling that runs into the
  // wall mid-gesture keeps pushing the same edge.
  let b = wheelBand
  if (b && (!bands.has(b.scroller) || now - b.lastInput > IDLE_MS)) b = wheelBand = null
  if (!b) {
    const scroller = ownerFor(e.target as Element, Math.sign(dy))
    if (!scroller) return // native scroll handles it — untouched
    b = bandFor(scroller)
    if (!b) return
    claim(b, Math.sign(dy), true)
    // Latched for the gesture, not re-read per event: a device reclassified mid-fling
    // would change the physics under the user's hand.
    b.omega = denseSrc ? TRACK_OMEGA : HOLD_OMEGA
    b.relOmega = denseSrc ? RELEASE_OMEGA_FLING : RELEASE_OMEGA
    b.leak = denseSrc
    wheelBand = b
  }
  if (pull(b, dy)) e.preventDefault()
  else wheelBand = null
}

function onTouchStart(e: TouchEvent) {
  if (e.touches.length !== 1) { onTouchEnd(); return } // pinch: not ours
  touchId = e.touches[0].identifier
  touchY = e.touches[0].clientY
  touchFrom = e.target as Element
  touchBand = null
}

function onTouchMove(e: TouchEvent) {
  if (touchId < 0 || e.touches.length !== 1) return
  const t = e.touches[0]
  if (t.identifier !== touchId) return
  const dy = touchY - t.clientY // finger up = scrolling down, like deltaY
  touchY = t.clientY
  if (!dy) return

  let b = touchBand
  if (!b) {
    const scroller = ownerFor(touchFrom, Math.sign(dy))
    if (!scroller) return // native scroll until the boundary
    b = bandFor(scroller)
    if (!b) return
    claim(b, Math.sign(dy), false)
    touchBand = b
  }
  b.omega = TOUCH_OMEGA
  if (pull(b, dy)) e.preventDefault()
  else touchBand = null
}

/** Let go: the spring keeps whatever velocity the finger left it with. */
function onTouchEnd() {
  if (touchBand) {
    touchBand.held = false
    touchBand.target = 0
    touchBand.autoRelease = true
    pump()
  }
  touchBand = null
  touchId = -1
  touchFrom = null
}

const KEY_DIR: Record<string, number> = {
  PageDown: 1, PageUp: -1, End: 1, Home: -1, ArrowDown: 1, ArrowUp: -1, ' ': 1,
}

/** A key that hits a wall gets an impulse straight into the release spring — no
 *  preventDefault, because native scrolling has nothing left to do there. */
function onKey(e: KeyboardEvent) {
  if (e.ctrlKey || e.altKey || e.metaKey) return
  const dir = KEY_DIR[e.key] ?? 0
  if (!dir) return
  const el = e.target as HTMLElement | null
  // Anything that consumes the key itself (Space on a button, arrows in a menu).
  if (el?.isContentEditable || el?.closest('input, textarea, select, button, a, [role="listbox"]')) return

  const scroller = ownerFor(el ?? document.activeElement, dir)
  if (!scroller) return
  const b = bandFor(scroller)
  if (!b) return
  b.edge = dir
  b.held = false
  b.autoRelease = true
  b.target = 0
  b.raw = 0
  b.vel = -dir * KEY_IMPULSE
  b.lastInput = performance.now()
  pump()
}

/** Mount once, from Shell. Re-arms live when the Animations preference flips. */
export function useRubberBand() {
  const {reduced} = useMotion()
  useEffect(() => {
    if (reduced) { resetBands(); return }
    document.addEventListener('wheel', onWheel, {passive: false})
    document.addEventListener('touchstart', onTouchStart, {passive: true})
    document.addEventListener('touchmove', onTouchMove, {passive: false})
    document.addEventListener('touchend', onTouchEnd, {passive: true})
    document.addEventListener('touchcancel', onTouchEnd, {passive: true})
    document.addEventListener('keydown', onKey)
    return () => {
      document.removeEventListener('wheel', onWheel)
      document.removeEventListener('touchstart', onTouchStart)
      document.removeEventListener('touchmove', onTouchMove)
      document.removeEventListener('touchend', onTouchEnd)
      document.removeEventListener('touchcancel', onTouchEnd)
      document.removeEventListener('keydown', onKey)
      resetBands()
    }
  }, [reduced])
}
