import {useEffect, useMemo, useState} from 'react'
import {AgentBandwidthHistory} from '../../wailsjs/go/app/App'
import {app} from '../../wailsjs/go/models'
import {BandwidthPanel} from '../components/BandwidthChart'
import {Emblem} from '../components/Emblem'
import {
  Badge, Card, CopyIcon, EmptyState, Overline, PageHeader, SegmentedControl, StatusDot, TextInput,
} from '../components/ui'
import {
  IconActivity, IconAgents, IconArrowRight, IconChevronRight, IconClock,
  IconSearch, IconServer, IconTunnels, IconUsers,
} from '../components/icons'
import {HistoryResult, RANGES, RangeKey} from '../history'
import {usePolled} from '../hooks'
import {fmtBytes, fmtUptime, hasRtt, UIStatus, worstHealth} from '../state'

type AgentUI = app.AgentUI
type Seg = 'good' | 'warn' | 'bad' | 'unknown'

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

const seg = (a: AgentUI): Seg => (a.healthScore || 'unknown') as Seg
const traffic = (a: AgentUI): number => a.linkBytesIn + a.linkBytesOut

type SortKey = 'health' | 'traffic' | 'players' | 'name'
type Density = 'grid' | 'list'
const loadSort = (): SortKey => (localStorage.getItem('pf-agents-sort') as SortKey) || 'health'
const loadDensity = (): Density => (localStorage.getItem('pf-agents-density') as Density) || 'grid'

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

/**
 * Agents (gateway role): the fleet — a wall of machine health cards, the
 * gateway analogue of the Players wall of faces. Selecting one drills into a
 * focused per-agent view (identity, health, its own bandwidth graph, tunnels,
 * sessions) without leaving this screen. One identity surface; the cards are
 * frost, never Signal Glass — N equal cards can't each answer the pointer.
 */
export function Agents({status}: {status: UIStatus}) {
  const [selected, setSelected] = useState<string | null>(null)
  const [sort, setSort] = useState<SortKey>(loadSort)
  const [density, setDensity] = useState<Density>(loadDensity)
  const [query, setQuery] = useState('')
  const agents = status.agents
  const list = agents ?? []

  // A selected agent that disconnects while we're looking at it drops us back to
  // the roster rather than stranding an empty detail.
  useEffect(() => {
    if (selected && !list.some(a => a.agentId === selected)) setSelected(null)
  }, [selected, list])

  const sorted = useMemo(() => {
    const f = query.trim().toLowerCase()
    const rows = list.filter(a =>
      !f || a.hostname.toLowerCase().includes(f) || a.agentId.toLowerCase().includes(f))
    return rows.sort((a, b) =>
      sort === 'name' ? a.hostname.localeCompare(b.hostname)
        : sort === 'players' ? b.players - a.players || traffic(b) - traffic(a)
          : sort === 'traffic' ? traffic(b) - traffic(a)
            : HEALTH_RANK[seg(a)] - HEALTH_RANK[seg(b)] || traffic(b) - traffic(a))
  }, [list, sort, query])

  // Honest-unavailable: an older background service predates per-agent status,
  // so it sends no roster at all — told apart from a gateway with zero agents.
  if (agents === undefined) {
    return (
      <div className="pf-stagger space-y-8">
        <Header />
        <Card><EmptyState icon={<IconAgents size={28} />} title="Roster unavailable"
          hint="The background service is an older version that doesn't report per-agent status. Update it to see the fleet." /></Card>
      </div>
    )
  }

  const current = selected ? list.find(a => a.agentId === selected) ?? null : null
  if (current) return <AgentDetail status={status} agent={current} onBack={() => setSelected(null)} />

  const pickSort = (v: SortKey) => { setSort(v); localStorage.setItem('pf-agents-sort', v) }
  const pickDensity = (v: Density) => { setDensity(v); localStorage.setItem('pf-agents-density', v) }
  const n = list.length

  return (
    <div className="pf-stagger space-y-8">
      <Header />

      {n === 0 ? (
        <Card><EmptyState icon={<IconAgents size={28} />} title="No agents connected"
          hint="Share your pairing code from Overview. Every agent that dials in with it appears here." /></Card>
      ) : (
        <>
          <FleetSummary agents={list} />

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
                    placeholder="Filter agents…" ariaLabel="Filter agents by hostname"
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
              {sorted.map(a => <AgentCard key={a.agentId} agent={a} onOpen={() => setSelected(a.agentId)} />)}
            </div>
          ) : (
            <Card pad={false}><AgentList agents={sorted} onOpen={setSelected} /></Card>
          )}
        </>
      )}
    </div>
  )
}

function Header() {
  return <PageHeader title="Agents" subtitle="The machines dialed into this gateway." />
}

/** FleetSummary: the page's one hero figure (agent count) with the fleet's
 * worst-of health and the rolled-up tunnel/player totals — type on whitespace,
 * never another card (DESIGN rule 5). Only rendered for a non-empty fleet. */
function FleetSummary({agents}: {agents: AgentUI[]}) {
  const n = agents.length
  const worst = worstHealth(agents)
  const tunnels = agents.reduce((s, a) => s + a.tunnels, 0)
  const players = agents.reduce((s, a) => s + a.players, 0)
  const verdict = worst === 'good' ? 'All healthy' : worst === 'warn' ? 'Needs attention' : 'Degraded'
  return (
    <div className="flex flex-wrap items-baseline gap-x-6 gap-y-2">
      <div className="flex items-baseline gap-2.5">
        <span className="text-[length:var(--fs-metric-hero)] font-semibold leading-none tabular-nums">{n}</span>
        <span className="text-sm text-[var(--text-2)]">{n === 1 ? 'agent' : 'agents'} online</span>
      </div>
      <span className="self-center"><StatusDot state={worst} label={verdict} pulse={worst === 'good'} /></span>
      <span className="text-sm tabular-nums text-[var(--text-3)]">{tunnels} tunnel{tunnels === 1 ? '' : 's'}</span>
      <span className="text-sm tabular-nums text-[var(--text-3)]">{players} player{players === 1 ? '' : 's'} online</span>
    </div>
  )
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

/** AgentCard: one machine's vital-signs monitor — frost glass that lifts to the
 * pointer (tactility, DESIGN rule 3) but never ignites; only the live health dot
 * breathes. The whole card is the drill-in affordance. */
function AgentCard({agent, onOpen}: {agent: AgentUI; onOpen: () => void}) {
  const h = seg(agent)
  return (
    <button
      type="button" onClick={onOpen} aria-label={`Open ${agent.hostname || 'agent'}`}
      className="pf-card pf-lift group flex flex-col gap-4 p-4 text-left"
    >
      <div className="flex items-start gap-3">
        <Emblem role="agent" size={38} fixed glow={h === 'good'} />
        <div className="min-w-0 flex-1">
          <div className="truncate font-semibold">{agent.hostname || 'unknown host'}</div>
          <div className="mt-0.5 truncate font-mono text-[11px] text-[var(--text-3)]">{agent.agentId || '—'}</div>
        </div>
        <div className="shrink-0"><StatusDot state={h} label={HEALTH_LABEL[h]} pulse /></div>
      </div>

      <div className="grid grid-cols-3 gap-3">
        <Metric label="Round trip" value={fmtRttMs(agent.rttMillis)} tone="neutral" />
        <Metric label="Jitter" value={fmtMs(agent.jitterMillis)} tone={jitterTone(agent.jitterMillis)} />
        <Metric label="Loss" value={fmtPct(agent.packetLossPct)} tone={lossTone(agent.packetLossPct)} />
      </div>

      <div className="flex flex-wrap items-center gap-x-4 gap-y-1 border-t border-[var(--border)] pt-3 text-[11px] text-[var(--text-3)]">
        <span className="inline-flex items-center gap-1.5"><IconTunnels size={13} /> {agent.tunnels} tunnel{agent.tunnels === 1 ? '' : 's'}</span>
        <span className="inline-flex items-center gap-1.5"><IconUsers size={13} /> {agent.players} player{agent.players === 1 ? '' : 's'}</span>
        {agent.linkUpSinceMs > 0 && (
          <span className="inline-flex items-center gap-1.5"><IconClock size={13} /> up <Uptime since={agent.linkUpSinceMs} /></span>
        )}
        <span className="ml-auto text-[var(--text-3)] opacity-0 transition-opacity duration-200 group-hover:opacity-100"><IconChevronRight size={14} /></span>
      </div>
    </button>
  )
}

/** AgentList: the compact density — one flush row per agent, extra columns
 * revealed as the container widens (@container), so a wide fleet reads as a
 * dense table and a narrow one stays legible. */
function AgentList({agents, onOpen}: {agents: AgentUI[]; onOpen: (id: string) => void}) {
  return (
    <div className="divide-y divide-[var(--border)]">
      {agents.map(a => {
        const h = seg(a)
        return (
          <button
            key={a.agentId} type="button" onClick={() => onOpen(a.agentId)}
            className="group flex w-full items-center gap-4 px-4 py-3 text-left transition-colors hover:bg-[color-mix(in_srgb,var(--panel-2)_60%,transparent)]"
          >
            <Emblem role="agent" size={30} fixed glow={h === 'good'} />
            <div className="min-w-0 flex-1">
              <div className="truncate text-sm font-medium">{a.hostname || 'unknown host'}</div>
              <div className="truncate font-mono text-[11px] text-[var(--text-3)]">{a.agentId || '—'}</div>
            </div>
            <div className="hidden w-28 shrink-0 @min-[40rem]:block"><StatusDot state={h} label={HEALTH_LABEL[h]} pulse /></div>
            <div className="hidden w-16 shrink-0 text-right font-mono text-xs tabular-nums text-[var(--text-2)] @min-[52rem]:block" title="Round trip">{fmtRttMs(a.rttMillis)}</div>
            <div className="hidden w-16 shrink-0 text-right text-xs tabular-nums text-[var(--text-3)] @min-[64rem]:block" title="Tunnels">{a.tunnels} tun</div>
            <div className="w-16 shrink-0 text-right text-xs tabular-nums text-[var(--text-3)]" title="Players">{a.players} ply</div>
            <span className="shrink-0 text-[var(--text-3)] opacity-0 transition-opacity duration-200 group-hover:opacity-100"><IconChevronRight size={16} /></span>
          </button>
        )
      })}
    </div>
  )
}

/** AgentDetail: the drill-in. One machine's identity, its link health, its own
 * bandwidth graph (same instrument as Traffic, scoped to this agent's RRD), and
 * the tunnels + live sessions attributed to it. */
function AgentDetail({status, agent, onBack}: {status: UIStatus; agent: AgentUI; onBack: () => void}) {
  const h = seg(agent)
  const tunnels = (status.tunnels ?? []).filter(t => t.agentId === agent.agentId)
  const conns = (status.connections ?? []).filter(c => c.agentId === agent.agentId)
  const source = useMemo(() => agentHistorySource(agent.agentId), [agent.agentId])

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
          title={agent.hostname || 'Agent'}
          subtitle={agent.agentId ? (
            <span className="inline-flex items-center gap-1.5">
              <span className="select-text font-mono text-[11px] text-[var(--text-3)]">{agent.agentId}</span>
              <CopyIcon text={agent.agentId} title="Copy agent ID" />
            </span>
          ) : undefined}
          action={<StatusDot state={h} label={HEALTH_LABEL[h]} pulse />}
        />
      </div>

      <div className="grid grid-cols-12 gap-[var(--grid-gap)]">
        <div className="col-span-12 @3xl:col-span-5"><AgentIdentity agent={agent} /></div>
        <div className="col-span-12 @3xl:col-span-7"><AgentHealth agent={agent} /></div>
      </div>

      {/* The detail's moment of light (DESIGN rules 5/7): the same bare hero
          graph Traffic uses, drawing THIS agent's per-agent RRD — range,
          candles, and series controls all reused. It breaks the surrounding
          card rhythm so the drill-in isn't a stack of frost panels. The old-
          daemon "history unavailable" state is threaded, told apart from the
          collecting-data empty state, exactly as Traffic/Overview do it. */}
      <BandwidthPanel hero useHistory={source} vtName={null} historyUnsupported={status.historyUnsupported} />

      <div className="grid grid-cols-12 gap-[var(--grid-gap)]">
        <div className="col-span-12 @4xl:col-span-6"><AgentTunnels tunnels={tunnels} /></div>
        <div className="col-span-12 @4xl:col-span-6"><AgentSessions conns={conns} /></div>
      </div>
    </div>
  )
}

/** AgentIdentity: where this machine is — its remote address prominent, LAN
 * quiet, with link uptime and lifetime link data. Wears the agent role swatch. */
function AgentIdentity({agent}: {agent: AgentUI}) {
  const lan = (agent.lanIps ?? []).filter(Boolean)
  return (
    <Card pad={false} className="h-full">
      <div className="flex items-start gap-3.5 p-4">
        <Emblem role="agent" size={40} fixed glow={seg(agent) !== 'bad'} />
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
              <div className="mt-0.5 text-sm font-medium tabular-nums text-[var(--text)]">{fmtBytes(agent.linkBytesIn + agent.linkBytesOut)}</div>
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
          {tunnels.map(t => (
            <div key={t.id} className="flex items-center gap-3 px-5 py-2.5">
              <div className="pf-control grid h-8 w-8 shrink-0 place-items-center rounded-[var(--r-md)] bg-[var(--input-bg)] text-[var(--text-2)]"><IconServer size={15} /></div>
              <div className="min-w-0 flex-1">
                <div className="truncate text-sm font-medium">{t.name}</div>
                <div className="truncate font-mono text-[11px] text-[var(--text-3)]">port {t.publicPort > 0 ? t.publicPort : '—'}</div>
              </div>
              <Badge tone={t.localKnown ? (t.localUp ? 'good' : 'bad') : 'neutral'}>
                {t.localKnown ? (t.localUp ? 'Server up' : 'Server down') : 'Unknown'}
              </Badge>
            </div>
          ))}
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
