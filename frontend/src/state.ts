import {useEffect, useState} from 'react'
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
