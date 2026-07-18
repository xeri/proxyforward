import { WorldMap } from 'frontend'

// CountryAgg rows (analytics.CountryAgg). SE rttAvg 0 = located, never
// RTT-sampled → neutral accent tint on the latency map, not a false green.
const data = [
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

/** Activity choropleth: accent tint on a sqrt scale over the inert land
 * backdrop, quiet→busy legend at the foot. */
export function Activity() {
  return (
    <div style={{ width: 560 }}>
      <WorldMap data={data} metric="activity" hoverCc={null} onHover={() => {}} />
    </div>
  )
}

/** Latency choropleth: good→warn→bad ramp per country; Sweden's unsampled 0
 * stays a neutral accent tint. fast→slow gradient legend. */
export function Latency() {
  return (
    <div style={{ width: 560 }}>
      <WorldMap data={data} metric="latency" hoverCc={null} onHover={() => {}} />
    </div>
  )
}

/** Parent-owned interaction state: Brazil hovered (accent outline + anchored
 * tooltip with players/sessions/data/ping) while DE holds the active filter
 * outline — the two-way state the companion GeoRank drives. (BR sits near the
 * map's center, so the tooltip stays inside the transform-contained cell.) */
export function HoverAndFilter() {
  return (
    <div style={{ width: 560 }}>
      <WorldMap data={data} metric="activity" hoverCc="BR" onHover={() => {}}
        selectedCc="DE" onSelect={() => {}} />
    </div>
  )
}
