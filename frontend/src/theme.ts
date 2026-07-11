import {useEffect, useState} from 'react'
import {SetTheme} from '../wailsjs/go/app/App'
import {WindowSetBackgroundColour} from '../wailsjs/runtime/runtime'

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

export function prefersReduced(): boolean {
  return window.matchMedia('(prefers-reduced-motion: reduce)').matches
}

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

/** Liquid swap: view transition when available, transition-everything fallback.
 * Either way the chrome sheet re-glazes — one glare sweep as the pane changes. */
function animateSwap(mutate: () => void) {
  const doc = document as Document & {startViewTransition?: (cb: () => void) => unknown}
  const el = document.documentElement
  if (!prefersReduced()) {
    el.classList.add('pf-reglaze')
    window.setTimeout(() => el.classList.remove('pf-reglaze'), 750)
  }
  if (!prefersReduced() && typeof doc.startViewTransition === 'function') {
    doc.startViewTransition(mutate)
    return
  }
  el.classList.add('pf-theme-anim')
  window.setTimeout(() => el.classList.remove('pf-theme-anim'), 420)
  mutate()
}

/** Apply the persisted theme synchronously (pre-paint) and follow OS changes. */
export function initTheme() {
  apply()
  media.addEventListener('change', () => {
    if (themePref() === 'system') animateSwap(apply)
  })
}

export function setThemePref(pref: ThemePref) {
  localStorage.setItem(KEY, pref)
  // Backend persists the preference; the swap applies even if that write fails.
  SetTheme(pref).catch(() => {})
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
