// A 2D SVG world choropleth: countries filled by activity or latency, drawn
// from the bundled equirectangular geometry (worldgeo.ts). Pure SVG — no
// projection library, no external tiles. Hover is two-way with the companion
// country list: the parent owns hoverCc so highlighting a row lights the map
// and vice versa. Fill scales sqrt for activity (so a few busy countries don't
// wash out the rest) and ramps good→bad for latency.
import {useMemo} from 'react'
import type {CountryAgg} from '../../analytics'
import {flagEmoji, fmtBytes, fmtRtt, hasRtt} from '../../state'
import {WORLD, WORLD_VIEWBOX, WorldCountry} from './worldgeo'

export type GeoMetric = 'activity' | 'latency'

/** activityFill: accent tint on a sqrt scale, floored so the quietest country
 * still reads as "present". Mirrors the peak-hours heatmap's accent ramp. */
function activityFill(sessions: number, max: number): string {
  const t = max > 0 ? Math.sqrt(Math.max(0, sessions)) / Math.sqrt(max) : 0
  return `color-mix(in srgb, var(--accent) ${Math.round(14 + 78 * t)}%, transparent)`
}

/** latencyFill: green→amber→red ramp. rttAvg 0 means located but never
 * RTT-sampled — a neutral accent tint keeps it on the map without a false
 * "great latency" green. */
function latencyFill(ms: number): string {
  if (ms <= 0) return 'color-mix(in srgb, var(--accent) 24%, transparent)'
  if (ms <= 90) {
    const p = Math.max(0, Math.min(1, (ms - 25) / 65))
    return `color-mix(in srgb, var(--warn) ${Math.round(p * 100)}%, var(--good))`
  }
  const p = Math.max(0, Math.min(1, (ms - 90) / 110))
  return `color-mix(in srgb, var(--bad) ${Math.round(p * 100)}%, var(--warn))`
}

const LAND = 'color-mix(in srgb, var(--text-3) 14%, transparent)'

export function WorldMap({data, metric, hoverCc, onHover, onSelect, selectedCc}: {
  data: CountryAgg[]
  metric: GeoMetric
  hoverCc: string | null
  onHover: (cc: string | null) => void
  /** Click-to-filter: called with the country's code (parent toggles). */
  onSelect?: (cc: string) => void
  /** The active country filter; outlined on the map. */
  selectedCc?: string | null
}) {
  const {byCc, maxSessions} = useMemo(() => {
    const m = new Map<string, CountryAgg>()
    let mx = 0
    for (const d of data) {
      m.set(d.cc.toUpperCase(), d)
      if (d.sessions > mx) mx = d.sessions
    }
    return {byCc: m, maxSessions: mx}
  }, [data])

  const fill = (d: CountryAgg) => (metric === 'activity' ? activityFill(d.sessions, maxSessions) : latencyFill(d.rttAvg))

  // Two passes: the inert land backdrop, then the data countries on top so
  // their fills and hover strokes are never overdrawn by a neighbour.
  const dataCountries: {geo: WorldCountry; d: CountryAgg}[] = []
  for (const g of WORLD) {
    const d = byCc.get(g.cc)
    if (d) dataCountries.push({geo: g, d})
  }
  const hoverGeo = hoverCc ? WORLD.find(g => g.cc === hoverCc) : undefined
  const hoverDatum = hoverCc ? byCc.get(hoverCc) : undefined

  return (
    <div className="relative">
      <svg
        viewBox={`0 0 ${WORLD_VIEWBOX.w} ${WORLD_VIEWBOX.h}`}
        preserveAspectRatio="xMidYMid meet"
        className="block h-auto w-full rounded-[var(--r-md)] border border-[var(--border)] bg-[var(--panel-2)]"
        role="img"
        aria-label="World map of where players connect from"
        onMouseLeave={() => onHover(null)}
      >
        {/* Land backdrop — every country, inert. */}
        <g fill={LAND} stroke="var(--border)" strokeWidth={0.4} vectorEffect="non-scaling-stroke">
          {WORLD.map(g => <path key={g.cc} d={g.d} />)}
        </g>
        {/* Data countries — interactive, filled by the active metric. Click
            (or Enter/Space) filters the dashboard by that country. */}
        <g stroke="var(--panel-2)" strokeWidth={0.5} vectorEffect="non-scaling-stroke">
          {dataCountries.map(({geo, d}) => (
            <path
              key={geo.cc}
              d={geo.d}
              fill={fill(d)}
              className="cursor-pointer outline-none transition-[fill] duration-150"
              style={{opacity: hoverCc && hoverCc !== geo.cc ? 0.55 : 1}}
              onMouseEnter={() => onHover(geo.cc)}
              {...(onSelect ? {
                tabIndex: 0,
                role: 'button',
                'aria-label': `Filter by ${d.country || geo.cc}`,
                'aria-pressed': selectedCc === geo.cc,
                onClick: () => onSelect(geo.cc),
                onKeyDown: (e: React.KeyboardEvent) => {
                  if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); onSelect(geo.cc) }
                },
                onFocus: () => onHover(geo.cc),
              } : {})}
            />
          ))}
        </g>
        {/* Active filter outlined persistently under the hover pass. */}
        {selectedCc && (() => {
          const g = WORLD.find(x => x.cc === selectedCc)
          return g ? (
            <path d={g.d} fill="none" stroke="var(--accent)" strokeWidth={1.4}
              vectorEffect="non-scaling-stroke" style={{pointerEvents: 'none'}} />
          ) : null
        })()}
        {/* Hovered country re-drawn on top with an accent outline. */}
        {hoverGeo && hoverDatum && (
          <path
            d={hoverGeo.d}
            fill={fill(hoverDatum)}
            stroke="var(--accent-2)"
            strokeWidth={1.4}
            vectorEffect="non-scaling-stroke"
            style={{pointerEvents: 'none'}}
          />
        )}
      </svg>

      {hoverGeo && hoverDatum && (
        <MapTooltip geo={hoverGeo} d={hoverDatum} />
      )}

      <Legend metric={metric} />
    </div>
  )
}

/** MapTooltip anchors to the country's label point (cx,cy in viewBox units),
 * expressed as a percentage of the rendered box so it tracks any width. It
 * sits above the country, flipping below near the top edge. */
function MapTooltip({geo, d}: {geo: WorldCountry; d: CountryAgg}) {
  const leftPct = Math.max(9, Math.min(91, (geo.cx / WORLD_VIEWBOX.w) * 100))
  const topPct = (geo.cy / WORLD_VIEWBOX.h) * 100
  const below = geo.cy < 90
  return (
    <div
      className="pointer-events-none absolute z-10 w-max max-w-[15rem] -translate-x-1/2 rounded-[var(--r-sm)] border border-[var(--border-strong)] bg-[var(--panel-3)] px-2.5 py-1.5 text-xs shadow-lg"
      style={{
        left: `${leftPct}%`,
        top: `${topPct}%`,
        transform: `translate(-50%, ${below ? '0.7rem' : 'calc(-100% - 0.7rem)'})`,
      }}
    >
      <div className="flex items-center gap-1.5 font-medium text-[var(--text)]">
        <span>{flagEmoji(d.cc) ?? '🌐'}</span>
        <span className="truncate">{d.country || d.cc}</span>
      </div>
      <div className="mt-1 grid grid-cols-2 gap-x-3 gap-y-0.5 tabular-nums text-[var(--text-2)]">
        <TipRow label="Players" value={String(d.players)} />
        <TipRow label="Sessions" value={String(d.sessions)} />
        <TipRow label="Data" value={fmtBytes(d.bytesIn + d.bytesOut)} />
        <TipRow label="Avg ping" value={hasRtt(d.rttAvg) ? fmtRtt(d.rttAvg) : '—'} />
      </div>
    </div>
  )
}

function TipRow({label, value}: {label: string; value: string}) {
  return (
    <div className="flex items-center justify-between gap-2">
      <span className="text-[var(--text-3)]">{label}</span>
      <span className="text-[var(--text)]">{value}</span>
    </div>
  )
}

function Legend({metric}: {metric: GeoMetric}) {
  if (metric === 'activity') {
    return (
      <div className="mt-2.5 flex items-center justify-end gap-2 text-[10px] text-[var(--text-3)]">
        <span>quiet</span>
        <div className="flex gap-[3px]">
          {[16, 38, 58, 76, 92].map(p => (
            <span key={p} className="h-3 w-5 rounded-[2px]" style={{background: `color-mix(in srgb, var(--accent) ${p}%, transparent)`}} />
          ))}
        </div>
        <span>busy</span>
      </div>
    )
  }
  return (
    <div className="mt-2.5 flex items-center justify-end gap-2 text-[10px] text-[var(--text-3)]">
      <span>fast</span>
      <div className="h-3 w-28 rounded-[2px]" style={{background: 'linear-gradient(90deg, var(--good), var(--warn), var(--bad))'}} />
      <span>slow</span>
    </div>
  )
}

