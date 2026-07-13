import {useEffect, useState} from 'react'
import {SetTheme} from '../wailsjs/go/app/App'
import {WindowSetBackgroundColour} from '../wailsjs/runtime/runtime'
import {prefersReduced} from './motion'

/**
 * Tri-state theme engine: Dark, Light, or System (follows the OS live).
 * The preference persists to the backend config via SetTheme and mirrors to
 * localStorage so the first paint resolves synchronously without a flash.
 */
export type ThemePref = 'dark' | 'light' | 'system'
export type ResolvedTheme = 'dark' | 'light'

const KEY = 'pf-theme'
const media = window.matchMedia('(prefers-color-scheme: dark)')
const listeners = new Set<() => void>()

export function themePref(): ThemePref {
  const v = localStorage.getItem(KEY)
  return v === 'light' || v === 'system' ? v : 'dark'
}

export function resolvedTheme(): ResolvedTheme {
  const pref = themePref()
  if (pref === 'system') return media.matches ? 'dark' : 'light'
  return pref
}

function apply() {
  const t = resolvedTheme()
  document.documentElement.setAttribute('data-theme', t)
  // Keep the native window's pre-paint colour in sync so resize flashes and
  // restore frames match the theme. No-op outside the WebView2 host.
  try {
    if (t === 'light') WindowSetBackgroundColour(236, 238, 246, 1)
    else WindowSetBackgroundColour(11, 13, 19, 1)
  } catch {
    /* plain browser (vite dev) */
  }
  listeners.forEach(fn => fn())
}

type VTDocument = Document & {startViewTransition?: (cb: () => void) => {finished: Promise<void>}}

const THEME_SWEEP_MS = 1050 // must equal --dur-theme (tokens.css)

let swapToken = 0

/** Liquid swap: the theme lands instantly beneath a view transition, then the
 * new pane sweeps in from the light corner (motion.css pf-relight) while the
 * chrome re-glazes. pf-theme-snap freezes every CSS transition for the swap
 * so both panes are static textures and the sweep is pure compositor work —
 * without it the registered token transitions (--accent, --mode-glow) restyle
 * whole subtrees per frame and every backdrop blur above the repainting
 * ambient re-rasterizes, which is what made the old swap chug. Fallback for
 * no-VT engines: the brief transition-everything cross-fade. */
function animateSwap(mutate: () => void) {
  const doc = document as VTDocument
  const el = document.documentElement
  if (prefersReduced()) {
    mutate()
    return
  }
  el.classList.add('pf-reglaze')
  window.setTimeout(() => el.classList.remove('pf-reglaze'), THEME_SWEEP_MS + 100)
  if (typeof doc.startViewTransition === 'function') {
    // Token guards rapid double-toggles: the first swap's cleanup must not
    // strip the freeze out from under a second, still-running sweep.
    const token = ++swapToken
    el.classList.add('pf-theme-snap', 'pf-relight')
    const done = () => {
      if (token === swapToken) el.classList.remove('pf-theme-snap', 'pf-relight')
    }
    doc.startViewTransition(mutate).finished.then(done, done)
    return
  }
  el.classList.add('pf-theme-anim')
  window.setTimeout(() => el.classList.remove('pf-theme-anim'), 700)
  mutate()
}

/** Apply the persisted theme synchronously (pre-paint) and follow OS changes. */
export function initTheme() {
  apply()
  // The relight glare: a specular band that crosses the glass in step with
  // the theme sweep (motion.css shows it only under html.pf-relight).
  const glare = document.createElement('div')
  glare.className = 'pf-relight-glare'
  glare.setAttribute('aria-hidden', 'true')
  document.body.appendChild(glare)
  media.addEventListener('change', () => {
    if (themePref() === 'system') animateSwap(apply)
  })
}

export function setThemePref(pref: ThemePref) {
  const before = resolvedTheme()
  localStorage.setItem(KEY, pref)
  // Backend persists the preference; the swap applies even if that write fails.
  SetTheme(pref).catch(() => {})
  // Same resolved appearance (dark ↔ system on a dark OS, light ↔ system on a
  // light one): land the preference silently — a relight sweep across
  // identical pixels reads as a glitch, not a transition.
  if (resolvedTheme() === before) {
    apply()
    return
  }
  animateSwap(apply)
}

/** Quick toggle (title bar): flips appearance to an explicit preference. */
export function toggleTheme() {
  setThemePref(resolvedTheme() === 'dark' ? 'light' : 'dark')
}

export function useTheme(): {pref: ThemePref; resolved: ResolvedTheme; setPref: (p: ThemePref) => void} {
  const [, force] = useState(0)
  useEffect(() => {
    const fn = () => force(x => x + 1)
    listeners.add(fn)
    return () => void listeners.delete(fn)
  }, [])
  return {pref: themePref(), resolved: resolvedTheme(), setPref: setThemePref}
}
