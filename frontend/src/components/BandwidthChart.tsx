import {useEffect, useMemo, useRef, useState} from 'react'
import {prefersReduced} from '../motion'
import {fmtBytes, fmtRate} from '../state'
import {
  Bucket, ChartMode, HistoryResult, RANGE_KEYS, RANGES, RangeKey, SeriesVisibility,
  loadCandlePref, loadRangePref, loadSeriesPref, loadUptimePref, modeFor,
  saveCandlePref, saveRangePref, saveSeriesPref, saveUptimePref,
  useBandwidthHistory,
} from '../history'
import {Button, Card, LiveDot, Switch} from './ui'

export {LiveDot} // moved to ui.tsx; re-exported for existing importers
import {IconArrowRight} from './icons'
import {NumberTicker} from './NumberTicker'
import {MONO, fmtTickTime, niceLinear, niceScale, useMeasuredWidth} from './charts/util'

// Engineering aesthetic: every numeral is mono + tabular, grid is fine and
// recessive, lines are straight (no smoothing), values are exact.
const BASE_W = 720
const H = 260
// Each enabled outboard axis (connections, then RTT) claims one column to the
// right of the upload axis. Both the viewBox width and the right pad grow by
// the same amount so the plot area — and download/upload — stay the same size.
// Wide enough for the spelled-out axis names ("conns" / "RTT ms").
const RIGHT_COL = 54
// The uptime strip claims one short lane below the time-label row when shown,
// so toggling it never resizes the plot itself.
const UPTIME_LANE = 20

// ---------------------------------------------------------------------------
// BandwidthPanel: range selector + mode toggle + legend/series toggles + stats
// row + chart — or, in compact form, a live-rate headline over a bare
// sparkline. Fully self-contained: it polls its own data at the range's
// cadence and keeps it in a module-level cache so tab switches never lose
// history.
// ---------------------------------------------------------------------------
export function BandwidthPanel({historyUnsupported, compact = false, hero = false, onExpand}: {
  historyUnsupported?: boolean
  /** Compact teaser: live-rate headline + a 1h sparkline, no controls, optional jump-off. */
  compact?: boolean
  /** Hero: Traffic's identity surface — bare, no card. The graph is the artwork. */
  hero?: boolean
  onExpand?: () => void
}) {
  const [rangePref, setRange] = useState<RangeKey>(loadRangePref)
  const [candles, setCandles] = useState<boolean>(loadCandlePref)
  const [vis, setVis] = useState<SeriesVisibility>(loadSeriesPref)
  const [uptime, setUptime] = useState<boolean>(loadUptimePref)
  const range: RangeKey = compact ? '1h' : rangePref
  const data = useBandwidthHistory(range)
  const spec = RANGES[range]
  const mode = modeFor(range, candles)
  const buckets = data?.buckets ?? []
  const last = buckets.length ? buckets[buckets.length - 1] : null

  const pickRange = (r: RangeKey) => { setRange(r); saveRangePref(r) }
  const pickCandles = (on: boolean) => { setCandles(on); saveCandlePref(on) }
  const pickUptime = (on: boolean) => { setUptime(on); saveUptimePref(on) }
  const toggle = (k: keyof SeriesVisibility) => {
    setVis(prev => { const next = {...prev, [k]: !prev[k]}; saveSeriesPref(next); return next })
  }

  const liveReadout = !compact && last && mode !== 'bars' ? (
    <div className="flex items-center gap-4 font-mono text-xs tabular-nums">
      <LiveDot />
      <span className="text-[var(--dl)]">↓ {fmtRate(last.oc)}</span>
      <span className="text-[var(--ul)]">↑ {fmtRate(last.ic)}</span>
    </div>
  ) : undefined

  const body = (
    <>
      {compact && (
        <>
          <div className="mb-3 grid grid-cols-2 gap-3">
            <RateHeadline color="var(--dl)" glyph="↓" label="Download" value={last ? last.oc : null} />
            <RateHeadline color="var(--ul)" glyph="↑" label="Upload" value={last ? last.ic : null} />
          </div>
          <SparkChart
            buckets={buckets}
            bucketMs={data?.bucketMs ?? 1000}
            windowMs={spec.windowMs}
            emptyHint={historyUnsupported
              ? 'History is unavailable — the background service is an older version.'
              : 'Collecting data — history builds while the app runs.'}
          />
        </>
      )}

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

      {!compact && (
        <>
          <BandwidthChart
            buckets={buckets}
            bucketMs={data?.bucketMs ?? 1000}
            windowMs={spec.windowMs}
            mode={mode}
            vis={vis}
            showUptime={uptime}
            height={hero ? 360 : H}
            emptyHint={historyUnsupported
              ? 'History is unavailable — the background service is an older version.'
              : 'Collecting data — history builds while the app runs.'}
          />
          {/* Coverage toggle: reveal a strip under the time axis marking when
              the app was actually recording — the stretches the plot draws as
              real data, versus the quiet gaps that read as 0 B/s. */}
          <div className="mt-2 flex items-center gap-2 text-[11px] text-[var(--text-3)]">
            <Switch checked={uptime} onChange={pickUptime} label="Show app uptime timeline" />
            <button type="button" onClick={() => pickUptime(!uptime)} className="select-none transition-colors hover:text-[var(--text-2)]">
              App uptime
            </button>
          </div>
        </>
      )}
    </>
  )

  return (
    // The panel names itself a shared element: navigating Overview ⇄ Traffic
    // morphs the teaser into the hero (each screen mounts exactly one). The
    // view-transition group animates the box, so the teaser's quiet card can
    // morph into the bare hero.
    <div style={{viewTransitionName: 'pf-bw'} as React.CSSProperties}>
      {hero ? (
        <section>
          <div className="mb-4 flex items-end justify-between gap-3">
            <div className="min-w-0">
              <h2 className="text-[length:var(--fs-title)] font-semibold tracking-tight text-[var(--text)]">Bandwidth</h2>
              <p className="mt-0.5 text-xs text-[var(--text-2)]">{subtitleFor(range, data)}</p>
            </div>
            {liveReadout}
          </div>
          {body}
        </section>
      ) : (
        <Card
          title={compact ? undefined : 'Bandwidth'}
          label={compact ? 'Bandwidth' : undefined}
          subtitle={compact ? 'Last hour' : subtitleFor(range, data)}
          action={compact ? (
            <div className="flex items-center gap-3">
              {last && <LiveDot />}
              {onExpand && (
                <Button variant="ghost" size="sm" onClick={onExpand}>
                  Open Traffic <IconArrowRight size={13} />
                </Button>
              )}
            </div>
          ) : liveReadout}
        >
          {body}
        </Card>
      )}
    </div>
  )
}


/** RateHeadline: one big live figure for the compact teaser. The numeral wears
 * its series color, so it doubles as the legend for the sparkline below. */
function RateHeadline({color, glyph, label, value}: {
  color: string; glyph: string; label: string; value: number | null
}) {
  return (
    <div>
      <div className="text-[11px] text-[var(--text-3)]">
        <span style={{color}}>{glyph}</span> {label}
      </div>
      <div className="mt-0.5 font-mono text-[length:var(--fs-metric)] font-semibold leading-tight tabular-nums" style={{color}}>
        {value !== null ? <NumberTicker value={value} format={fmtRate} /> : <span className="text-[var(--text-3)]">—</span>}
      </div>
    </div>
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
      className={`pf-press inline-flex items-center gap-1.5 rounded-full border px-2 py-0.5 transition-all duration-150 ${
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

/** zeroBucket synthesizes an all-quiet bucket at slot time t. The app wasn't
 * running, so the tunnel relayed zero bytes — but the connection count and RTT
 * are genuinely *unknown* for that stretch, so their gauges stay at the -1
 * sentinel (the crosshair reads "0 B/s" yet omits conn/RTT rather than
 * inventing a fake 0 — same rule as any unknown gauge). */
function zeroBucket(t: number): Bucket {
  return {
    t, in: 0, out: 0,
    io: 0, ih: 0, il: 0, ic: 0,
    oo: 0, oh: 0, ol: 0, oc: 0,
    co: -1, ch: -1, cl: -1, cc: -1,
    ro: -1, rh: -1, rl: -1, rc: -1,
    po: -1, ph: -1, pl: -1, pc: -1,
    lo: -1, lh: -1, ll: -1, lc: -1,
  }
}

/** resolveHover maps a cursor x to the bucket under it: the real bucket whose
 * drawn slot the cursor sits in, or a synthetic zero bucket when the cursor is
 * over a time the app wasn't recording — a mid-history gap or the empty
 * pre-history lead-in before the window filled. So every x on the axis reads
 * out, quiet stretches honestly as 0 B/s, instead of snapping the crosshair to
 * the nearest moment the app happened to be open. */
function resolveHover(
  hoverX: number, buckets: Bucket[], bucketMs: number,
  t0: number, t1: number, span: number, padL: number, plotW: number,
  x: (t: number) => number,
): {b: Bucket; cx: number} | null {
  if (!buckets.length) return null
  // Slots tile [x(t), x(t+bucketMs)); contiguous buckets abut exactly, so a hit
  // here is genuine data (candle-coalesced, unevenly spaced slots included).
  for (const b of buckets) {
    if (hoverX >= x(b.t) && hoverX < x(b.t + bucketMs)) return {b, cx: x(b.t + bucketMs / 2)}
  }
  // Empty time: invert x to a timestamp (clamped into the domain), snap to the
  // bucket grid, and render it as zero throughput at that slot's center.
  const ht = Math.min(t1 - 1, Math.max(t0, t0 + ((hoverX - padL) / plotW) * span))
  const slot = Math.floor(ht / bucketMs) * bucketMs
  return {b: zeroBucket(slot), cx: x(slot + bucketMs / 2)}
}

/** useTweenedPlots eases plotted values toward each fresh target over ~220ms
 * (ease-out cubic), aligning by timestamp so refreshes glide instead of
 * snapping. Buckets with no prior value (newly appeared) snap in. */
function useTweenedPlots(target: Plot[]): Plot[] {
  const dispRef = useRef<Map<number, Plot>>(new Map())
  const [, force] = useState(0)
  useEffect(() => {
    // Reduced motion: snap to the final values, no rAF loop.
    if (prefersReduced()) {
      dispRef.current = new Map(target.map(p => [p.t, p]))
      force(x => x + 1)
      return
    }
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
// SparkChart: the Overview teaser plot — the last hour as pure shape. No axes,
// no grid, no time labels; the headline above and the hover readout carry the
// exact numbers. The viewBox tracks the well's CSS width so strokes and text
// render 1:1 at any card size (the full chart's fixed 720 viewBox is what made
// the old teaser feel cramped). Series use independent scales, mirroring the
// hero's left/right axes, so the small upload series keeps its shape.
// ---------------------------------------------------------------------------
const SPARK_H = 160
const SPARK_PAD = {t: 14, r: 2, b: 4, l: 2}

function SparkChart({buckets, bucketMs, windowMs = 0, emptyHint}: {
  buckets: Bucket[]
  bucketMs: number
  /** Range window (ms); anchors the time domain so young data grows in from
   * the right edge instead of floating mid-plot. 0 = fit the data extent. */
  windowMs?: number
  emptyHint?: string
}) {
  const svgRef = useRef<SVGSVGElement>(null)
  const [wellRef, w] = useMeasuredWidth()
  const [hoverX, setHoverX] = useState<number | null>(null)

  const plotW = w - SPARK_PAD.l - SPARK_PAD.r
  const baseY = SPARK_H - SPARK_PAD.b

  const view = useMemo(() => {
    if (!buckets.length || !bucketMs) return null
    // Anchor the domain to the full range window: a freshly started app has
    // only seconds of history, and fitting the extent floated a short line in
    // the middle of the plot. Anchored, the line hugs the right ("now") edge
    // and grows leftward until the window fills — the live-monitor read.
    const t1 = buckets[buckets.length - 1].t + bucketMs
    const t0 = windowMs > 0 ? Math.min(buckets[0].t, t1 - windowMs) : buckets[0].t
    const span = Math.max(1, t1 - t0)
    const x = (t: number) => SPARK_PAD.l + ((t - t0) / span) * plotW
    const dn = niceScale(Math.max(1, ...buckets.map(b => b.out * 1000 / bucketMs)))
    const up = niceScale(Math.max(1, ...buckets.map(b => b.in * 1000 / bucketMs)))
    const plotH = baseY - SPARK_PAD.t
    const yDn = (v: number) => baseY - (v / dn.max) * plotH
    const yUp = (v: number) => baseY - (v / up.max) * plotH
    return {t0, t1, span, x, yDn, yUp, nowMs: Date.now()}
  }, [buckets, bucketMs, windowMs, plotW, baseY])

  const plotsTarget = useMemo(
    () => (view ? toPlots(buckets, bucketMs, 'line', view.nowMs) : []),
    [buckets, bucketMs, view?.nowMs],
  )
  const plots = useTweenedPlots(plotsTarget)

  const hover = useMemo(
    () => (hoverX === null || !view ? null
      : resolveHover(hoverX, buckets, bucketMs, view.t0, view.t1, view.span, SPARK_PAD.l, plotW, view.x)),
    [hoverX, view, buckets, bucketMs, plotW],
  )

  if (!view) {
    // HTML well (not SVG text) so the long hints wrap; height matches the
    // populated spark so the card doesn't jump when data arrives.
    return (
      <div ref={wellRef} className="pf-well grid h-[138px] place-items-center p-3 text-center font-mono text-[11px] leading-relaxed text-[var(--text-3)]">
        {emptyHint ?? 'no data'}
      </div>
    )
  }

  const {x, yDn, yUp} = view
  const cx = (t: number) => x(t + bucketMs / 2)
  const dnLine = plots.map((p, i) => `${i === 0 ? 'M' : 'L'}${cx(p.t).toFixed(1)},${yDn(p.dn).toFixed(1)}`).join('')
  const upLine = plots.map((p, i) => `${i === 0 ? 'M' : 'L'}${cx(p.t).toFixed(1)},${yUp(p.up).toFixed(1)}`).join('')
  // A series that is flat zero across the window is not drawn: both lines
  // would stack neon on the baseline and whichever painted last (blue upload)
  // won. The baseline rule IS the zero line; the headlines above still say
  // "0 B/s" in each series color, so nothing goes unexplained.
  const dnIdle = plots.every(p => p.dn <= 0)
  const upIdle = plots.every(p => p.up <= 0)
  const first = plots[0]
  const lastP = plots[plots.length - 1]
  const hoverDn = hover ? hover.b.out * 1000 / bucketMs : 0
  const hoverUp = hover ? hover.b.in * 1000 / bucketMs : 0
  const hoverLeft = hover ? hover.cx < w / 2 : false

  return (
    <div ref={wellRef} className="pf-well relative p-1.5">
      <svg
        ref={svgRef}
        viewBox={`0 0 ${w} ${SPARK_H}`}
        className="block w-full"
        onMouseMove={e => {
          const r = svgRef.current!.getBoundingClientRect()
          setHoverX(((e.clientX - r.left) / r.width) * w)
        }}
        onMouseLeave={() => setHoverX(null)}
      >
        {/* Same vertical-fade recipe as the full chart, slightly stronger —
            there is no grid here for the fills to compete with. */}
        <defs>
          <linearGradient id="pf-spark-dl-area" gradientUnits="userSpaceOnUse" x1="0" y1={SPARK_PAD.t} x2="0" y2={baseY}>
            <stop offset="0" stopColor="var(--dl)" stopOpacity="0.35" />
            <stop offset="0.55" stopColor="var(--dl)" stopOpacity="0.10" />
            <stop offset="1" stopColor="var(--dl)" stopOpacity="0" />
          </linearGradient>
          <linearGradient id="pf-spark-ul-area" gradientUnits="userSpaceOnUse" x1="0" y1={SPARK_PAD.t} x2="0" y2={baseY}>
            <stop offset="0" stopColor="var(--ul)" stopOpacity="0.20" />
            <stop offset="0.55" stopColor="var(--ul)" stopOpacity="0.05" />
            <stop offset="1" stopColor="var(--ul)" stopOpacity="0" />
          </linearGradient>
        </defs>

        <line x1={SPARK_PAD.l} x2={w - SPARK_PAD.r} y1={baseY} y2={baseY} stroke="var(--border)" strokeWidth="1" opacity="0.4" />

        {!dnIdle && <>
          <path d={`${dnLine}L${cx(lastP.t).toFixed(1)},${baseY}L${cx(first.t).toFixed(1)},${baseY}Z`} fill="url(#pf-spark-dl-area)" />
          <path d={dnLine} fill="none" stroke="var(--dl)" strokeWidth="4.5" strokeLinejoin="round" className="pf-chart-halo" />
          <path d={dnLine} fill="none" stroke="var(--dl)" strokeWidth="1.5" strokeLinejoin="round" className="pf-chart-glow-hot" style={{color: 'var(--dl)'}} />
        </>}
        {!upIdle && <>
          <path d={`${upLine}L${cx(lastP.t).toFixed(1)},${baseY}L${cx(first.t).toFixed(1)},${baseY}Z`} fill="url(#pf-spark-ul-area)" />
          <path d={upLine} fill="none" stroke="var(--ul)" strokeWidth="4.5" strokeLinejoin="round" className="pf-chart-halo" />
          <path d={upLine} fill="none" stroke="var(--ul)" strokeWidth="1.5" strokeLinejoin="round" className="pf-chart-glow-hot" style={{color: 'var(--ul)'}} />
        </>}

        {/* End dots anchor the headline numerals to their line ends. */}
        {!dnIdle && <circle cx={cx(lastP.t)} cy={yDn(lastP.dn)} r="2" fill="var(--dl)" />}
        {!upIdle && <circle cx={cx(lastP.t)} cy={yUp(lastP.up)} r="2" fill="var(--ul)" />}

        {/* Hover: hairline + dots + one readout line in the top pad lane,
            keeping to the side away from the cursor. */}
        {hover && (
          <g pointerEvents="none">
            <line x1={hover.cx} x2={hover.cx} y1={SPARK_PAD.t} y2={baseY} stroke="var(--text-3)" strokeWidth="1" strokeDasharray="3 3" opacity="0.8" />
            <circle cx={hover.cx} cy={yDn(hoverDn)} r="2.5" fill="var(--dl)" stroke="var(--panel)" strokeWidth="1.5" />
            <circle cx={hover.cx} cy={yUp(hoverUp)} r="2.5" fill="var(--ul)" stroke="var(--panel)" strokeWidth="1.5" />
            <text
              x={hoverLeft ? w - 6 : 6} y={10} textAnchor={hoverLeft ? 'end' : 'start'}
              fontSize="10" fontFamily={MONO} style={{fontVariantNumeric: 'tabular-nums'}}
              paintOrder="stroke" stroke="var(--panel)" strokeWidth="3"
            >
              <tspan fill="var(--text-3)">{fmtTickTime(hover.b.t, 'minute')}</tspan>
              <tspan fill="var(--dl)" dx="8">↓ {fmtRate(hoverDn)}</tspan>
              <tspan fill="var(--ul)" dx="8">↑ {fmtRate(hoverUp)}</tspan>
            </text>
          </g>
        )}
      </svg>
    </div>
  )
}

// ---------------------------------------------------------------------------
// BandwidthChart: pure presentational SVG. Download (left axis) + upload
// (inner-right axis) are the dominant series; connections (step-line) and RTT
// (thin line) are recessive overlays, each on its own outboard right axis added
// only when enabled, so they never overlap the download/upload scales.
// ---------------------------------------------------------------------------
export function BandwidthChart({buckets: rawBuckets, bucketMs: rawBucketMs, windowMs = 0, mode, vis, emptyHint, height = H, showUptime = false}: {
  buckets: Bucket[]
  bucketMs: number
  /** Range window (ms); anchors the time domain so young data grows in from
   * the right edge instead of floating mid-plot. 0 = fit the data extent. */
  windowMs?: number
  mode: ChartMode
  vis: SeriesVisibility
  emptyHint?: string
  /** viewBox height; the plot area absorbs the change (default 260). */
  height?: number
  /** Draw the uptime coverage strip in an extra lane below the time axis. */
  showUptime?: boolean
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
  const plotH = height - PAD.t - PAD.b
  const baseY = PAD.t + plotH
  const plotRight = PAD.l + plotW

  const view = useMemo(() => {
    if (!buckets.length || !bucketMs) return null
    // Line/candle ranges are a live feed: right-anchor to the window so young
    // history hugs the "now" edge and grows leftward instead of floating short
    // in the middle. Bars are historical totals — fit the data extent instead,
    // so a sparse history (say a week of data inside the 30d window) spreads
    // across the plot rather than cramming against the right at a tiny scale
    // with overlapping date labels.
    const t1 = buckets[buckets.length - 1].t + bucketMs
    const t0 = mode === 'bars'
      ? buckets[0].t
      : windowMs > 0 ? Math.min(buckets[0].t, t1 - windowMs) : buckets[0].t
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
  }, [buckets, bucketMs, windowMs, mode, PAD.r, showConn, showRtt])

  const plotsTarget = useMemo(
    () => (view && mode !== 'bars' ? toPlots(buckets, bucketMs, mode, view.nowMs) : []),
    [buckets, bucketMs, mode, view?.nowMs],
  )
  const plots = useTweenedPlots(plotsTarget)

  const hover = useMemo(
    () => (hoverX === null || !view ? null
      : resolveHover(hoverX, buckets, bucketMs, view.t0, view.t1, view.span, PAD.l, plotW, view.x)),
    [hoverX, view, buckets, bucketMs, PAD.l, plotW],
  )

  if (!view) {
    return (
      <div className="pf-well relative p-1.5">
        <svg viewBox={`0 0 ${W} ${height}`} className="w-full">
          <EmptyGrid pad={PAD} plotW={plotW} plotH={plotH} />
          <text x={W / 2} y={height / 2} textAnchor="middle" fontSize="11" fill="var(--text-3)" fontFamily={MONO}>
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
  // Flat-zero series are not painted (see SparkChart): the baseline rule is
  // the zero line, and two glowing strokes stacked on it just read as one
  // wrong-colored line.
  const dnIdle = plots.length > 0 && plots.every(p => p.dn <= 0)
  const upIdle = plots.length > 0 && plots.every(p => p.up <= 0)

  // Outboard axis label x-positions (each column just right of the previous).
  const axisX = (idx: number) => plotRight + (mode === 'bars' ? 16 : 68) + idx * RIGHT_COL + 6
  const connIdx = 0
  const rttIdx = showConn ? 1 : 0

  // Uptime strip: an extra lane below the time labels, so the plot geometry is
  // unchanged whether it shows or not. Bar sits low in the lane, aligned to the
  // same x-domain as the axis above it.
  const svgH = showUptime ? height + UPTIME_LANE : height
  const stripY = height + 5
  const stripBarH = 6

  return (
    <div className="pf-well relative p-1.5">
      <svg
        ref={svgRef}
        viewBox={`0 0 ${W} ${svgH}`}
        className="w-full"
        onMouseMove={e => {
          const r = svgRef.current!.getBoundingClientRect()
          setHoverX(((e.clientX - r.left) / r.width) * W)
        }}
        onMouseLeave={() => setHoverX(null)}
      >
        {/* Clip the time-label lane to the plot's x-span so a label centered on
            a tick near either edge is cut at the axis gridline — it slides
            *under* the axis (paired with the edge fade below) instead of
            spilling into the y-axis value columns or the corner axis markers. */}
        <defs>
          <clipPath id="pf-bw-xlabels">
            <rect x={PAD.l} y={baseY} width={plotW} height={svgH - baseY} />
          </clipPath>
        </defs>

        {/* fine grid: horizontal at value ticks, vertical at time ticks —
            recessive; the data is the artwork */}
        <g stroke="var(--border)" strokeWidth="1" opacity="0.35">
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
          {/* time labels — clipped to the plot x-span (above) and cross-faded
              at both edges, so a tick drifting toward an edge (live data slides
              the axis) dims and tucks under the axis gridline instead of
              popping or overrunning it; a new tick fades in from the right. The
              ramp reaches 0 exactly at the gridline (24px window), where the
              clip has already hidden the overhang. */}
          <g clipPath="url(#pf-bw-xlabels)">
            {timeTicks.map((t, i) => {
              const fade = Math.min(1, (t.x - PAD.l) / 24, (plotRight - t.x) / 24)
              if (fade <= 0.02) return null
              return (
                <text key={`t${i}`} x={t.x} y={height - 8} textAnchor="middle" fill="var(--text-3)" opacity={fade.toFixed(2)}>
                  {t.label}
                </text>
              )
            })}
          </g>
        </g>
        {/* axis names, in the time-label row's empty corners — spelled out and
            wearing their exact series color, so the outboard tick columns
            never read as mystery numbers */}
        <g fontSize="9" fontFamily={MONO}>
          {showDn && <text x={PAD.l - 6} y={height - 8} textAnchor="end" fill="var(--dl)">↓</text>}
          {showUp && <text x={plotRight + 6} y={height - 8} textAnchor="start" fill="var(--ul)">↑</text>}
          {view.connScale && <text x={axisX(connIdx)} y={height - 8} textAnchor="start" fill="var(--conn)">conns</text>}
          {view.rttScale && <text x={axisX(rttIdx)} y={height - 8} textAnchor="start" fill="var(--rtt)">RTT ms</text>}
        </g>

        {mode === 'line' && (
          <>
            {/* Static gradient ids are safe: exactly one BandwidthPanel mounts
                per screen. The vertical fade is the glass glowing under the
                line; each series gets a fat translucent halo twin beneath the
                crisp stroke (filterless — survives busy tween frames) and the
                layered pf-chart-glow-hot neon on top. */}
            <defs>
              <linearGradient id="pf-bw-dl-area" gradientUnits="userSpaceOnUse" x1="0" y1={PAD.t} x2="0" y2={baseY}>
                <stop offset="0" stopColor="var(--dl)" stopOpacity="0.30" />
                <stop offset="0.55" stopColor="var(--dl)" stopOpacity="0.08" />
                <stop offset="1" stopColor="var(--dl)" stopOpacity="0" />
              </linearGradient>
              <linearGradient id="pf-bw-ul-area" gradientUnits="userSpaceOnUse" x1="0" y1={PAD.t} x2="0" y2={baseY}>
                <stop offset="0" stopColor="var(--ul)" stopOpacity="0.16" />
                <stop offset="0.55" stopColor="var(--ul)" stopOpacity="0.04" />
                <stop offset="1" stopColor="var(--ul)" stopOpacity="0" />
              </linearGradient>
            </defs>
            {showDn && !dnIdle && <>
              <path
                d={`${dnLine}L${cx(plots[plots.length - 1].t).toFixed(1)},${baseY}L${cx(plots[0].t).toFixed(1)},${baseY}Z`}
                fill="url(#pf-bw-dl-area)"
              />
              <path d={dnLine} fill="none" stroke="var(--dl)" strokeWidth="4.5" strokeLinejoin="round" className="pf-chart-halo" />
              <path d={dnLine} fill="none" stroke="var(--dl)" strokeWidth="1.5" strokeLinejoin="round" className="pf-chart-glow-hot" style={{color: 'var(--dl)'}} />
              {plots.length === 1 && <circle cx={cx(plots[0].t)} cy={yL(plots[0].dn)} r="2.5" fill="var(--dl)" />}
            </>}
            {showUp && !upIdle && <>
              <path
                d={`${upLinePlots}L${cx(plots[plots.length - 1].t).toFixed(1)},${baseY}L${cx(plots[0].t).toFixed(1)},${baseY}Z`}
                fill="url(#pf-bw-ul-area)"
              />
              <path d={upLinePlots} fill="none" stroke="var(--ul)" strokeWidth="4.5" strokeLinejoin="round" className="pf-chart-halo" />
              <path d={upLinePlots} fill="none" stroke="var(--ul)" strokeWidth="1.5" strokeLinejoin="round" className="pf-chart-glow-hot" style={{color: 'var(--ul)'}} />
              {plots.length === 1 && <circle cx={cx(plots[0].t)} cy={yR(plots[0].up)} r="2.5" fill="var(--ul)" />}
            </>}
          </>
        )}

        {mode === 'candles' && (
          <>
            {/* Two direction groups so the bloom filter runs once per color,
                not once per candle. Frosted translucent bodies let the well
                ghost through — up stays lighter than down so direction still
                reads without the old hollow-vs-solid trick. */}
            {showDn && (['up', 'down'] as const).map(dir => (
              <g key={dir} className="pf-chart-glow" style={{color: dir === 'up' ? 'var(--good)' : 'var(--bad)'}}>
                {buckets.map((b, i) => {
                  const rising = b.oc >= b.oo
                  if ((dir === 'up') !== rising) return null
                  const bcx = x(b.t + bucketMs / 2)
                  const bw = Math.max(2, Math.min(14, slotW * 0.66))
                  const col = rising ? 'var(--good)' : 'var(--bad)'
                  const top = yL(Math.max(b.oo, b.oc))
                  const bot = yL(Math.min(b.oo, b.oc))
                  return (
                    <g key={i}>
                      <line x1={bcx} x2={bcx} y1={yL(b.oh)} y2={yL(b.ol)} stroke={col} strokeWidth="1" opacity="0.8" />
                      <rect
                        x={bcx - bw / 2} y={top} width={bw} height={Math.max(1, bot - top)} rx="1"
                        fill={`color-mix(in srgb, ${col} ${rising ? 24 : 42}%, transparent)`}
                        stroke={col} strokeWidth="1"
                      />
                    </g>
                  )
                })}
              </g>
            ))}
            {showUp && !upIdle && <path d={upLinePlots} fill="none" stroke="var(--ul)" strokeWidth="1" opacity="0.85" />}
          </>
        )}

        {/* Connections step-line overlay (gauge: constant within a bucket). */}
        {showConn && view.yConn && stepSegments(buckets, x, bucketMs, view.yConn, b => b.cc).map((d, i) => (
          <path key={`cs${i}`} d={d} fill="none" stroke="var(--conn)" strokeWidth="1.25" opacity="0.9" strokeLinejoin="round" />
        ))}
        {/* RTT overlay: thin line, broken at unknown gaps. */}
        {showRtt && view.yRtt && gappedSegments(plots, cx, view.yRtt, p => p.rtt).map((d, i) => (
          <path key={`rl${i}`} d={d} fill="none" stroke="var(--rtt)" strokeWidth="1.25" opacity="0.9" strokeLinejoin="round" />
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

        {/* uptime coverage strip: neutral fill across the spans the app was
            recording, faint track across downtime — the plot's quiet gaps line
            up with the empty stretches here. */}
        {showUptime && (
          <g>
            <rect x={PAD.l} y={stripY} width={plotW} height={stripBarH} rx="2" fill="var(--border)" opacity="0.6" />
            {coverageRuns(buckets, bucketMs).map((r, i) => {
              const rx0 = Math.max(PAD.l, x(r.a))
              const rx1 = Math.min(plotRight, x(r.b))
              return (
                <rect key={`up${i}`} x={rx0} y={stripY} width={Math.max(1, rx1 - rx0)} height={stripBarH}
                  rx="2" fill="var(--text-2)" opacity="0.8" />
              )
            })}
          </g>
        )}

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

/** coverageRuns collapses the buckets into the wall-clock spans the app was
 * actually recording: adjacent buckets (starts within half a slot of the prior
 * slot's end) join one run; a real gap starts a new one. These are the filled
 * segments of the uptime strip — everything between them is downtime. */
function coverageRuns(buckets: Bucket[], bucketMs: number): {a: number; b: number}[] {
  const runs: {a: number; b: number}[] = []
  for (const bk of buckets) {
    const last = runs[runs.length - 1]
    if (last && bk.t - last.b <= bucketMs * 0.5) last.b = bk.t + bucketMs
    else runs.push({a: bk.t, b: bk.t + bucketMs})
  }
  return runs
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

// ---- ticks ----------------------------------------------------------------
// (niceScale / niceLinear / fmtTickTime moved to charts/util.ts.)

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
