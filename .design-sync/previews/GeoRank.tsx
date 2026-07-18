import { GeoRank } from 'frontend'

// CountryAgg shape (frontend/src/analytics.ts → wailsjs analytics.CountryAgg).
// rttAvg 0 on SE = located but never RTT-sampled → renders "—".
const rows = [
  { cc: 'US', country: 'United States', players: 14, sessions: 39, bytesIn: 812_402_688, bytesOut: 9_663_676_416, rttAvg: 158 },
  { cc: 'NZ', country: 'New Zealand', players: 6, sessions: 31, bytesIn: 402_653_184, bytesOut: 5_368_709_120, rttAvg: 12 },
  { cc: 'DE', country: 'Germany', players: 5, sessions: 17, bytesIn: 268_435_456, bytesOut: 3_221_225_472, rttAvg: 251 },
  { cc: 'AU', country: 'Australia', players: 4, sessions: 15, bytesIn: 134_217_728, bytesOut: 2_147_483_648, rttAvg: 41 },
  { cc: 'JP', country: 'Japan', players: 3, sessions: 9, bytesIn: 100_663_296, bytesOut: 1_610_612_736, rttAvg: 132 },
  { cc: 'BR', country: 'Brazil', players: 2, sessions: 8, bytesIn: 67_108_864, bytesOut: 966_367_641, rttAvg: 214 },
  { cc: 'GB', country: 'United Kingdom', players: 2, sessions: 6, bytesIn: 33_554_432, bytesOut: 536_870_912, rttAvg: 268 },
  { cc: 'SE', country: 'Sweden', players: 1, sessions: 3, bytesIn: 16_777_216, bytesOut: 268_435_456, rttAvg: 0 },
  { cc: 'KR', country: 'South Korea', players: 1, sessions: 2, bytesIn: 8_388_608, bytesOut: 134_217_728, rttAvg: 121 },
]

/** Sessions ranking (the Analytics Geography companion list): accent metric
 * bars scaled to the top row, player counts on the sub-line. */
export function Activity() {
  return (
    <div style={{ maxWidth: 340 }}>
      <GeoRank rows={rows} metric="activity" />
    </div>
  )
}

/** Latency ranking: each RTT wears its tone (good < 60 ms < warn < 130 ms <
 * bad), bars match, and SE's unsampled 0 renders "—" — never a fake number. */
export function Latency() {
  const byLat = [...rows].sort((a, b) => (b.rttAvg > 0 ? b.rttAvg : -1) - (a.rttAvg > 0 ? a.rttAvg : -1))
  return (
    <div style={{ maxWidth: 340 }}>
      <GeoRank rows={byLat} metric="latency" />
    </div>
  )
}

/** Compact (the Players-wall "Latency by country" card): at most 8 tight rows,
 * no header, with an active country filter marked on its row. */
export function CompactSelected() {
  const byLat = [...rows].sort((a, b) => (b.rttAvg > 0 ? b.rttAvg : -1) - (a.rttAvg > 0 ? a.rttAvg : -1))
  return (
    <div style={{ maxWidth: 320 }}>
      <GeoRank rows={byLat} metric="latency" compact selectedCc="DE" onSelect={() => {}} />
    </div>
  )
}
