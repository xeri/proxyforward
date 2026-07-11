/**
 * Effects-level preference. `data-fx` on <html> tiers the glass system:
 *   (unset) — full glass: every card blurs its backdrop
 *   "low"   — solid cards, no caustics/chart glow (weak GPUs)
 *   "high"  — adds real refraction on the palette (devmock &fx=high)
 * Persisted to localStorage only — it is a per-machine rendering choice, not
 * config the backend needs to own.
 */
const KEY = 'pf-fx'

export function fxPref(): string {
  return localStorage.getItem(KEY) || ''
}

export function setFxPref(v: string) {
  if (v) localStorage.setItem(KEY, v)
  else localStorage.removeItem(KEY)
  if (v) document.documentElement.dataset.fx = v
  else delete document.documentElement.dataset.fx
}

/** Apply the persisted level synchronously (pre-paint), like initTheme. */
export function initFx() {
  const v = fxPref()
  if (v) document.documentElement.dataset.fx = v
}
