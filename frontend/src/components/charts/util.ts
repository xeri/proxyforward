// Shared chart primitives, extracted from BandwidthChart so every hand-rolled
// SVG chart (bandwidth, per-player traffic/latency, uptime, loss) draws from
// one vocabulary: the mono data face, the two axis scales, width measurement,
// tick placement, and the timestamp-keyed refresh tween.
import {useEffect, useRef, useState} from 'react'
import {prefersReduced} from '../../motion'

/** The engineering data face: every chart numeral is mono + tabular. */
export const MONO = "ui-monospace, 'Cascadia Mono', Consolas, monospace"

/** niceScale rounds the axis max up to 4 divisions of a clean binary step, so
 * labels land on values like 64 KiB/s · 128 KiB/s · 192 KiB/s · 256 KiB/s. */
export function niceScale(max: number): {max: number; ticks: number[]} {
  const divisions = 4
  const raw = max / divisions
  const step = Math.pow(2, Math.ceil(Math.log2(Math.max(1, raw))))
  const top = step * divisions
  return {max: top, ticks: [0, 1, 2, 3, 4].map(i => i * step)}
}

/** niceLinear rounds up to 4 divisions of a decimal 1/2/5 step — right for
 * gauges (connection counts, milliseconds) where powers of two read oddly. */
export function niceLinear(max: number, divisions = 4): {max: number; ticks: number[]} {
  const raw = Math.max(1, max) / divisions
  const mag = Math.pow(10, Math.floor(Math.log10(raw)))
  const norm = raw / mag
  const step = (norm <= 1 ? 1 : norm <= 2 ? 2 : norm <= 5 ? 5 : 10) * mag
  const top = step * divisions
  return {max: top, ticks: Array.from({length: divisions + 1}, (_, i) => i * step)}
}

/** useMeasuredWidth tracks an element's content width so a chart can size its
 * viewBox in CSS pixels. Measures synchronously on mount (observer delivery
 * pauses in hidden documents), then observes for resizes. Clamped and rounded
 * to keep the observer stable. */
export function useMeasuredWidth(fallback = 360) {
  const ref = useRef<HTMLDivElement>(null)
  const [w, setW] = useState(fallback)
  useEffect(() => {
    const el = ref.current
    if (!el) return
    const apply = (width: number) => { if (width > 0) setW(Math.max(160, Math.round(width))) }
    const cs = getComputedStyle(el)
    apply(el.clientWidth - parseFloat(cs.paddingLeft) - parseFloat(cs.paddingRight))
    const ro = new ResizeObserver(entries => {
      const rect = entries[entries.length - 1]?.contentRect
      if (rect) apply(rect.width)
    })
    ro.observe(el)
    return () => ro.disconnect()
  }, [])
  return [ref, w] as const
}

export function fmtTickTime(t: number, kind: 'second' | 'minute' | 'day'): string {
  const d = new Date(t)
  const p = (n: number) => String(n).padStart(2, '0')
  if (kind === 'day') {
    return d.toLocaleDateString(undefined, {month: 'short', day: 'numeric'})
  }
  if (kind === 'second') return `${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`
  return `${p(d.getHours())}:${p(d.getMinutes())}`
}

/** timeTicks picks ~6 nice wall-clock gridline times across [t0, t1]. */
export function timeTicks(t0: number, t1: number): {t: number; label: string}[] {
  const span = Math.max(1, t1 - t0)
  const steps = [
    1_000, 2_000, 5_000, 10_000, 15_000, 30_000,
    60_000, 120_000, 300_000, 600_000, 900_000, 1_800_000,
    3_600_000, 7_200_000, 10_800_000, 21_600_000, 43_200_000, 86_400_000, 172_800_000, 604_800_000,
  ]
  const step = steps.find(s => span / s <= 7) ?? steps[steps.length - 1]
  const out: {t: number; label: string}[] = []
  if (step >= 86_400_000) {
    const d = new Date(t0)
    d.setHours(0, 0, 0, 0)
    for (let t = d.getTime(); t <= t1; t += step) {
      if (t >= t0) out.push({t, label: fmtTickTime(t, 'day')})
    }
  } else {
    const kind = step < 60_000 ? 'second' : 'minute'
    for (let t = Math.ceil(t0 / step) * step; t <= t1; t += step) {
      out.push({t, label: fmtTickTime(t, kind)})
    }
  }
  return out
}

/** useTweenedValues eases per-timestamp values toward each fresh target over
 * ~220ms (ease-out cubic), aligning by timestamp so refreshes glide instead of
 * snapping. Timestamps with no prior value snap in; null (unknown) values
 * pass through untweened. The generic core behind every live chart. */
export function useTweenedValues(target: {t: number; vs: (number | null)[]}[]): {t: number; vs: (number | null)[]}[] {
  const dispRef = useRef<Map<number, (number | null)[]>>(new Map())
  const [, force] = useState(0)
  useEffect(() => {
    // Reduced motion: snap to the final values, no rAF loop (the NumberTicker
    // idiom — data changes are instant, never eased).
    if (prefersReduced()) {
      dispRef.current = new Map(target.map(row => [row.t, row.vs]))
      force(x => x + 1)
      return
    }
    const from = dispRef.current
    const start = performance.now()
    let raf = 0
    const lerp = (a: number | null, b: number | null, e: number) =>
      a === null || b === null ? b : a + (b - a) * e
    const tick = (now: number) => {
      const p = Math.min(1, (now - start) / 220)
      const e = 1 - Math.pow(1 - p, 3)
      const next = new Map<number, (number | null)[]>()
      for (const row of target) {
        const f = from.get(row.t)
        next.set(row.t, f ? row.vs.map((v, i) => lerp(f[i] ?? null, v, e)) : row.vs)
      }
      dispRef.current = next
      force(x => x + 1)
      if (p < 1) raf = requestAnimationFrame(tick)
    }
    raf = requestAnimationFrame(tick)
    return () => cancelAnimationFrame(raf)
  }, [target])
  return target.map(row => ({t: row.t, vs: dispRef.current.get(row.t) ?? row.vs}))
}
