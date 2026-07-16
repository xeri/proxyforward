import {useEffect, useMemo, useState} from 'react'
import {RANGE_MS, Range, useGeoSnapshot, useGeoStatus} from '../analytics'
import {AvatarImg} from '../components/AvatarImg'
import {LineChart, LineSeries} from '../components/charts/LineChart'
import {Column, DataTable} from '../components/DataTable'
import {GeoRank} from '../components/GeoRank'
import {IconChevronRight, IconClose, IconPlayers, IconSearch} from '../components/icons'
import {
  Badge, Button, Card, CopyIcon, EmptyState, LiveDot, MonoChip, Overline, PageHeader, Pill, PillGroup, SegmentedControl, Skeleton, TextInput,
} from '../components/ui'
import {useDebounced} from '../hooks'
import {
  PLAYERS_PAGE_SIZE, PlayerCard, PlayersQuery, SessionMeta,
  fmtLastSeen, fmtPlaytime, takePendingDossier, usePlayerDetail, usePlayerHistory, usePlayerLatency, usePlayersPage,
} from '../players'
import {UIStatus, flagEmoji, fmtBytes, fmtDuration, fmtRtt, hasRtt} from '../state'

/** Players: the head wall and the per-player dossier. Detail is screen-local
 * state (no router) — the wall stays mounted-warm via the module caches. */
export function Players({status}: {status: UIStatus}) {
  // A queued handoff (Traffic → player click) opens straight into the dossier.
  const [detail, setDetail] = useState<string | null>(() => takePendingDossier())

  // Live UUIDs from the 2 Hz tick: the wall's "online" dots never wait for
  // the 5 s poll.
  const liveUUIDs = useMemo(() => {
    const set = new Set<string>()
    for (const c of status.connections ?? []) {
      if (c.playerUuid) set.add(c.playerUuid)
    }
    return set
  }, [status.connections])

  // The daemon can't answer analytics (older build, or the store failed to
  // open): one honest empty state instead of skeletons that never fill.
  if (status.analyticsUnsupported) {
    return (
      <div className="pf-stagger space-y-4">
        <PageHeader title="Players" subtitle="Everyone who has joined through the tunnel, by name." />
        <Card><AnalyticsUnavailable /></Card>
      </div>
    )
  }

  if (detail) {
    return <PlayerDossier uuid={detail} live={liveUUIDs.has(detail)} onBack={() => setDetail(null)} />
  }
  return <Wall status={status} liveUUIDs={liveUUIDs} onOpen={setDetail} />
}

/** AnalyticsUnavailable: the shared explanation for a daemon without the
 * analytics store (Analytics.tsx renders the same). */
export function AnalyticsUnavailable() {
  return (
    <EmptyState
      icon={<IconPlayers size={28} />}
      title="Analytics isn't available"
      hint="The connected daemon is an older build without the analytics store, or the analytics database failed to open (see logs)."
    />
  )
}

// ---------------------------------------------------------------------------
// The wall
// ---------------------------------------------------------------------------

type SortKey = PlayersQuery['sort']
type FilterChip = 'all' | 'online' | string // string = tunnel id

function Wall({status, liveUUIDs, onOpen}: {
  status: UIStatus
  liveUUIDs: Set<string>
  onOpen: (uuid: string) => void
}) {
  const [search, setSearch] = useState('')
  const [sort, setSort] = useState<SortKey>('recent')
  const [chip, setChip] = useState<FilterChip>('all')
  const [cc, setCc] = useState('')
  const [offset, setOffset] = useState(0)
  const debouncedSearch = useDebounced(search, 250)

  // Filter/search changes restart paging from the first page.
  useEffect(() => { setOffset(0) }, [debouncedSearch, sort, chip, cc])

  const query: PlayersQuery = {
    search: debouncedSearch,
    sort,
    tunnelId: chip === 'all' || chip === 'online' ? '' : chip,
    cc,
    offset,
    limit: PLAYERS_PAGE_SIZE,
  }
  const page = usePlayersPage(query)
  const loading = page === null

  // "Online" is a client-side lens over the live tick join — the store has no
  // notion of "connected right now".
  const players = useMemo(() => {
    const rows = page?.players ?? []
    if (chip !== 'online') return rows
    return rows.filter(p => p.online || liveUUIDs.has(p.uuid))
  }, [page, chip, liveUUIDs])

  const tunnels = status.tunnels ?? []
  const total = page?.total ?? 0
  const canPage = chip !== 'online' && total > PLAYERS_PAGE_SIZE

  return (
    <div className="pf-stagger space-y-4">
      <PageHeader
        title="Players"
        subtitle="Everyone who has joined through the tunnel, by name."
        action={
          <span className="inline-flex items-center gap-2.5">
            {liveUUIDs.size > 0 && <LiveDot />}
            <Badge tone={liveUUIDs.size ? 'good' : 'neutral'}>{liveUUIDs.size} online</Badge>
          </span>
        }
      />

      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="flex flex-wrap items-center gap-1.5">
          <PillGroup<FilterChip>
            value={chip}
            onChange={setChip}
            options={[
              {value: 'all', label: 'All'},
              {value: 'online', label: (
                <>
                  <span className="inline-flex h-1.5 w-1.5 rounded-full" style={{background: 'var(--good)'}} />
                  Online
                </>
              )},
              ...(tunnels.length > 1 ? tunnels.map(t => ({value: t.id as FilterChip, label: <>{t.name}</>})) : []),
            ]}
          />
          {cc && (
            <Pill on onClick={() => setCc('')}>
              <span className="inline-flex items-center gap-1.5">
                {flagEmoji(cc) ?? '🌐'} {cc} <IconClose size={11} />
              </span>
            </Pill>
          )}
        </div>
        <div className="flex items-center gap-2">
          <div className="w-44">
            <TextInput
              size="sm" icon={<IconSearch size={14} />}
              value={search} onChange={setSearch}
              placeholder="Search names…" ariaLabel="Search players by name"
            />
          </div>
          <SegmentedControl<SortKey>
            value={sort}
            onChange={setSort}
            options={[
              {value: 'recent', label: 'Recent'},
              {value: 'name', label: 'Name'},
              {value: 'playtime', label: 'Playtime'},
              {value: 'sessions', label: 'Sessions'},
              {value: 'data', label: 'Data'},
            ]}
          />
        </div>
      </div>

      {loading ? (
        <TileGrid>
          {Array.from({length: 12}, (_, i) => <TileSkeleton key={i} />)}
        </TileGrid>
      ) : players.length === 0 ? (
        <Card>
          <EmptyState
            icon={<IconPlayers size={28} />}
            title={debouncedSearch ? 'No players match' : cc ? `Nobody from ${cc} yet` : chip === 'online' ? 'Nobody is online right now' : 'No players seen yet'}
            hint={debouncedSearch
              ? 'Try a shorter fragment — search matches anywhere in the name.'
              : cc
                ? 'Clear the country filter to see the whole wall.'
                : chip === 'online'
                  ? 'Heads light up here the moment someone joins through the tunnel.'
                  : <>Player names are read from Minecraft logins on tunnels with <b>Minecraft aware</b> enabled (Tunnels → tunnel options). Once someone joins through such a tunnel, their head appears here.</>}
          />
        </Card>
      ) : (
        <>
          <TileGrid>
            {players.map(p => (
              <PlayerTile key={p.uuid} p={p} live={p.online || liveUUIDs.has(p.uuid)}
                showData={sort === 'data'} onClick={() => onOpen(p.uuid)} />
            ))}
          </TileGrid>
          {canPage && (
            <div className="flex items-center justify-between text-xs text-[var(--text-3)]">
              <span className="tabular-nums">{offset + 1}–{Math.min(offset + PLAYERS_PAGE_SIZE, total)} of {total}</span>
              <div className="flex gap-1.5">
                <Button variant="ghost" size="sm" disabled={offset === 0}
                  onClick={() => setOffset(o => Math.max(0, o - PLAYERS_PAGE_SIZE))}>Previous</Button>
                <Button variant="ghost" size="sm" disabled={offset + PLAYERS_PAGE_SIZE >= total}
                  onClick={() => setOffset(o => o + PLAYERS_PAGE_SIZE)}>Next</Button>
              </div>
            </div>
          )}
        </>
      )}

      <LatencyByCountry cc={cc} onSelect={next => setCc(prev => (prev === next ? '' : next))} />
    </div>
  )
}

/** LatencyByCountry: a compact 30-day country ranking on the wall — click a
 * row to filter the wall by that country. Hidden when geo is unconfigured or
 * nothing has been located (the Analytics Geography card explains those). */
function LatencyByCountry({cc, onSelect}: {cc: string; onSelect: (cc: string) => void}) {
  const rows = useGeoSnapshot(RANGE_MS['30d'])
  const geoStatus = useGeoStatus()
  const sorted = useMemo(() => {
    const r = rows ? [...rows] : []
    const lat = (a: {rttAvg: number}) => (hasRtt(a.rttAvg) ? a.rttAvg : -1)
    r.sort((a, b) => lat(b) - lat(a))
    return r
  }, [rows])
  if (!rows || rows.length === 0 || (geoStatus && !geoStatus.cityLoaded)) return null
  return (
    <Card label="Latency by country" subtitle="Average ping over the last 30 days — click to filter the wall">
      <GeoRank rows={sorted} metric="latency" compact selectedCc={cc || null} onSelect={onSelect} />
    </Card>
  )
}

function TileGrid({children}: {children: React.ReactNode}) {
  return (
    <div className="pf-stagger-grid grid grid-cols-[repeat(auto-fill,minmax(11rem,1fr))] gap-3">
      {children}
    </div>
  )
}

/** PlayerTile: one head on the wall. `showData` swaps the last-seen line for
 * the player's total traffic so a data-sorted wall reads as a ranking. */
function PlayerTile({p, live, showData, onClick}: {p: PlayerCard; live: boolean; showData?: boolean; onClick: () => void}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className="pf-card pf-lift pf-press group flex flex-col items-center gap-2 p-3.5 text-center"
    >
      <span className="relative">
        <AvatarImg
          id={p.uuid}
          size={128}
          px={84}
          className="h-[84px] w-[84px] rounded-[var(--r-md)] border border-[var(--border)] bg-[var(--panel-2)]"
        />
        {live && (
          <span
            className="pf-halo absolute -bottom-1 -right-1 h-3 w-3 rounded-full border-2 border-[var(--panel)]"
            style={{background: 'var(--good)', ['--halo' as string]: 'var(--good)'}}
          />
        )}
      </span>
      <span className="w-full">
        <span className="block truncate font-mono text-[13px] font-semibold text-[var(--text)]" title={p.name}>
          {p.name}
        </span>
        <span className="mt-0.5 flex items-center justify-center gap-1.5 text-[10.5px] text-[var(--text-3)]">
          {p.offline && <Badge tone="warn">cracked</Badge>}
          {hasRtt(p.rttMs) && <PingBadge ms={p.rttMs} />}
          {showData
            ? <span className="truncate tabular-nums" title={`↓ ${fmtBytes(p.bytesOut)} · ↑ ${fmtBytes(p.bytesIn)}`}>{fmtBytes(p.bytesIn + p.bytesOut)}</span>
            : <span className="truncate">{fmtLastSeen(p.lastSeen, live)}</span>}
        </span>
      </span>
    </button>
  )
}

function TileSkeleton() {
  return (
    <div className="pf-card flex flex-col items-center gap-2 p-3.5">
      <Skeleton className="h-[84px] w-[84px] rounded-[var(--r-md)]" />
      <Skeleton className="h-3.5 w-20 rounded" />
      <Skeleton className="h-2.5 w-14 rounded" />
    </div>
  )
}

/** PingBadge: the player's average RTT on the --rtt ramp (populates once the
 * gateway starts reporting per-connection RTT). */
function PingBadge({ms}: {ms: number}) {
  const tone = ms < 60 ? 'var(--good)' : ms < 130 ? 'var(--warn)' : 'var(--bad)'
  return (
    <span className="inline-flex items-center gap-1 font-mono tabular-nums" style={{color: tone}}>
      <span className="inline-block h-1.5 w-1.5 rounded-full" style={{background: tone}} />
      {Math.round(ms)}ms
    </span>
  )
}

// ---------------------------------------------------------------------------
// The dossier (detail view)
// ---------------------------------------------------------------------------

type HistRange = Range // 24h | 7d | 30d | all — shared with the dashboard

function PlayerDossier({uuid, live, onBack}: {uuid: string; live: boolean; onBack: () => void}) {
  const det = usePlayerDetail(uuid)
  const [range, setRange] = useState<HistRange>('7d')
  const history = usePlayerHistory(uuid, RANGE_MS[range])
  const latency = usePlayerLatency(uuid, RANGE_MS[range])
  const card = det?.card

  const traffic = useMemo((): LineSeries[] => {
    const pts = history ?? []
    return [
      // Direction mapping (see history.ts): download = server→player = out.
      {points: pts.map(p => ({t: p.t, v: p.out})), cssVar: 'var(--dl)', label: '↓', fill: true, format: fmtBytes},
      {points: pts.map(p => ({t: p.t, v: p.in})), cssVar: 'var(--ul)', label: '↑', format: fmtBytes},
    ]
  }, [history])

  const ping = useMemo((): LineSeries[] => {
    const pts = latency ?? []
    return [{points: pts.map(p => ({t: p.t, v: p.avg})), cssVar: 'var(--rtt)', label: 'RTT', fill: true, format: fmtRtt}]
  }, [latency])

  // The store answered but knows no such player (stale link, pruned row):
  // an honest not-found beats rendering "undefined".
  if (det !== null && !det.card?.uuid) {
    return (
      <div className="pf-stagger space-y-4">
        <div>
          <Button variant="ghost" size="sm" onClick={onBack}>
            <IconChevronRight size={13} className="rotate-180" /> All players
          </Button>
        </div>
        <Card>
          <EmptyState
            icon={<IconPlayers size={28} />}
            title="Player not found"
            hint="This player is no longer in the store — their history may have aged past the retention window."
          />
        </Card>
      </div>
    )
  }

  const sessionCols: Column<SessionMeta>[] = [
    {
      key: 'when', header: 'Started', pin: true,
      render: s => <span className="text-[var(--text-2)]">{fmtLastSeen(s.startedMs)}</span>,
    },
    {
      key: 'dur', header: 'Duration',
      render: s => s.endedMs
        ? <span className="tabular-nums text-[var(--text-2)]">{fmtDuration(s.endedMs - s.startedMs)}</span>
        : <Badge tone="good">live</Badge>,
    },
    {key: 'tunnel', header: 'Tunnel', render: s => <span className="text-[var(--text-2)]">{s.tunnelName || '—'}</span>},
    {
      key: 'ip', header: 'From', mono: true,
      render: s => (
        <span className="inline-flex items-center gap-1.5">
          {flagEmoji(s.cc) && <span title={s.cc}>{flagEmoji(s.cc)}</span>}
          <span className="select-text">{s.clientIp}</span>
          <CopyIcon text={s.clientIp} title="Copy IP" />
        </span>
      ),
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
    <div className="pf-stagger space-y-4">
      <div>
        <Button variant="ghost" size="sm" onClick={onBack}>
          <IconChevronRight size={13} className="rotate-180" /> All players
        </Button>
      </div>

      {/* identity band */}
      <Card>
        <div className="flex flex-col gap-4 @2xl:flex-row @2xl:items-center">
          <AvatarImg
            id={uuid}
            size={128}
            px={96}
            className="h-24 w-24 shrink-0 rounded-[var(--r-lg)] border border-[var(--border)] bg-[var(--panel-2)]"
          />
          <div className="min-w-0 flex-1">
            <div className="flex flex-wrap items-center gap-2">
              <h2 className="font-mono text-[length:var(--fs-metric)] font-bold leading-tight tracking-tight text-[var(--text)]">
                {card ? card.name : <Skeleton className="h-7 w-44 rounded" />}
              </h2>
              {live && <Badge tone="good">online</Badge>}
              {card?.offline && <Badge tone="warn">cracked / offline-mode</Badge>}
              {card && hasRtt(card.rttMs) && <PingBadge ms={card.rttMs} />}
              {card?.lastCc && flagEmoji(card.lastCc) && <span title={card.lastCc}>{flagEmoji(card.lastCc)}</span>}
            </div>
            <div className="mt-1 flex flex-wrap gap-x-4 gap-y-0.5 text-xs text-[var(--text-3)]">
              {card && !card.offline && (
                <span className="inline-flex items-center gap-1 font-mono">
                  {shortUuid(uuid)} <CopyIcon text={uuid} title="Copy UUID" />
                </span>
              )}
              {card && <span>first seen {fmtLastSeen(card.firstSeen)}</span>}
              {card && <span>last seen {fmtLastSeen(card.lastSeen, live)}</span>}
            </div>
          </div>
          <div className="grid shrink-0 grid-cols-3 gap-x-7 gap-y-1 text-center @2xl:text-right">
            <DossierStat label="Sessions" value={card ? String(card.sessions) : '—'} />
            <DossierStat label="Playtime" value={card ? fmtPlaytime(card.playMs) : '—'} />
            <DossierStat
              label="Traffic"
              value={card ? fmtBytes(card.bytesIn + card.bytesOut) : '—'}
              sub={card ? `↓ ${fmtBytes(card.bytesOut)} · ↑ ${fmtBytes(card.bytesIn)}` : undefined}
            />
          </div>
        </div>
      </Card>

      {/* history — one range picker drives both charts below */}
      <div className="flex items-center justify-between gap-3 px-1">
        <h3 className="text-[length:var(--fs-title)] font-semibold tracking-tight text-[var(--text)]">History</h3>
        <SegmentedControl<HistRange>
          value={range}
          onChange={setRange}
          options={[
            {value: '24h', label: '24h'}, {value: '7d', label: '7d'},
            {value: '30d', label: '30d'}, {value: 'all', label: 'All'},
          ]}
        />
      </div>

      <Card label="Traffic" subtitle="Bytes moved per interval while this player was connected">
        <LineChart
          series={traffic}
          height={200}
          scale="binary"
          formatY={fmtBytes}
          label={card ? `${card.name}'s traffic history` : 'Player traffic history'}
          emptyHint={history === null ? 'loading…' : 'No recorded traffic in this range.'}
        />
      </Card>

      <Card label="Latency" subtitle="Round-trip time measured at the gateway while this player was connected">
        <LineChart
          series={ping}
          height={160}
          formatY={fmtRtt}
          label={card ? `${card.name}'s latency history` : 'Player latency history'}
          emptyHint={latency === null ? 'loading…'
            : 'No latency recorded in this range — needs a gateway build that reports per-connection RTT.'}
        />
      </Card>

      <div className="grid grid-cols-1 gap-4 @min-[88rem]:grid-cols-2">
        {/* sessions */}
        <Card label="Sessions" subtitle="Most recent connections" pad={false}
          action={<div className="pr-4"><Badge tone="neutral">{det?.recent?.length ?? 0}</Badge></div>}>
          <DataTable
            columns={sessionCols}
            rows={det?.recent ?? []}
            rowKey={s => s.id}
            dense
            empty={{title: 'No sessions recorded', hint: 'Connection history lands here as this player joins.'}}
          />
        </Card>

        <div className="space-y-4">
          {/* username history — locally observed only (Mojang removed the
              public name-history API in 2022). */}
          <Card label="Names seen on this proxy" subtitle="Local observations — not Mojang's full history" pad={false}>
            {det && det.names?.length ? (
              <ul className="divide-y divide-[var(--border)]">
                {det.names.map(n => (
                  <li key={n.name} className="flex items-baseline justify-between px-4 py-2">
                    <span className="font-mono text-[13px] font-medium text-[var(--text)]">{n.name}</span>
                    <span className="text-[11px] tabular-nums text-[var(--text-3)]">
                      {fmtSpan(n.firstSeen, n.lastSeen)}
                    </span>
                  </li>
                ))}
              </ul>
            ) : (
              <div className="px-4 pb-2"><EmptyState title="No names recorded yet" /></div>
            )}
          </Card>

          {/* IPs */}
          <Card label="Addresses" subtitle="Where this player connects from" pad={false}>
            {det && det.ips?.length ? (
              <ul className="divide-y divide-[var(--border)]">
                {det.ips.map(ip => (
                  <li key={ip.ip} className="flex items-center justify-between gap-2 px-4 py-2">
                    <span className="inline-flex min-w-0 items-center gap-1.5 font-mono text-[13px]">
                      {flagEmoji(ip.cc) && <span title={ip.cc}>{flagEmoji(ip.cc)}</span>}
                      <span className="select-text truncate">{ip.ip}</span>
                      <CopyIcon text={ip.ip} title="Copy IP" />
                    </span>
                    <span className="shrink-0 text-[11px] tabular-nums text-[var(--text-3)]">
                      <MonoChip>{ip.sessions}×</MonoChip> <span className="ml-1.5">{fmtLastSeen(ip.lastSeen)}</span>
                    </span>
                  </li>
                ))}
              </ul>
            ) : (
              <div className="px-4 pb-2"><EmptyState title="No addresses recorded yet" /></div>
            )}
          </Card>
        </div>
      </div>
    </div>
  )
}

function DossierStat({label, value, sub}: {label: string; value: string; sub?: string}) {
  return (
    <div className="min-w-0">
      <Overline>{label}</Overline>
      <div className="mt-0.5 truncate text-[length:var(--fs-metric)] font-semibold leading-tight tabular-nums" title={value}>{value}</div>
      {sub && <div className="truncate text-[11px] tabular-nums text-[var(--text-3)]" title={sub}>{sub}</div>}
    </div>
  )
}

// ---- helpers ---------------------------------------------------------------

function shortUuid(uuid: string): string {
  return uuid.length > 13 ? `${uuid.slice(0, 8)}…${uuid.slice(-4)}` : uuid
}

/** fmtSpan renders a first→last window compactly ("Mar 3 – Jun 12"). */
function fmtSpan(first: number, last: number): string {
  const f = new Date(first).toLocaleDateString(undefined, {month: 'short', day: 'numeric'})
  const l = new Date(last).toLocaleDateString(undefined, {month: 'short', day: 'numeric'})
  return f === l ? f : `${f} – ${l}`
}
