import {useEffect, useState} from 'react'
import {fmtUptime, useTickStale, UIStatus} from '../state'
import {RoleWord, Spinner} from './ui'

/**
 * The one-glance link state in the title bar: state + consequence, with RTT,
 * loss and uptime once the link is up. Shows "Syncing…" when snapshots stall.
 */
export function ConnectionPill({status}: {status: UIStatus}) {
  const stale = useTickStale(status)
  const isAgent = status.role === 'agent'
  const up = isAgent ? status.linkUp : status.agentConnected

  // 1 Hz re-render so uptime ticks between the 2 Hz snapshots.
  const [, force] = useState(0)
  useEffect(() => {
    const t = setInterval(() => force(x => x + 1), 1000)
    return () => clearInterval(t)
  }, [])
  const uptime = status.linkUpSinceMs ? fmtUptime(Date.now() - status.linkUpSinceMs) : null

  if (stale) {
    return (
      <div className="flex items-center gap-2 rounded-[var(--r-sm)] border border-[var(--border)] bg-[var(--panel)] px-2.5 py-1 text-xs text-[var(--text-2)] shadow-[var(--shadow-soft)]">
        <span className="text-[var(--warn)]"><Spinner size={12} /></span>
        <span className="font-medium">Syncing…</span>
      </div>
    )
  }

  const label = isAgent
    ? up ? 'Connected' : 'Reconnecting…'
    : up
      ? <><RoleWord role="agent">Agent</RoleWord> online</>
      : <>Waiting for <RoleWord role="agent">agent</RoleWord></>
  // Once up, the health rollup drives the dot so jitter/loss degradation shows
  // at a glance. Both roles measure their own health.
  const health = status.healthScore
  const tone = !up ? (isAgent ? 'bad' : 'warn')
    : health === 'warn' || health === 'bad' ? health
    : 'good'
  const color = {good: 'var(--good)', bad: 'var(--bad)', warn: 'var(--warn)'}[tone]
  const showLoss = up && status.packetLossPct > 0

  return (
    <div className="flex items-center gap-2 rounded-[var(--r-sm)] border border-[var(--border)] bg-[var(--panel)] px-2.5 py-1 text-xs shadow-[var(--shadow-soft)] transition-colors duration-300">
      {/* The dot keeps --halo-gap from its own label — it breathes a 5px ring
          (motion.css .pf-halo) — while the readouts that follow stay on the
          pill's tighter rhythm. */}
      <span className="flex items-center gap-[var(--halo-gap)]">
        <span
          className={`inline-flex h-2 w-2 shrink-0 rounded-full ${up ? 'pf-halo' : ''}`}
          style={{background: color, ['--halo' as string]: color}}
        />
        <span className="font-medium text-[var(--text-2)]">{label}</span>
      </span>
      {up && <span className="tabular-nums text-[var(--text-3)]">· {status.rttMillis} ms</span>}
      {showLoss && (
        <span className="tabular-nums text-[var(--warn)]">
          · {status.packetLossPct.toFixed(status.packetLossPct < 10 ? 1 : 0)}% loss
        </span>
      )}
      {up && uptime && <span className="tabular-nums text-[var(--text-3)]">· up {uptime}</span>}
    </div>
  )
}
