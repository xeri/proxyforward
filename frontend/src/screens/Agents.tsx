import {useEffect, useMemo, useRef, useState} from 'react'
import {
  AgentBandwidthHistory, GatewayEvents, ListAgents, RenameAgent, RevokeAgent, SetAgentScope,
} from '../../wailsjs/go/app/App'
import {app, gateway} from '../../wailsjs/go/models'
import {BandwidthPanel} from '../components/BandwidthChart'
import {Emblem} from '../components/Emblem'
import {
  Badge, Banner, Button, Card, CopyIcon, Disclosure, EmptyState, ErrorBanner, Field, Modal,
  Overline, PageHeader, SegmentedControl, Skeleton, StatusDot, TextInput,
} from '../components/ui'
import {
  IconActivity, IconAgents, IconArrowRight, IconChevronRight, IconClock, IconLogs,
  IconSearch, IconServer, IconShield, IconTunnels, IconUsers,
} from '../components/icons'
import {HistoryResult, RANGES, RangeKey} from '../history'
import {usePolled} from '../hooks'
import {fmtBytes, fmtUptime, hasRtt, scopeLabel, UIStatus, worstHealth} from '../state'

type AgentUI = app.AgentUI
type AgentView = gateway.AgentView
type GwEvent = gateway.GatewayEvent
type Seg = 'good' | 'warn' | 'bad' | 'unknown'

// A roster row joins the identity/enrollment view (ListAgents — the full roster,
// online or off) with the live health snapshot (status.agents — connected only).
type Row = {view: AgentView; live: AgentUI | undefined}

// Health language, mirrored from Overview so a verdict reads the same wherever
// it appears. Tones ARE the signal (DESIGN rule 4); the thresholds match the
// backend's score exactly (jitter/loss bands in agent.go).
const segColor: Record<Seg, string> = {good: 'var(--good)', warn: 'var(--warn)', bad: 'var(--bad)', unknown: 'var(--text-3)'}
const HEALTH_LABEL: Record<Seg, string> = {good: 'Healthy', warn: 'Fair', bad: 'Poor', unknown: 'Unknown'}
const HEALTH_TONE: Record<Seg, 'good' | 'warn' | 'bad' | 'neutral'> = {good: 'good', warn: 'warn', bad: 'bad', unknown: 'neutral'}
// Worst-first ordering for the Health sort: the machine that needs eyes leads.
const HEALTH_RANK: Record<Seg, number> = {bad: 0, warn: 1, unknown: 2, good: 3}

const jitterTone = (v: number): Seg => (v < 0 ? 'unknown' : v > 100 ? 'bad' : v > 30 ? 'warn' : 'good')
const lossTone = (v: number): Seg => (v < 0 ? 'unknown' : v > 5 ? 'bad' : v > 1 ? 'warn' : 'good')
const fmtMs = (v: number): string => (v < 0 ? '—' : `${v.toFixed(1)} ms`)
const fmtPct = (v: number): string => (v < 0 ? '—' : `${v.toFixed(v > 0 && v < 10 ? 1 : 0)}%`)
const fmtRttMs = (v: number): string => (hasRtt(v) ? `${Math.round(v)} ms` : '—')
const fmtDate = (ms: number): string => (ms > 0 ? new Date(ms).toLocaleDateString(undefined, {year: 'numeric', month: 'short', day: 'numeric'}) : '—')
const fmtTime = (ms: number): string => new Date(ms).toLocaleTimeString(undefined, {hour12: false})

const seg = (a: AgentUI): Seg => (a.healthScore || 'unknown') as Seg
const rowSeg = (r: Row): Seg => (r.live ? seg(r.live) : 'unknown')
const traffic = (r: Row): number => (r.live ? r.live.linkBytesIn + r.live.linkBytesOut : 0)
const players = (r: Row): number => r.live?.players ?? 0
const isConnected = (v: AgentView): boolean => v.connected && !v.revoked
// connected → offline → revoked, so struggling live machines and dead entries
// don't shuffle together under the traffic/health/players sorts.
const tier = (r: Row): number => (r.view.revoked ? 2 : r.view.connected ? 0 : 1)

// The roster's title: a chosen nickname wins, else the machine's own hostname,
// else the raw id — never blank.
const displayName = (v: AgentView): string => v.nickname || v.hostname || v.agentId

// Scope as a human phrase. Both empty = the agent may bind anything.
function scopeText(v: AgentView): string {
  const ports = v.scopePorts ?? []
  const tuns = v.scopeTunnels ?? []
  if (ports.length === 0 && tuns.length === 0) return 'Unrestricted'
  const parts: string[] = []
  if (ports.length) parts.push(`${ports.length === 1 ? 'port' : 'ports'} ${ports.join(', ')}`)
  if (tuns.length) parts.push(`${tuns.length} tunnel${tuns.length === 1 ? '' : 's'}`)
  return parts.join(' · ')
}

type SortKey = 'health' | 'traffic' | 'players' | 'name'
type Density = 'grid' | 'list'
const loadSort = (): SortKey => (localStorage.getItem('pf-agents-sort') as SortKey) || 'health'
const loadDensity = (): Density => (localStorage.getItem('pf-agents-density') as Density) || 'grid'

// Roster cache for usePolled: keyed by a nonce so a mutation can force an
// immediate re-poll (the key changes) without waiting out the interval.
const rosterCache = new Map<string, AgentView[]>()

// Per-agent bandwidth source: a hook bound to one agentId, keyed so each agent
// keeps its own cached history. Fed to BandwidthPanel so the drill-in draws the
// same instrument as Traffic — scoped to this machine's RRD.
const agentHistCache = new Map<string, HistoryResult>()
function agentHistorySource(agentId: string) {
  return (range: RangeKey): HistoryResult | null => {
    const spec = RANGES[range]
    return usePolled(agentHistCache, `${agentId}:${range}`,
      () => AgentBandwidthHistory(agentId, spec.windowMs, spec.buckets), spec.pollMs)
  }
}

// GatewayEvents tail: mirrors Activity's since-cursor accumulate. Paused (key
// off) when this isn't the gateway. `null` until the first poll resolves so the
// consumers can tell "loading" from "loaded, empty".
const EVENTS_CAP = 200
function useGatewayEvents(enabled: boolean): GwEvent[] | null {
  const [events, setEvents] = useState<GwEvent[] | null>(null)
  const lastSeq = useRef(0)
  useEffect(() => {
    if (!enabled) { setEvents(null); lastSeq.current = 0; return }
    let alive = true
    const pull = async () => {
      try {
        const fresh = await GatewayEvents(lastSeq.current)
        if (!alive) return
        if (fresh.length) lastSeq.current = fresh[fresh.length - 1].seq
        setEvents(prev => (fresh.length ? [...(prev ?? []), ...fresh].slice(-EVENTS_CAP) : prev ?? []))
      } catch {
        // A daemon without the ring degrades to the written-empty state rather
        // than a stuck skeleton; the screen-level unavailable state covers the
        // pre-roster daemon before we ever get here.
        if (alive) setEvents(prev => prev ?? [])
      }
    }
    pull()
    const t = setInterval(pull, 3000)
    return () => { alive = false; clearInterval(t) }
  }, [enabled])
  return events
}

/**
 * Agents (gateway role): the fleet — a wall of machine cards, the gateway
 * analogue of the Players wall of faces. The roster now comes from ListAgents
 * (every enrolled agent, online or off, plus any connected shared-token agent);
 * live health/RTT joins in from status.agents for the connected ones. Selecting
 * one drills into a focused per-agent view with rename/scope/revoke controls.
 * One identity surface; the cards are frost, never Signal Glass — N equal cards
 * can't each answer the pointer.
 */
export function Agents({status}: {status: UIStatus}) {
  const [selected, setSelected] = useState<string | null>(null)
  const [sort, setSort] = useState<SortKey>(loadSort)
  const [density, setDensity] = useState<Density>(loadDensity)
  const [query, setQuery] = useState('')
  const [nonce, setNonce] = useState(0)
  const [dismissed, setDismissed] = useState<Set<number>>(() => new Set())

  const isGateway = status.role === 'gateway'
  const rosterKey = isGateway ? `roster:${nonce}` : null
  const roster = usePolled(rosterCache, rosterKey, () => ListAgents(), 4000)
  const events = useGatewayEvents(isGateway)

  // A mutation re-polls at once: carry the current data into the next key so the
  // poll refreshes without flashing the skeleton, then bump the nonce.
  const refreshRoster = () => {
    if (roster && rosterKey) rosterCache.set(`roster:${nonce + 1}`, roster)
    setNonce(n => n + 1)
  }

  const liveById = useMemo(
    () => new Map((status.agents ?? []).map(a => [a.agentId, a])),
    [status.agents])
  const rows = useMemo<Row[]>(
    () => (roster ?? []).map(v => ({view: v, live: liveById.get(v.agentId)})),
    [roster, liveById])

  const rosterIds = useMemo(() => new Set((roster ?? []).map(v => v.agentId)), [roster])

  // Unresolved conflicts: dedupe the event ring by conflict identity (latest
  // wins), drop the client-dismissed ones. A newer event for the same identity
  // resurfaces even after an older one was dismissed.
  const conflicts = useMemo(() => {
    if (!events) return []
    const byKey = new Map<string, GwEvent>()
    for (const e of events) {
      const key = `${e.kind}:${e.agentId ?? ''}:${e.tunnelId ?? ''}:${e.requestedPort ?? ''}`
      const prev = byKey.get(key)
      if (!prev || e.seq > prev.seq) byKey.set(key, e)
    }
    return [...byKey.values()].filter(e => !dismissed.has(e.seq)).sort((a, b) => b.seq - a.seq)
  }, [events, dismissed])

  const sorted = useMemo(() => {
    const f = query.trim().toLowerCase()
    const filtered = rows.filter(r => !f
      || r.view.hostname.toLowerCase().includes(f)
      || r.view.agentId.toLowerCase().includes(f)
      || (r.view.nickname || '').toLowerCase().includes(f))
    return filtered.sort((a, b) => {
      if (sort === 'name') return displayName(a.view).localeCompare(displayName(b.view))
      const t = tier(a) - tier(b)
      if (t !== 0) return t
      return sort === 'players' ? players(b) - players(a) || traffic(b) - traffic(a)
        : sort === 'traffic' ? traffic(b) - traffic(a)
          : HEALTH_RANK[rowSeg(a)] - HEALTH_RANK[rowSeg(b)] || traffic(b) - traffic(a)
    })
  }, [rows, sort, query])

  // A selected agent that leaves the roster entirely drops us back to the list.
  useEffect(() => {
    if (selected && roster && !roster.some(v => v.agentId === selected)) setSelected(null)
  }, [selected, roster])

  // Honest-unavailable, told apart from an empty roster: this machine is an agent
  // (no roster to show), or the background service predates the roster and sends
  // no agents array at all (status.agents === undefined; &fleet=old).
  if (!isGateway) {
    return (
      <div className="pf-stagger space-y-8">
        <Header />
        <Card><EmptyState icon={<IconAgents size={28} />} title="Roster unavailable"
          hint="Agents live on the gateway. This machine is running as an agent, so it has no fleet to manage." /></Card>
      </div>
    )
  }
  if (status.agents === undefined) {
    return (
      <div className="pf-stagger space-y-8">
        <Header />
        <Card><EmptyState icon={<IconAgents size={28} />} title="Roster unavailable"
          hint="The background service is an older version that doesn't report the agent roster. Update it to manage agents." /></Card>
      </div>
    )
  }

  const current = selected ? rows.find(r => r.view.agentId === selected) ?? null : null
  if (current) return <AgentDetail status={status} row={current} onBack={() => setSelected(null)} onChanged={refreshRoster} />

  const pickSort = (v: SortKey) => { setSort(v); localStorage.setItem('pf-agents-sort', v) }
  const pickDensity = (v: Density) => { setDensity(v); localStorage.setItem('pf-agents-density', v) }
  const dismiss = (seq: number) => setDismissed(prev => new Set(prev).add(seq))
  const n = rows.length

  return (
    <div className="pf-stagger space-y-8">
      <Header />

      <ConflictCards conflicts={conflicts} rosterIds={rosterIds} onSelect={setSelected} onDismiss={dismiss} />

      {roster === null ? (
        <RosterSkeleton density={density} />
      ) : n === 0 ? (
        <Card><EmptyState icon={<IconAgents size={28} />} title="No agents enrolled yet"
          hint="Issue a pairing code (Overview, or the setup wizard) and share it with a machine. Every agent that enrolls with it appears here — online or off." /></Card>
      ) : (
        <>
          <FleetSummary rows={rows} />

          <div className="flex flex-wrap items-center justify-between gap-3">
            <SegmentedControl<SortKey>
              value={sort} onChange={pickSort}
              options={[
                {value: 'health', label: 'Health'}, {value: 'traffic', label: 'Traffic'},
                {value: 'players', label: 'Players'}, {value: 'name', label: 'Name'},
              ]}
            />
            <div className="flex items-center gap-2">
              {n > 4 && (
                <div className="w-48">
                  <TextInput
                    size="sm" icon={<IconSearch size={14} />} value={query} onChange={setQuery}
                    placeholder="Filter agents…" ariaLabel="Filter agents by name, host, or id"
                  />
                </div>
              )}
              <SegmentedControl<Density>
                value={density} onChange={pickDensity}
                options={[{value: 'grid', label: 'Grid'}, {value: 'list', label: 'List'}]}
              />
            </div>
          </div>

          {sorted.length === 0 ? (
            <Card><EmptyState icon={<IconSearch size={26} />} title="No agents match"
              hint={`Nothing matches "${query.trim()}".`} /></Card>
          ) : density === 'grid' ? (
            <div className="pf-stagger grid grid-cols-1 gap-[var(--grid-gap)] @min-[52rem]:grid-cols-2 @min-[80rem]:grid-cols-3">
              {sorted.map(r => <AgentCard key={r.view.agentId} row={r} onOpen={() => setSelected(r.view.agentId)} />)}
            </div>
          ) : (
            <Card pad={false}><AgentList rows={sorted} onOpen={setSelected} /></Card>
          )}
        </>
      )}

      <EventLog events={events} />
    </div>
  )
}

function Header() {
  return <PageHeader title="Agents" subtitle="Every machine enrolled with this gateway." />
}

/** FleetSummary: the page's one hero figure (agent count) with the connected
 * fleet's worst-of health and the rolled-up totals — type on whitespace, never
 * another card (DESIGN rule 5). Only rendered for a non-empty roster. */
function FleetSummary({rows}: {rows: Row[]}) {
  const n = rows.length
  const online = rows.filter(r => isConnected(r.view)).length
  const revoked = rows.filter(r => r.view.revoked).length
  const liveAgents = rows.filter(r => r.live).map(r => r.live as AgentUI)
  const worst = worstHealth(liveAgents)
  const tunnels = rows.reduce((s, r) => s + r.view.tunnels, 0)
  const plrs = rows.reduce((s, r) => s + players(r), 0)
  const verdict = worst === 'good' ? 'All healthy' : worst === 'warn' ? 'Needs attention' : 'Degraded'
  return (
    <div className="flex flex-wrap items-baseline gap-x-6 gap-y-2">
      <div className="flex items-baseline gap-2.5">
        <span className="text-[length:var(--fs-metric-hero)] font-semibold leading-none tabular-nums">{n}</span>
        <span className="text-sm text-[var(--text-2)]">{n === 1 ? 'agent' : 'agents'}</span>
      </div>
      {online > 0
        ? <span className="self-center"><StatusDot state={worst} label={verdict} pulse={worst === 'good'} /></span>
        : <span className="self-center"><StatusDot state="unknown" label="None online" /></span>}
      <span className="text-sm tabular-nums text-[var(--text-3)]">{online} online</span>
      {revoked > 0 && <span className="text-sm tabular-nums text-[var(--text-3)]">{revoked} revoked</span>}
      <span className="text-sm tabular-nums text-[var(--text-3)]">{tunnels} tunnel{tunnels === 1 ? '' : 's'}</span>
      <span className="text-sm tabular-nums text-[var(--text-3)]">{plrs} player{plrs === 1 ? '' : 's'} online</span>
    </div>
  )
}

/** StatusMark: one agent's connectivity verdict — health dot when connected, a
 * quiet "Offline" dot when enrolled-but-away, a Revoked badge when cut off. */
function StatusMark({view, h}: {view: AgentView; h: Seg}) {
  if (view.revoked) return <Badge tone="bad">Revoked</Badge>
  if (isConnected(view)) return <StatusDot state={h} label={HEALTH_LABEL[h]} pulse />
  return <StatusDot state="unknown" label="Offline" />
}

/** Metric: tier-3 stat — type on whitespace with a hairline lead, no box; the
 * status tone lives on the numeral. Shared by the cards and the detail health. */
function Metric({label, value, tone}: {label: string; value: string; tone: Seg | 'neutral'}) {
  const c = tone === 'neutral' ? 'var(--text)' : segColor[tone]
  return (
    <div className="min-w-0 border-l border-[var(--hairline)] pl-3">
      <Overline>{label}</Overline>
      <div className="mt-1 truncate text-[length:var(--fs-metric)] font-semibold leading-tight tabular-nums" style={{color: c}} title={value}>{value}</div>
    </div>
  )
}

/** Uptime: a 1 Hz self-updating duration so the readout ticks between snapshots. */
function Uptime({since}: {since: number}) {
  const [, tick] = useState(0)
  useEffect(() => {
    const t = setInterval(() => tick(x => x + 1), 1000)
    return () => clearInterval(t)
  }, [])
  return <span className="tabular-nums">{fmtUptime(Date.now() - since)}</span>
}

/** ScopeChip: the agent's bind scope as a quiet inline phrase (footer/list). */
function ScopeChip({view}: {view: AgentView}) {
  const s = scopeText(view)
  return (
    <span className="inline-flex min-w-0 items-center gap-1.5" title={`Scope: ${s}`}>
      <IconShield size={12} /> <span className="truncate">{s}</span>
    </span>
  )
}

/** AgentCard: one machine's card — frost glass that lifts to the pointer
 * (tactility, DESIGN rule 3) but never ignites; only a live health dot breathes.
 * The whole card is the drill-in affordance. Connected agents show live vitals;
 * offline/revoked ones show their standing instead, at matched geometry. */
function AgentCard({row, onOpen}: {row: Row; onOpen: () => void}) {
  const {view, live} = row
  const h = rowSeg(row)
  const connected = isConnected(view)
  return (
    <button
      type="button" onClick={onOpen} aria-label={`Open ${displayName(view)}`}
      className="pf-card pf-lift group flex flex-col gap-4 p-4 text-left"
    >
      <div className="flex items-start gap-3">
        <Emblem role="agent" size={38} fixed glow={connected && h === 'good'} />
        <div className="min-w-0 flex-1">
          <div className="truncate font-semibold">{displayName(view)}</div>
          <div className="mt-0.5 truncate font-mono text-[11px] text-[var(--text-3)]">{view.agentId || '—'}</div>
        </div>
        <div className="shrink-0"><StatusMark view={view} h={h} /></div>
      </div>

      {connected && live ? (
        <div className="grid grid-cols-3 gap-3">
          <Metric label="Round trip" value={fmtRttMs(live.rttMillis)} tone="neutral" />
          <Metric label="Jitter" value={fmtMs(live.jitterMillis)} tone={jitterTone(live.jitterMillis)} />
          <Metric label="Loss" value={fmtPct(live.packetLossPct)} tone={lossTone(live.packetLossPct)} />
        </div>
      ) : (
        <div className="min-w-0 border-l border-[var(--hairline)] pl-3">
          <Overline>{view.revoked ? 'Status' : 'Enrolled'}</Overline>
          <div className="mt-1 truncate text-sm text-[var(--text-2)]">
            {view.revoked ? 'Access revoked' : fmtDate(view.issuedAtMs)}
          </div>
        </div>
      )}

      <div className="flex flex-wrap items-center gap-x-4 gap-y-1 border-t border-[var(--border)] pt-3 text-[11px] text-[var(--text-3)]">
        <span className="inline-flex items-center gap-1.5"><IconTunnels size={13} /> {view.tunnels} tunnel{view.tunnels === 1 ? '' : 's'}</span>
        {connected && <span className="inline-flex items-center gap-1.5"><IconUsers size={13} /> {players(row)} player{players(row) === 1 ? '' : 's'}</span>}
        <ScopeChip view={view} />
        {connected && view.linkUpSinceMs > 0 && (
          <span className="inline-flex items-center gap-1.5"><IconClock size={13} /> up <Uptime since={view.linkUpSinceMs} /></span>
        )}
        <span className="ml-auto text-[var(--text-3)] opacity-0 transition-opacity duration-200 group-hover:opacity-100"><IconChevronRight size={14} /></span>
      </div>
    </button>
  )
}

/** AgentList: the compact density — one flush row per agent, extra columns
 * revealed as the container widens (@container), so a wide fleet reads as a
 * dense table and a narrow one stays legible. */
function AgentList({rows, onOpen}: {rows: Row[]; onOpen: (id: string) => void}) {
  return (
    <div className="divide-y divide-[var(--border)]">
      {rows.map(r => {
        const {view, live} = r
        const h = rowSeg(r)
        const connected = isConnected(view)
        return (
          <button
            key={view.agentId} type="button" onClick={() => onOpen(view.agentId)}
            className="group flex w-full items-center gap-4 px-4 py-3 text-left transition-colors hover:bg-[color-mix(in_srgb,var(--panel-2)_60%,transparent)]"
          >
            <Emblem role="agent" size={30} fixed glow={connected && h === 'good'} />
            <div className="min-w-0 flex-1">
              <div className="truncate text-sm font-medium">{displayName(view)}</div>
              <div className="truncate font-mono text-[11px] text-[var(--text-3)]">{view.agentId || '—'}</div>
            </div>
            <div className="hidden w-28 shrink-0 @min-[40rem]:block"><StatusMark view={view} h={h} /></div>
            <div className="hidden w-16 shrink-0 text-right font-mono text-xs tabular-nums text-[var(--text-2)] @min-[52rem]:block" title="Round trip">{connected && live ? fmtRttMs(live.rttMillis) : '—'}</div>
            <div className="hidden w-36 shrink-0 text-right text-xs tabular-nums text-[var(--text-3)] @min-[64rem]:block" title={`Scope: ${scopeText(view)}`}><span className="truncate">{scopeText(view)}</span></div>
            <div className="w-16 shrink-0 text-right text-xs tabular-nums text-[var(--text-3)]" title="Tunnels">{view.tunnels} tun</div>
            <span className="shrink-0 text-[var(--text-3)] opacity-0 transition-opacity duration-200 group-hover:opacity-100"><IconChevronRight size={16} /></span>
          </button>
        )
      })}
    </div>
  )
}

/** RosterSkeleton: geometry-matched placeholder for the summary line + card grid
 * (or the compact rows) while ListAgents resolves. */
function RosterSkeleton({density}: {density: Density}) {
  return (
    <div className="space-y-6" aria-busy="true">
      <div className="flex flex-wrap items-center gap-x-6 gap-y-2">
        <Skeleton className="h-9 w-14 rounded-[var(--r-md)]" />
        <Skeleton className="h-4 w-24 rounded-full" />
        <Skeleton className="h-4 w-20 rounded-full" />
      </div>
      {density === 'list' ? (
        <Card pad={false}>
          <div className="divide-y divide-[var(--border)]">
            {[0, 1, 2, 3].map(i => (
              <div key={i} className="flex items-center gap-4 px-4 py-3">
                <Skeleton className="h-[30px] w-[30px] rounded-[var(--r-md)]" />
                <div className="flex-1 space-y-1.5"><Skeleton className="h-3.5 w-40 rounded-full" /><Skeleton className="h-3 w-56 rounded-full" /></div>
                <Skeleton className="h-3.5 w-20 rounded-full" />
              </div>
            ))}
          </div>
        </Card>
      ) : (
        <div className="grid grid-cols-1 gap-[var(--grid-gap)] @min-[52rem]:grid-cols-2 @min-[80rem]:grid-cols-3">
          {[0, 1, 2].map(i => <SkeletonCard key={i} />)}
        </div>
      )}
    </div>
  )
}

function SkeletonCard() {
  return (
    <div className="pf-card flex flex-col gap-4 p-4">
      <div className="flex items-start gap-3">
        <Skeleton className="h-[38px] w-[38px] rounded-[var(--r-md)]" />
        <div className="flex-1 space-y-1.5"><Skeleton className="h-3.5 w-32 rounded-full" /><Skeleton className="h-3 w-40 rounded-full" /></div>
        <Skeleton className="h-4 w-16 rounded-full" />
      </div>
      <div className="grid grid-cols-3 gap-3">
        {[0, 1, 2].map(i => <div key={i} className="space-y-1.5"><Skeleton className="h-2.5 w-12 rounded-full" /><Skeleton className="h-4 w-14 rounded-full" /></div>)}
      </div>
      <div className="flex gap-3 border-t border-[var(--border)] pt-3">
        <Skeleton className="h-3 w-16 rounded-full" /><Skeleton className="h-3 w-16 rounded-full" /><Skeleton className="h-3 w-20 rounded-full" />
      </div>
    </div>
  )
}

/** ConflictCards: the gateway's unresolved conflicts, surfaced above the roster.
 * Informational only — there is no one-click reclaim/evict on the backend; each
 * card explains the conflict and links to the contesting agent. Nothing shows
 * when there are none (no empty box). */
function ConflictCards({conflicts, rosterIds, onSelect, onDismiss}: {
  conflicts: GwEvent[]; rosterIds: Set<string>; onSelect: (id: string) => void; onDismiss: (seq: number) => void
}) {
  if (conflicts.length === 0) return null
  return (
    <div className="pf-stagger space-y-3">
      {conflicts.map(e => (
        <ConflictCard
          key={e.seq} e={e}
          linkable={!!e.agentId && rosterIds.has(e.agentId)}
          onSelect={() => e.agentId && onSelect(e.agentId)}
          onDismiss={() => onDismiss(e.seq)}
        />
      ))}
    </div>
  )
}

function ConflictCard({e, linkable, onSelect, onDismiss}: {
  e: GwEvent; linkable: boolean; onSelect: () => void; onDismiss: () => void
}) {
  const clone = e.kind === 'clone-suspected'
  const hasPorts = e.requestedPort != null && e.actualPort != null
  return (
    <Banner
      tone={clone ? 'bad' : 'warn'} onDismiss={onDismiss}
      action={linkable ? <Button size="sm" variant="ghost" onClick={onSelect}>View agent</Button> : undefined}
    >
      <div className="min-w-0 space-y-0.5">
        <div className="flex flex-wrap items-center gap-x-2 gap-y-0.5">
          <span className="font-medium text-[var(--text)]">{clone ? 'Possible cloned agent' : 'Port auto-reassigned'}</span>
          {!clone && hasPorts && (
            <span className="font-mono text-[11px] tabular-nums text-[var(--text-3)]">requested {e.requestedPort} → bound {e.actualPort}</span>
          )}
          <span className="ml-auto text-[11px] tabular-nums text-[var(--text-3)]">{fmtTime(e.timeMs)}</span>
        </div>
        <div className="break-words text-[var(--text-2)]">{e.message}</div>
      </div>
    </Banner>
  )
}

/** EventLog: a modest, collapsible tail of the gateway's conflict/auto-fix ring
 * below the roster — timestamp + kind chip + message, newest first. Four states:
 * skeleton while loading, rows, a written empty state, and (at the screen level)
 * the honest-unavailable roster state for a pre-roster daemon. */
function EventLog({events}: {events: GwEvent[] | null}) {
  return (
    <Disclosure label="Gateway events" hint="Conflicts and automatic fixes, newest first">
      {events === null ? (
        <div className="space-y-2" aria-busy="true">
          {[0, 1, 2].map(i => (
            <div key={i} className="flex items-center gap-2">
              <Skeleton className="h-3 w-14 rounded-full" />
              <Skeleton className="h-4 w-16 rounded-[var(--r-sm)]" />
              <Skeleton className="h-3 flex-1 rounded-full" />
            </div>
          ))}
        </div>
      ) : events.length === 0 ? (
        <EmptyState icon={<IconLogs size={24} />} title="No gateway events yet"
          hint="Port reassignments and clone warnings will appear here as they happen." />
      ) : (
        <div className="space-y-0.5">
          {[...events].reverse().slice(0, 60).map(e => <EventRow key={e.seq} e={e} />)}
        </div>
      )}
    </Disclosure>
  )
}

function EventRow({e}: {e: GwEvent}) {
  const clone = e.kind === 'clone-suspected'
  return (
    <div className="flex items-baseline gap-2 py-1 text-[12px]">
      <span className="shrink-0 font-mono tabular-nums text-[var(--text-3)]">{fmtTime(e.timeMs)}</span>
      <span className="shrink-0"><Badge tone={clone ? 'bad' : 'warn'}>{clone ? 'clone' : 'reassign'}</Badge></span>
      <span className="min-w-0 flex-1 text-[var(--text-2)]">{e.message}</span>
    </div>
  )
}

/** AgentDetail: the drill-in. One machine's identity, its link health, the
 * management controls (rename / scope / revoke), its own bandwidth graph (same
 * instrument as Traffic, scoped to this agent's RRD), and the tunnels + live
 * sessions attributed to it. Offline agents synthesize an unknown live snapshot
 * so the identity/health panels read honestly (— everywhere), while the RRD
 * chart still draws their history. */
function AgentDetail({status, row, onBack, onChanged}: {
  status: UIStatus; row: Row; onBack: () => void; onChanged: () => void
}) {
  const {view, live} = row
  const connected = isConnected(view)
  const h = rowSeg(row)
  const liveOrSynth: AgentUI = live ?? app.AgentUI.createFrom({
    agentId: view.agentId, hostname: view.hostname, lanIps: [], remoteIp: view.remoteIp,
    linkUpSinceMs: connected ? view.linkUpSinceMs : 0, rttMillis: 0, jitterMillis: -1,
    packetLossPct: -1, healthScore: 'unknown', linkBytesIn: 0, linkBytesOut: 0,
    tunnels: view.tunnels, players: 0,
  })
  const tunnels = (status.tunnels ?? []).filter(t => t.agentId === view.agentId)
  const conns = (status.connections ?? []).filter(c => c.agentId === view.agentId)
  const source = useMemo(() => agentHistorySource(view.agentId), [view.agentId])

  return (
    <div className="pf-stagger space-y-8">
      <div>
        <button
          onClick={onBack}
          className="pf-press mb-4 inline-flex items-center gap-1.5 text-sm text-[var(--text-3)] transition-colors hover:text-[var(--text)]"
        >
          <IconArrowRight size={14} className="rotate-180" /> All agents
        </button>
        <PageHeader
          title={displayName(view)}
          subtitle={view.agentId ? (
            <span className="inline-flex items-center gap-1.5">
              <span className="select-text font-mono text-[11px] text-[var(--text-3)]">{view.agentId}</span>
              <CopyIcon text={view.agentId} title="Copy agent ID" />
            </span>
          ) : undefined}
          action={<StatusMark view={view} h={h} />}
        />
      </div>

      <div className="grid grid-cols-12 gap-[var(--grid-gap)]">
        <div className="col-span-12 @3xl:col-span-5"><AgentIdentity agent={liveOrSynth} /></div>
        <div className="col-span-12 @3xl:col-span-7"><AgentHealth agent={liveOrSynth} /></div>
      </div>

      <AgentManagement view={view} onChanged={onChanged} />

      {/* The detail's moment of light (DESIGN rules 5/7): the same bare hero
          graph Traffic uses, drawing THIS agent's per-agent RRD. It breaks the
          surrounding card rhythm so the drill-in isn't a stack of frost panels. */}
      <BandwidthPanel hero useHistory={source} vtName={null} historyUnsupported={status.historyUnsupported} />

      <div className="grid grid-cols-12 gap-[var(--grid-gap)]">
        <div className="col-span-12 @4xl:col-span-6"><AgentTunnels tunnels={tunnels} /></div>
        <div className="col-span-12 @4xl:col-span-6"><AgentSessions conns={conns} /></div>
      </div>
    </div>
  )
}

/** AgentManagement: the gateway's controls over one agent — rename, scope, and
 * revoke. Each write follows the write-then-repoll loop (persist, then re-poll
 * ListAgents so the roster and this view refresh). Revoke is guarded by a Modal. */
function AgentManagement({view, onChanged}: {view: AgentView; onChanged: () => void}) {
  const [nickname, setNickname] = useState(view.nickname || '')
  const [ports, setPorts] = useState((view.scopePorts ?? []).join(', '))
  const [tuns, setTuns] = useState((view.scopeTunnels ?? []).join(', '))
  const [savingName, setSavingName] = useState(false)
  const [savingScope, setSavingScope] = useState(false)
  const [confirming, setConfirming] = useState(false)
  const [revoking, setRevoking] = useState(false)
  const [err, setErr] = useState('')

  // Reseed the fields when we're pointed at a different agent, or when the
  // persisted values actually change. The deps are the serialized VALUES, not
  // the array references — the roster re-polls every few seconds with fresh
  // objects, and depending on the arrays would wipe an in-progress edit each poll.
  useEffect(() => {
    setNickname(view.nickname || '')
    setPorts((view.scopePorts ?? []).join(', '))
    setTuns((view.scopeTunnels ?? []).join(', '))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [view.agentId, view.nickname, (view.scopePorts ?? []).join(','), (view.scopeTunnels ?? []).join(',')])

  const parsePorts = (s: string): number[] =>
    s.split(',').map(x => parseInt(x.trim(), 10)).filter(nu => nu > 0 && nu < 65536)
  const parseTuns = (s: string): string[] => s.split(',').map(x => x.trim()).filter(Boolean)

  const nameChanged = nickname.trim() !== (view.nickname || '')
  const scopeChanged = parsePorts(ports).join(',') !== (view.scopePorts ?? []).join(',')
    || parseTuns(tuns).join(',') !== (view.scopeTunnels ?? []).join(',')

  const saveName = async () => {
    setSavingName(true); setErr('')
    try { await RenameAgent(view.agentId, nickname.trim()); onChanged() }
    catch (e) { setErr(String(e)) } finally { setSavingName(false) }
  }
  const saveScope = async () => {
    setSavingScope(true); setErr('')
    try { await SetAgentScope(view.agentId, parsePorts(ports), parseTuns(tuns)); onChanged() }
    catch (e) { setErr(String(e)) } finally { setSavingScope(false) }
  }
  const doRevoke = async () => {
    setRevoking(true); setErr('')
    try { await RevokeAgent(view.agentId); setConfirming(false); onChanged() }
    catch (e) { setErr(String(e)); setConfirming(false) } finally { setRevoking(false) }
  }

  return (
    <Card label="Management">
      <div className="space-y-4">
        {err && <ErrorBanner message={err} onDismiss={() => setErr('')} />}

        <Field label="Nickname" hint="A friendly name for this machine. Shown as its title across the console.">
          <div className="flex items-center gap-2">
            <div className="min-w-0 flex-1">
              <TextInput value={nickname} onChange={setNickname} placeholder={view.hostname || 'Name this agent'} onEnter={saveName} />
            </div>
            <Button variant="subtle" onClick={saveName} disabled={savingName || !nameChanged}>{savingName ? 'Saving…' : 'Save'}</Button>
          </div>
        </Field>

        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
          <Field label="Allowed ports" hint="Comma-separated public ports this agent may bind. Empty = unrestricted.">
            <TextInput value={ports} onChange={setPorts} placeholder="e.g. 25565, 25566" mono onEnter={saveScope} />
          </Field>
          <Field label="Allowed tunnel IDs" hint="Comma-separated tunnel IDs. Empty = any tunnel.">
            <TextInput value={tuns} onChange={setTuns} placeholder="Any tunnel" mono onEnter={saveScope} />
          </Field>
        </div>
        <div className="flex items-center justify-between gap-3">
          <span className="min-w-0 truncate text-[11px] text-[var(--text-3)]" title={`Scope: ${scopeText(view)}`}>Current scope: {scopeText(view)}</span>
          <Button variant="subtle" onClick={saveScope} disabled={savingScope || !scopeChanged}>{savingScope ? 'Saving…' : 'Update scope'}</Button>
        </div>

        <div className="flex items-center justify-between gap-4 border-t border-[var(--border)] pt-3">
          <div className="min-w-0">
            <div className="text-sm font-medium text-[var(--text)]">{view.revoked ? 'Access revoked' : 'Revoke access'}</div>
            <div className="mt-0.5 text-xs leading-relaxed text-[var(--text-3)]">
              {view.revoked
                ? 'This agent has been revoked. Its next connection is refused.'
                : 'Removes the agent and drops its live session. The next connect is refused with “access revoked”.'}
            </div>
          </div>
          {!view.revoked && <Button variant="danger" onClick={() => setConfirming(true)}>Revoke</Button>}
        </div>

        <div className="text-[11px] text-[var(--text-3)]">
          Enrolled {fmtDate(view.issuedAtMs)} · {view.enrolled ? 'Ed25519 identity' : 'shared token'}
        </div>
      </div>

      {confirming && (
        <Modal
          title="Revoke agent" onClose={() => setConfirming(false)}
          footer={<>
            <Button variant="ghost" onClick={() => setConfirming(false)}>Cancel</Button>
            <Button variant="danger" onClick={doRevoke} disabled={revoking}>{revoking ? 'Revoking…' : 'Revoke access'}</Button>
          </>}
        >
          <p className="text-sm leading-relaxed text-[var(--text-2)]">
            Revoking <b className="text-[var(--text)]">{displayName(view)}</b> removes it from the gateway and immediately
            drops its live session. Its next connection attempt is refused with “access revoked”. To restore it, enroll it
            again with a fresh pairing code.
          </p>
        </Modal>
      )}
    </Card>
  )
}

/** AgentIdentity: where this machine is — its remote address prominent, LAN
 * quiet, with link uptime and lifetime link data. Wears the agent role swatch. */
function AgentIdentity({agent}: {agent: AgentUI}) {
  const lan = (agent.lanIps ?? []).filter(Boolean)
  return (
    <Card pad={false} className="h-full">
      <div className="flex items-start gap-3.5 p-4">
        <Emblem role="agent" size={40} fixed glow={seg(agent) !== 'bad' && seg(agent) !== 'unknown'} />
        <div className="min-w-0 flex-1 space-y-2.5">
          <div>
            <Overline>Remote address</Overline>
            <div className="mt-0.5 flex items-center gap-1.5">
              <span className="select-text truncate font-mono text-[14px] font-semibold text-[var(--text)]">{agent.remoteIp || '—'}</span>
              {agent.remoteIp && <CopyIcon text={agent.remoteIp} title="Copy remote address" />}
            </div>
          </div>
          {lan.length > 0 && (
            <div>
              <Overline>LAN</Overline>
              <div className="mt-0.5 truncate font-mono text-[12px] text-[var(--text-2)]">{lan.join(', ')}</div>
            </div>
          )}
          <div className="flex gap-6 pt-0.5">
            <div>
              <Overline>Link uptime</Overline>
              <div className="mt-0.5 text-sm font-medium tabular-nums text-[var(--text)]">
                {agent.linkUpSinceMs > 0 ? <Uptime since={agent.linkUpSinceMs} /> : '—'}
              </div>
            </div>
            <div>
              <Overline>Link data</Overline>
              <div className="mt-0.5 text-sm font-medium tabular-nums text-[var(--text)]">
                {agent.linkBytesIn + agent.linkBytesOut > 0 ? fmtBytes(agent.linkBytesIn + agent.linkBytesOut) : '—'}
              </div>
            </div>
          </div>
        </div>
      </div>
    </Card>
  )
}

/** AgentHealth: this agent's link-health rollup — the verdict beside jitter,
 * loss and round trip as explicit numbers. Mirrors Overview's HealthPanel. */
function AgentHealth({agent}: {agent: AgentUI}) {
  const h = seg(agent)
  const c = segColor[h]
  return (
    <Card pad={false} className="h-full">
      <div className="flex flex-wrap items-center gap-x-6 gap-y-4 p-5">
        <div className="flex items-center gap-3">
          <span
            className="grid h-12 w-12 place-items-center rounded-[var(--r-lg)] border transition-all duration-500"
            style={{
              color: c,
              borderColor: `color-mix(in srgb, ${c} 40%, var(--border))`,
              background: `color-mix(in srgb, ${c} 10%, transparent)`,
              boxShadow: h !== 'unknown' ? `0 0 24px -6px color-mix(in srgb, ${c} 60%, transparent)` : undefined,
            }}
          >
            <IconActivity size={22} />
          </span>
          <div>
            <Overline>Link health</Overline>
            <div className="mt-0.5"><Badge tone={HEALTH_TONE[h]}>{HEALTH_LABEL[h]}</Badge></div>
          </div>
        </div>
        <div className="grid min-w-0 flex-1 grid-cols-3 gap-3">
          <Metric label="Round trip" value={fmtRttMs(agent.rttMillis)} tone="neutral" />
          <Metric label="Jitter" value={fmtMs(agent.jitterMillis)} tone={jitterTone(agent.jitterMillis)} />
          <Metric label="Packet loss" value={fmtPct(agent.packetLossPct)} tone={lossTone(agent.packetLossPct)} />
        </div>
      </div>
    </Card>
  )
}

/** AgentTunnels: the ports this agent has bound, with each local server's state. */
function AgentTunnels({tunnels}: {tunnels: app.TunnelUI[]}) {
  return (
    <Card label="Tunnels" pad={false}
      action={tunnels.length > 0 ? <div className="pr-1"><Badge>{tunnels.length}</Badge></div> : undefined}>
      {tunnels.length === 0 ? (
        <div className="px-5 pb-5"><EmptyState icon={<IconTunnels size={24} />} title="No tunnels bound"
          hint="This agent hasn't registered any ports yet." /></div>
      ) : (
        <div className="divide-y divide-[var(--border)]">
          {tunnels.map(t => {
            const capped = (t.bandwidthLimitMbps ?? 0) > 0
            const scoped = capped && (t.bandwidthLimitScope || 'combined') !== 'combined'
            const sub = `port ${t.publicPort > 0 ? t.publicPort : '—'}`
              + (capped ? ` · ${t.bandwidthLimitMbps} Mbps` : '')
              + (scoped ? ` · ${scopeLabel(t.bandwidthLimitScope)}` : '')
            return (
              <div key={t.id} className="flex items-center gap-3 px-5 py-2.5">
                <div className="pf-control grid h-8 w-8 shrink-0 place-items-center rounded-[var(--r-md)] bg-[var(--input-bg)] text-[var(--text-2)]"><IconServer size={15} /></div>
                <div className="min-w-0 flex-1">
                  <div className="truncate text-sm font-medium">{t.name}</div>
                  <div className="truncate font-mono text-[11px] text-[var(--text-3)]" title={sub}>{sub}</div>
                </div>
                <Badge tone={t.localKnown ? (t.localUp ? 'good' : 'bad') : 'neutral'}>
                  {t.localKnown ? (t.localUp ? 'Server up' : 'Server down') : 'Unknown'}
                </Badge>
              </div>
            )
          })}
        </div>
      )}
    </Card>
  )
}

/** AgentSessions: the live player connections attributed to this agent. */
function AgentSessions({conns}: {conns: app.ConnUI[]}) {
  return (
    <Card label="Live sessions" pad={false}
      action={<div className="pr-1"><Badge tone={conns.length ? 'good' : 'neutral'}>{conns.length} live</Badge></div>}>
      {conns.length === 0 ? (
        <div className="px-5 pb-5"><EmptyState icon={<IconUsers size={24} />} title="No players connected"
          hint="Active player sessions on this agent appear here." /></div>
      ) : (
        <div className="divide-y divide-[var(--border)]">
          {conns.map(c => (
            <div key={c.id} className="flex items-center gap-3 px-5 py-2.5">
              <div className="min-w-0 flex-1">
                <div className="truncate text-sm font-medium">{c.playerName || c.clientAddr}</div>
                <div className="truncate font-mono text-[11px] text-[var(--text-3)]">{c.tunnelName} · {c.clientAddr}</div>
              </div>
              <div className="shrink-0 text-right">
                <div className="font-mono text-xs tabular-nums text-[var(--text-2)]">{fmtBytes(c.bytesIn + c.bytesOut)}</div>
                {hasRtt(c.rttMs) && <div className="font-mono text-[11px] tabular-nums text-[var(--text-3)]">{Math.round(c.rttMs)} ms</div>}
              </div>
            </div>
          ))}
        </div>
      )}
    </Card>
  )
}
