import {useEffect, useRef, useState} from 'react'
import {EventsOn} from '../wailsjs/runtime/runtime'
import {Status} from '../wailsjs/go/app/App'
import {app} from '../wailsjs/go/models'

export type UIStatus = app.UIStatus

export type RunState = 'running' | 'stopping' | 'stopped'

/** The tunnel run's lifecycle. A daemon too old to report it is running, for
 * the same reason it has no off switch. */
export function runStateOf(s: UIStatus): RunState {
  return (s.runState || 'running') as RunState
}

/** Deliberately off: the run is stopped and there is no fatal error. This is the
 * state the power controls put it in — distinct from a crash, which stops the
 * run too but keeps engineFatal, and must keep reading as the alarm it is. */
export function isEngineOff(s: UIStatus): boolean {
  return runStateOf(s) === 'stopped' && !s.engineFatal
}

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

// Bandwidth-cap scope options and their labels — shared by the agent Tunnels
// editor (select + summary chip) and the gateway Agents drill-in. Values match
// config.go's BandwidthScope* constants; empty normalizes to combined.
export const BANDWIDTH_SCOPES = [
  {value: 'combined', label: 'Combined'},
  {value: 'per-direction', label: 'Per-direction'},
  {value: 'per-connection', label: 'Per-connection'},
]
/** Tunnel protocols the agent can serve. Labelled by what a user is actually
 * setting up rather than by the transport name, because "UDP" alone does not
 * tell a Minecraft admin whether it is the one they need. */
export const PROTOCOLS = [
  {value: 'tcp', label: 'TCP — Java Edition'},
  {value: 'udp', label: 'UDP — Bedrock, Geyser, voice chat'},
]
export function scopeLabel(s: string): string {
  return BANDWIDTH_SCOPES.find(o => o.value === (s || 'combined'))?.label ?? 'Combined'
}

/** How a tunnel's local server is checked. Values match config.go's
 * HealthProbe* constants; empty normalizes to "tcp". Labelled by what the check
 * can actually tell you, because that is the whole difference between them. */
export const HEALTH_PROBES = [
  {value: 'tcp', label: 'Connect — is anything listening?'},
  {value: 'mc-status', label: 'Server ping — is Minecraft answering?'},
]
export function probeLabel(s: string): string {
  return HEALTH_PROBES.find(o => o.value === (s || 'tcp'))?.label ?? 'Connect'
}

/** Human labels for the active data-plane transport (Settings' link line and the
 * Agents drill-in chip). An unknown/empty value renders as "—" at the call site. */
export const TRANSPORT_LABEL: Record<string, string> = {
  quic: 'QUIC',
  'per-conn': 'Per-connection',
  mux: 'Multiplexed',
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

/** hasRtt: the one RTT-known predicate. Backends use both -1 and 0 as "no
 * sample" sentinels, so anything ≤ 0 renders as unknown. */
export function hasRtt(ms: number): boolean {
  return ms > 0
}

/** rttTone: the player-ping ramp, and the one place its cutoffs live — shared
 * by the Players badge, the Analytics tile and the GeoRank rows. Intentionally
 * *not* the link-health thresholds (`engine.go healthScore`): a player's ping to
 * the gateway and the control link's own round trip are different measurements
 * and move for different reasons. Guard with hasRtt — unknown has no tone. */
export function rttTone(ms: number): 'good' | 'warn' | 'bad' {
  return ms < 60 ? 'good' : ms < 130 ? 'warn' : 'bad'
}

/** worstHealth: the fleet rollup — the least-healthy agent sets the verdict, so
 * one struggling machine is never hidden behind healthy ones. Unknown among
 * healthy reads as caution. Empty is 'good' (callers gate on count). The one
 * definition, shared by the connection pill and the Agents roster. */
export function worstHealth(items: {healthScore: string}[]): 'good' | 'warn' | 'bad' {
  let tone: 'good' | 'warn' | 'bad' = 'good'
  for (const it of items) {
    if (it.healthScore === 'bad') return 'bad'
    if (it.healthScore === 'warn' || it.healthScore === 'unknown') tone = 'warn'
  }
  return tone
}

/** fmtRtt renders a known round-trip time ("34 ms"). Guard with hasRtt. */
export function fmtRtt(ms: number): string {
  return `${Math.round(ms)} ms`
}

/** flagEmoji turns an ISO country code into its regional-indicator emoji,
 * null when the code is missing/invalid (callers pick their own fallback). */
export function flagEmoji(cc?: string): string | null {
  if (!cc || !/^[A-Za-z]{2}$/.test(cc)) return null
  const up = cc.toUpperCase()
  return String.fromCodePoint(0x1f1e6 + up.charCodeAt(0) - 65, 0x1f1e6 + up.charCodeAt(1) - 65)
}
