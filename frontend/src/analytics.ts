// Dashboard data layer: polled reads of the analytics rollups with
// module-level caches so the Analytics screen paints instantly on remount and
// keeps its data between tab switches. Cadences here are slow (15–60 s) —
// these are historical aggregates, not the live 2 Hz tick. Every query is
// already clamped server-side to fit the 64 KiB IPC frame.
import {GeoSnapshot, GeoStatus, PeakMatrix, Sessions, SessionTimeline, Summary, TunnelUptime} from '../wailsjs/go/app/App'
import {analytics, geo} from '../wailsjs/go/models'
import {Bucket, RangeKey, useBandwidthHistory} from './history'
import {usePolled} from './hooks'

// Type aliases carry the `Data` suffix where the analytics class name collides
// with the imported binding function of the same name (Summary, PeakMatrix, …).
export type SummaryData = analytics.Summary
export type PeakMatrixData = analytics.PeakMatrix
export type UptimeReport = analytics.UptimeReport
export type TunnelUptimeRow = analytics.TunnelUptime
export type UptimeSpan = analytics.UptimeSpan
export type SessionMeta = analytics.SessionMeta
export type SessionsPage = analytics.SessionsPage
export type SessionTimelineData = analytics.SessionTimeline
export type CountryAgg = analytics.CountryAgg
export type GeoStatusData = geo.Status

export type SessionsQuery = {
  playerUuid: string
  tunnelId: string
  cc: string
  sinceMs: number
  offset: number
  limit: number
}

export const SESSIONS_PAGE_SIZE = 50 // under the server clamp of 100
export const PEAK_WEEKS = 8 // lookback for the day×hour heatmap

/** The dashboard range picker: a trailing window (0 = all time). */
export type Range = '24h' | '7d' | '30d' | 'all'
export const RANGE_MS: Record<Range, number> = {
  '24h': 86_400_000, '7d': 604_800_000, '30d': 2_592_000_000, all: 0,
}

const summaryCache = new Map<string, SummaryData>()
const peakCache = new Map<string, PeakMatrixData>()
const uptimeCache = new Map<string, UptimeReport>()
const sessionsCache = new Map<string, SessionsPage>()
const timelineCache = new Map<string, SessionTimelineData>()
const geoCache = new Map<string, CountryAgg[]>()
const geoStatusCache = new Map<string, GeoStatusData>()

/** useSummary polls the range stat-tile payload (30 s). */
export function useSummary(rangeMs: number, pollMs = 30_000): SummaryData | null {
  return usePolled(summaryCache, String(rangeMs), () => Summary(rangeMs), pollMs)
}

/** usePeakMatrix polls the day-of-week × hour-of-day player heatmap (60 s). */
export function usePeakMatrix(weeks: number, pollMs = 60_000): PeakMatrixData | null {
  return usePolled(peakCache, String(weeks), () => PeakMatrix(weeks), pollMs)
}

/** useTunnelUptime polls the per-tunnel + control-link uptime report (30 s). */
export function useTunnelUptime(windowMs: number, pollMs = 30_000): UptimeReport | null {
  return usePolled(uptimeCache, String(windowMs), () => TunnelUptime(windowMs), pollMs)
}

/** useSessions polls one page of connection history (15 s). */
export function useSessions(q: SessionsQuery, pollMs = 15_000): SessionsPage | null {
  const key = JSON.stringify(q)
  return usePolled(sessionsCache, key, () => Sessions(analytics.SessionsQuery.createFrom(q)), pollMs)
}

/** useSessionTimeline polls one session's replay (traffic + RTT). Null id
 * pauses (no session selected). */
export function useSessionTimeline(id: number | null, pollMs = 15_000): SessionTimelineData | null {
  return usePolled(timelineCache, id === null ? null : String(id), () => SessionTimeline(id!), pollMs)
}

/** useGeoSnapshot polls the per-country session aggregates behind the world
 * map and latency-by-country list (30 s). Server-clamped to ≤250 countries. */
export function useGeoSnapshot(rangeMs: number, pollMs = 30_000): CountryAgg[] | null {
  return usePolled(geoCache, String(rangeMs), () => GeoSnapshot(rangeMs), pollMs)
}

/** useGeoStatus polls which GeoLite2 databases the engine has loaded (60 s),
 * so the map can tell "not configured" apart from "configured but no data". */
export function useGeoStatus(pollMs = 60_000): GeoStatusData | null {
  return usePolled(geoStatusCache, 'geo', () => GeoStatus(), pollMs)
}

// Packet-loss history rides the bandwidth store's loss gauge (columns l*),
// so it reuses the same polled history the Traffic screen draws from.
const LOSS_RANGE: Record<Range, RangeKey> = {'24h': '24h', '7d': '7d', '30d': '30d', all: 'all'}

/** useBandwidthLoss returns the history buckets carrying the loss gauge for a
 * dashboard range (null while the first fetch is in flight). */
export function useBandwidthLoss(range: Range): Bucket[] | null {
  const h = useBandwidthHistory(LOSS_RANGE[range])
  return h ? h.buckets : null
}

// ---- shared formatting ------------------------------------------------------

/** fmtPct renders a 0–100 percentage; -1 (unknown) becomes an em dash. */
export function fmtPct(v: number, digits = 1): string {
  if (v < 0) return '—'
  return `${v.toFixed(digits)}%`
}

/** fmtWhen: absolute timestamp for peak markers (e.g. "Jul 3, 20:14"). */
export function fmtWhen(t: number): string {
  if (!t) return ''
  return new Date(t).toLocaleString(undefined, {month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit'})
}
