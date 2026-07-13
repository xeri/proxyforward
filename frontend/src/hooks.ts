// Shared data-layer hooks. usePolled is the fetch-on-interval core behind
// every analytics/player poll (one definition; analytics.ts and players.ts
// build their typed hooks on it); useDebounced trails a fast-changing value
// (search inputs) by a settle delay.
import {useEffect, useRef, useState} from 'react'

/** usePolled: fetch-on-interval with a per-endpoint module cache. A null key
 * pauses polling (e.g. no player selected); the cache gives an instant paint
 * on remount and between tab switches. */
export function usePolled<T>(cache: Map<string, T>, key: string | null, fetcher: () => Promise<T>, pollMs: number): T | null {
  const [data, setData] = useState<T | null>(() => (key !== null ? cache.get(key) ?? null : null))
  const fetchRef = useRef(fetcher)
  fetchRef.current = fetcher
  useEffect(() => {
    if (key === null) { setData(null); return }
    setData(cache.get(key) ?? null) // instant paint from cache
    let alive = true
    const fetchOnce = () => {
      fetchRef.current()
        .then(v => { if (alive) { cache.set(key, v); setData(v) } })
        .catch(() => {})
    }
    fetchOnce()
    const t = setInterval(fetchOnce, pollMs)
    return () => { alive = false; clearInterval(t) }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key, pollMs])
  return data
}

/** useDebounced trails value by ms — the standard search-input settle. */
export function useDebounced<T>(value: T, ms: number): T {
  const [v, setV] = useState(value)
  useEffect(() => {
    const t = setTimeout(() => setV(value), ms)
    return () => clearTimeout(t)
  }, [value, ms])
  return v
}
