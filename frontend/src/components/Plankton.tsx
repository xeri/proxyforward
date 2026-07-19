import {useEffect, useRef} from 'react'
import {useMotion} from '../motion'

/**
 * Plankton: the swarm layer of the atmosphere stack (base.css) — a fixed,
 * pointer-transparent canvas of bioluminescent motes drifting between the
 * glow fields and the content, so every trench-glass panel smears them like
 * light through water. The swarm orbits a slowly wandering nucleus and is
 * curious: it leans toward the pointer through ~0.6s of water lag.
 *
 * Discipline (the same rules as rubberband.ts):
 *   - Under reduced motion it mounts NOTHING — no canvas, no listeners; the
 *     kill switch has nothing to zero because nothing exists.
 *   - Under data-fx="low" the loop clears once and draws nothing (checked
 *     per frame so a Settings change takes effect live, no subscription).
 *   - The loop parks entirely while the document is hidden.
 *   - Colors re-read from the computed accent ramp every ~2s, so role and
 *     theme swaps re-tint the swarm without a React dependency.
 */
export function Plankton({count = 22}: {count?: number}) {
  const ref = useRef<HTMLCanvasElement>(null)
  const {reduced} = useMotion()

  useEffect(() => {
    if (reduced) return
    const canvas = ref.current
    if (!canvas) return
    const ctx = canvas.getContext('2d')
    if (!ctx) return

    const dpr = Math.min(2, window.devicePixelRatio || 1)
    let w = 0
    let h = 0
    const resize = () => {
      w = window.innerWidth
      h = window.innerHeight
      canvas.width = Math.round(w * dpr)
      canvas.height = Math.round(h * dpr)
      ctx.setTransform(dpr, 0, 0, dpr, 0, 0)
    }
    resize()
    window.addEventListener('resize', resize)

    // Each mote is its own tiny organism: unique orbit, speed, and phase so
    // no two move alike. Mulberry-ish hash keeps the swarm stable per mount.
    const rand = (i: number, salt: number) => {
      let t = (i + 1) * 0x6d2b79f5 + salt * 0x9e3779b9
      t = Math.imul(t ^ (t >>> 15), t | 1)
      t ^= t + Math.imul(t ^ (t >>> 7), t | 61)
      return ((t ^ (t >>> 14)) >>> 0) / 4294967296
    }
    const motes = Array.from({length: count}, (_, i) => ({
      orbit: 60 + rand(i, 1) * 320,          // px from the nucleus
      angle: rand(i, 2) * Math.PI * 2,
      speed: (0.02 + rand(i, 3) * 0.05) * (rand(i, 4) > 0.5 ? 1 : -1), // rad/s
      size: 0.8 + rand(i, 5) * 1.2,
      bob: rand(i, 6) * Math.PI * 2,         // vertical breathing phase
      blue: rand(i, 7) > 0.68,               // most glow jade, some ride the current
      tw: 0.4 + rand(i, 8) * 0.5,            // twinkle depth
    }))

    // The pointer, followed through water: the tracked point lerps toward the
    // real cursor a little each frame (~0.6s to close the gap at 60fps).
    let px = w / 2
    let py = h / 2
    let cx = px
    let cy = py
    const onMove = (e: PointerEvent) => {
      px = e.clientX
      py = e.clientY
    }
    window.addEventListener('pointermove', onMove, {passive: true})

    // Computed accent ramp, refreshed occasionally — cheap enough to poll,
    // and it keeps the swarm honest across role/theme swaps mid-session.
    let colA = ''
    let colB = ''
    let colFrames = 0
    const refreshColors = () => {
      const cs = getComputedStyle(document.documentElement)
      colA = cs.getPropertyValue('--accent').trim() || '#2fffc0'
      colB = cs.getPropertyValue('--contrast-glow').trim() || '#1c8bff'
    }
    refreshColors()

    let raf = 0
    let last = performance.now()
    let t = 0
    const frame = (now: number) => {
      raf = requestAnimationFrame(frame)
      const dt = Math.min(0.1, (now - last) / 1000)
      last = now
      if (document.documentElement.dataset.fx === 'low') {
        ctx.clearRect(0, 0, w, h)
        return
      }
      t += dt
      if (++colFrames >= 120) {
        colFrames = 0
        refreshColors()
      }
      // Water lag toward the cursor.
      cx += (px - cx) * Math.min(1, dt * 2.8)
      cy += (py - cy) * Math.min(1, dt * 2.8)
      // The nucleus wanders the middle of the column.
      const nx = w * (0.5 + 0.26 * Math.sin(t * 0.055) + 0.08 * Math.sin(t * 0.13))
      const ny = h * (0.48 + 0.22 * Math.cos(t * 0.043) + 0.07 * Math.sin(t * 0.1))

      ctx.clearRect(0, 0, w, h)
      for (const m of motes) {
        m.angle += m.speed * dt
        let x = nx + Math.cos(m.angle) * m.orbit
        let y = ny + Math.sin(m.angle) * m.orbit * 0.72 + Math.sin(t * 0.9 + m.bob) * 6
        // Curiosity: lean toward the (lagged) cursor, harder when close.
        const dx = cx - x
        const dy = cy - y
        const lean = 0.22 * Math.exp(-Math.hypot(dx, dy) / 420)
        x += dx * lean
        y += dy * lean
        const glow = 0.55 + m.tw * Math.sin(t * (0.6 + m.tw) + m.bob)
        const col = m.blue ? colB : colA
        ctx.fillStyle = col
        // Halo first, then the core — two fills per mote, no shadowBlur.
        ctx.globalAlpha = 0.05 * glow
        ctx.beginPath()
        ctx.arc(x, y, m.size * 3.4, 0, Math.PI * 2)
        ctx.fill()
        ctx.globalAlpha = 0.5 * glow
        ctx.beginPath()
        ctx.arc(x, y, m.size, 0, Math.PI * 2)
        ctx.fill()
      }
      ctx.globalAlpha = 1
    }
    const start = () => {
      if (!raf) {
        last = performance.now()
        raf = requestAnimationFrame(frame)
      }
    }
    const stop = () => {
      if (raf) {
        cancelAnimationFrame(raf)
        raf = 0
      }
    }
    const onVis = () => (document.hidden ? stop() : start())
    document.addEventListener('visibilitychange', onVis)
    start()

    return () => {
      stop()
      document.removeEventListener('visibilitychange', onVis)
      window.removeEventListener('pointermove', onMove)
      window.removeEventListener('resize', resize)
    }
  }, [reduced, count])

  if (reduced) return null
  return <canvas ref={ref} aria-hidden className="pointer-events-none fixed inset-0" />
}
