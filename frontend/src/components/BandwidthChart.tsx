import {useMemo, useRef, useState} from 'react'
import {fmtBytes, fmtRate} from '../state'
import {
  Bucket, ChartMode, HistoryResult, RANGE_KEYS, RANGES, RangeKey,
  loadCandlePref, loadRangePref, modeFor, saveCandlePref, saveRangePref,
  useBandwidthHistory,
} from '../history'
import {Card} from './ui'

// Engineering aesthetic: every numeral is mono + tabular, grid is fine and
// recessive, lines are straight (no smoothing), values are exact.
const MONO = "ui-monospace, 'Cascadia Mono', Consolas, monospace"

const W = 720
const H = 260

// ---------------------------------------------------------------------------
// BandwidthPanel: range selector + mode toggle + stats row + chart. Fully
// self-contained: it polls its own data at the range's cadence and keeps it
// in a module-level cache so tab switches never lose history.
// ---------------------------------------------------------------------------
export function BandwidthPanel({historyUnsupported}: {historyUnsupported?: boolean}) {
  const [range, setRange] = useState<RangeKey>(loadRangePref)
  const [candles, setCandles] = useState<boolean>(loadCandlePref)
  const data = useBandwidthHistory(range)
  const spec = RANGES[range]
  const mode = modeFor(range, candles)
  const buckets = data?.buckets ?? []
  const last = buckets.length ? buckets[buckets.length - 1] : null

  const pickRange = (r: RangeKey) => { setRange(r); saveRangePref(r) }
  const pickCandles = (on: boolean) => { setCandles(on); saveCandlePref(on) }

  return (
    <Card
      title="Bandwidth"
      subtitle={subtitleFor(range, data)}
      action={last && mode !== 'bars' ? (
        <div className="flex gap-4 text-xs tabular-nums" style={{fontFamily: MONO}}>
          <span className="text-[var(--dl)]">↓ {fmtRate(last.oc)}</span>
          <span className="text-[var(--ul)]">↑ {fmtRate(last.ic)}</span>
        </div>
      ) : undefined}
    >
      <div className="mb-3 flex flex-wrap items-center justify-between gap-2">
        <div className="inline-flex rounded-lg border border-[var(--border)] bg-[var(--panel-2)] p-0.5">
          {RANGE_KEYS.map(k => (
            <button
              key={k}
              onClick={() => pickRange(k)}
              className={`rounded-md px-2 py-1 text-[11px] font-medium tabular-nums transition-colors duration-150 ${
                k === range
                  ? 'bg-[var(--panel)] text-[var(--text)] shadow-[var(--shadow-soft)]'
                  : 'text-[var(--text-3)] hover:text-[var(--text)]'
              }`}
              style={{fontFamily: MONO}}
            >{RANGES[k].label}</button>
          ))}
        </div>
        {spec.render === 'bars' ? (
          <span className="text-[11px] text-[var(--text-3)]" style={{fontFamily: MONO}}>
            {RANGES[range].windowMs === 604_800_000 ? 'hourly totals' : 'daily totals'}
          </span>
        ) : (
          <div className="inline-flex rounded-lg border border-[var(--border)] bg-[var(--panel-2)] p-0.5">
            {(['line', 'candles'] as const).map(m => {
              const disabled = m === 'candles' && !spec.candlesOk
              const on = mode === m
              return (
                <button
                  key={m}
                  disabled={disabled}
                  title={disabled ? 'Candles need 15m or longer ranges' : undefined}
                  onClick={() => pickCandles(m === 'candles')}
                  className={`rounded-md px-2 py-1 text-[11px] font-medium transition-colors duration-150 ${
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

      <StatsRow buckets={buckets} mode={mode} bucketMs={data?.bucketMs ?? 0} />

      <BandwidthChart
        buckets={buckets}
        bucketMs={data?.bucketMs ?? 1000}
        mode={mode}
        emptyHint={historyUnsupported
          ? 'history unavailable — the daemon is an older version'
          : 'collecting data — history builds while the app runs'}
      />
    </Card>
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

/** Legend + min/avg/max/last (rates) or window totals (bars), all mono. */
function StatsRow({buckets, mode, bucketMs}: {buckets: Bucket[]; mode: ChartMode; bucketMs: number}) {
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

  return (
    <div className="mb-1.5 flex flex-wrap items-center justify-between gap-x-4 gap-y-1">
      <div className="flex items-center gap-4 text-[11px] text-[var(--text-2)]">
        <span className="inline-flex items-center gap-1.5">
          <span className="inline-block h-[3px] w-4 rounded-full" style={{background: 'var(--dl)'}} />
          ↓ download
        </span>
        <span className="inline-flex items-center gap-1.5">
          <span className="inline-block h-[3px] w-4 rounded-full" style={{background: 'var(--ul)'}} />
          ↑ upload
        </span>
      </div>
      {stats && (
        <div className="text-[10.5px] tabular-nums text-[var(--text-3)]" style={{fontFamily: MONO}}>
          {stats.kind === 'rates'
            ? <>min {fmtRate(stats.min)} · avg {fmtRate(stats.avg)} · max {fmtRate(stats.max)} · last {fmtRate(stats.last)}</>
            : <>Σ↓ {fmtBytes(stats.dn)} · Σ↑ {fmtBytes(stats.up)} · peak {fmtBytes(stats.peak)}</>}
        </div>
      )}
    </div>
  )
}

// ---------------------------------------------------------------------------
// BandwidthChart: pure presentational SVG. Three modes over the same buckets:
//   line    — straight polylines of per-bucket average rate, dual y-axes
//             (left = download, right = upload: the ~100× disparity gets its
//             own scale instead of a squashed line)
//   candles — download rate OHLC per bucket (hollow up / filled down, so
//             direction never rides on color alone); upload stays a line
//   bars    — per-bucket transferred bytes, single byte axis
// ---------------------------------------------------------------------------
export function BandwidthChart({buckets: rawBuckets, bucketMs: rawBucketMs, mode, emptyHint}: {
  buckets: Bucket[]
  bucketMs: number
  mode: ChartMode
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
      })
    }
    return [out, rawBucketMs * k]
  }, [rawBuckets, rawBucketMs, mode])

  const PAD = {l: 68, r: mode === 'bars' ? 16 : 68, t: 10, b: 22}
  const plotW = W - PAD.l - PAD.r
  const plotH = H - PAD.t - PAD.b
  const baseY = PAD.t + plotH

  const view = useMemo(() => {
    if (!buckets.length || !bucketMs) return null
    const t0 = buckets[0].t
    const t1 = buckets[buckets.length - 1].t + bucketMs
    const span = Math.max(1, t1 - t0)
    const x = (t: number) => PAD.l + ((t - t0) / span) * plotW

    // Left scale: download (rates or bytes); right scale: upload rates.
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

    const timeTicks = ticksFor(t0, t1, bucketMs, buckets)
    const nowMs = Date.now()
    return {t0, t1, span, x, left, right, yL, yR, timeTicks, nowMs}
  }, [buckets, bucketMs, mode, PAD.r])

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
      <div className="relative">
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

  // Line-mode geometry (also the upload line in candle mode).
  const linePath = (get: (b: Bucket) => number, y: (v: number) => number) =>
    buckets.map((b, i) => `${i === 0 ? 'M' : 'L'}${x(b.t + bucketMs / 2).toFixed(1)},${y(get(b)).toFixed(1)}`).join('')
  const dnRate = (b: Bucket) => b.out * 1000 / bucketMs
  const upRate = (b: Bucket) => b.in * 1000 / bucketMs
  const upLine = mode === 'candles' ? linePath(b => b.ic, yR) : linePath(upRate, yR)

  const slotW = (bucketMs / view.span) * plotW

  return (
    <div className="relative">
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
            <line key={`h${i}`} x1={PAD.l} x2={W - PAD.r} y1={yL(v)} y2={yL(v)} />
          ))}
          {timeTicks.map((t, i) => (
            <line key={`v${i}`} x1={x(t.t)} x2={x(t.t)} y1={PAD.t} y2={baseY} />
          ))}
        </g>
        <line x1={PAD.l} x2={W - PAD.r} y1={baseY} y2={baseY} stroke="var(--border)" strokeWidth="1" />

        {/* y-axis labels: left = download, right = upload */}
        <g fontSize="10" fontFamily={MONO} fill="var(--text-3)" style={{fontVariantNumeric: 'tabular-nums'}}>
          {left.ticks.map((v, i) => v > 0 && (
            <text key={`l${i}`} x={PAD.l - 6} y={yL(v) + 3.5} textAnchor="end">
              {mode === 'bars' ? fmtBytes(v) : fmtRate(v)}
            </text>
          ))}
          {mode !== 'bars' && right.ticks.map((v, i) => v > 0 && (
            <text key={`r${i}`} x={W - PAD.r + 6} y={yR(v) + 3.5} textAnchor="start">{fmtRate(v)}</text>
          ))}
          {timeTicks.map((t, i) => (
            <text key={`t${i}`} x={x(t.t)} y={H - 8} textAnchor="middle">{t.label}</text>
          ))}
        </g>
        {/* axis direction markers, in the time-label row's empty corners */}
        <g fontSize="9" fontFamily={MONO}>
          <text x={PAD.l - 6} y={H - 8} textAnchor="end" fill="var(--dl)">↓</text>
          {mode !== 'bars' && <text x={W - PAD.r + 6} y={H - 8} textAnchor="start" fill="var(--ul)">↑</text>}
        </g>

        {mode === 'line' && (
          <>
            <path
              d={`${linePath(dnRate, yL)}L${x(view.t1 - bucketMs / 2).toFixed(1)},${baseY}L${x(view.t0 + bucketMs / 2).toFixed(1)},${baseY}Z`}
              fill="var(--dl)" opacity="0.08"
            />
            <path d={linePath(dnRate, yL)} fill="none" stroke="var(--dl)" strokeWidth="1.5" strokeLinejoin="round" />
            <path d={upLine} fill="none" stroke="var(--ul)" strokeWidth="1.5" strokeLinejoin="round" />
            {buckets.length === 1 && (
              <>
                <circle cx={x(buckets[0].t + bucketMs / 2)} cy={yL(dnRate(buckets[0]))} r="2.5" fill="var(--dl)" />
                <circle cx={x(buckets[0].t + bucketMs / 2)} cy={yR(upRate(buckets[0]))} r="2.5" fill="var(--ul)" />
              </>
            )}
          </>
        )}

        {mode === 'candles' && (
          <>
            {buckets.map((b, i) => {
              const cx = x(b.t + bucketMs / 2)
              const bw = Math.max(2, Math.min(14, slotW * 0.66))
              const up = b.oc >= b.oo
              const col = up ? 'var(--good)' : 'var(--bad)'
              const top = yL(Math.max(b.oo, b.oc))
              const bot = yL(Math.min(b.oo, b.oc))
              return (
                <g key={i}>
                  <line x1={cx} x2={cx} y1={yL(b.oh)} y2={yL(b.ol)} stroke={col} strokeWidth="1" />
                  <rect
                    x={cx - bw / 2} y={top} width={bw} height={Math.max(1, bot - top)}
                    fill={up ? 'var(--panel)' : col} stroke={col} strokeWidth="1"
                  />
                </g>
              )
            })}
            <path d={upLine} fill="none" stroke="var(--ul)" strokeWidth="1" opacity="0.85" />
          </>
        )}

        {mode === 'bars' && buckets.map((b, i) => {
          const bx = x(b.t)
          const bw = Math.max(1, slotW - Math.min(2, slotW * 0.2))
          const partial = b.t + bucketMs > view.nowMs
          return (
            <g key={i} opacity={partial ? 0.6 : 1}>
              <rect x={bx} y={yL(b.out)} width={bw} height={Math.max(0, baseY - yL(b.out))} fill="var(--dl)" opacity="0.75" rx="1" />
              <rect x={bx} y={yL(b.in)} width={bw} height={Math.max(0, baseY - yL(b.in))} fill="var(--ul)" rx="1" />
            </g>
          )
        })}

        {/* crosshair + readout */}
        {hover && (
          <Crosshair
            hover={hover} mode={mode} bucketMs={bucketMs}
            yL={yL} yR={yR} pad={PAD} plotH={plotH}
          />
        )}
      </svg>
    </div>
  )
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

function Crosshair({hover, mode, bucketMs, yL, yR, pad, plotH}: {
  hover: {b: Bucket; cx: number}
  mode: ChartMode
  bucketMs: number
  yL: (v: number) => number
  yR: (v: number) => number
  pad: {l: number; r: number; t: number; b: number}
  plotH: number
}) {
  const {b, cx} = hover
  const dnRate = b.out * 1000 / bucketMs
  const upRate = b.in * 1000 / bucketMs
  const yDn = mode === 'bars' ? yL(b.out) : mode === 'candles' ? yL(b.oc) : yL(dnRate)

  const timeLabel = fmtTickTime(b.t, bucketMs >= 86_400_000 ? 'day' : bucketMs >= 60_000 ? 'minute' : 'second')
  const lines: {k: string; v: string; c?: string}[] = mode === 'candles'
    ? [
      {k: 'O', v: fmtRate(b.oo)}, {k: 'H', v: fmtRate(b.oh)},
      {k: 'L', v: fmtRate(b.ol)}, {k: 'C', v: fmtRate(b.oc), c: b.oc >= b.oo ? 'var(--good)' : 'var(--bad)'},
      {k: '↑', v: fmtRate(b.ic), c: 'var(--ul)'},
    ]
    : mode === 'bars'
      ? [{k: '↓', v: fmtBytes(b.out), c: 'var(--dl)'}, {k: '↑', v: fmtBytes(b.in), c: 'var(--ul)'}]
      : [{k: '↓', v: fmtRate(dnRate), c: 'var(--dl)'}, {k: '↑', v: fmtRate(upRate), c: 'var(--ul)'}]

  const boxW = 128
  const boxH = 18 + lines.length * 13
  const boxX = cx + 10 + boxW > W - pad.r ? cx - 10 - boxW : cx + 10
  const boxY = pad.t + 4

  return (
    <g pointerEvents="none">
      <line x1={cx} x2={cx} y1={pad.t} y2={pad.t + plotH} stroke="var(--text-3)" strokeWidth="1" strokeDasharray="3 3" opacity="0.8" />
      <line x1={pad.l} x2={W - pad.r} y1={yDn} y2={yDn} stroke="var(--text-3)" strokeWidth="1" strokeDasharray="3 3" opacity="0.5" />
      {mode !== 'bars' && (
        <>
          <circle cx={cx} cy={yDn} r="3" fill="var(--dl)" stroke="var(--panel)" strokeWidth="1.5" />
          <circle cx={cx} cy={yR(mode === 'candles' ? b.ic : upRate)} r="3" fill="var(--ul)" stroke="var(--panel)" strokeWidth="1.5" />
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
