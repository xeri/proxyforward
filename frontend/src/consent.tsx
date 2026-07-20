// Mutual-consent config changes (config-consent), shared by both roles.
//
// An authored tunnel edit — this machine's, or the other end's — applies only once
// the other side approves. Both approval surfaces need the same thing: to show what
// would change, in the other party's terms, before anyone agrees to it. Hence one
// diff here rather than two that drift.
//
// Arbitration (a port reclaim, an auto-reassign) is NOT consent and never appears
// here: it is immediate and gets a notice, not a veto.

import {useCallback, useEffect, useState} from 'react'
import {AgentConsent} from '../wailsjs/go/app/App'
import type {agent, control} from '../wailsjs/go/models'
import {Badge} from './components/ui'

/** A tunnel as the diff needs it: what the peer would see change. */
export type DiffTunnel = {id: string; name?: string; publicPort?: number}

export type ChangeKind = 'added' | 'removed' | 'moved'

export type Change = {
  kind: ChangeKind
  id: string
  name: string
  /** Ports are only meaningful on a `moved` row; 0 means the gateway picks. */
  fromPort?: number
  toPort?: number
}

const portLabel = (p?: number) => (p && p > 0 ? `:${p}` : 'auto')

/**
 * diffTunnels: what a proposal would do to the set that is live today. Only the
 * gateway-visible fields are compared, because those are the only ones a proposal
 * carries — a local address change never reaches here and never needs approval.
 * A same-set proposal yields [], which the callers render as "no visible change"
 * rather than an empty box.
 */
export function diffTunnels(current: DiffTunnel[], proposed: DiffTunnel[]): Change[] {
  const byId = new Map(current.map(t => [t.id, t]))
  const out: Change[] = []
  for (const p of proposed) {
    const c = byId.get(p.id)
    if (!c) {
      out.push({kind: 'added', id: p.id, name: p.name || p.id, toPort: p.publicPort})
      continue
    }
    if ((c.publicPort ?? 0) !== (p.publicPort ?? 0)) {
      out.push({kind: 'moved', id: p.id, name: p.name || p.id, fromPort: c.publicPort, toPort: p.publicPort})
    }
  }
  const keep = new Set(proposed.map(t => t.id))
  for (const c of current) {
    if (!keep.has(c.id)) out.push({kind: 'removed', id: c.id, name: c.name || c.id, fromPort: c.publicPort})
  }
  return out
}

/** specsOf narrows wire specs (or any tunnel-ish rows) to what the diff reads. */
export const specsOf = (t: control.TunnelSpec[] | undefined): DiffTunnel[] =>
  (t ?? []).map(s => ({id: s.id, name: s.name, publicPort: s.publicPort}))

const TONE: Record<ChangeKind, 'good' | 'bad' | 'warn'> = {added: 'good', removed: 'bad', moved: 'warn'}
const VERB: Record<ChangeKind, string> = {added: 'Add', removed: 'Remove', moved: 'Move'}

/**
 * ProposalDiff: the change list an approval is actually agreeing to. Every row
 * names the tunnel and what happens to its public port, so "approve" is never a
 * blind yes. Colour is signal only — the tone carries the verb, and the verb is
 * written out too, so the row never depends on colour alone.
 */
export function ProposalDiff({current, proposed}: {current: DiffTunnel[]; proposed: DiffTunnel[]}) {
  const changes = diffTunnels(current, proposed)
  if (changes.length === 0) {
    return (
      <p className="text-body text-[var(--text-3)]">
        Nothing the gateway serves would change — the difference is local to the agent.
      </p>
    )
  }
  return (
    <ul className="space-y-1">
      {changes.map(c => (
        <li key={`${c.kind}:${c.id}`} className="flex items-center gap-2 text-body">
          <Badge tone={TONE[c.kind]}>{VERB[c.kind]}</Badge>
          <span className="min-w-0 flex-1 truncate text-[var(--text)]">{c.name}</span>
          <span className="shrink-0 font-mono text-sub text-[var(--text-3)]">
            {c.kind === 'moved' ? `${portLabel(c.fromPort)} → ${portLabel(c.toPort)}` : portLabel(c.fromPort ?? c.toPort)}
          </span>
        </li>
      ))}
    </ul>
  )
}

/** proposalAge renders how long a proposal has been waiting, in words. */
export function proposalAge(atMs: number): string {
  const secs = Math.max(0, Math.round((Date.now() - atMs) / 1000))
  if (secs < 60) return 'just now'
  const mins = Math.round(secs / 60)
  if (mins < 60) return `${mins} min ago`
  const hrs = Math.round(mins / 60)
  return `${hrs} hr ago`
}

export type ConsentState = {
  /** null until the first answer: no surface should guess before then. */
  view: agent.PendingView | null
  refresh: () => void
}

/**
 * useAgentConsent polls this machine's consent state (agent role): what the gateway
 * has proposed, what we have proposed, and why our last edit was declined.
 *
 * There is deliberately no error state. App.AgentConsent collapses every failure —
 * wrong role, detached, a daemon too old to know the op — to a zero view, and that
 * zero view IS the honest answer: consentActive false means "edits apply the old
 * way", which is what such a daemon does. An error flag here could only produce a
 * second way to say the same thing, or a promise of an approval that never comes.
 *
 * Bespoke rather than usePolled for the refresh(): a decision has to re-read at once
 * rather than wait out the interval. Polled, not pushed — the tick stays the single
 * coalesced Go→JS event.
 */
export function useAgentConsent(enabled: boolean, pollMs = 2000): ConsentState {
  const [view, setView] = useState<agent.PendingView | null>(null)
  const [nonce, setNonce] = useState(0)
  const refresh = useCallback(() => setNonce(n => n + 1), [])

  useEffect(() => {
    if (!enabled) return
    let alive = true
    const tick = () => {
      AgentConsent()
        .then(v => { if (alive) setView(v) })
        .catch(() => { if (alive) setView(null) })
    }
    tick()
    const id = setInterval(tick, pollMs)
    return () => { alive = false; clearInterval(id) }
  }, [enabled, pollMs, nonce])

  return {view, refresh}
}
