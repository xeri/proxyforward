import {useEffect, useMemo, useRef, useState} from 'react'
import {fmtBytes, fmtRate} from '../state'
import {
  Bucket, ChartMode, HistoryResult, RANGE_KEYS, RANGES, RangeKey, SeriesVisibility,
  loadCandlePref, loadRangePref, loadSeriesPref, modeFor, saveCandlePref, saveRangePref, saveSeriesPref,
  useBandwidthHistory,
} from '../history'
import {Button, Card} from './ui'
import {IconArrowRight} from './icons'

// Engineering aesthetic: every numeral is mono + tabular, grid is fine and
// recessive, lines are straight (no smoothing), values are exact.
const MONO = "ui-monospace, 'Cascadia Mono', Consolas, monospace"

const BASE_W = 720
const H = 260
// Each enabled outboard axis (connections, then RTT) claims one column to the
// right of the upload axis. Both the viewBox width and the right pad grow by
// the same amount so the plot area — and download/upload — stay the same size.
const RIGHT_COL = 46

// ---------------------------------------------------------------------------
// BandwidthPanel: range selector + mode toggle + legend/series toggles + stats
// row + chart. Fully self-contained: it polls its own data at the range's
// cadence and keeps it in a module-level cache so tab switches never lose
// history.
// ---------------------------------------------------------------------------
const COMPACT_VIS: SeriesVisibility = {dl: true, ul: true, conn: false, rtt: false}

export function BandwidthPanel({historyUnsupported, compact = false, onExpand}: {
  historyUnsupported?: boolean
  /** Compact teaser: fixed 1h line view, no controls, optional jump-off. */
  compact?: boolean
  onExpand?: () => void
}) {
  const [rangePref, setRange] = useState<RangeKey>(loadRangePref)
  const [candles, setCandles] = useState<boolean>(loadCandlePref)
  const [visPref, setVis] = useState<SeriesVisibility>(loadSeriesPref)
  const range: RangeKey = compact ? '1h' : rangePref
  const vis = compact ? COMPACT_VIS : visPref
  const data = useBandwidthHistory(range)
  const spec = RANGES[range]
  const mode = compact ? 'line' : modeFor(range, candles)
  const buckets = data?.buckets ?? []
  const last = buckets.length ? buckets[buckets.length - 1] : null

  const pickRange = (r: RangeKey) => { setRange(r); saveRangePref(r) }
  const pickCandles = (on: boolean) => { setCandles(on); saveCandlePref(on) }
  const toggle = (k: keyof SeriesVisibility) => {
    setVis(prev => { const next = {...prev, [k]: !prev[k]}; saveSeriesPref(next); return next })
  }

  const liveReadout = last && mode !== 'bars' ? (
    <div className="flex items-center gap-4 font-mono text-xs tabular-nums">
      <LiveDot />
      <span className="text-[var(--dl)]">↓ {fmtRate(last.oc)}</span>
      <span className="text-[var(--ul)]">↑ {fmtRate(last.ic)}</span>
    </div>
  ) : undefined

  return (
    <Card
      title="Bandwidth"
      subtitle={compact ? 'Last hour' : subtitleFor(range, data)}
      action={compact ? (
        <div className="flex items-center gap-3">
          {liveReadout}
          {onExpand && (
            <Button variant="ghost" size="sm" onClick={onExpand}>
              Open Traffic <IconArrowRight size={13} />
            </Button>
          )}
        </div>
      ) : liveReadout}
    >
      {!compact && (
        <div className="mb-3 flex flex-wrap items-center justify-between gap-2">
          <div className="inline-flex rounded-[var(--r-md)] border border-[var(--border)] bg-[var(--panel-2)] p-0.5">
            {RANGE_KEYS.map(k => (
              <button
                key={k}
                onClick={() => pickRange(k)}
                className={`rounded-[calc(var(--r-md)-2px)] px-2 py-1 font-mono text-[11px] font-medium tabular-nums transition-colors duration-150 ${
                  k === range
                    ? 'bg-[var(--panel)] text-[var(--text)] shadow-[var(--shadow-soft)]'
                    : 'text-[var(--text-3)] hover:text-[var(--text)]'
                }`}
              >{RANGES[k].label}</button>
            ))}
          </div>
          {spec.render === 'bars' ? (
            <span className="font-mono text-[11px] text-[var(--text-3)]">
              {RANGES[range].windowMs === 604_800_000 ? 'hourly totals' : 'daily totals'}
            </span>
          ) : (
            <div className="inline-flex rounded-[var(--r-md)] border border-[var(--border)] bg-[var(--panel-2)] p-0.5">
              {(['line', 'candles'] as const).map(m => {
                const disabled = m === 'candles' && !spec.candlesOk
                const on = mode === m
                return (
                  <button
                    key={m}
                    disabled={disabled}
                    title={disabled ? 'Candles need 15m or longer ranges' : undefined}
                    onClick={() => pickCandles(m === 'candles')}
                    className={`rounded-[calc(var(--r-md)-2px)] px-2 py-1 text-[11px] font-medium transition-colors duration-150 ${
                      on ? 'bg-[var(--panel)] text-[var(--text)] shadow-[var(--shadow-soft)]'
                        : disabled ? 'cursor-not-allowed text-[var(--text-3)] opacity-40'
                          : 'text-[var(--text-3)] hover:text-[var(--text)]'
                    }`}
                  >{m === 'line' ? 'Line' : 'Candles'}</button>
                )
              })}
            </div>
          )}
        </div>
      )}

      {!compact && <StatsRow buckets={buckets} mode={mode} bucketMs={data?.bucketMs ?? 0} vis={vis} onToggle={toggle} />}

      <BandwidthChart
        buckets={buckets}
        bucketMs={data?.bucketMs ?? 1000}
        mode={mode}
        vis={vis}
        emptyHint={historyUnsupported
          ? 'History is unavailable — the background service is an older version.'
          : 'Collecting data — history builds while the app runs.'}
      />
    </Card>
  )
}

/** LiveDot: a small breathing dot that marks the chart as a live, self-updating
 * surface (pairs with the smoothed transitions). */
function LiveDot() {
  return (
    <span className="inline-flex items-center gap-1.5 text-[10px] uppercase tracking-wide text-[var(--text-3)]">
      <span className="inline-flex h-2 w-2 rounded-full pf-halo" style={{background: 'var(--good)', ['--halo' as string]: 'var(--good)'}} />
      live
    </span>
  )
}

function subtitleFor(range: RangeKey, data: HistoryResult | null): string {
  const spec = RANGES[range]
  const win = spec.windowMs === 0
    ? (data && data.windowMs ? `Since first run` : 'All time')
    : `Last ${labelDuration(spec.windowMs)}`
  const res = data && data.bucketMs ? ` · ${labelDuration(data.bucketMs)} buckets` : ''
  return win + res
}

function labelDuration(ms: number): string {
  if (ms < 1000) return `${ms}ms`
  if (ms < 60_000) return `${stripZero(ms / 1000)}s`
  if (ms < 3_600_000) return `${stripZero(ms / 60_000)}m`
  if (ms < 86_400_000) return `${stripZero(ms / 3_600_000)}h`
  return `${stripZero(ms / 86_400_000)}d`
}
function stripZero(v: number): string {
  return v % 1 === 0 ? String(v) : v.toFixed(1)
}

/** Legend doubles as the series toggles: click a chip to show/hide download,
 * upload, connections or RTT. Stats (min/avg/max/last, and window averages for
 * the gauges) sit on the right. */
function StatsRow({buckets, mode, bucketMs, vis, onToggle}: {
  buckets: Bucket[]; mode: ChartMode; bucketMs: number; vis: SeriesVisibility
  onToggle: (k: keyof SeriesVisibility) => void
}) {
  const stats = useMemo(() => {
    if (!buckets.length || !bucketMs) return null
    if (mode === 'bars') {
      let dn = 0, up = 0, peak = 0
      for (const b of buckets) { dn += b.out; up += b.in; peak = Math.max(peak, b.out) }
      return {kind: 'totals' as const, dn, up, peak}
    }
    const rates = buckets.map(b => b.out * 1000 / bucketMs)
    const dn = rates.reduce((a, v) => a + v, 0)
    return {
      kind: 'rates' as const,
      min: Math.min(...rates), avg: dn / rates.length,
      max: Math.max(...rates), last: rates[rates.length - 1],
    }
  }, [buckets, mode, bucketMs])

  const avgConn = useMemo(() => avgGauge(buckets, b => b.cc), [buckets])
  const avgRtt = useMemo(() => avgGauge(buckets, b => b.rc), [buckets])
  const hasConn = avgConn !== null
  const hasRtt = avgRtt !== null

  return (
    <div className="mb-1.5 flex flex-wrap items-center justify-between gap-x-4 gap-y-1">
      <div className="flex flex-wrap items-center gap-2 text-[11px]">
        <LegendChip color="var(--dl)" label="↓ download" on={vis.dl} onClick={() => onToggle('dl')} />
        <LegendChip color="var(--ul)" label="↑ upload" on={vis.ul} onClick={() => onToggle('ul')} />
        {mode !== 'bars' && (
          <LegendChip color="var(--conn)" label="connections" on={vis.conn} disabled={!hasConn}
            onClick={() => onToggle('conn')} title={hasConn ? undefined : 'no connection data in this range yet'} />
        )}
        {mode !== 'bars' && (
          <LegendChip color="var(--rtt)" label="RTT" on={vis.rtt} disabled={!hasRtt}
            onClick={() => onToggle('rtt')} title={hasRtt ? undefined : 'no RTT data in this range yet'} />
        )}
      </div>
      {stats && (
        <div className="text-[10.5px] tabular-nums text-[var(--text-3)]" style={{fontFamily: MONO}}>
          {stats.kind === 'rates'
            ? <>min {fmtRate(stats.min)} · avg {fmtRate(stats.avg)} · max {fmtRate(stats.max)} · last {fmtRate(stats.last)}
              {vis.conn && hasConn && <> · <span className="text-[var(--conn)]">~{avgConn!.toFixed(1)} conns</span></>}
              {vis.rtt && hasRtt && <> · <span className="text-[var(--rtt)]">~{Math.round(avgRtt!)} ms</span></>}</>
            : <>Σ↓ {fmtBytes(stats.dn)} · Σ↑ {fmtBytes(stats.up)} · peak {fmtBytes(stats.peak)}</>}
        </div>
      )}
    </div>
  )
}

/** avgGauge averages a gauge series (connections / RTT) over the known buckets,
 * ignoring the unknown (-1) sentinel. Returns null when nothing is known. */
function avgGauge(buckets: Bucket[], get: (b: Bucket) => number): number | null {
  let sum = 0, n = 0
  for (const b of buckets) { const v = get(b); if (v >= 0) { sum += v; n++ } }
  return n ? sum / n : null
}

function LegendChip({color, label, on, disabled, onClick, title}: {
  color: string; label: string; on: boolean; disabled?: boolean; onClick: () => void; title?: string
}) {
  return (
    <button
      type="button" onClick={disabled ? undefined : onClick} disabled={disabled} title={title}
      aria-pressed={on}
      className={`inline-flex items-center gap-1.5 rounded-full border px-2 py-0.5 transition-all duration-150 ${
        disabled
          ? 'cursor-not-allowed border-[var(--border)] text-[var(--text-3)] opacity-40'
          : on
            ? 'border-[var(--border-strong)] bg-[var(--panel-2)] text-[var(--text-2)] hover:border-[var(--accent)]'
            : 'border-[var(--border)] text-[var(--text-3)] opacity-60 hover:opacity-100'
      }`}
    >
      <span className="inline-block h-[3px] w-4 rounded-full" style={{background: color, opacity: on ? 1 : 0.4}} />
      {label}
    </button>
  )
}

// ---------------------------------------------------------------------------
// Plotted samples: per-bucket values already resolved for the line renderer,
// so the tween has a stable, timestamp-keyed target to animate toward. `dn`/`up`
// are rates (bytes/sec); the trailing in-progress bucket uses its close rate so
// a freshly-rolled bucket does not sawtooth down to a partial-window average.
// `conn`/`rtt` are gauges, null when unknown (breaks the overlay line).
// ---------------------------------------------------------------------------
type Plot = {t: number; dn: number; up: number; conn: number | null; rtt: number | null}

function toPlots(buckets: Bucket[], bucketMs: number, mode: ChartMode, nowMs: number): Plot[] {
  return buckets.map((b, i) => {
    const partial = i === buckets.length - 1 && b.t + bucketMs > nowMs
    const dn = partial ? b.oc : b.out * 1000 / bucketMs
    const up = mode === 'candles' ? b.ic : (partial ? b.ic : b.in * 1000 / bucketMs)
    return {t: b.t, dn, up, conn: b.cc >= 0 ? b.cc : null, rtt: b.rc >= 0 ? b.rc : null}
  })
}

/** useTweenedPlots eases plotted values toward each fresh target over ~220ms
 * (ease-out cubic), aligning by timestamp so refreshes glide instead of
 * snapping. Buckets with no prior value (newly appeared) snap in. */
function useTweenedPlots(target: Plot[]): Plot[] {
  const dispRef = useRef<Map<number, Plot>>(new Map())
  const [, force] = useState(0)
  useEffect(() => {
    const from = dispRef.current
    const to = new Map(target.map(p => [p.t, p]))
    const start = performance.now()
    let raf = 0
    const lerp = (a: number, b: number, e: number) => a + (b - a) * e
    const lerpGauge = (a: number | null, b: number | null, e: number) =>
      a === null || b === null ? b : lerp(a, b, e)
    const tick = (now: number) => {
      const p = Math.min(1, (now - start) / 220)
      const e = 1 - Math.pow(1 - p, 3)
      const next = new Map<number, Plot>()
      for (const [t, s] of to) {
        const f = from.get(t)
        next.set(t, f ? {t, dn: lerp(f.dn, s.dn, e), up: lerp(f.up, s.up, e),
          conn: lerpGauge(f.conn, s.conn, e), rtt: lerpGauge(f.rtt, s.rtt, e)} : s)
      }
      dispRef.current = next
      force(x => x + 1)
      if (p < 1) raf = requestAnimationFrame(tick)
    }
    raf = requestAnimationFrame(tick)
    return () => cancelAnimationFrame(raf)
  }, [target])
  return target.map(p => dispRef.current.get(p.t) ?? p)
}

// ---------------------------------------------------------------------------
// BandwidthChart: pure presentational SVG. Download (left axis) + upload
// (inner-right axis) are the dominant series; connections (step-line) and RTT
// (thin line) are recessive overlays, each on its own outboard right axis added
// only when enabled, so they never overlap the download/upload scales.
// ---------------------------------------------------------------------------
export function BandwidthChart({buckets: rawBuckets, bucketMs: rawBucketMs, mode, vis, emptyHint}: {
  buckets: Bucket[]
  bucketMs: number
  mode: ChartMode
  vis: SeriesVisibility
  emptyHint?: string
}) {
  const svgRef = useRef<SVGSVGElement>(null)
  const [hoverX, setHoverX] = useState<number | null>(null)

  // Candles need room to read as candles: coalesce down to ≤150 so each body
  // gets a few pixels. Line mode keeps the full density.
  const [buckets, bucketMs] = useMemo((): [Bucket[], number] => {
    if (mode !== 'candles' || rawBuckets.length <= 150) return [rawBuckets, rawBucketMs]
    const k = Math.ceil(rawBuckets.length / 150)
    const out: Bucket[] = []
    for (let i = 0; i < rawBuckets.length; i += k) {
      const grp = rawBuckets.slice(i, i + k)
      const first = grp[0], last = grp[grp.length - 1]
      out.push({
        ...first,
        in: grp.reduce((a, b) => a + b.in, 0),
        out: grp.reduce((a, b) => a + b.out, 0),
        oh: Math.max(...grp.map(b => b.oh)), ol: Math.min(...grp.map(b => b.ol)), oc: last.oc,
        ih: Math.max(...grp.map(b => b.ih)), il: Math.min(...grp.map(b => b.il)), ic: last.ic,
        cc: mergeGaugeClose(grp, b => b.cc), rc: mergeGaugeClose(grp, b => b.rc),
      })
    }
    return [out, rawBucketMs * k]
  }, [rawBuckets, rawBucketMs, mode])

  const hasConn = mode !== 'bars' && buckets.some(b => b.cc >= 0)
  const hasRtt = mode !== 'bars' && buckets.some(b => b.rc >= 0)
  const showConn = vis.conn && hasConn
  const showRtt = vis.rtt && hasRtt
  const showDn = mode === 'candles' || vis.dl // candles ARE download
  const showUp = mode !== 'bars' && vis.ul

  // Outboard axes: connections first, then RTT. Each widens both W and PAD.r by
  // one column so the plot geometry is invariant.
  const outboard = (mode === 'bars' ? [] : [
    ...(showConn ? ['conn' as const] : []),
    ...(showRtt ? ['rtt' as const] : []),
  ])
  const W = BASE_W + outboard.length * RIGHT_COL
  const PAD = {l: 68, r: (mode === 'bars' ? 16 : 68) + outboard.length * RIGHT_COL, t: 10, b: 22}
  const plotW = W - PAD.l - PAD.r
  const plotH = H - PAD.t - PAD.b
  const baseY = PAD.t + plotH
  const plotRight = PAD.l + plotW

  const view = useMemo(() => {
    if (!buckets.length || !bucketMs) return null
    const t0 = buckets[0].t
    const t1 = buckets[buckets.length - 1].t + bucketMs
    const span = Math.max(1, t1 - t0)
    const x = (t: number) => PAD.l + ((t - t0) / span) * plotW

    // Left scale: download (rates or bytes); inner-right scale: upload rates.
    const dnMax = mode === 'bars'
      ? Math.max(1, ...buckets.map(b => b.out))
      : mode === 'candles'
        ? Math.max(1, ...buckets.map(b => b.oh))
        : Math.max(1, ...buckets.map(b => b.out * 1000 / bucketMs))
    const upMax = mode === 'bars'
      ? Math.max(1, ...buckets.map(b => b.in))
      : Math.max(1, ...buckets.map(b => mode === 'candles' ? b.ih : b.in * 1000 / bucketMs))
    const left = niceScale(dnMax)
    const right = niceScale(upMax)
    const yL = (v: number) => baseY - (v / left.max) * plotH
    const yR = (v: number) => baseY - (v / right.max) * plotH

    // Gauge scales (nice decimal), only when their overlay is shown.
    const connScale = showConn ? niceLinear(Math.max(1, ...buckets.filter(b => b.cc >= 0).map(b => b.cc))) : null
    const rttScale = showRtt ? niceLinear(Math.max(1, ...buckets.filter(b => b.rc >= 0).map(b => b.rc))) : null
    const yConn = connScale ? (v: number) => baseY - (v / connScale.max) * plotH : null
    const yRtt = rttScale ? (v: number) => baseY - (v / rttScale.max) * plotH : null

    const timeTicks = ticksFor(t0, t1, bucketMs, buckets).map(tk => ({...tk, x: x(tk.t)}))
    const nowMs = Date.now()
    return {t0, t1, span, x, left, right, yL, yR, connScale, rttScale, yConn, yRtt, timeTicks, nowMs}
  }, [buckets, bucketMs, mode, PAD.r, showConn, showRtt])

  const plotsTarget = useMemo(
    () => (view && mode !== 'bars' ? toPlots(buckets, bucketMs, mode, view.nowMs) : []),
    [buckets, bucketMs, mode, view?.nowMs],
  )
  const plots = useTweenedPlots(plotsTarget)

  const hover = useMemo(() => {
    if (hoverX === null || !view || !buckets.length) return null
    let best = 0
    let bestD = Infinity
    for (let i = 0; i < buckets.length; i++) {
      const cx = view.x(buckets[i].t + bucketMs / 2)
      const d = Math.abs(cx - hoverX)
      if (d < bestD) { bestD = d; best = i }
    }
    const b = buckets[best]
    return {b, cx: view.x(b.t + bucketMs / 2)}
  }, [hoverX, view, buckets, bucketMs])

  if (!view) {
    return (
      <div className="pf-well relative p-1.5">
        <svg viewBox={`0 0 ${W} ${H}`} className="w-full">
          <EmptyGrid pad={PAD} plotW={plotW} plotH={plotH} />
          <text x={W / 2} y={H / 2} textAnchor="middle" fontSize="11" fill="var(--text-3)" fontFamily={MONO}>
            {emptyHint ?? 'no data'}
          </text>
        </svg>
      </div>
    )
  }

  const {x, left, right, yL, yR, timeTicks} = view
  const slotW = (bucketMs / view.span) * plotW

  // Line-mode geometry from the tweened plots (x at bucket centers).
  const cx = (t: number) => x(t + bucketMs / 2)
  const dnLine = plots.map((p, i) => `${i === 0 ? 'M' : 'L'}${cx(p.t).toFixed(1)},${yL(p.dn).toFixed(1)}`).join('')
  const upLinePlots = plots.map((p, i) => `${i === 0 ? 'M' : 'L'}${cx(p.t).toFixed(1)},${yR(p.up).toFixed(1)}`).join('')

  // Outboard axis label x-positions (each column just right of the previous).
  const axisX = (idx: number) => plotRight + (mode === 'bars' ? 16 : 68) + idx * RIGHT_COL + 6
  const connIdx = 0
  const rttIdx = showConn ? 1 : 0

  return (
    <div className="pf-well relative p-1.5">
      <svg
        ref={svgRef}
        viewBox={`0 0 ${W} ${H}`}
        className="w-full"
        onMouseMove={e => {
          const r = svgRef.current!.getBoundingClientRect()
          setHoverX(((e.clientX - r.left) / r.width) * W)
        }}
        onMouseLeave={() => setHoverX(null)}
      >
        {/* fine grid: horizontal at value ticks, vertical at time ticks */}
        <g stroke="var(--border)" strokeWidth="1" opacity="0.55">
          {left.ticks.map((v, i) => (
            <line key={`h${i}`} x1={PAD.l} x2={plotRight} y1={yL(v)} y2={yL(v)} />
          ))}
          {timeTicks.map((t, i) => (
            <line key={`v${i}`} x1={t.x} x2={t.x} y1={PAD.t} y2={baseY} />
          ))}
        </g>
        <line x1={PAD.l} x2={plotRight} y1={baseY} y2={baseY} stroke="var(--border)" strokeWidth="1" />

        {/* y-axis labels: left = download, inner-right = upload */}
        <g fontSize="10" fontFamily={MONO} style={{fontVariantNumeric: 'tabular-nums'}}>
          {showDn && left.ticks.map((v, i) => v > 0 && (
            <text key={`l${i}`} x={PAD.l - 6} y={yL(v) + 3.5} textAnchor="end" fill="var(--text-3)">
              {mode === 'bars' ? fmtBytes(v) : fmtRate(v)}
            </text>
          ))}
          {showUp && right.ticks.map((v, i) => v > 0 && (
            <text key={`r${i}`} x={plotRight + 6} y={yR(v) + 3.5} textAnchor="start" fill="var(--text-3)">{fmtRate(v)}</text>
          ))}
          {/* outboard connection axis */}
          {view.connScale && view.connScale.ticks.map((v, i) => v > 0 && (
            <text key={`c${i}`} x={axisX(connIdx)} y={view.yConn!(v) + 3.5} textAnchor="start" fill="var(--conn)">{v}</text>
          ))}
          {/* outboard RTT axis */}
          {view.rttScale && view.rttScale.ticks.map((v, i) => v > 0 && (
            <text key={`p${i}`} x={axisX(rttIdx)} y={view.yRtt!(v) + 3.5} textAnchor="start" fill="var(--rtt)">{v}</text>
          ))}
          {/* time labels — skip the ones that would collide with the corner
              direction glyphs at the plot edges */}
          {timeTicks.map((t, i) => (t.x > PAD.l + 11 && t.x < plotRight - 11) && (
            <text key={`t${i}`} x={t.x} y={H - 8} textAnchor="middle" fill="var(--text-3)">{t.label}</text>
          ))}
        </g>
        {/* axis direction / unit markers, in the time-label row's empty corners */}
        <g fontSize="9" fontFamily={MONO}>
          {showDn && <text x={PAD.l - 6} y={H - 8} textAnchor="end" fill="var(--dl)">↓</text>}
          {showUp && <text x={plotRight + 6} y={H - 8} textAnchor="start" fill="var(--ul)">↑</text>}
          {view.connScale && <text x={axisX(connIdx)} y={H - 8} textAnchor="start" fill="var(--conn)">#</text>}
          {view.rttScale && <text x={axisX(rttIdx)} y={H - 8} textAnchor="start" fill="var(--rtt)">ms</text>}
        </g>

        {mode === 'line' && (
          <>
            {showDn && <>
              <path
                d={`${dnLine}L${cx(plots[plots.length - 1].t).toFixed(1)},${baseY}L${cx(plots[0].t).toFixed(1)},${baseY}Z`}
                fill="var(--dl)" opacity="0.08"
              />
              <path d={dnLine} fill="none" stroke="var(--dl)" strokeWidth="1.5" strokeLinejoin="round" />
              {plots.length === 1 && <circle cx={cx(plots[0].t)} cy={yL(plots[0].dn)} r="2.5" fill="var(--dl)" />}
            </>}
            {showUp && <>
              <path d={upLinePlots} fill="none" stroke="var(--ul)" strokeWidth="1.5" strokeLinejoin="round" />
              {plots.length === 1 && <circle cx={cx(plots[0].t)} cy={yR(plots[0].up)} r="2.5" fill="var(--ul)" />}
            </>}
          </>
        )}

        {mode === 'candles' && (
          <>
            {showDn && buckets.map((b, i) => {
              const bcx = x(b.t + bucketMs / 2)
              const bw = Math.max(2, Math.min(14, slotW * 0.66))
              const up = b.oc >= b.oo
              const col = up ? 'var(--good)' : 'var(--bad)'
              const top = yL(Math.max(b.oo, b.oc))
              const bot = yL(Math.min(b.oo, b.oc))
              return (
                <g key={i}>
                  <line x1={bcx} x2={bcx} y1={yL(b.oh)} y2={yL(b.ol)} stroke={col} strokeWidth="1" />
                  <rect
                    x={bcx - bw / 2} y={top} width={bw} height={Math.max(1, bot - top)}
                    fill={up ? 'var(--panel)' : col} stroke={col} strokeWidth="1"
                  />
                </g>
              )
            })}
            {showUp && <path d={upLinePlots} fill="none" stroke="var(--ul)" strokeWidth="1" opacity="0.85" />}
          </>
        )}

        {/* Connections step-line overlay (gauge: constant within a bucket). */}
        {showConn && view.yConn && stepSegments(buckets, x, bucketMs, view.yConn, b => b.cc).map((d, i) => (
          <path key={`cs${i}`} d={d} fill="none" stroke="var(--conn)" strokeWidth="1.25" opacity="0.7" strokeLinejoin="round" />
        ))}
        {/* RTT overlay: thin line, broken at unknown gaps. */}
        {showRtt && view.yRtt && gappedSegments(plots, cx, view.yRtt, p => p.rtt).map((d, i) => (
          <path key={`rl${i}`} d={d} fill="none" stroke="var(--rtt)" strokeWidth="1.25" opacity="0.75" strokeLinejoin="round" />
        ))}

        {mode === 'bars' && buckets.map((b, i) => {
          const bx = x(b.t)
          const bw = Math.max(1, slotW - Math.min(2, slotW * 0.2))
          const partial = b.t + bucketMs > view.nowMs
          return (
            <g key={i} opacity={partial ? 0.6 : 1}>
              {vis.dl && <rect x={bx} y={yL(b.out)} width={bw} height={Math.max(0, baseY - yL(b.out))} fill="var(--dl)" opacity="0.75" rx="1" />}
              {vis.ul && <rect x={bx} y={yL(b.in)} width={bw} height={Math.max(0, baseY - yL(b.in))} fill="var(--ul)" rx="1" />}
            </g>
          )
        })}

        {/* crosshair + readout */}
        {hover && (
          <Crosshair
            hover={hover} mode={mode} bucketMs={bucketMs} vis={vis}
            yL={yL} yR={yR} yConn={showConn ? view.yConn : null} yRtt={showRtt ? view.yRtt : null}
            showDn={showDn} showUp={showUp} pad={PAD} plotH={plotH} w={W} plotRight={plotRight}
          />
        )}
      </svg>
    </div>
  )
}

/** mergeGaugeClose returns the last known gauge value in a group (for candle
 * coalescing), or -1 when the whole group is unknown. */
function mergeGaugeClose(grp: Bucket[], get: (b: Bucket) => number): number {
  for (let i = grp.length - 1; i >= 0; i--) { const v = get(grp[i]); if (v >= 0) return v }
  return -1
}

/** stepSegments builds one path per run of known gauge buckets, drawing a
 * horizontal segment across each bucket's width (a gauge is constant within a
 * slot) joined by vertical steps. Unknown (-1) buckets break the line. */
function stepSegments(buckets: Bucket[], x: (t: number) => number, bucketMs: number, y: (v: number) => number, get: (b: Bucket) => number): string[] {
  const out: string[] = []
  let cur: string[] = []
  const flush = () => { if (cur.length) { out.push(cur.join('')); cur = [] } }
  for (const b of buckets) {
    const v = get(b)
    if (v < 0) { flush(); continue }
    const xl = x(b.t).toFixed(1), xr = x(b.t + bucketMs).toFixed(1), yv = y(v).toFixed(1)
    cur.push(`${cur.length === 0 ? 'M' : 'L'}${xl},${yv}L${xr},${yv}`)
  }
  flush()
  return out
}

/** gappedSegments builds one polyline per run of non-null values, breaking at
 * nulls (unknown gauge). Points are at bucket centers. */
function gappedSegments(plots: Plot[], cx: (t: number) => number, y: (v: number) => number, get: (p: Plot) => number | null): string[] {
  const out: string[] = []
  let cur: string[] = []
  const flush = () => { if (cur.length) { out.push(cur.join('')); cur = [] } }
  for (const p of plots) {
    const v = get(p)
    if (v === null) { flush(); continue }
    cur.push(`${cur.length === 0 ? 'M' : 'L'}${cx(p.t).toFixed(1)},${y(v).toFixed(1)}`)
  }
  flush()
  return out
}

function EmptyGrid({pad, plotW, plotH}: {pad: {l: number; r: number; t: number; b: number}; plotW: number; plotH: number}) {
  return (
    <g stroke="var(--border)" strokeWidth="1" opacity="0.45">
      {[0, 0.25, 0.5, 0.75, 1].map(f => (
        <line key={f} x1={pad.l} x2={pad.l + plotW} y1={pad.t + plotH * f} y2={pad.t + plotH * f} />
      ))}
    </g>
  )
}

function Crosshair({hover, mode, bucketMs, vis, yL, yR, yConn, yRtt, showDn, showUp, pad, plotH, w, plotRight}: {
  hover: {b: Bucket; cx: number}
  mode: ChartMode
  bucketMs: number
  vis: SeriesVisibility
  yL: (v: number) => number
  yR: (v: number) => number
  yConn: ((v: number) => number) | null
  yRtt: ((v: number) => number) | null
  showDn: boolean
  showUp: boolean
  pad: {l: number; r: number; t: number; b: number}
  plotH: number
  w: number
  plotRight: number
}) {
  const {b, cx} = hover
  const dnRate = b.out * 1000 / bucketMs
  const upRate = b.in * 1000 / bucketMs
  const yDn = mode === 'bars' ? yL(b.out) : mode === 'candles' ? yL(b.oc) : yL(dnRate)

  const timeLabel = fmtTickTime(b.t, bucketMs >= 86_400_000 ? 'day' : bucketMs >= 60_000 ? 'minute' : 'second')
  const lines: {k: string; v: string; c?: string}[] = []
  if (mode === 'candles') {
    lines.push(
      {k: 'O', v: fmtRate(b.oo)}, {k: 'H', v: fmtRate(b.oh)},
      {k: 'L', v: fmtRate(b.ol)}, {k: 'C', v: fmtRate(b.oc), c: b.oc >= b.oo ? 'var(--good)' : 'var(--bad)'},
    )
    if (vis.ul) lines.push({k: '↑', v: fmtRate(b.ic), c: 'var(--ul)'})
  } else if (mode === 'bars') {
    if (vis.dl) lines.push({k: '↓', v: fmtBytes(b.out), c: 'var(--dl)'})
    if (vis.ul) lines.push({k: '↑', v: fmtBytes(b.in), c: 'var(--ul)'})
  } else {
    if (vis.dl) lines.push({k: '↓', v: fmtRate(dnRate), c: 'var(--dl)'})
    if (vis.ul) lines.push({k: '↑', v: fmtRate(upRate), c: 'var(--ul)'})
  }
  if (yConn && b.cc >= 0) lines.push({k: '#', v: `${Math.round(b.cc)} conn${Math.round(b.cc) === 1 ? '' : 's'}`, c: 'var(--conn)'})
  if (yRtt && b.rc >= 0) lines.push({k: 'ms', v: `${Math.round(b.rc)} ms`, c: 'var(--rtt)'})

  const boxW = 132
  const boxH = 18 + lines.length * 13
  const boxX = cx + 10 + boxW > plotRight ? cx - 10 - boxW : cx + 10
  const boxY = pad.t + 4

  return (
    <g pointerEvents="none">
      <line x1={cx} x2={cx} y1={pad.t} y2={pad.t + plotH} stroke="var(--text-3)" strokeWidth="1" strokeDasharray="3 3" opacity="0.8" />
      {showDn && mode !== 'bars' && <line x1={pad.l} x2={plotRight} y1={yDn} y2={yDn} stroke="var(--text-3)" strokeWidth="1" strokeDasharray="3 3" opacity="0.5" />}
      {mode !== 'bars' && (
        <>
          {showDn && <circle cx={cx} cy={yDn} r="3" fill="var(--dl)" stroke="var(--panel)" strokeWidth="1.5" />}
          {showUp && <circle cx={cx} cy={yR(mode === 'candles' ? b.ic : upRate)} r="3" fill="var(--ul)" stroke="var(--panel)" strokeWidth="1.5" />}
          {yConn && b.cc >= 0 && <circle cx={cx} cy={yConn(b.cc)} r="2.5" fill="var(--conn)" stroke="var(--panel)" strokeWidth="1.5" />}
          {yRtt && b.rc >= 0 && <circle cx={cx} cy={yRtt(b.rc)} r="2.5" fill="var(--rtt)" stroke="var(--panel)" strokeWidth="1.5" />}
        </>
      )}
      <g transform={`translate(${boxX}, ${boxY})`} fontFamily={MONO} style={{fontVariantNumeric: 'tabular-nums'}}>
        <rect width={boxW} height={boxH} rx="5" fill="var(--panel-2)" stroke="var(--border)" />
        <text x="8" y="13" fontSize="9" fill="var(--text-3)">{timeLabel}</text>
        {lines.map((l, i) => (
          <g key={i}>
            <text x="8" y={26 + i * 13} fontSize="9.5" fill={l.c ?? 'var(--text-3)'}>{l.k}</text>
            <text x={boxW - 8} y={26 + i * 13} fontSize="9.5" textAnchor="end" fill="var(--text-2)">{l.v}</text>
          </g>
        ))}
      </g>
    </g>
  )
}

// ---- scales & ticks -------------------------------------------------------

/** niceScale rounds the axis max up to 4 divisions of a clean binary step, so
 * labels land on values like 64 KiB/s · 128 KiB/s · 192 KiB/s · 256 KiB/s. */
function niceScale(max: number): {max: number; ticks: number[]} {
  const divisions = 4
  const raw = max / divisions
  const step = Math.pow(2, Math.ceil(Math.log2(Math.max(1, raw))))
  const top = step * divisions
  return {max: top, ticks: [0, 1, 2, 3, 4].map(i => i * step)}
}

/** niceLinear rounds up to 4 divisions of a decimal 1/2/5 step — right for
 * gauges (connection counts, milliseconds) where powers of two read oddly. */
function niceLinear(max: number, divisions = 4): {max: number; ticks: number[]} {
  const raw = Math.max(1, max) / divisions
  const mag = Math.pow(10, Math.floor(Math.log10(raw)))
  const norm = raw / mag
  const step = (norm <= 1 ? 1 : norm <= 2 ? 2 : norm <= 5 ? 5 : 10) * mag
  const top = step * divisions
  return {max: top, ticks: Array.from({length: divisions + 1}, (_, i) => i * step)}
}

/** ticksFor picks vertical gridline times: nice wall-clock steps for
 * sub-daily buckets, bucket-edge steps for daily bars (which are UTC-aligned
 * and would visibly miss local midnight lines). */
function ticksFor(t0: number, t1: number, bucketMs: number, buckets: Bucket[]): {t: number; label: string}[] {
  const span = t1 - t0
  if (bucketMs >= 86_400_000) {
    // Daily bars: gridlines on bucket edges, labeled by the bucket's date.
    const every = Math.max(1, Math.ceil(buckets.length / 6))
    const out: {t: number; label: string}[] = []
    for (let i = 0; i < buckets.length; i += every) {
      out.push({t: buckets[i].t, label: fmtTickTime(buckets[i].t + bucketMs / 2, 'day')})
    }
    return out
  }
  const steps = [
    1_000, 2_000, 5_000, 10_000, 15_000, 30_000,
    60_000, 120_000, 300_000, 600_000, 900_000, 1_800_000,
    3_600_000, 7_200_000, 10_800_000, 21_600_000, 43_200_000, 86_400_000, 172_800_000,
  ]
  const step = steps.find(s => span / s <= 7) ?? steps[steps.length - 1]
  const out: {t: number; label: string}[] = []
  if (step >= 86_400_000) {
    // Align to local midnight.
    const d = new Date(t0)
    d.setHours(0, 0, 0, 0)
    for (let t = d.getTime(); t <= t1; t += step) {
      if (t >= t0) out.push({t, label: fmtTickTime(t, 'day')})
    }
  } else {
    const kind = step < 60_000 ? 'second' : 'minute'
    for (let t = Math.ceil(t0 / step) * step; t <= t1; t += step) {
      out.push({t, label: fmtTickTime(t, kind)})
    }
  }
  return out
}

function fmtTickTime(t: number, kind: 'second' | 'minute' | 'day'): string {
  const d = new Date(t)
  const p = (n: number) => String(n).padStart(2, '0')
  if (kind === 'day') {
    return d.toLocaleDateString(undefined, {month: 'short', day: 'numeric'})
  }
  if (kind === 'second') return `${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`
  return `${p(d.getHours())}:${p(d.getMinutes())}`
}
