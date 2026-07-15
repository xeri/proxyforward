// GeoRank: the ranked country list — full-size beside the Geography map on
// Analytics, compact ("Latency by country") on the Players wall. Each row
// carries a metric bar; hover cross-highlights the map, click filters by
// country (re-click clears — the parent owns both states).
import {useMemo} from 'react'
import type {CountryAgg} from '../analytics'
import type {GeoMetric} from './charts/WorldMap'
import {flagEmoji, fmtRtt, hasRtt} from '../state'

const rttTone = (ms: number): 'good' | 'warn' | 'bad' => (ms < 60 ? 'good' : ms < 130 ? 'warn' : 'bad')

export function GeoRank({rows, metric, hoverCc, onHover, onSelect, selectedCc, compact = false}: {
  rows: CountryAgg[]
  metric: GeoMetric
  hoverCc?: string | null
  onHover?: (cc: string | null) => void
  /** Click-to-filter: called with the row's country code (parent toggles). */
  onSelect?: (cc: string) => void
  /** The currently active country filter, marked on its row. */
  selectedCc?: string | null
  /** Compact: at most 8 tight rows (the Players-wall card). */
  compact?: boolean
}) {
  const shown = compact ? rows.slice(0, 8) : rows
  const max = useMemo(
    () => shown.reduce((m, r) => Math.max(m, metric === 'latency' ? r.rttAvg : r.sessions), 0),
    [shown, metric],
  )
  return (
    <div className="min-w-0" onMouseLeave={() => onHover?.(null)}>
      {!compact && (
        <div className="mb-1.5 flex items-center justify-between px-0.5 text-[11px] uppercase tracking-wide text-[var(--text-3)]">
          <span>Country</span>
          <span>{metric === 'latency' ? 'Avg ping' : 'Sessions'}</span>
        </div>
      )}
      {/* Deliberately NOT overscroll-contain (rubberband.ts): an embedded list on
          a long page must keep chaining, or reaching its end would freeze the
          page under the cursor. Overscroll here falls through to the page, and
          the page is what bounces. */}
      <div className={compact ? 'space-y-0.5' : 'max-h-[22rem] space-y-0.5 overflow-y-auto pr-1'}>
        {shown.map(r => {
          const val = metric === 'latency' ? r.rttAvg : r.sessions
          const frac = max > 0 && val > 0 ? val / max : 0
          const on = hoverCc === r.cc || selectedCc === r.cc
          const barColor = metric === 'latency'
            ? `var(--${hasRtt(r.rttAvg) ? rttTone(r.rttAvg) : 'text-3'})`
            : 'var(--accent)'
          return (
            <button
              key={r.cc}
              type="button"
              onMouseEnter={() => onHover?.(r.cc)}
              onClick={onSelect ? () => onSelect(r.cc) : undefined}
              aria-pressed={selectedCc === r.cc}
              className={`flex w-full items-center gap-2.5 rounded-[var(--r-sm)] px-2 text-left transition-colors ${
                compact ? 'py-1' : 'py-1.5'
              } ${on ? 'bg-[color-mix(in_srgb,var(--accent)_12%,transparent)]' : 'hover:bg-[var(--panel-2)]'}`}
            >
              <span className="w-5 shrink-0 text-center text-sm leading-none">{flagEmoji(r.cc) ?? '🌐'}</span>
              <span className="min-w-0 flex-1">
                <span className="flex items-baseline justify-between gap-2">
                  <span className="truncate text-[13px] text-[var(--text)]">{r.country || r.cc}</span>
                  <span className="shrink-0 tabular-nums text-[13px] text-[var(--text-2)]">
                    {metric === 'latency'
                      ? (hasRtt(r.rttAvg) ? <span style={{color: `var(--${rttTone(r.rttAvg)})`}}>{fmtRtt(r.rttAvg)}</span> : '—')
                      : r.sessions}
                  </span>
                </span>
                <span className="mt-1 flex items-center gap-2">
                  <span className="relative h-1 flex-1 overflow-hidden rounded-full bg-[var(--panel-2)]">
                    <span className="absolute inset-y-0 left-0 rounded-full" style={{width: `${Math.max(3, frac * 100)}%`, background: barColor}} />
                  </span>
                  <span className="shrink-0 text-[10.5px] tabular-nums text-[var(--text-3)]">{r.players}p</span>
                </span>
              </span>
            </button>
          )
        })}
      </div>
    </div>
  )
}
