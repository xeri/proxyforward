import {useEffect, useState} from 'react'

/**
 * Animation preference. `data-motion` on <html> overrides the OS signal
 * (Windows "Animation effects" → prefers-reduced-motion):
 *   "on"     — full motion regardless of the OS (the default)
 *   "off"    — no motion regardless of the OS
 *   "system" — follow prefers-reduced-motion (attribute left unset)
 * Persisted to localStorage only — a per-machine rendering choice, like fx.
 * motion.css mirrors the JS gate below via its data-motion selector blocks.
 */
export type MotionPref = 'on' | 'off' | 'system'

const KEY = 'pf-motion'
const reduceMedia = window.matchMedia('(prefers-reduced-motion: reduce)')
const listeners = new Set<() => void>()

export function motionPref(): MotionPref {
  const v = localStorage.getItem(KEY)
  return v === 'off' || v === 'system' ? v : 'on'
}

/** JS gate: true when scripted animation code should not run. */
export function prefersReduced(): boolean {
  const p = motionPref()
  if (p === 'system') return reduceMedia.matches
  return p === 'off'
}

function stamp(v: MotionPref) {
  if (v === 'system') delete document.documentElement.dataset.motion
  else document.documentElement.dataset.motion = v
}

export function setMotionPref(v: MotionPref) {
  if (v === 'on') localStorage.removeItem(KEY)
  else localStorage.setItem(KEY, v)
  stamp(v)
  listeners.forEach(fn => fn())
}

/** Apply the persisted preference synchronously (pre-paint), like initFx. */
export function initMotion() {
  stamp(motionPref())
  // System-mode consumers follow live OS flips (Settings > Accessibility).
  reduceMedia.addEventListener('change', () => listeners.forEach(fn => fn()))
}

export function useMotion(): {pref: MotionPref; reduced: boolean; setPref: (v: MotionPref) => void} {
  const [, force] = useState(0)
  useEffect(() => {
    const fn = () => force(x => x + 1)
    listeners.add(fn)
    return () => void listeners.delete(fn)
  }, [])
  return {pref: motionPref(), reduced: prefersReduced(), setPref: setMotionPref}
}
