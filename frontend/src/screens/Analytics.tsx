import {Fragment, ReactNode, useEffect, useMemo, useState} from 'react'
import {AvatarImg} from '../components/AvatarImg'
import {LineChart, LineSeries} from '../components/charts/LineChart'
import {fmtTickTime} from '../components/charts/util'
import {GeoMetric, WorldMap} from '../components/charts/WorldMap'
import {Column, DataTable} from '../components/DataTable'
import {GeoRank} from '../components/GeoRank'
import {NumberTicker} from '../components/NumberTicker'
import {
  IconActivity, IconClock, IconConnections, IconGauge, IconGlobe, IconPlayers, IconUsers,
} from '../components/icons'
import {
  Badge, Button, Card, CopyIcon, EmptyState, LiveDot, Modal, PageHeader, Pill, SegmentedControl, Skeleton, StatTile,
} from '../components/ui'
import {
  CountryAgg, PEAK_WEEKS, RANGE_MS, Range, SESSIONS_PAGE_SIZE, SessionMeta, SessionsQuery,
  UptimeReport, fmtPct, fmtWhen, useBandwidthLoss, useGeoSnapshot, useGeoStatus, usePeakMatrix,
  useSessionTimeline, useSessions, useSummary, useTunnelUptime,
} from '../analytics'
import {fmtLastSeen} from '../players'
import {AnalyticsUnavailable} from './Players'
import {UIStatus, flagEmoji, fmtBytes, fmtDuration, fmtRate, fmtRtt, hasRtt} from '../state'

/** Analytics: the historical dashboard — range summary, the peak-hours
 * heatmap, per-tunnel uptime and packet-loss timelines, and browsable
 * connection history with a per-session replay. All cards read the one range
 * picker (except the peak-hours grid, which is intrinsically multi-week). */
export function Analytics({status}: {status: UIStatus}) {
  const [range, setRange] = useState<Range>('7d')
  // The country filter is hoisted: the Geography map/list set it, the
  // connection history narrows to it.
  const [ccFilter, setCcFilter] = useState('')
  const rangeMs = RANGE_MS[range]
  const summary = useSummary(rangeMs)
  const tunnels = status.tunnels ?? []

  // The daemon can't answer analytics (older build, or the store failed to
  // open): one honest empty state instead of skeletons that never fill.
  if (status.analyticsUnsupported) {
    return (
      <div className="pf-stagger space-y-4">
        <PageHeader title="Analytics" subtitle="Traffic, players, and uptime over time." />
        <Card><AnalyticsUnavailable /></Card>
      </div>
    )
  }

  return (
    <div className="pf-stagger space-y-4">
      <PageHeader
        title="Analytics"
        subtitle="Traffic, players, and uptime over time."
        action={
          <SegmentedControl<Range>
            value={range}
            onChange={setRange}
            options={[
              {value: '24h', label: '24h'}, {value: '7d', label: '7d'},
              {value: '30d', label: '30d'}, {value: 'all', label: 'All'},
            ]}
          />
        }
      />

      {/* Range-scoped cards remount under a range key so a 24h→7d switch
          replays each card's entrance cascade — the range change reads as
          the dashboard rebuilding itself, not numbers silently mutating.
          Peak hours sits outside: it is intrinsically multi-week. */}
      <div key={`sum-${range}`} className="pf-stagger space-y-4">
        <SummaryTiles s={summary} />
        <RecordsStrip s={summary} />

        <Geography rangeMs={rangeMs} selectedCc={ccFilter}
          onSelectCc={cc => setCcFilter(prev => (prev === cc ? '' : cc))} />
      </div>

      <Card title="Peak hours" subtitle={`Identified players by weekday and hour — last ${PEAK_WEEKS} weeks, your local time`}>
        <PeakHours />
      </Card>

      <div key={`range-${range}`} className="pf-stagger space-y-4">
        <div className="grid grid-cols-1 gap-4 @min-[80rem]:grid-cols-2">
          <Card title="Uptime" subtitle="Control link and per-tunnel availability across the range">
            <UptimeBands windowMs={rangeMs} />
          </Card>
          <Card title="Packet loss" subtitle="Peak loss on the control link per interval">
            <LossChart range={range} />
          </Card>
        </div>

        <ConnectionHistory tunnels={tunnels} rangeMs={rangeMs} cc={ccFilter} onClearCc={() => setCcFilter('')} />
      </div>
    </div>
  )
}

// ---------------------------------------------------------------------------
// Summary tiles
// ---------------------------------------------------------------------------

const fmtInt = (n: number) => String(Math.round(n))
const rttTone = (ms: number): 'good' | 'warn' | 'bad' => (ms < 60 ? 'good' : ms < 130 ? 'warn' : 'bad')

function SummaryTiles({s}: {s: ReturnType<typeof useSummary>}) {
  if (!s) {
    return (
      <div className="pf-stagger-grid grid grid-cols-2 gap-3 @xl:grid-cols-3 @5xl:grid-cols-6">
        {Array.from({length: 6}, (_, i) => (
          <div key={i} className="pf-card p-4"><Skeleton className="h-3 w-16 rounded" /><Skeleton className="mt-2 h-6 w-24 rounded" /></div>
        ))}
      </div>
    )
  }
  const rtt = s.avgRttMs
  const peakBps = Math.max(s.peakInBps, s.peakOutBps)
  const peakBpsAt = s.peakOutBps >= s.peakInBps ? s.peakOutAt : s.peakInAt
  return (
    <div className="pf-stagger-grid grid grid-cols-2 gap-3 @xl:grid-cols-3 @5xl:grid-cols-6">
      <StatTile
        size="lg" icon={<IconActivity size={15} />} label="Data moved"
        value={<NumberTicker value={s.bytesIn + s.bytesOut} format={n => fmtBytes(Math.round(n))} />}
        sub={`↓ ${fmtBytes(s.bytesOut)} · ↑ ${fmtBytes(s.bytesIn)}`}
      />
      <StatTile
        size="lg" icon={<IconConnections size={15} />} label="Sessions"
        value={<NumberTicker value={s.sessions} format={fmtInt} />}
      />
      <StatTile
        size="lg" icon={<IconPlayers size={15} />} label="Unique players"
        value={<NumberTicker value={s.uniquePlayers} format={fmtInt} />}
      />
      <StatTile
        size="lg" icon={<IconUsers size={15} />} label="Peak players"
        value={s.peakPlayers >= 0 ? String(Math.round(s.peakPlayers)) : '—'}
        sub={s.peakPlayers >= 0 && s.peakPlayersAt ? fmtWhen(s.peakPlayersAt) : undefined}
      />
      <StatTile
        size="lg" icon={<IconActivity size={15} />} label="Peak throughput"
        value={peakBps > 0 ? fmtRate(peakBps) : '—'}
        sub={peakBps > 0 && peakBpsAt ? fmtWhen(peakBpsAt) : undefined}
      />
      <StatTile
        size="lg" icon={<IconGauge size={15} />} label="Avg latency"
        value={rtt >= 0 ? fmtRtt(rtt) : '—'}
        tone={rtt >= 0 ? rttTone(rtt) : undefined}
      />
    </div>
  )
}

/** RecordsStrip: all-time peaks, independent of the selected range. Hidden
 * until at least one record exists. */
function RecordsStrip({s}: {s: ReturnType<typeof useSummary>}) {
  if (!s || (s.recOutBps <= 0 && s.recInBps <= 0 && s.recPlayers < 0)) return null
  const items: {label: string; value: string; when: number}[] = []
  if (s.recOutBps > 0) items.push({label: 'Peak ↓', value: fmtRate(s.recOutBps), when: s.recOutAt})
  if (s.recInBps > 0) items.push({label: 'Peak ↑', value: fmtRate(s.recInBps), when: s.recInAt})
  if (s.recPlayers >= 0) items.push({label: 'Most players', value: String(Math.round(s.recPlayers)), when: s.recPlayersAt})
  if (s.recConns >= 0) items.push({label: 'Most connections', value: String(Math.round(s.recConns)), when: s.recConnsAt})
  return (
    <div className="flex flex-wrap items-center gap-x-5 gap-y-1 px-1 text-[11px] text-[var(--text-3)]">
      <span className="font-medium uppercase tracking-wide">All-time records</span>
      {items.map(it => (
        <span key={it.label} className="inline-flex items-baseline gap-1.5">
          {it.label} <span className="font-mono font-semibold tabular-nums text-[var(--text-2)]">{it.value}</span>
          {it.when > 0 && <span className="text-[var(--text-3)]">· {fmtWhen(it.when)}</span>}
        </span>
      ))}
    </div>
  )
}

// ---------------------------------------------------------------------------
// Geography — world choropleth + country ranking
// ---------------------------------------------------------------------------

/** Geography pairs the SVG world map with a ranked country list, both keyed to
 * one metric toggle (activity ↔ latency) and cross-highlighted on hover.
 * Clicking a country (map or list) filters the connection history below;
 * re-clicking clears. Needs a loaded GeoLite2 database and public-IP sessions;
 * the empty states tell the two apart. */
function Geography({rangeMs, selectedCc, onSelectCc}: {
  rangeMs: number
  selectedCc: string
  onSelectCc: (cc: string) => void
}) {
  const snap = useGeoSnapshot(rangeMs)
  const geoStatus = useGeoStatus()
  const [metric, setMetric] = useState<GeoMetric>('activity')
  const [hoverCc, setHoverCc] = useState<string | null>(null)

  const rows = useMemo(() => {
    const r = snap ? [...snap] : []
    // Rank to match the map's coloring: busiest first, or slowest first. No-RTT
    // countries sink to the bottom of the latency ranking.
    const lat = (a: CountryAgg) => (hasRtt(a.rttAvg) ? a.rttAvg : -1)
    r.sort(metric === 'latency' ? (a, b) => lat(b) - lat(a) : (a, b) => b.sessions - a.sessions)
    return r
  }, [snap, metric])

  const body = () => {
    if (snap === null) {
      return (
        <div className="grid grid-cols-1 gap-4 @min-[72rem]:grid-cols-[1.6fr_1fr]">
          <Skeleton className="aspect-[2/1] w-full rounded-[var(--r-md)]" />
          <div className="space-y-2">
            {Array.from({length: 7}, (_, i) => <Skeleton key={i} className="h-9 w-full rounded" />)}
          </div>
        </div>
      )
    }
    if (rows.length === 0) {
      return geoStatus && !geoStatus.cityLoaded ? (
        <EmptyState icon={<IconGlobe size={26} />} title="GeoIP not configured"
          hint="Add MaxMind GeoLite2 databases in Settings → Analytics to map where your players connect from." />
      ) : (
        <EmptyState icon={<IconGlobe size={26} />} title="No located connections yet"
          hint="Sessions from public addresses land on the map once GeoLite2 resolves them — local and LAN clients aren't mapped." />
      )
    }
    return (
      <div className="grid grid-cols-1 gap-4 @min-[72rem]:grid-cols-[1.6fr_1fr]">
        <WorldMap data={rows} metric={metric} hoverCc={hoverCc} onHover={setHoverCc}
          onSelect={onSelectCc} selectedCc={selectedCc || null} />
        <GeoRank rows={rows} metric={metric} hoverCc={hoverCc} onHover={setHoverCc}
          onSelect={onSelectCc} selectedCc={selectedCc || null} />
      </div>
    )
  }

  return (
    <Card
      title="Geography"
      subtitle="Where players connect from across the range — click a country to filter the history below"
      action={
        <SegmentedControl<GeoMetric>
          value={metric}
          onChange={setMetric}
          options={[{value: 'activity', label: 'Activity'}, {value: 'latency', label: 'Latency'}]}
        />
      }
    >
      {body()}
    </Card>
  )
}

// ---------------------------------------------------------------------------
// Peak-hours heatmap (pure CSS grid)
// ---------------------------------------------------------------------------

const DAY_ORDER = [1, 2, 3, 4, 5, 6, 0] // Mon-first; matrix weekday 0 = Sunday
const DAY_LABELS = ['Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat', 'Sun']

function PeakHours() {
  const m = usePeakMatrix(PEAK_WEEKS)
  const maxAvg = useMemo(() => {
    if (!m) return 0
    let mx = 0
    for (const row of m.cells) for (const c of row) if (c.avg > mx) mx = c.avg
    return mx
  }, [m])

  if (!m) return <Skeleton className="h-40 w-full rounded-[var(--r-md)]" />

  const hasData = m.cells.some(row => row.some(c => c.avg >= 0))
  if (!hasData) {
    return <EmptyState icon={<IconClock size={26} />} title="No player history yet"
      hint="Once identified players connect over a few days, their busiest hours light up here." />
  }

  return (
    <div className="overflow-x-auto">
      <div
        className="min-w-[34rem]"
        role="img"
        aria-label={`Peak-hours heatmap: identified players by weekday and hour over the last ${PEAK_WEEKS} weeks`}
      >
        <div className="grid gap-[3px]" style={{gridTemplateColumns: 'auto repeat(24, minmax(0, 1fr))'}}>
          {DAY_ORDER.map((dow, ri) => (
            <Fragment key={dow}>
              <div className="pr-2 text-right text-[10px] leading-4 text-[var(--text-3)]">{DAY_LABELS[ri]}</div>
              {Array.from({length: 24}, (_, h) => {
                const c = m.cells[dow][h]
                const known = c.avg >= 0
                const ratio = known && maxAvg > 0 ? c.avg / maxAvg : 0
                return (
                  <div
                    key={h}
                    title={known
                      ? `${DAY_LABELS[ri]} ${String(h).padStart(2, '0')}:00 — avg ${c.avg.toFixed(1)}, peak ${Math.round(Math.max(0, c.max))}`
                      : `${DAY_LABELS[ri]} ${String(h).padStart(2, '0')}:00 — no data`}
                    className="h-4 rounded-[2px] transition-colors"
                    style={{
                      background: known
                        ? `color-mix(in srgb, var(--accent) ${Math.round(7 + 85 * ratio)}%, transparent)`
                        : 'var(--panel-2)',
                    }}
                  />
                )
              })}
            </Fragment>
          ))}
          {/* hour axis */}
          <div />
          {Array.from({length: 24}, (_, h) => (
            <div key={h} className="mt-1 text-center text-[9px] leading-none text-[var(--text-3)]">{h % 6 === 0 ? h : ''}</div>
          ))}
        </div>
        <div className="mt-3 flex items-center justify-end gap-2 text-[10px] text-[var(--text-3)]">
          <span>quiet</span>
          <div className="flex gap-[3px]">
            {[10, 30, 55, 80, 100].map(p => (
              <span key={p} className="h-3 w-4 rounded-[2px]" style={{background: `color-mix(in srgb, var(--accent) ${p}%, transparent)`}} />
            ))}
          </div>
          <span>busy</span>
        </div>
      </div>
    </div>
  )
}

// ---------------------------------------------------------------------------
// Uptime bands
// ---------------------------------------------------------------------------

function UptimeBands({windowMs}: {windowMs: number}) {
  const rep = useTunnelUptime(windowMs)
  // Freeze the right edge to a 30 s bucket so bands don't re-lay-out every render.
  const [nowBucket, setNowBucket] = useState(() => Math.floor(Date.now() / 30_000))
  useEffect(() => {
    const t = setInterval(() => setNowBucket(Math.floor(Date.now() / 30_000)), 30_000)
    return () => clearInterval(t)
  }, [])
  const now = nowBucket * 30_000

  if (!rep) return <Skeleton className="h-28 w-full rounded-[var(--r-md)]" />

  const rows: UptimeReport['tunnels'] = [rep.link, ...rep.tunnels]
  const hasAny = rows.some(r => r.events && r.events.length > 0)
  if (!hasAny) {
    return <EmptyState icon={<IconClock size={26} />} title="No uptime history yet"
      hint="Link and tunnel state changes are recorded here as they happen." />
  }

  // Shared domain across all rows: window start (or earliest event for 'all').
  let t0 = windowMs > 0 ? now - windowMs : Infinity
  if (windowMs <= 0) {
    for (const r of rows) if (r.events?.length) t0 = Math.min(t0, r.events[0].t)
    if (!isFinite(t0)) t0 = now - 86_400_000
  }
  const span = Math.max(1, now - t0)

  return (
    <div className="space-y-3">
      {rows.map((r, i) => (
        <div key={r.tunnelId || `link-${i}`}>
          <div className="mb-1 flex items-center justify-between gap-2 text-xs">
            <span className="truncate font-medium text-[var(--text-2)]">{r.name || r.tunnelId || 'Tunnel'}</span>
            <UptimePctBadge pct={r.uptimePct} />
          </div>
          <Band events={r.events ?? []} t0={t0} span={span} now={now} />
        </div>
      ))}
      <div className="flex items-center gap-3 pt-1 text-[10px] text-[var(--text-3)]">
        <LegendSwatch color="var(--good)" label="up" />
        <LegendSwatch color="var(--bad)" label="down" />
        <LegendSwatch color="var(--panel-2)" label="unknown" />
      </div>
    </div>
  )
}

function Band({events, t0, span, now}: {events: {t: number; up: boolean}[]; t0: number; span: number; now: number}) {
  // Build [start,end,up?] segments. Time before the first event is "unknown".
  const segs: {left: number; width: number; up: boolean | null}[] = []
  const pct = (t: number) => ((Math.min(now, Math.max(t0, t)) - t0) / span) * 100
  const clipped = events.filter(e => e.t < now)
  if (!clipped.length || clipped[0].t > t0) {
    const end = clipped.length ? clipped[0].t : now
    segs.push({left: 0, width: pct(end), up: null})
  }
  for (let i = 0; i < clipped.length; i++) {
    const start = Math.max(clipped[i].t, t0)
    const end = i + 1 < clipped.length ? clipped[i + 1].t : now
    if (end <= start) continue
    segs.push({left: pct(start), width: pct(end) - pct(start), up: clipped[i].up})
  }
  const color = (up: boolean | null) => (up === null ? 'var(--panel-2)' : up ? 'var(--good)' : 'var(--bad)')
  return (
    <div className="relative h-3 overflow-hidden rounded-[var(--r-sm)] border border-[var(--border)] bg-[var(--panel-2)]">
      {segs.map((s, i) => (
        <div key={i} className="absolute inset-y-0" style={{left: `${s.left}%`, width: `${Math.max(0, s.width)}%`, background: color(s.up)}} />
      ))}
    </div>
  )
}

function UptimePctBadge({pct}: {pct: number}) {
  if (pct < 0) return <Badge tone="neutral">unknown</Badge>
  const tone = pct >= 99.5 ? 'good' : pct >= 97 ? 'warn' : 'bad'
  return <Badge tone={tone}>{fmtPct(pct, 2)} up</Badge>
}

function LegendSwatch({color, label}: {color: string; label: string}) {
  return (
    <span className="inline-flex items-center gap-1.5">
      <span className="h-2.5 w-2.5 rounded-[2px] border border-[var(--border)]" style={{background: color}} />
      {label}
    </span>
  )
}

// ---------------------------------------------------------------------------
// Packet-loss chart (rides the bandwidth-history loss gauge)
// ---------------------------------------------------------------------------

function LossChart({range}: {range: Range}) {
  const buckets = useBandwidthLoss(range)
  const series = useMemo((): LineSeries[] => {
    const pts = (buckets ?? []).map(b => ({t: b.t, v: b.lh >= 0 ? b.lh : null}))
    return [{points: pts, cssVar: 'var(--bad)', label: 'peak', fill: true, format: v => `${v.toFixed(2)}%`}]
  }, [buckets])
  const hasData = (buckets ?? []).some(b => b.lh >= 0)
  if (buckets === null) return <Skeleton className="h-[180px] w-full rounded-[var(--r-md)]" />
  if (!hasData) {
    return <EmptyState icon={<IconActivity size={26} />} title="No loss recorded"
      hint="Packet loss on the control link is charted here once it's been sampled." />
  }
  return <LineChart series={series} height={180} formatY={v => `${v.toFixed(1)}%`}
    label="Control-link packet loss" emptyHint="No loss recorded" />
}

// ---------------------------------------------------------------------------
// Connection history + session replay
// ---------------------------------------------------------------------------

function ConnectionHistory({tunnels, rangeMs, cc, onClearCc}: {
  tunnels: UIStatus['tunnels']; rangeMs: number; cc: string; onClearCc: () => void
}) {
  const [chip, setChip] = useState<string>('all') // 'all' | tunnel id
  const [offset, setOffset] = useState(0)
  const [replay, setReplay] = useState<SessionMeta | null>(null)
  const list = tunnels ?? []

  // Quantise the window edge to a minute so the query key is stable between
  // polls (raw Date.now() would bust the cache every render).
  const [nowMin, setNowMin] = useState(() => Math.floor(Date.now() / 60_000))
  useEffect(() => {
    const t = setInterval(() => setNowMin(Math.floor(Date.now() / 60_000)), 60_000)
    return () => clearInterval(t)
  }, [])
  useEffect(() => { setOffset(0) }, [chip, rangeMs, cc])

  const query: SessionsQuery = {
    playerUuid: '',
    tunnelId: chip === 'all' ? '' : chip,
    cc,
    sinceMs: rangeMs > 0 ? nowMin * 60_000 - rangeMs : 0,
    offset,
    limit: SESSIONS_PAGE_SIZE,
  }
  const page = useSessions(query)
  const rows = page?.sessions ?? []
  const total = page?.total ?? 0
  const loading = page === null

  const columns: Column<SessionMeta>[] = [
    {
      key: 'when', header: 'Started', pin: true,
      render: s => <span className="text-[var(--text-2)]">{fmtLastSeen(s.startedMs)}</span>,
    },
    {
      key: 'player', header: 'Player',
      render: s => (
        <span className="inline-flex items-center gap-2">
          {s.playerUuid
            ? <AvatarImg id={s.playerUuid} size={32} px={20}
                className="h-5 w-5 rounded-[var(--r-xs)] border border-[var(--border)] bg-[var(--panel-2)]" />
            : <span className="h-5 w-5 rounded-[var(--r-xs)] border border-[var(--border)] bg-[var(--panel-2)]" />}
          <span className="truncate font-mono text-[13px] text-[var(--text)]">{s.playerName || '—'}</span>
        </span>
      ),
    },
    {key: 'tunnel', header: 'Tunnel', render: s => <span className="text-[var(--text-2)]">{s.tunnelName || '—'}</span>},
    {
      key: 'ip', header: 'From', mono: true,
      render: s => (
        <span className="inline-flex items-center gap-1.5">
          {flagEmoji(s.cc) && <span title={s.cc}>{flagEmoji(s.cc)}</span>}
          <span className="select-text">{s.clientIp}</span>
        </span>
      ),
    },
    {
      key: 'dur', header: 'Duration', align: 'right',
      render: s => s.endedMs
        ? <span className="tabular-nums text-[var(--text-2)]">{fmtDuration(s.endedMs - s.startedMs)}</span>
        : <Badge tone="good">live</Badge>,
    },
    {key: 'dl', header: '↓', align: 'right', render: s => fmtBytes(s.bytesOut)},
    {key: 'ul', header: '↑', align: 'right', render: s => fmtBytes(s.bytesIn)},
    {
      key: 'rtt', header: 'Ping', align: 'right',
      render: s => hasRtt(s.rttAvg)
        ? <span className="tabular-nums text-[var(--rtt)]">{fmtRtt(s.rttAvg)}</span>
        : <span className="text-[var(--text-3)]">—</span>,
    },
  ]

  return (
    <Card
      title={<span className="inline-flex items-center gap-2.5">Connection history <LiveDot /></span>}
      subtitle="Every session in this range — click a row to replay it"
      pad={false}
      action={
        <div className="flex flex-wrap items-center gap-1.5 pr-1">
          {cc && (
            <Pill on onClick={onClearCc}>
              <span className="inline-flex items-center gap-1.5">{flagEmoji(cc) ?? '🌐'} {cc} ✕</span>
            </Pill>
          )}
          {list.length > 1 && (
            <>
              <Pill on={chip === 'all'} onClick={() => setChip('all')}>All</Pill>
              {list.map(t => <Pill key={t.id} on={chip === t.id} onClick={() => setChip(t.id)}>{t.name}</Pill>)}
            </>
          )}
        </div>
      }
    >
      {loading ? (
        <div className="space-y-2 p-4">
          {Array.from({length: 6}, (_, i) => <Skeleton key={i} className="h-9 w-full rounded" />)}
        </div>
      ) : (
        <>
          <DataTable
            columns={columns}
            rows={rows}
            rowKey={s => s.id}
            dense
            onRowClick={setReplay}
            empty={{
              icon: <IconConnections size={26} />,
              title: cc ? `No sessions from ${cc} in this range` : 'No sessions in this range',
              hint: cc ? 'Clear the country filter to see every session.' : 'Connections recorded on Minecraft-aware tunnels appear here.',
            }}
          />
          {total > SESSIONS_PAGE_SIZE && (
            <div className="flex items-center justify-between px-4 py-2.5 text-xs text-[var(--text-3)]">
              <span className="tabular-nums">{offset + 1}–{Math.min(offset + SESSIONS_PAGE_SIZE, total)} of {total}</span>
              <div className="flex gap-1.5">
                <Button variant="ghost" size="sm" disabled={offset === 0}
                  onClick={() => setOffset(o => Math.max(0, o - SESSIONS_PAGE_SIZE))}>Previous</Button>
                <Button variant="ghost" size="sm" disabled={offset + SESSIONS_PAGE_SIZE >= total}
                  onClick={() => setOffset(o => o + SESSIONS_PAGE_SIZE)}>Next</Button>
              </div>
            </div>
          )}
        </>
      )}
      {replay && <SessionReplay session={replay} onClose={() => setReplay(null)} />}
    </Card>
  )
}

function SessionReplay({session, onClose}: {session: SessionMeta; onClose: () => void}) {
  const tl = useSessionTimeline(session.id)
  // One playhead shared by both charts and the scrubber: hover either chart
  // or drag the range input, and every surface tracks the same instant.
  const [cursorT, setCursorT] = useState<number | null>(null)

  const traffic = useMemo((): LineSeries[] => {
    const pts = tl?.traffic ?? []
    return [
      {points: pts.map(p => ({t: p.t, v: p.out})), cssVar: 'var(--dl)', label: '↓', fill: true, format: fmtBytes},
      {points: pts.map(p => ({t: p.t, v: p.in})), cssVar: 'var(--ul)', label: '↑', format: fmtBytes},
    ]
  }, [tl])
  const ping = useMemo((): LineSeries[] => {
    const pts = tl?.rtt ?? []
    return [{points: pts.map(p => ({t: p.t, v: p.avg})), cssVar: 'var(--rtt)', label: 'RTT', fill: true, format: fmtRtt}]
  }, [tl])

  // Scrubber domain and step come from the timeline's own buckets.
  const domain = useMemo(() => {
    const ts = [...(tl?.traffic ?? []).map(p => p.t), ...(tl?.rtt ?? []).map(p => p.t)]
    if (ts.length < 2) return null
    const sorted = [...new Set(ts)].sort((a, b) => a - b)
    let step = Infinity
    for (let i = 1; i < sorted.length; i++) step = Math.min(step, sorted[i] - sorted[i - 1])
    return {min: sorted[0], max: sorted[sorted.length - 1], step: isFinite(step) ? step : 15_000}
  }, [tl])

  // Readout at the playhead: nearest traffic and RTT samples.
  const readout = useMemo(() => {
    if (cursorT === null || !tl) return null
    const nearest = <T extends {t: number}>(pts: T[]): T | null => {
      let best: T | null = null
      let bestD = Infinity
      for (const p of pts) {
        const d = Math.abs(p.t - cursorT)
        if (d < bestD) { bestD = d; best = p }
      }
      return best
    }
    const tp = nearest(tl.traffic)
    const rp = nearest(tl.rtt)
    const stepSec = (domain?.step ?? 15_000) / 1000
    return {
      t: cursorT,
      dl: tp ? tp.out / stepSec : null,
      ul: tp ? tp.in / stepSec : null,
      rtt: rp ? rp.avg : null,
    }
  }, [cursorT, tl, domain])

  const dur = session.endedMs ? fmtDuration(session.endedMs - session.startedMs) : 'live'

  return (
    <Modal title="Session replay" onClose={onClose} wide>
      <div className="space-y-4">
        <div className="flex items-center gap-3">
          {session.playerUuid
            ? <AvatarImg id={session.playerUuid} size={64} px={44}
                className="h-11 w-11 shrink-0 rounded-[var(--r-md)] border border-[var(--border)] bg-[var(--panel-2)]" />
            : <span className="h-11 w-11 shrink-0 rounded-[var(--r-md)] border border-[var(--border)] bg-[var(--panel-2)]" />}
          <div className="min-w-0">
            <div className="flex items-center gap-2">
              <span className="truncate font-mono text-base font-semibold text-[var(--text)]">{session.playerName || 'Unidentified'}</span>
              {flagEmoji(session.cc) && <span title={session.cc}>{flagEmoji(session.cc)}</span>}
              {session.endedMs === 0 && <Badge tone="good">live</Badge>}
            </div>
            <div className="mt-0.5 flex flex-wrap items-center gap-x-3 gap-y-0.5 text-xs text-[var(--text-3)]">
              <span className="inline-flex items-center gap-1 font-mono">
                {session.clientIp} <CopyIcon text={session.clientIp} title="Copy IP" />
              </span>
              <span>{session.tunnelName || '—'}</span>
              <span>{fmtLastSeen(session.startedMs)}</span>
            </div>
          </div>
        </div>

        <div className="grid grid-cols-2 gap-3 @xl:grid-cols-4">
          <ReplayStat label="Duration" value={dur} />
          <ReplayStat label="Traffic" value={fmtBytes(session.bytesIn + session.bytesOut)} sub={`↓ ${fmtBytes(session.bytesOut)} · ↑ ${fmtBytes(session.bytesIn)}`} />
          <ReplayStat label="Avg ping" value={hasRtt(session.rttAvg) ? fmtRtt(session.rttAvg) : '—'} />
          <ReplayStat label="Country" value={flagEmoji(session.cc) ? `${flagEmoji(session.cc)} ${session.cc}` : '—'} />
        </div>

        <div>
          <div className="mb-1.5 text-xs font-medium text-[var(--text-2)]">Traffic</div>
          <LineChart
            series={traffic}
            height={170}
            scale="binary"
            formatY={fmtBytes}
            label="Session traffic"
            cursorT={cursorT}
            onCursor={setCursorT}
            emptyHint={tl === null ? 'loading…' : 'No per-connection samples for this session.'}
          />
        </div>
        <div>
          <div className="mb-1.5 text-xs font-medium text-[var(--text-2)]">Latency</div>
          <LineChart
            series={ping}
            height={140}
            formatY={fmtRtt}
            label="Session latency"
            cursorT={cursorT}
            onCursor={setCursorT}
            emptyHint={tl === null ? 'loading…' : 'No round-trip samples for this session.'}
          />
        </div>

        {/* Playhead scrubber: a manual timeline walk across both charts.
            Native range semantics give arrow-key stepping for free; there is
            no autoplay, so it is inherently reduced-motion-safe. */}
        {domain && (
          <div className="space-y-1.5">
            <input
              type="range"
              aria-label="Session playhead"
              min={domain.min}
              max={domain.max}
              step={domain.step}
              value={cursorT ?? domain.max}
              onChange={e => setCursorT(Number(e.target.value))}
              className="w-full accent-[var(--accent)]"
            />
            <div className="flex items-center justify-between font-mono text-[11px] tabular-nums text-[var(--text-3)]">
              <span>{fmtTickTime(domain.min, 'second')}</span>
              {readout && (
                <span className="inline-flex items-center gap-3">
                  <span className="text-[var(--text-2)]">{fmtTickTime(readout.t, 'second')}</span>
                  {readout.dl !== null && <span className="text-[var(--dl)]">↓ {fmtRate(readout.dl)}</span>}
                  {readout.ul !== null && <span className="text-[var(--ul)]">↑ {fmtRate(readout.ul)}</span>}
                  {readout.rtt !== null && <span className="text-[var(--rtt)]">{fmtRtt(readout.rtt)}</span>}
                </span>
              )}
              <span>{fmtTickTime(domain.max, 'second')}</span>
            </div>
          </div>
        )}
      </div>
    </Modal>
  )
}

function ReplayStat({label, value, sub}: {label: string; value: string; sub?: string}) {
  return (
    <div className="pf-well px-3 py-2">
      <div className="text-[11px] text-[var(--text-3)]">{label}</div>
      <div className="mt-0.5 truncate text-sm font-semibold tabular-nums" title={value}>{value}</div>
      {sub && <div className="truncate text-[10.5px] tabular-nums text-[var(--text-3)]" title={sub}>{sub}</div>}
    </div>
  )
}
