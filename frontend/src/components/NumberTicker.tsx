import {useEffect, useRef, useState} from 'react'
import {prefersReduced} from '../motion'

/**
 * NumberTicker: interpolates numeric changes so stats glide instead of snap.
 * Pass the raw number and a formatter; reduced-motion users get instant values.
 */
export function NumberTicker({value, format = String, duration = 500, className = ''}: {
  value: number
  format?: (n: number) => string
  duration?: number
  className?: string
}) {
  const [display, setDisplay] = useState(value)
  const fromRef = useRef(value)
  const rafRef = useRef(0)

  useEffect(() => {
    if (prefersReduced() || !isFinite(value) || !isFinite(fromRef.current)) {
      fromRef.current = value
      setDisplay(value)
      return
    }
    const from = fromRef.current
    if (from === value) return
    const start = performance.now()
    cancelAnimationFrame(rafRef.current)
    const step = (now: number) => {
      const t = Math.min(1, (now - start) / duration)
      const eased = 1 - Math.pow(1 - t, 3) // ease-out cubic
      const v = from + (value - from) * eased
      setDisplay(t >= 1 ? value : v)
      if (t < 1) rafRef.current = requestAnimationFrame(step)
      else fromRef.current = value
    }
    rafRef.current = requestAnimationFrame(step)
    return () => cancelAnimationFrame(rafRef.current)
  }, [value, duration])

  return <span className={`tabular-nums ${className}`}>{format(display)}</span>
}
