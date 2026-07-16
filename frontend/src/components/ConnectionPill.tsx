import {useEffect, useState} from 'react'
import {fmtUptime, useTickStale, UIStatus, worstHealth} from '../state'
import {RoleWord, Spinner} from './ui'

/**
 * The one-glance link state in the title bar: state + consequence, with RTT,
 * loss and uptime once the link is up. For a gateway it rolls the whole fleet
 * up — "N agents online" with the worst-of-fleet health driving the dot. Shows
 * "Syncing…" when snapshots stall.
 */
export function ConnectionPill({status}: {status: UIStatus}) {
  const stale = useTickStale(status)
  const isAgent = status.role === 'agent'
  const agents = status.agents ?? []
  const n = agents.length
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
    : n === 0
      ? <>Waiting for <RoleWord role="agent">agents</RoleWord></>
      : n === 1
        ? <><RoleWord role="agent">Agent</RoleWord> online</>
        : <>{n} <RoleWord role="agent">agents</RoleWord> online</>
  // Once up, the health rollup drives the dot so jitter/loss degradation shows
  // at a glance. The agent scores its own link; the gateway takes the fleet's
  // worst so one struggling agent isn't hidden behind healthy ones.
  const health = status.healthScore
  const tone = !up ? (isAgent ? 'bad' : 'warn')
    : isAgent
      ? (health === 'warn' || health === 'bad' ? health : 'good')
      : worstHealth(agents)
  const color = {good: 'var(--good)', bad: 'var(--bad)', warn: 'var(--warn)'}[tone]
  // Per-link RTT/loss/uptime are only unambiguous for one link: the agent's own,
  // or a gateway with exactly one agent. With a fleet, the roster owns them.
  const showTail = up && (isAgent || n === 1)
  const showLoss = showTail && status.packetLossPct > 0

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
      {showTail && <span className="tabular-nums text-[var(--text-3)]">· {status.rttMillis} ms</span>}
      {showLoss && (
        <span className="tabular-nums text-[var(--warn)]">
          · {status.packetLossPct.toFixed(status.packetLossPct < 10 ? 1 : 0)}% loss
        </span>
      )}
      {showTail && uptime && <span className="tabular-nums text-[var(--text-3)]">· up {uptime}</span>}
    </div>
  )
}
