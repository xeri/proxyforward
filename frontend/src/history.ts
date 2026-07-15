// Bandwidth-history data layer: per-range polling of the Go store with a
// module-level cache so charts paint instantly on remount (tab switches) and
// keep their data between visits.
//
// DIRECTION MAPPING — the single place it happens: the store/wire speak
// conntrack ("in" = client → server, "out" = server → client). The UI calls
// server → player traffic "download". Therefore: download = out/oo/oh/ol/oc,
// upload = in/io/ih/il/ic. Nothing else in the frontend re-maps directions.
import {useEffect, useRef, useState} from 'react'
import {BandwidthHistory, PeerStats} from '../wailsjs/go/app/App'
import {stats} from '../wailsjs/go/models'

export type Bucket = stats.Bucket
export type HistoryResult = stats.HistoryResult
export type PeerStat = stats.PeerStat

export type RangeKey = '15s' | '1m' | '15m' | '1h' | '6h' | '24h' | '7d' | '30d' | 'all'
export type ChartMode = 'line' | 'candles' | 'bars'

export type RangeSpec = {
  label: string
  windowMs: number // 0 = everything the store has
  pollMs: number
  buckets: number
  render: 'line' | 'bars' // bars ranges show per-bucket totals, not rates
  candlesOk: boolean
}

export const RANGE_KEYS: RangeKey[] = ['15s', '1m', '15m', '1h', '6h', '24h', '7d', '30d', 'all']

export const RANGES: Record<RangeKey, RangeSpec> = {
  '15s': {label: '15s', windowMs: 15_000, pollMs: 250, buckets: 150, render: 'line', candlesOk: false},
  '1m':  {label: '1m',  windowMs: 60_000, pollMs: 500, buckets: 300, render: 'line', candlesOk: false},
  '15m': {label: '15m', windowMs: 900_000, pollMs: 1_000, buckets: 300, render: 'line', candlesOk: true},
  '1h':  {label: '1h',  windowMs: 3_600_000, pollMs: 5_000, buckets: 240, render: 'line', candlesOk: true},
  '6h':  {label: '6h',  windowMs: 21_600_000, pollMs: 15_000, buckets: 288, render: 'line', candlesOk: true},
  '24h': {label: '24h', windowMs: 86_400_000, pollMs: 30_000, buckets: 288, render: 'line', candlesOk: true},
  '7d':  {label: '7d',  windowMs: 604_800_000, pollMs: 60_000, buckets: 168, render: 'bars', candlesOk: false},
  '30d': {label: '30d', windowMs: 2_592_000_000, pollMs: 60_000, buckets: 30, render: 'bars', candlesOk: false},
  'all': {label: 'All', windowMs: 0, pollMs: 60_000, buckets: 300, render: 'bars', candlesOk: false},
}

// Module-level cache: survives unmounts, seeds the hook synchronously.
const historyCache = new Map<RangeKey, HistoryResult>()

/** useBandwidthHistory polls the backend at the range's cadence. */
export function useBandwidthHistory(range: RangeKey): HistoryResult | null {
  const [data, setData] = useState<HistoryResult | null>(() => historyCache.get(range) ?? null)
  const rangeRef = useRef(range)
  rangeRef.current = range
  useEffect(() => {
    setData(historyCache.get(range) ?? null) // instant paint from cache
    const spec = RANGES[range]
    let alive = true
    const fetchOnce = () => {
      BandwidthHistory(spec.windowMs, spec.buckets)
        .then(h => {
          if (!alive || rangeRef.current !== range) return
          historyCache.set(range, h)
          setData(h)
        })
        .catch(() => {})
    }
    fetchOnce()
    const t = setInterval(fetchOnce, spec.pollMs)
    return () => { alive = false; clearInterval(t) }
  }, [range])
  return data
}

let peersCache: PeerStat[] | null = null

/** usePeers polls the per-client lifetime records. */
export function usePeers(pollMs = 5_000): PeerStat[] {
  const [peers, setPeers] = useState<PeerStat[]>(() => peersCache ?? [])
  useEffect(() => {
    let alive = true
    const fetchOnce = () => {
      PeerStats()
        .then(p => {
          if (!alive) return
          peersCache = p ?? []
          setPeers(peersCache)
        })
        .catch(() => {})
    }
    fetchOnce()
    const t = setInterval(fetchOnce, pollMs)
    return () => { alive = false; clearInterval(t) }
  }, [pollMs])
  return peers
}

// ---- chart preferences (persisted) ----

export function loadRangePref(): RangeKey {
  const v = localStorage.getItem('pf-chart-range') as RangeKey | null
  return v && RANGES[v] ? v : '1m'
}
export function saveRangePref(r: RangeKey) { localStorage.setItem('pf-chart-range', r) }

export function loadCandlePref(): boolean {
  return localStorage.getItem('pf-chart-candles') === '1'
}
export function saveCandlePref(on: boolean) { localStorage.setItem('pf-chart-candles', on ? '1' : '0') }

/** Uptime strip: show the coverage timeline (when the app was actually
 * recording) beneath the time axis. Off by default — it's a secondary read. */
export function loadUptimePref(): boolean {
  return localStorage.getItem('pf-chart-uptime') === '1'
}
export function saveUptimePref(on: boolean) { localStorage.setItem('pf-chart-uptime', on ? '1' : '0') }

/** Per-series visibility: download / upload / connections / RTT. */
export type SeriesVisibility = {dl: boolean; ul: boolean; conn: boolean; rtt: boolean}

export function loadSeriesPref(): SeriesVisibility {
  try {
    const raw = localStorage.getItem('pf-chart-series')
    // Connections and RTT default OFF so download/upload stay the primary read;
    // the user opts into the recessive overlays.
    if (!raw) return {dl: true, ul: true, conn: false, rtt: false}
    const p = JSON.parse(raw)
    return {dl: p.dl !== false, ul: p.ul !== false, conn: p.conn === true, rtt: p.rtt === true}
  } catch {
    return {dl: true, ul: true, conn: false, rtt: false}
  }
}
export function saveSeriesPref(s: SeriesVisibility) {
  localStorage.setItem('pf-chart-series', JSON.stringify(s))
}

/** Effective render mode for a range given the candle preference. */
export function modeFor(range: RangeKey, candles: boolean): ChartMode {
  const spec = RANGES[range]
  if (spec.render === 'bars') return 'bars'
  return candles && spec.candlesOk ? 'candles' : 'line'
}
