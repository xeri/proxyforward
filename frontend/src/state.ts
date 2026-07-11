import {useEffect, useRef, useState} from 'react'
import {EventsOn} from '../wailsjs/runtime/runtime'
import {Status} from '../wailsjs/go/app/App'
import {app} from '../wailsjs/go/models'

export type UIStatus = app.UIStatus

/** useTick subscribes to the Go side's 2 Hz status snapshots. */
export function useTick(): UIStatus | null {
  const [status, setStatus] = useState<UIStatus | null>(null)
  useEffect(() => {
    let mounted = true
    Status().then(s => { if (mounted) setStatus(s) }).catch(() => {})
    const off = EventsOn('tick', (s: UIStatus) => setStatus(s))
    return () => { mounted = false; off() }
  }, [])
  return status
}

/** True when the 2 Hz snapshots go quiet (>2.5 s) — the backend is busy or
 * the pipe dropped. The connection pill shows "Syncing…" instead of stale data. */
export function useTickStale(status: UIStatus | null): boolean {
  const [stale, setStale] = useState(false)
  const lastRef = useRef(Date.now())
  useEffect(() => {
    lastRef.current = Date.now()
    setStale(false)
  }, [status])
  useEffect(() => {
    const t = setInterval(() => setStale(Date.now() - lastRef.current > 2500), 1000)
    return () => clearInterval(t)
  }, [])
  return stale && status !== null
}

export function fmtUptime(ms: number): string {
  const s = Math.floor(ms / 1000)
  const d = Math.floor(s / 86400)
  const h = Math.floor((s % 86400) / 3600)
  const m = Math.floor((s % 3600) / 60)
  const sec = s % 60
  if (d > 0) return `${d}d ${h}h`
  if (h > 0) return `${h}h ${m}m`
  if (m > 0) return `${m}m ${sec}s`
  return `${sec}s`
}

export function fmtBytes(n: number): string {
  if (!n || n < 0) return '0 B'
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB']
  let i = 0
  let v = n
  while (v >= 1024 && i < units.length - 1) { v /= 1024; i++ }
  return `${v >= 100 || i === 0 ? Math.round(v) : v.toFixed(1)} ${units[i]}`
}

export function fmtRate(bytesPerSec: number): string {
  return `${fmtBytes(bytesPerSec)}/s`
}

export function fmtDuration(ms: number): string {
  const s = Math.max(0, Math.floor(ms / 1000))
  const h = Math.floor(s / 3600)
  const m = Math.floor((s % 3600) / 60)
  const sec = s % 60
  if (h > 0) return `${h}h ${m}m`
  if (m > 0) return `${m}m ${sec}s`
  return `${sec}s`
}
