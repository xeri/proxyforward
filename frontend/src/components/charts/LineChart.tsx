// LineChart: the generic single-axis live chart — per-player traffic and
// latency, tunnel uptime, packet loss. Same engineering language as the
// bandwidth hero (mono numerals, fine grid, straight segments, tweened
// refreshes, glow that self-gates on data-fx) but one shared y-scale and a
// plain {t, v} series shape. BandwidthChart keeps its own OHLC renderer.
import {useId, useMemo, useRef, useState} from 'react'
import {MONO, fmtTickTime, niceLinear, niceScale, timeTicks, useMeasuredWidth, useTweenedValues} from './util'

export type LinePoint = {t: number; v: number | null}

export type LineSeries = {
  points: LinePoint[]
  /** Stroke/fill color, e.g. 'var(--dl)'. Never hardcode a hex here. */
  cssVar: string
  /** Readout key in the hover line. */
  label: string
  /** Draw the vertical-fade area under the line. */
  fill?: boolean
  /** Value formatter for this series' hover readout. */
  format?: (v: number) => string
}

export function LineChart({series, height = 180, scale = 'linear', formatY, emptyHint, stepped = false, label, cursorT, onCursor}: {
  series: LineSeries[]
  height?: number
  /** 'binary' steps the axis in powers of two (byte rates); 'linear' uses 1/2/5. */
  scale?: 'linear' | 'binary'
  /** Axis-label formatter (defaults to the first series' format, then round). */
  formatY?: (v: number) => string
  emptyHint?: string
  /** Draw horizontal runs joined by vertical steps (uptime bands). */
  stepped?: boolean
  /** Accessible name for the chart ("Steve's traffic"). */
  label?: string
  /** Controlled cursor time: when provided (non-undefined), the crosshair
   * follows it instead of local hover — several charts share one playhead. */
  cursorT?: number | null
  /** Reports the nearest-row time under the pointer (null on leave). With
   * cursorT this makes the chart a controlled component. */
  onCursor?: (t: number | null) => void
}) {
  const uid = useId()
  const svgRef = useRef<SVGSVGElement>(null)
  const [wellRef, w] = useMeasuredWidth()
  const [localT, setLocalT] = useState<number | null>(null)

  const PAD = {l: 56, r: 10, t: 12, b: 20}
  const plotW = w - PAD.l - PAD.r
  const baseY = height - PAD.b
  const plotH = baseY - PAD.t

  // Shared time domain and y-scale across all series.
  const view = useMemo(() => {
    const ts = series.flatMap(s => s.points.map(p => p.t))
    if (!ts.length) return null
    const t0 = Math.min(...ts)
    const t1 = Math.max(...ts)
    const span = Math.max(1, t1 - t0)
    const x = (t: number) => PAD.l + ((t - t0) / span) * plotW
    const vmax = Math.max(1, ...series.flatMap(s => s.points.map(p => p.v ?? 0)))
    const sc = scale === 'binary' ? niceScale(vmax) : niceLinear(vmax)
    const y = (v: number) => baseY - (v / sc.max) * plotH
    return {t0, t1, span, x, y, sc, ticks: timeTicks(t0, t1).map(tk => ({...tk, x: x(tk.t)}))}
  }, [series, plotW, baseY, plotH, scale])

  // One tween across all series, rows keyed by timestamp.
  const rowsTarget = useMemo(() => {
    if (!view) return []
    const byT = new Map<number, (number | null)[]>()
    series.forEach((s, i) => {
      for (const p of s.points) {
        let row = byT.get(p.t)
        if (!row) { row = series.map(() => null); byT.set(p.t, row) }
        row[i] = p.v
      }
    })
    return [...byT.entries()].sort((a, b) => a[0] - b[0]).map(([t, vs]) => ({t, vs}))
  }, [series, view])
  const rows = useTweenedValues(rowsTarget)

  // The active cursor: controlled (a shared playhead) wins over local hover.
  // Both are times, resolved to the nearest data row — a controlled time from
  // a sibling chart's buckets still lands on this chart's closest row.
  const cursor = cursorT !== undefined ? cursorT : localT
  const hover = useMemo(() => {
    if (cursor === null || cursor === undefined || !rows.length) return null
    let best = rows[0]
    let bestD = Infinity
    for (const r of rows) {
      const d = Math.abs(r.t - cursor)
      if (d < bestD) { bestD = d; best = r }
    }
    return best
  }, [cursor, rows])

  // nearestT maps a pointer x (viewBox units) to the closest row's time.
  const nearestT = (px: number): number | null => {
    if (!view || !rows.length) return null
    let best: number | null = null
    let bestD = Infinity
    for (const r of rows) {
      const d = Math.abs(view.x(r.t) - px)
      if (d < bestD) { bestD = d; best = r.t }
    }
    return best
  }

  if (!view) {
    return (
      <div ref={wellRef} className="pf-well grid place-items-center p-3 text-center font-mono text-[11px] leading-relaxed text-[var(--text-3)]" style={{height}}>
        {emptyHint ?? 'no data'}
      </div>
    )
  }

  const fmtAxis = formatY ?? series[0]?.format ?? ((v: number) => String(Math.round(v)))

  // One path per run of known values; nulls break the line.
  const pathFor = (i: number): string[] => {
    const out: string[] = []
    let cur: string[] = []
    let prevY: string | null = null
    const flush = () => { if (cur.length) { out.push(cur.join('')); cur = [] }; prevY = null }
    for (const r of rows) {
      const v = r.vs[i]
      if (v === null || v === undefined) { flush(); continue }
      const px = view.x(r.t).toFixed(1)
      const py = view.y(v).toFixed(1)
      if (!cur.length) cur.push(`M${px},${py}`)
      else if (stepped && prevY !== null) cur.push(`L${px},${prevY}L${px},${py}`)
      else cur.push(`L${px},${py}`)
      prevY = py
    }
    flush()
    return out
  }

  // Area fill uses only the first contiguous run's envelope — cheap and right
  // for the common dense case; gapped series just skip the fill.
  const areaFor = (i: number): string | null => {
    const runs = pathFor(i)
    if (runs.length !== 1) return null
    const known = rows.filter(r => r.vs[i] !== null && r.vs[i] !== undefined)
    if (known.length < 2) return null
    const x0 = view.x(known[0].t).toFixed(1)
    const x1 = view.x(known[known.length - 1].t).toFixed(1)
    return `${runs[0]}L${x1},${baseY}L${x0},${baseY}Z`
  }

  const lastKnown = (i: number) => {
    for (let r = rows.length - 1; r >= 0; r--) {
      const v = rows[r].vs[i]
      if (v !== null && v !== undefined) return {t: rows[r].t, v}
    }
    return null
  }

  const hoverLeft = hover ? view.x(hover.t) < w / 2 : false
  const timeKind = view.span >= 3 * 86_400_000 ? 'day' : view.span < 120_000 ? 'second' : 'minute'

  return (
    <div ref={wellRef} className="pf-well relative p-1.5">
      <svg
        ref={svgRef}
        viewBox={`0 0 ${w} ${height}`}
        className="block w-full"
        role="img"
        aria-label={label}
        onMouseMove={e => {
          const r = svgRef.current!.getBoundingClientRect()
          const t = nearestT(((e.clientX - r.left) / r.width) * w)
          if (onCursor) onCursor(t)
          else setLocalT(t)
        }}
        onMouseLeave={() => { if (onCursor) onCursor(null); else setLocalT(null) }}
      >
        <defs>
          {series.map((s, i) => s.fill && (
            <linearGradient key={i} id={`${uid}-a${i}`} gradientUnits="userSpaceOnUse" x1="0" y1={PAD.t} x2="0" y2={baseY}>
              <stop offset="0" stopColor={s.cssVar} stopOpacity="0.28" />
              <stop offset="0.55" stopColor={s.cssVar} stopOpacity="0.07" />
              <stop offset="1" stopColor={s.cssVar} stopOpacity="0" />
            </linearGradient>
          ))}
        </defs>

        {/* fine grid — recessive; the data is the artwork */}
        <g stroke="var(--border)" strokeWidth="1" opacity="0.35">
          {view.sc.ticks.map((v, i) => (
            <line key={`h${i}`} x1={PAD.l} x2={w - PAD.r} y1={view.y(v)} y2={view.y(v)} />
          ))}
          {view.ticks.map((t, i) => (
            <line key={`v${i}`} x1={t.x} x2={t.x} y1={PAD.t} y2={baseY} />
          ))}
        </g>
        <line x1={PAD.l} x2={w - PAD.r} y1={baseY} y2={baseY} stroke="var(--border)" strokeWidth="1" />

        {/* axis labels */}
        <g fontSize="10" fontFamily={MONO} style={{fontVariantNumeric: 'tabular-nums'}}>
          {view.sc.ticks.map((v, i) => v > 0 && (
            <text key={`yl${i}`} x={PAD.l - 6} y={view.y(v) + 3.5} textAnchor="end" fill="var(--text-3)">{fmtAxis(v)}</text>
          ))}
          {view.ticks.map((t, i) => (t.x > PAD.l + 14 && t.x < w - PAD.r - 14) && (
            <text key={`tl${i}`} x={t.x} y={height - 7} textAnchor="middle" fill="var(--text-3)">{t.label}</text>
          ))}
        </g>

        {/* series: area, halo twin, then the hot stroke */}
        {series.map((s, i) => {
          const runs = pathFor(i)
          const area = s.fill ? areaFor(i) : null
          const end = lastKnown(i)
          return (
            <g key={i}>
              {area && <path d={area} fill={`url(#${uid}-a${i})`} />}
              {runs.map((d, j) => (
                <g key={j}>
                  <path d={d} fill="none" stroke={s.cssVar} strokeWidth="4.5" strokeLinejoin="round" className="pf-chart-halo" />
                  <path d={d} fill="none" stroke={s.cssVar} strokeWidth="1.5" strokeLinejoin="round" className="pf-chart-glow-hot" style={{color: s.cssVar}} />
                </g>
              ))}
              {end && <circle cx={view.x(end.t)} cy={view.y(end.v)} r="2" fill={s.cssVar} />}
            </g>
          )
        })}

        {/* hover: hairline + dots + one readout line in the top pad lane */}
        {hover && (
          <g pointerEvents="none">
            <line x1={view.x(hover.t)} x2={view.x(hover.t)} y1={PAD.t} y2={baseY} stroke="var(--text-3)" strokeWidth="1" strokeDasharray="3 3" opacity="0.8" />
            {series.map((s, i) => {
              const v = hover.vs[i]
              return v !== null && v !== undefined && (
                <circle key={i} cx={view.x(hover.t)} cy={view.y(v)} r="2.5" fill={s.cssVar} stroke="var(--panel)" strokeWidth="1.5" />
              )
            })}
            <text
              x={hoverLeft ? w - 8 : PAD.l} y={PAD.t - 3} textAnchor={hoverLeft ? 'end' : 'start'}
              fontSize="10" fontFamily={MONO} style={{fontVariantNumeric: 'tabular-nums'}}
              paintOrder="stroke" stroke="var(--panel)" strokeWidth="3"
            >
              <tspan fill="var(--text-3)">{fmtTickTime(hover.t, timeKind)}</tspan>
              {series.map((s, i) => {
                const v = hover.vs[i]
                return v !== null && v !== undefined && (
                  <tspan key={i} fill={s.cssVar} dx="8">
                    {s.label} {(s.format ?? fmtAxis)(v)}
                  </tspan>
                )
              })}
            </text>
          </g>
        )}
      </svg>
    </div>
  )
}
