import {ReactNode, useCallback, useEffect, useMemo, useRef, useState} from 'react'
import {
  AgentBandwidthHistory, AgentDesiredConfig, ApproveAgentProposal, GatewayEvents, IssuePairingCode,
  ListAgents, ListInvites, ProposeAgentConfig, ReclaimPort, RejectAgentProposal, RenameAgent,
  RevokeAgent, RevokeInvite, SetAgentScope,
} from '../../wailsjs/go/app/App'
import {app, control, engine, gateway} from '../../wailsjs/go/models'
import {PauseControl, PausedBadge, pausedLabel} from '../components/PauseControl'
import {proposalAge, ProposalDiff, specsOf} from '../consent'
import {BandwidthPanel} from '../components/BandwidthChart'
import {Emblem} from '../components/Emblem'
import {
  Badge, Banner, Button, Card, Codebox, CopyButton, CopyIcon, Disclosure, EmptyState, ErrorBanner,
  Field, Modal, MonoChip, Overline, PageHeader, SegmentedControl, Skeleton, StatusDot, TextInput,
} from '../components/ui'
import {
  IconActivity, IconAgents, IconArrowRight, IconChevronRight, IconClock, IconKey, IconLogs,
  IconSearch, IconServer, IconShield, IconTunnels, IconUsers,
} from '../components/icons'
import {HistoryResult, RANGES, RangeKey} from '../history'
import {usePolled} from '../hooks'
import {fmtBytes, fmtUptime, hasRtt, scopeLabel, TRANSPORT_LABEL, UIStatus, worstHealth} from '../state'

type AgentUI = app.AgentUI
type AgentView = gateway.AgentView
type InviteView = engine.InviteView
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

// Gateway event kinds (mirrors internal/gateway/conflict.go). Only the OPEN ones are
// resolvable conflicts and become cards; port-reclaimed is a completed action that
// only lands in the log. EVENT_BADGE labels every kind the log can show.
const CONFLICT_KINDS = new Set(['port-reassigned', 'clone-suspected'])
const EVENT_BADGE: Record<string, {label: string; tone: 'warn' | 'bad' | 'good'}> = {
  'port-reassigned': {label: 'reassign', tone: 'warn'},
  'clone-suspected': {label: 'clone', tone: 'bad'},
  'port-reclaimed': {label: 'reclaim', tone: 'good'},
  'proposal-declined': {label: 'declined', tone: 'warn'},
  'agent-enrolled': {label: 'enrolled', tone: 'good'},
}

// NEW_AGENT_MS is the roster's "New" badge window: a freshly-enrolled agent wears
// it this long after its IssuedAt, so a machine that joined while nobody was
// watching still announces itself on the next look.
const NEW_AGENT_MS = 24 * 60 * 60 * 1000
const isFresh = (v: AgentView): boolean => !v.revoked && v.issuedAtMs > 0 && Date.now() - v.issuedAtMs < NEW_AGENT_MS

// A single-use invite's remaining lifetime, as a countdown phrase.
const fmtTtl = (ms: number): string => {
  const s = Math.max(0, Math.round(ms / 1000))
  if (s < 90) return `${s} s`
  const m = Math.round(s / 60)
  if (m < 90) return `${m} min`
  return `${Math.round(m / 60)} h`
}

const jitterTone = (v: number): Seg => (v < 0 ? 'unknown' : v > 100 ? 'bad' : v > 30 ? 'warn' : 'good')
const lossTone = (v: number): Seg => (v < 0 ? 'unknown' : v > 5 ? 'bad' : v > 1 ? 'warn' : 'good')
const fmtMs = (v: number): string => (v < 0 ? '—' : `${v.toFixed(1)} ms`)
const fmtPct = (v: number): string => (v < 0 ? '—' : `${v.toFixed(v > 0 && v < 10 ? 1 : 0)}%`)
const fmtRttMs = (v: number): string => (hasRtt(v) ? `${Math.round(v)} ms` : '—')
const fmtDate = (ms: number): string => (ms > 0 ? new Date(ms).toLocaleDateString(undefined, {year: 'numeric', month: 'short', day: 'numeric'}) : '—')
const fmtTime = (ms: number): string => new Date(ms).toLocaleTimeString(undefined, {hour12: false})

// The roster's "Update" badge fires only when the agent build is provably OLDER
// than the gateway's, compared by leading major.minor.patch. A version we can't
// parse — a missing one (offline agent, or a legacy build that sent none) or an
// odd string — yields no verdict, so we never paint a fake "Update" or imply a
// stale build is current (GUI contract: unknown is a sentinel, never a fake zero).
function parseVer(v: string): [number, number, number] | null {
  const m = /^\s*v?(\d+)\.(\d+)\.(\d+)/.exec(v || '')
  return m ? [+m[1], +m[2], +m[3]] : null
}
function updateAvailable(agentVer: string, gatewayVer: string): boolean {
  const a = parseVer(agentVer), g = parseVer(gatewayVer)
  if (!a || !g) return false
  for (let i = 0; i < 3; i++) if (a[i] !== g[i]) return a[i] < g[i]
  return false
}

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

// Scope as a human phrase. Both empty = the holder may bind anything. Widened
// past AgentView so invite rows share the exact wording (one concept, one term).
function scopeText(v: {scopePorts?: number[]; scopeTunnels?: string[]}): string {
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
// immediate re-poll (the key changes) without waiting out the interval. The
// invites cache mirrors it — invite rows live in the same wall.
const rosterCache = new Map<string, AgentView[]>()
const invitesCache = new Map<string, InviteView[]>()

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

// Per-agent gateway-authoritative config for the drill-in panel. Fetched once per
// drill-in and cached for an instant re-open; config is essentially static in Batch A
// (the editing path is Batch B), so it doesn't poll. `loading` separates the skeleton
// from a resolved-but-empty set, and `err` is the honest-unavailable state (a
// pre-roster daemon that lacks the binding) — usePolled swallows errors and can't tell
// those apart, so this is its own hook like useGatewayEvents.
// Polled, not one-shot: under config-consent a proposal can arrive from the agent at
// any moment and a decision on either side moves the generation, so the panel has to
// notice both without a reload. Cached by agentId so re-opening a drill-in paints
// instantly; refresh() forces the re-read after this operator decides something.
const agentConfigCache = new Map<string, gateway.AgentConfigView>()
function useAgentConfig(agentId: string, pollMs = 3000): {
  cfg: gateway.AgentConfigView | null; loading: boolean; err: boolean; refresh: () => void
} {
  const [cfg, setCfg] = useState<gateway.AgentConfigView | null>(() => agentConfigCache.get(agentId) ?? null)
  const [loading, setLoading] = useState(!agentConfigCache.has(agentId))
  const [err, setErr] = useState(false)
  const [nonce, setNonce] = useState(0)
  const refresh = useCallback(() => setNonce(n => n + 1), [])
  useEffect(() => {
    let alive = true
    const cached = agentConfigCache.get(agentId)
    setCfg(cached ?? null); setLoading(!cached); setErr(false)
    const tick = () => {
      AgentDesiredConfig(agentId)
        .then(v => { if (!alive) return; agentConfigCache.set(agentId, v); setCfg(v); setLoading(false); setErr(false) })
        .catch(() => { if (alive) { setErr(true); setLoading(false) } })
    }
    tick()
    const id = setInterval(tick, pollMs)
    return () => { alive = false; clearInterval(id) }
  }, [agentId, pollMs, nonce])
  return {cfg, loading, err, refresh}
}

/**
 * Agents (gateway role): the fleet — a wall of machine cards, the gateway
 * analogue of the Players wall of faces. The roster now comes from ListAgents
 * (every enrolled agent, online or off); live health/RTT joins in from
 * status.agents for the connected ones. Selecting
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
  const [issuing, setIssuing] = useState(false)

  const isGateway = status.role === 'gateway'
  const rosterKey = isGateway ? `roster:${nonce}` : null
  const roster = usePolled(rosterCache, rosterKey, () => ListAgents(), 4000)
  const invites = usePolled(invitesCache, isGateway ? `invites:${nonce}` : null, () => ListInvites(), 4000)
  const events = useGatewayEvents(isGateway)

  // A mutation re-polls at once: carry the current data into the next key so the
  // poll refreshes without flashing the skeleton, then bump the nonce.
  const refreshRoster = () => {
    if (roster && rosterKey) rosterCache.set(`roster:${nonce + 1}`, roster)
    if (invites && rosterKey) invitesCache.set(`invites:${nonce + 1}`, invites)
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
  // resurfaces even after an older one was dismissed. Only the OPEN conflict kinds
  // become cards — a port-reclaimed record is a completed action and belongs in the
  // event log, not in a card asking to resolve it.
  const conflicts = useMemo(() => {
    if (!events) return []
    const byKey = new Map<string, GwEvent>()
    for (const e of events) {
      if (!CONFLICT_KINDS.has(e.kind)) continue
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
  // A single-use invite that just expired drops out client-side between polls,
  // so a dead code never lingers as a live-looking row.
  const liveInvites = (invites ?? []).filter(iv => iv.expiresAtMs === 0 || iv.expiresAtMs > Date.now())

  return (
    <div className="pf-stagger space-y-8">
      <Header action={<Button size="sm" onClick={() => setIssuing(true)}>Issue pairing code</Button>} />
      {issuing && <IssuePairingModal onClose={() => { setIssuing(false); refreshRoster() }} />}

      <ConflictCards
        conflicts={conflicts} roster={roster ?? []} tunnels={status.tunnels ?? []} rosterIds={rosterIds}
        onSelect={setSelected} onDismiss={dismiss} onReclaimed={refreshRoster}
      />

      {roster === null ? (
        <RosterSkeleton density={density} />
      ) : n === 0 && liveInvites.length === 0 ? (
        <Card><EmptyState icon={<IconAgents size={28} />} title="No agents enrolled yet"
          hint="Issue a pairing code (here, on Overview, or in the setup wizard) and share it with a machine. Every agent that enrolls with it appears here — online or off." /></Card>
      ) : n === 0 ? (
        <div className="pf-stagger grid grid-cols-1 gap-[var(--grid-gap)] @min-[52rem]:grid-cols-2 @min-[80rem]:grid-cols-3">
          {liveInvites.map(iv => <InviteCard key={iv.handle} invite={iv} onChanged={refreshRoster} />)}
        </div>
      ) : (
        <>
          <FleetSummary rows={rows} invites={liveInvites.length} />

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
              {sorted.map(r => <AgentCard key={r.view.agentId} row={r} gwVersion={status.version} onOpen={() => setSelected(r.view.agentId)} />)}
              {liveInvites.map(iv => <InviteCard key={iv.handle} invite={iv} onChanged={refreshRoster} />)}
            </div>
          ) : (
            <>
              <Card pad={false}><AgentList rows={sorted} gwVersion={status.version} onOpen={setSelected} /></Card>
              {liveInvites.length > 0 && (
                <div className="pf-stagger grid grid-cols-1 gap-[var(--grid-gap)] @min-[52rem]:grid-cols-2 @min-[80rem]:grid-cols-3">
                  {liveInvites.map(iv => <InviteCard key={iv.handle} invite={iv} onChanged={refreshRoster} />)}
                </div>
              )}
            </>
          )}
        </>
      )}

      <EventLog events={events} />
    </div>
  )
}

function Header({action}: {action?: ReactNode}) {
  return <PageHeader eyebrow="fleet" title="Agents" subtitle="Every machine enrolled with this gateway." action={action} />
}

/** IssuePairingModal: mint an enrollment code to hand to a new machine — the roster
 * analogue of the wizard's GatewayLive. A mode toggle picks the lifetime (single-use
 * expires in 10 min; reusable never expires until revoked), optional scope restricts
 * what the enrolling agent may bind, and the code renders with a copy button. One code
 * shows at a time; changing any setting invalidates it so the shown code's lifetime
 * hint can never lie about how it was issued, and "New code" re-issues. States:
 * pre-issue form, generating, issued code, and an issue error (honest unavailable). */
function IssuePairingModal({onClose}: {onClose: () => void}) {
  const [reusable, setReusable] = useState(false)
  const [label, setLabel] = useState('')
  const [ports, setPorts] = useState('')
  const [tuns, setTuns] = useState('')
  const [code, setCode] = useState('')
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')

  const parsePorts = (s: string): number[] => s.split(',').map(x => parseInt(x.trim(), 10)).filter(nu => nu > 0 && nu < 65536)
  const parseTuns = (s: string): string[] => s.split(',').map(x => x.trim()).filter(Boolean)

  // Any setting change drops the shown code: it was minted with the old
  // mode/scope/label, so keeping it visible would misdescribe how it was issued.
  useEffect(() => { setCode(''); setErr('') }, [reusable, label, ports, tuns])

  const issue = async () => {
    setBusy(true); setErr(''); setCode('')
    // Single-use expires in 10 min; a reusable code lives until revoked (ttl 0).
    try { setCode(await IssuePairingCode(reusable, reusable ? 0 : 600, parsePorts(ports), parseTuns(tuns), label.trim())) }
    catch (e) { setErr(String(e)) } finally { setBusy(false) }
  }

  return (
    <Modal title="Issue pairing code" onClose={onClose} wide
      footer={<Button variant="ghost" onClick={onClose}>Done</Button>}>
      <div className="space-y-4">
        <p className="text-sm leading-relaxed text-[var(--text-2)]">
          Share this code with a machine. Every agent that enrolls with it joins this gateway's roster and proves a
          durable identity you can scope and revoke.
        </p>

        <Field label="Type" hint={reusable
          ? 'Enrolls multiple agents; lives until you revoke it.'
          : 'Enrolls one agent, then expires. Valid for 10 minutes.'}>
          <SegmentedControl<'single' | 'reusable'>
            value={reusable ? 'reusable' : 'single'}
            onChange={v => setReusable(v === 'reusable')}
            options={[{value: 'single', label: 'Single-use'}, {value: 'reusable', label: 'Reusable'}]}
          />
        </Field>

        <Field label="Label" hint="Names this code in the roster — who it's for, or what it's for.">
          <TextInput value={label} onChange={setLabel} placeholder="e.g. friends" onEnter={issue} />
        </Field>

        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
          <Field label="Allowed ports" hint="Comma-separated public ports this agent may bind. Empty = unrestricted.">
            <TextInput value={ports} onChange={setPorts} placeholder="e.g. 25565, 25566" mono onEnter={issue} />
          </Field>
          <Field label="Allowed tunnel IDs" hint="Comma-separated tunnel IDs. Empty = any tunnel.">
            <TextInput value={tuns} onChange={setTuns} placeholder="Any tunnel" mono onEnter={issue} />
          </Field>
        </div>

        {err ? (
          <ErrorBanner message={err} onDismiss={() => setErr('')} />
        ) : code ? (
          <div className="space-y-2">
            <Codebox text={code} action={<CopyButton text={code} />} />
            <div className="flex items-center justify-between gap-3">
              <span className="min-w-0 text-xs text-[var(--text-3)]">
                {reusable
                  ? 'Reusable — a standing code, listed in the roster below until you revoke it.'
                  : 'Single-use — expires once one agent enrolls, or after 10 minutes.'}
              </span>
              <Button variant="ghost" size="sm" onClick={issue} disabled={busy}>New code</Button>
            </div>
          </div>
        ) : busy ? (
          <div className="pf-well px-3 py-2.5 text-sm text-[var(--text-3)]">Generating code…</div>
        ) : (
          <Button onClick={issue} disabled={busy}>Issue code</Button>
        )}
      </div>
    </Modal>
  )
}

/** FleetSummary: the page's one hero figure (agent count) with the connected
 * fleet's worst-of health and the rolled-up totals — type on whitespace, never
 * another card (DESIGN rule 5). Only rendered for a non-empty roster. */
function FleetSummary({rows, invites}: {rows: Row[]; invites: number}) {
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
        <span className="font-display text-metric-hero font-bold leading-none tabular-nums">{n}</span>
        <span className="text-sm text-[var(--text-2)]">{n === 1 ? 'agent' : 'agents'}</span>
      </div>
      {online > 0
        ? <span className="self-center"><StatusDot state={worst} label={verdict} pulse={worst === 'good'} /></span>
        : <span className="self-center"><StatusDot state="unknown" label="None online" /></span>}
      <span className="text-sm tabular-nums text-[var(--text-3)]">{online} online</span>
      {revoked > 0 && <span className="text-sm tabular-nums text-[var(--text-3)]">{revoked} revoked</span>}
      <span className="text-sm tabular-nums text-[var(--text-3)]">{tunnels} tunnel{tunnels === 1 ? '' : 's'}</span>
      <span className="text-sm tabular-nums text-[var(--text-3)]">{plrs} player{plrs === 1 ? '' : 's'} online</span>
      {invites > 0 && <span className="text-sm tabular-nums text-[var(--text-3)]">{invites} live invite{invites === 1 ? '' : 's'}</span>}
    </div>
  )
}

/** StatusMark: one agent's connectivity verdict — health dot when connected, a
 * quiet "Offline" dot when enrolled-but-away, a Revoked badge when cut off. An
 * `update` flag (a connected agent behind the gateway's build) adds a warn-tone
 * "Update" badge alongside — color IS the signal, so it's a status tone, not accent.
 *
 * A pending config proposal rides here too, because the roster is where an operator
 * is looking: a proposal awaiting *them* is a call to act ("Review"), while one
 * awaiting the agent is a quiet state ("Proposed"). Without this a waiting agent is
 * indistinguishable from an idle one until you drill into it — which is exactly the
 * fleet case consent exists for. Both are false for an offline agent (pending state
 * is session-scoped), so neither can go stale. */
function StatusMark({view, h, update}: {view: AgentView; h: Seg; update?: boolean}) {
  const fresh = isFresh(view)
  const mark = view.revoked
    ? <Badge tone="bad">Revoked</Badge>
    : isConnected(view)
      ? <StatusDot state={h} label={HEALTH_LABEL[h]} pulse />
      : <StatusDot state="unknown" label="Offline" />
  if (!update && !fresh && !view.pendingInbound && !view.pendingOutbound && !view.pausedAll) return mark
  return (
    <span className="inline-flex items-center gap-2">
      {mark}
      {view.pausedAll && <Badge tone="warn">Paused</Badge>}
      {view.pendingInbound && <Badge tone="warn">Review</Badge>}
      {view.pendingOutbound && !view.pendingInbound && <Badge>Proposed</Badge>}
      {update && <Badge tone="warn">Update</Badge>}
      {fresh && !update && !view.pendingInbound && <Badge tone="good">New</Badge>}
    </span>
  )
}

/** Metric: tier-3 stat — type on whitespace with a hairline lead, no box; the
 * status tone lives on the numeral. Shared by the cards and the detail health. */
function Metric({label, value, tone}: {label: string; value: string; tone: Seg | 'neutral'}) {
  const c = tone === 'neutral' ? 'var(--text)' : segColor[tone]
  return (
    <div className="min-w-0 border-l border-[var(--hairline)] pl-3">
      <Overline>{label}</Overline>
      <div className="font-display mt-1 truncate text-metric font-bold leading-[var(--lh-tight)] tabular-nums" style={{color: c}} title={value}>{value}</div>
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
function AgentCard({row, gwVersion, onOpen}: {row: Row; gwVersion: string; onOpen: () => void}) {
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
          <div className="mt-0.5 truncate font-mono text-sub text-[var(--text-3)]">{view.agentId || '—'}</div>
        </div>
        <div className="shrink-0"><StatusMark view={view} h={h} update={updateAvailable(view.version, gwVersion)} /></div>
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

      <div className="flex flex-wrap items-center gap-x-4 gap-y-1 border-t border-[var(--border)] pt-3 text-sub text-[var(--text-3)]">
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

/** InviteCard: a live pairing code holding a seat in the wall — the machine it
 * will admit isn't here yet, so the card wears the invite variant of the frost
 * (dashed rim, no Signal Glass; the roster stays the screen's one identity
 * surface). A single-use invite shows its countdown and vanishes when consumed —
 * the enrolled agent's card takes its place on the next poll (the swap rides the
 * roster's pf-stagger/pf-fade CSS, which the reduced-motion kill switch zeroes).
 * Reusable invites persist until revoked, wearing their enrolled count. */
function InviteCard({invite, onChanged}: {invite: InviteView; onChanged: () => void}) {
  const [confirming, setConfirming] = useState(false)
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')
  const left = invite.expiresAtMs > 0 ? invite.expiresAtMs - Date.now() : 0
  const scope = scopeText(invite)
  const revoke = () => {
    setBusy(true); setErr('')
    RevokeInvite(invite.handle).then(onChanged)
      .catch(e => setErr(String(e?.message ?? e)))
      .finally(() => setBusy(false))
  }
  return (
    <div className="pf-card pf-invite flex flex-col gap-4 p-4">
      <div className="flex items-start gap-3">
        <div className="pf-control grid h-[38px] w-[38px] shrink-0 place-items-center rounded-[var(--r-md)] bg-[var(--input-bg)] text-[var(--text-2)]"><IconKey size={17} /></div>
        <div className="min-w-0 flex-1">
          <div className="truncate font-semibold">{invite.label || 'Pairing code'}</div>
          <div className="mt-0.5 truncate font-mono text-sub text-[var(--text-3)]">invite {invite.handle}</div>
        </div>
        <div className="shrink-0">{invite.reusable ? <Badge>Reusable</Badge> : <Badge tone="warn">Single-use</Badge>}</div>
      </div>

      <div className="min-w-0 border-l border-[var(--hairline)] pl-3">
        <Overline>Waiting to enroll</Overline>
        <div className="mt-1 truncate text-sm text-[var(--text-2)]">
          {invite.reusable
            ? invite.uses > 0
              ? `${invite.uses} agent${invite.uses === 1 ? '' : 's'} enrolled · stays valid until revoked`
              : 'Any machine with this code can join, until you revoke it'
            : left > 0 ? `Expires in ${fmtTtl(left)}, or when one agent enrolls` : 'Expires when one agent enrolls'}
        </div>
      </div>

      <div className="flex flex-wrap items-center gap-x-4 gap-y-1 border-t border-[var(--border)] pt-3 text-sub text-[var(--text-3)]">
        <span className="inline-flex min-w-0 items-center gap-1.5"><IconShield size={13} /> <span className="truncate">{scope}</span></span>
        <span className="ml-auto inline-flex shrink-0 items-center gap-1.5">
          <CopyButton text={invite.code} size="sm" label="Copy code" />
          {confirming ? (
            <span className="pf-fade inline-flex items-center gap-1.5">
              <Button variant="ghost" size="sm" onClick={() => setConfirming(false)}>Keep</Button>
              <Button variant="danger" size="sm" disabled={busy} onClick={revoke}>{busy ? 'Revoking…' : 'Revoke'}</Button>
            </span>
          ) : (
            <Button variant="ghost" size="sm" onClick={() => setConfirming(true)}>Revoke</Button>
          )}
        </span>
      </div>
      {err && <div className="text-sub text-[var(--bad)]">{err}</div>}
    </div>
  )
}

/** AgentList: the compact density — one flush row per agent, extra columns
 * revealed as the container widens (@container), so a wide fleet reads as a
 * dense table and a narrow one stays legible. */
function AgentList({rows, gwVersion, onOpen}: {rows: Row[]; gwVersion: string; onOpen: (id: string) => void}) {
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
              <div className="truncate font-mono text-sub text-[var(--text-3)]">{view.agentId || '—'}</div>
            </div>
            <div className="hidden w-28 shrink-0 @min-[40rem]:block"><StatusMark view={view} h={h} update={updateAvailable(view.version, gwVersion)} /></div>
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

/** ConflictCards: the gateway's unresolved conflicts, surfaced above the roster. Each
 * card explains the conflict and links to the contesting agent; a port clash also
 * offers the one-click reclaim, but only when the gateway can actually make the trade
 * (see incumbentOf). Nothing shows when there are none (no empty box). */
function ConflictCards({conflicts, roster, tunnels, rosterIds, onSelect, onDismiss, onReclaimed}: {
  conflicts: GwEvent[]; roster: AgentView[]; tunnels: app.TunnelUI[]; rosterIds: Set<string>
  onSelect: (id: string) => void; onDismiss: (seq: number) => void; onReclaimed: () => void
}) {
  if (conflicts.length === 0) return null
  const byId = new Map(roster.map(v => [v.agentId, v]))
  const eligible = (id?: string): boolean => {
    const v = id ? byId.get(id) : undefined
    return !!v && !v.revoked
  }
  // The agent holding the contested port right now — non-null only when the reclaim
  // is the gateway's to make: both parties are still in the roster (not revoked) and
  // the holder is somebody else. We don't offer an action that can't work.
  const incumbentOf = (e: GwEvent): AgentView | null => {
    if (e.kind !== 'port-reassigned' || !e.requestedPort || !e.tunnelId) return null
    if (!eligible(e.agentId)) return null
    const holderId = tunnels.find(t => t.publicPort === e.requestedPort)?.agentId
    if (!holderId || holderId === e.agentId || !eligible(holderId)) return null
    return byId.get(holderId) ?? null
  }
  return (
    <div className="pf-stagger space-y-3">
      {conflicts.map(e => (
        <ConflictCard
          key={e.seq} e={e}
          linkable={!!e.agentId && rosterIds.has(e.agentId)}
          incumbent={incumbentOf(e)}
          onSelect={() => e.agentId && onSelect(e.agentId)}
          onDismiss={() => onDismiss(e.seq)}
          onReclaimed={onReclaimed}
        />
      ))}
    </div>
  )
}

function ConflictCard({e, linkable, incumbent, onSelect, onDismiss, onReclaimed}: {
  e: GwEvent; linkable: boolean; incumbent: AgentView | null
  onSelect: () => void; onDismiss: () => void; onReclaimed: () => void
}) {
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')
  const clone = e.kind === 'clone-suspected'
  const hasPorts = e.requestedPort != null && e.actualPort != null

  // Reclaim, then re-poll so the roster and this card's own event ring catch up. A
  // refusal (a party turned legacy, or the holder moved between poll and click) is
  // shown in place rather than silently doing nothing.
  const reclaim = async () => {
    // incumbentOf only returns non-null once these are all set; re-checked here so the
    // wire's omitempty optionals narrow without a cast.
    if (!e.agentId || !e.tunnelId || !e.requestedPort) return
    setBusy(true); setErr('')
    try {
      await ReclaimPort(e.agentId, e.tunnelId, e.requestedPort)
      onReclaimed()
      onDismiss()
    } catch (x) { setErr(String(x)) } finally { setBusy(false) }
  }

  return (
    <Banner
      tone={clone ? 'bad' : 'warn'} onDismiss={onDismiss}
      action={linkable ? <Button size="sm" variant="ghost" onClick={onSelect}>View agent</Button> : undefined}
    >
      <div className="min-w-0 space-y-0.5">
        <div className="flex flex-wrap items-center gap-x-2 gap-y-0.5">
          <span className="font-medium text-[var(--text)]">{clone ? 'Possible cloned agent' : 'Port auto-reassigned'}</span>
          {!clone && hasPorts && (
            <span className="font-mono text-sub tabular-nums text-[var(--text-3)]">requested {e.requestedPort} → bound {e.actualPort}</span>
          )}
          <span className="ml-auto text-sub tabular-nums text-[var(--text-3)]">{fmtTime(e.timeMs)}</span>
        </div>
        <div className="break-words text-[var(--text-2)]">{e.message}</div>
        {incumbent && (
          <div className="flex flex-wrap items-center gap-2 pt-2">
            <Button size="sm" variant="subtle" onClick={reclaim} disabled={busy}>
              {busy ? 'Moving…' : `Give :${e.requestedPort} to this agent`}
            </Button>
            <Button size="sm" variant="ghost" onClick={onDismiss}>Keep the reassignment</Button>
            <span className="min-w-0 text-sub text-[var(--text-3)]">
              {displayName(incumbent)} holds :{e.requestedPort} now — it moves to another port.
            </span>
          </div>
        )}
        {err && <div className="pt-1.5 text-sub text-[var(--bad)]">{err}</div>}
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
  // An unknown kind (a newer daemon than this GUI) still shows its message with the
  // raw kind as its chip, rather than being mislabelled as one we do know.
  const badge = EVENT_BADGE[e.kind] ?? {label: e.kind, tone: 'warn' as const}
  return (
    <div className="flex items-baseline gap-2 py-1 text-body">
      <span className="shrink-0 font-mono tabular-nums text-[var(--text-3)]">{fmtTime(e.timeMs)}</span>
      <span className="shrink-0"><Badge tone={badge.tone}>{badge.label}</Badge></span>
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
          eyebrow="fleet"
          title={displayName(view)}
          subtitle={view.agentId ? (
            <span className="inline-flex items-center gap-1.5">
              <span className="select-text font-mono text-sub text-[var(--text-3)]">{view.agentId}</span>
              <CopyIcon text={view.agentId} title="Copy agent ID" />
            </span>
          ) : undefined}
          action={<StatusMark view={view} h={h} update={updateAvailable(view.version, status.version)} />}
        />
      </div>

      <div className="grid grid-cols-12 gap-[var(--grid-gap)]">
        <div className="col-span-12 @3xl:col-span-5"><AgentIdentity agent={liveOrSynth} transport={view.transport} /></div>
        <div className="col-span-12 @3xl:col-span-7"><AgentHealth agent={liveOrSynth} /></div>
      </div>

      <AgentManagement view={view} players={conns.length} onChanged={onChanged} />

      <AgentConfigPanel view={view} />

      {/* The detail's moment of light (DESIGN rules 5/7): the same bare hero
          graph Traffic uses, drawing THIS agent's per-agent RRD. It breaks the
          surrounding card rhythm so the drill-in isn't a stack of frost panels. */}
      <BandwidthPanel hero useHistory={source} vtName={null} historyUnsupported={status.historyUnsupported} />

      <div className="grid grid-cols-12 gap-[var(--grid-gap)]">
        <div className="col-span-12 @4xl:col-span-6"><AgentTunnels tunnels={tunnels} conns={conns} view={view} /></div>
        <div className="col-span-12 @4xl:col-span-6"><AgentSessions conns={conns} /></div>
      </div>
    </div>
  )
}

/** AgentManagement: the gateway's controls over one agent — rename, scope, and
 * revoke. Each write follows the write-then-repoll loop (persist, then re-poll
 * ListAgents so the roster and this view refresh). Revoke is guarded by a Modal. */
function AgentManagement({view, players, onChanged}: {view: AgentView; players: number; onChanged: () => void}) {
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
          <span className="min-w-0 truncate text-sub text-[var(--text-3)]" title={`Scope: ${scopeText(view)}`}>Current scope: {scopeText(view)}</span>
          <Button variant="subtle" onClick={saveScope} disabled={savingScope || !scopeChanged}>{savingScope ? 'Saving…' : 'Update scope'}</Button>
        </div>

        {view.connected && (
          <div className="flex items-center justify-between gap-4 border-t border-[var(--border)] pt-3">
            <div className="min-w-0">
              <div className="text-sm font-medium text-[var(--text)]">
                {view.pausedAll ? pausedLabel(view.pausedAllBy ?? '', 'the agent') : 'Pause agent'}
              </div>
              <div className="mt-0.5 text-xs leading-relaxed text-[var(--text-3)]">
                {view.pausedAll
                  ? 'Every tunnel keeps its port but turns players away until resumed.'
                  : 'Turn players away on every tunnel while keeping the ports. Softer than revoke — one click undoes it.'}
              </div>
            </div>
            <PauseControl
              agentId={view.agentId}
              what="this agent"
              paused={!!view.pausedAll}
              pausedBy={view.pausedAllBy ?? ''}
              players={players}
              unheard={!view.pauseSupported}
              onDone={onChanged}
            />
          </div>
        )}

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

        <div className="text-sub text-[var(--text-3)]">
          Enrolled {fmtDate(view.issuedAtMs)}{view.enrolledVia ? <> · via invite “{view.enrolledVia}”</> : null} · Ed25519 identity
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

/** InboundProposalBanner: this agent has authored an edit and is waiting on us. The
 * diff is the whole point — approving is agreeing to specific changes, so they are
 * spelled out rather than summarised. Rejecting asks for a reason, because the agent
 * shows it verbatim to whoever made the edit. */
function InboundProposalBanner({agentId, proposal, current, onDecided}: {
  agentId: string
  proposal: gateway.ProposalView
  current: control.TunnelSpec[]
  onDecided: () => void
}) {
  const [reason, setReason] = useState('')
  const [rejecting, setRejecting] = useState(false)
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')

  const decide = (fn: () => Promise<void>) => {
    setBusy(true); setErr('')
    fn().then(() => { setRejecting(false); setReason(''); onDecided() })
      .catch(e => setErr(String(e?.message ?? e)))
      .finally(() => setBusy(false))
  }

  return (
    <Banner tone="warn">
      <div className="space-y-2.5">
        <div>
          <div className="font-medium">This agent proposes a config change</div>
          <div className="text-body text-[var(--text-3)]">Sent {proposalAge(proposal.atMs)}. Nothing changes until you approve it.</div>
        </div>
        <ProposalDiff current={specsOf(current)} proposed={specsOf(proposal.tunnels)} />
        {rejecting ? (
          <div className="space-y-2">
            <TextInput value={reason} onChange={setReason} size="sm" autoFocus
              placeholder="Why? The agent shows this to whoever made the edit."
              onEnter={() => { if (!busy) decide(() => RejectAgentProposal(agentId, proposal.proposalId, reason)) }} />
            <div className="flex gap-2">
              <Button size="sm" variant="danger" disabled={busy}
                onClick={() => decide(() => RejectAgentProposal(agentId, proposal.proposalId, reason))}>Reject</Button>
              <Button size="sm" variant="ghost" disabled={busy} onClick={() => { setRejecting(false); setErr('') }}>Cancel</Button>
            </div>
          </div>
        ) : (
          <div className="flex gap-2">
            <Button size="sm" disabled={busy}
              onClick={() => decide(() => ApproveAgentProposal(agentId, proposal.proposalId))}>Approve</Button>
            <Button size="sm" variant="subtle" disabled={busy} onClick={() => setRejecting(true)}>Reject…</Button>
          </div>
        )}
        {err && <div className="text-body text-[var(--bad)]">{err}</div>}
      </div>
    </Banner>
  )
}

/** AgentConfigPanel: the gateway-authoritative tunnel set the gateway holds for this
 * agent — and, under config-consent, where an operator edits it. An edit here is a
 * *proposal*: Save sends it and the agent must accept before anything moves, so the
 * button says so and the panel then shows the wait.
 *
 * Ports and removal only. The gateway allocates ports and decides what it serves; it
 * has no idea where a tunnel points on the agent's LAN, and the wire never carries
 * that — so an operator-invented tunnel would land with nothing to dial. Adding stays
 * the agent's job.
 *
 * Four states: skeleton while AgentDesiredConfig resolves, the tunnel list, a written
 * empty state for an agent with no config yet, and the honest-unavailable state
 * for a daemon too old to report gateway-held config. */
function AgentConfigPanel({view}: {view: AgentView}) {
  const {cfg, loading, err, refresh} = useAgentConfig(view.agentId)
  const server = useMemo(() => cfg?.tunnels ?? [], [cfg])
  const pendingOut = cfg?.pendingOutbound
  const pendingIn = cfg?.pendingInbound

  // Reseed the draft from the server's set, but never over an edit in progress —
  // this panel re-polls every 3s and would otherwise wipe what is being typed.
  const serverKey = useMemo(() => JSON.stringify(server.map(t => [t.id, t.publicPort])), [server])
  const [draft, setDraft] = useState<control.TunnelSpec[]>([])
  const [dirty, setDirty] = useState(false)
  const [saveErr, setSaveErr] = useState('')
  const [saving, setSaving] = useState(false)
  useEffect(() => {
    if (dirty) return
    setDraft(server.map(t => ({...t} as control.TunnelSpec)))
  }, [serverKey, dirty, server])

  // Editing needs a live consent session and nothing already in flight — in either
  // direction. Ours would silently supersede itself; and countering the agent's
  // still-pending proposal just races it, since approving either one moves the
  // generation and the loser is dropped as stale. Decide theirs first.
  const editable = !!cfg?.consentActive && !pendingOut && !pendingIn
  const edit = (id: string, port: number) => {
    setDirty(true)
    setDraft(d => d.map(t => (t.id === id ? {...t, publicPort: port} as control.TunnelSpec : t)))
  }
  const drop = (id: string) => { setDirty(true); setDraft(d => d.filter(t => t.id !== id)) }
  const reset = () => { setDirty(false); setSaveErr(''); setDraft(server.map(t => ({...t} as control.TunnelSpec))) }
  const submit = () => {
    setSaving(true); setSaveErr('')
    ProposeAgentConfig(view.agentId, draft)
      .then(() => { setDirty(false); refresh() })
      .catch(e => setSaveErr(String(e?.message ?? e)))
      .finally(() => setSaving(false))
  }

  const rows = dirty ? draft : server
  return (
    <Card label="Gateway config" pad={false}
      action={cfg ? <div className="pr-1"><MonoChip>gen {cfg.generation}</MonoChip></div> : undefined}>
      {loading ? (
        <div className="space-y-2 px-5 pb-5 pt-1" aria-busy="true">
          {[0, 1].map(i => (
            <div key={i} className="flex items-center gap-3">
              <Skeleton className="h-8 w-8 rounded-[var(--r-md)]" />
              <div className="flex-1 space-y-1.5"><Skeleton className="h-3.5 w-32 rounded-full" /><Skeleton className="h-3 w-40 rounded-full" /></div>
              <Skeleton className="h-4 w-14 rounded-full" />
            </div>
          ))}
        </div>
      ) : err ? (
        <div className="px-5 pb-5"><EmptyState icon={<IconServer size={24} />} title="Config unavailable"
          hint="The background service is an older version that doesn't report gateway-held config. Update it to see this agent's authoritative tunnel set." /></div>
      ) : (
        <>
          {(pendingIn || pendingOut || (cfg && !cfg.consentActive)) && (
            <div className="space-y-2 px-5 pb-3">
              {pendingIn && (
                <InboundProposalBanner agentId={view.agentId} proposal={pendingIn} current={server} onDecided={refresh} />
              )}
              {pendingOut && (
                <Banner tone="info">
                  <div className="space-y-2">
                    <div>
                      <div className="font-medium">Proposed — awaiting the agent’s approval</div>
                      <div className="text-body text-[var(--text-3)]">
                        Sent {proposalAge(pendingOut.atMs)}. It applies only if the agent accepts.
                        {view.headless && ' The agent runs headless — nobody may be watching for this until someone opens its app.'}
                      </div>
                    </div>
                    <ProposalDiff current={specsOf(server)} proposed={specsOf(pendingOut.tunnels)} />
                  </div>
                </Banner>
              )}
              {cfg && !cfg.consentActive && (
                <Banner tone="info">
                  {view.connected
                    ? 'This agent runs a build without config approvals, so its tunnels can’t be edited from here. Update the agent to edit them.'
                    : 'This agent is offline. Editing needs a live session — a proposal has to be delivered and accepted.'}
                </Banner>
              )}
            </div>
          )}
          {rows.length === 0 ? (
            <div className="px-5 pb-5"><EmptyState icon={<IconTunnels size={24} />} title="No tunnels yet"
              hint="The gateway holds no config for this agent — it will seed on connect." /></div>
          ) : (
            <div className="divide-y divide-[var(--border)]">
              {rows.map(t => (
                <div key={t.id} className="flex items-center gap-3 px-5 py-2.5">
                  <div className="pf-control grid h-8 w-8 shrink-0 place-items-center rounded-[var(--r-md)] bg-[var(--input-bg)] text-[var(--text-2)]"><IconServer size={15} /></div>
                  <div className="min-w-0 flex-1">
                    <div className="truncate text-sm font-medium">{t.name || t.id}</div>
                    <div className="truncate font-mono text-sub text-[var(--text-3)]">{t.id}</div>
                  </div>
                  <div className="flex shrink-0 items-center gap-2">
                    <Badge>{(t.type || 'tcp').toUpperCase()}</Badge>
                    {editable ? (
                      <>
                        <div className="w-20">
                          <TextInput value={t.publicPort ? String(t.publicPort) : ''} placeholder="auto" size="sm" mono
                            ariaLabel={`Public port for ${t.name || t.id}`}
                            onChange={v => edit(t.id, Math.max(0, Math.min(65535, parseInt(v.replace(/\D/g, ''), 10) || 0)))} />
                        </div>
                        <Button size="sm" variant="ghost" onClick={() => drop(t.id)} title={`Stop serving ${t.name || t.id}`}>Remove</Button>
                      </>
                    ) : (
                      <MonoChip>{t.publicPort > 0 ? `:${t.publicPort}` : 'auto'}</MonoChip>
                    )}
                  </div>
                </div>
              ))}
            </div>
          )}
          {editable && dirty && (
            <div className="space-y-2 border-t border-[var(--border)] px-5 py-3">
              <ProposalDiff current={specsOf(server)} proposed={specsOf(draft)} />
              <div className="flex items-center gap-2">
                <Button size="sm" disabled={saving} onClick={submit}>Propose to agent</Button>
                <Button size="sm" variant="ghost" disabled={saving} onClick={reset}>Cancel</Button>
                <span className="text-body text-[var(--text-3)]">The agent must accept before anything moves.</span>
              </div>
              {saveErr && <div className="text-body text-[var(--bad)]">{saveErr}</div>}
            </div>
          )}
          {/* Something arrived (or the session went) while this edit was open. The
              draft is still on screen, so say why it can't be sent rather than let
              the submit quietly vanish. */}
          {!editable && dirty && (
            <div className="flex items-center gap-2 border-t border-[var(--border)] px-5 py-3">
              <span className="flex-1 text-body text-[var(--text-3)]">
                {pendingIn ? 'Your edit can’t be sent while this agent’s proposal is waiting — decide that first.'
                  : pendingOut ? 'Your edit can’t be sent while a proposal is already awaiting the agent.'
                    : 'Your edit can’t be sent — the agent’s session ended.'}
              </span>
              <Button size="sm" variant="ghost" onClick={reset}>Discard edit</Button>
            </div>
          )}
        </>
      )}
    </Card>
  )
}

/** AgentIdentity: where this machine is — its remote address prominent, LAN
 * quiet, with link uptime, lifetime link data, and the active transport. Wears the
 * agent role swatch. The gateway reports the transport it accepted; the agent owns
 * the dial, so an offline agent (empty) reads "—", never a guessed value. */
function AgentIdentity({agent, transport}: {agent: AgentUI; transport: string}) {
  const lan = (agent.lanIps ?? []).filter(Boolean)
  return (
    <Card pad={false} className="h-full">
      <div className="flex items-start gap-3.5 p-4">
        <Emblem role="agent" size={40} fixed glow={seg(agent) !== 'bad' && seg(agent) !== 'unknown'} />
        <div className="min-w-0 flex-1 space-y-2.5">
          <div>
            <Overline>Remote address</Overline>
            <div className="mt-0.5 flex items-center gap-1.5">
              <span className="select-text truncate font-mono text-emphasis font-semibold text-[var(--text)]">{agent.remoteIp || '—'}</span>
              {agent.remoteIp && <CopyIcon text={agent.remoteIp} title="Copy remote address" />}
            </div>
          </div>
          {lan.length > 0 && (
            <div>
              <Overline>LAN</Overline>
              <div className="mt-0.5 truncate font-mono text-body text-[var(--text-2)]">{lan.join(', ')}</div>
            </div>
          )}
          <div className="flex flex-wrap gap-x-6 gap-y-2.5 pt-0.5">
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
            <div>
              <Overline>Transport</Overline>
              <div className="mt-1">
                {transport
                  ? <MonoChip>{TRANSPORT_LABEL[transport] ?? transport}</MonoChip>
                  : <span className="text-sm font-medium text-[var(--text-3)]">—</span>}
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
function AgentTunnels({tunnels, conns, view}: {tunnels: app.TunnelUI[]; conns: app.ConnUI[]; view: AgentView}) {
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
                  <div className="truncate font-mono text-sub text-[var(--text-3)]" title={sub}>{sub}</div>
                </div>
                {t.paused
                  ? <PausedBadge pausedBy={t.pausedBy} peer="the agent" />
                  : <Badge tone={t.localKnown ? (t.localUp ? 'good' : 'bad') : 'neutral'}>
                      {t.localKnown ? (t.localUp ? 'Server up' : 'Server down') : 'Unknown'}
                    </Badge>}
                <PauseControl
                  agentId={view.agentId}
                  tunnelId={t.id}
                  what="this tunnel"
                  paused={!!t.paused}
                  pausedBy={t.pausedBy}
                  players={conns.filter(c => c.tunnelName === t.name).length}
                  unheard={!view.pauseSupported}
                />
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
                <div className="truncate font-mono text-sub text-[var(--text-3)]">{c.tunnelName} · {c.clientAddr}</div>
              </div>
              <div className="shrink-0 text-right">
                <div className="font-mono text-xs tabular-nums text-[var(--text-2)]">{fmtBytes(c.bytesIn + c.bytesOut)}</div>
                {hasRtt(c.rttMs) && <div className="font-mono text-sub tabular-nums text-[var(--text-3)]">{Math.round(c.rttMs)} ms</div>}
              </div>
            </div>
          ))}
        </div>
      )}
    </Card>
  )
}
