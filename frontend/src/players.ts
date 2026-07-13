// Player-intelligence data layer: polled reads of the analytics store with
// module-level caches so the wall and detail views paint instantly on
// remount. All queries are already clamped server-side (≤80 players, ≤100
// sessions, ≤300 chart points) to fit the IPC frame; nothing here re-fetches
// more than one page at a time.
import {PlayerDetail, PlayerHistory, PlayerLatency, Players} from '../wailsjs/go/app/App'
import {analytics} from '../wailsjs/go/models'
import {usePolled} from './hooks'

export type PlayerCard = analytics.PlayerCard
export type PlayersPage = analytics.PlayersPage
export type PlayerDetailData = analytics.PlayerDetail
export type SessionMeta = analytics.SessionMeta
export type TrafficPoint = analytics.TrafficPoint
export type LatencyPoint = analytics.LatencyPoint

export type PlayersQuery = {
  search: string
  sort: 'recent' | 'name' | 'playtime' | 'sessions' | 'data'
  tunnelId: string
  cc: string
  offset: number
  limit: number
}

export const PLAYERS_PAGE_SIZE = 60 // under the server clamp of 80

// Cross-screen handoff: other screens (Traffic's live sessions) queue a
// dossier to open the next time Players mounts. Nav switches unmount screens
// wholesale, so this can't ride component state; Players consumes it in its
// initial-state callback.
let pendingDossier: string | null = null

/** openDossierOnMount queues a player dossier for the Players screen's next
 * mount. Pair it with a navigate('players') call. */
export function openDossierOnMount(uuid: string) { pendingDossier = uuid }

/** takePendingDossier returns and clears the queued dossier. */
export function takePendingDossier(): string | null {
  const v = pendingDossier
  pendingDossier = null
  return v
}

const pagesCache = new Map<string, PlayersPage>()
const detailCache = new Map<string, PlayerDetailData>()
const historyCache = new Map<string, TrafficPoint[]>()
const latencyCache = new Map<string, LatencyPoint[]>()

/** usePlayersPage polls one wall page at 5 s. */
export function usePlayersPage(q: PlayersQuery, pollMs = 5_000): PlayersPage | null {
  const key = JSON.stringify(q)
  return usePolled(pagesCache, key, () => Players(analytics.PlayersQuery.createFrom(q)), pollMs)
}

/** usePlayerDetail polls the full per-player view while one is selected. */
export function usePlayerDetail(uuid: string | null, pollMs = 5_000): PlayerDetailData | null {
  return usePolled(detailCache, uuid, () => PlayerDetail(uuid!), pollMs)
}

/** usePlayerHistory polls the player's bucketed traffic series. */
export function usePlayerHistory(uuid: string | null, windowMs: number, pollMs = 15_000): TrafficPoint[] | null {
  const key = uuid === null ? null : `${uuid}:${windowMs}`
  return usePolled(historyCache, key, () => PlayerHistory(uuid!, windowMs), pollMs)
}

/** usePlayerLatency polls the player's bucketed round-trip latency series. */
export function usePlayerLatency(uuid: string | null, windowMs: number, pollMs = 15_000): LatencyPoint[] | null {
  const key = uuid === null ? null : `${uuid}:${windowMs}`
  return usePolled(latencyCache, key, () => PlayerLatency(uuid!, windowMs), pollMs)
}

// ---- shared formatting ------------------------------------------------------

/** fmtLastSeen: relative "when" for wall tiles and history spans. */
export function fmtLastSeen(t: number, online?: boolean): string {
  if (online) return 'online now'
  if (!t) return '—'
  const s = Math.max(0, Math.floor((Date.now() - t) / 1000))
  if (s < 90) return 'just now'
  if (s < 3600) return `${Math.round(s / 60)}m ago`
  if (s < 86400) return `${Math.round(s / 3600)}h ago`
  if (s < 7 * 86400) return `${Math.round(s / 86400)}d ago`
  return `${Math.round(s / (7 * 86400))}w ago`
}

/** fmtPlaytime: compact hours-first duration for stat tiles. */
export function fmtPlaytime(ms: number): string {
  const m = Math.floor(ms / 60_000)
  if (m < 60) return `${m}m`
  const h = Math.floor(m / 60)
  if (h < 48) return `${h}h ${m % 60}m`
  return `${Math.floor(h / 24)}d ${h % 24}h`
}
