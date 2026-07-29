import {useEffect, useMemo, useRef, useState} from 'react'
import {ExportDiagnostics, LogsSince} from '../../wailsjs/go/app/App'
import {logging} from '../../wailsjs/go/models'
import {Badge, Button, Checkbox, EmptyState, PageHeader, Select, Skeleton, TextInput, copyText} from '../components/ui'
import {IconExternal, IconLogs, IconTerminal} from '../components/icons'

type Entry = logging.Entry
const CAP = 2000
const LEVELS = ['debug', 'info', 'warn', 'error']

// How far off the bottom still counts as "at the tail". A scroll measurement,
// so it is px and deliberately does NOT ride --ui-scale; it only has to absorb
// the sub-pixel remainder a fractional device-pixel scrollHeight leaves behind.
const TAIL_SLACK = 8

/** Activity: a live tail of the engine's log ring, filterable by level and
 * text, with copy and one-click diagnostics export. */
export function Activity({attached}: {attached: boolean}) {
  const [entries, setEntries] = useState<Entry[]>([])
  // An empty ring and an unanswered first poll look identical from `entries`
  // alone, and the poll is 250 ms away — without this flag the well opens on
  // "No log lines" and then contradicts itself.
  const [loaded, setLoaded] = useState(false)
  const [level, setLevel] = useState('info')
  const [query, setQuery] = useState('')
  const [follow, setFollow] = useState(true)
  const [exportMsg, setExportMsg] = useState('')
  const lastSeq = useRef(0)
  const bodyRef = useRef<HTMLDivElement>(null)
  // Whether the reader is parked at the tail. Kept in a ref, not state: it is
  // read by an effect and written on every scroll frame, and re-rendering the
  // whole log that often is exactly what the well cannot afford. Two writers,
  // deliberately: the poll (authoritative, see below) and the scroll handler,
  // which keeps it fresh for the renders no poll drives — a level or filter
  // change while nothing is arriving.
  const atTail = useRef(true)

  // Poll the ring for new lines at 4 Hz.
  useEffect(() => {
    let alive = true
    const pull = async () => {
      try {
        const fresh = await LogsSince(lastSeq.current)
        if (alive && fresh.length) {
          // Where the reader stands, read BEFORE the new lines land: once they
          // are in the DOM the distance from the bottom already counts them,
          // and the scroll handler alone loses the race (a scroll event is
          // dispatched a frame later, so a poll landing in that gap would read
          // a stale "at the tail" and yank the reader back).
          const el = bodyRef.current
          atTail.current = !el || el.scrollHeight - el.scrollTop - el.clientHeight <= TAIL_SLACK
          lastSeq.current = fresh[fresh.length - 1].seq
          setEntries(prev => [...prev, ...fresh].slice(-CAP))
        }
      } catch { /* engine may be mid-restart */ }
      // Answered or refused, the first poll has landed — an engine that is
      // mid-restart must still retire the skeleton, or it never comes back.
      // Re-setting an unchanged `true` every 250 ms is a React bail-out, not
      // a render.
      finally { if (alive) setLoaded(true) }
    }
    pull()
    const t = setInterval(pull, 250)
    return () => { alive = false; clearInterval(t) }
  }, [])

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase()
    const minRank = level === 'all' ? -1 : LEVELS.indexOf(level)
    return entries.filter(e => {
      // The Go backend emits uppercase level names ("INFO"); compare case-insensitively.
      if (minRank >= 0 && LEVELS.indexOf(e.level.toLowerCase()) < minRank) return false
      if (q && !(`${e.msg} ${e.attrs}`.toLowerCase().includes(q))) return false
      return true
    })
  }, [entries, level, query])

  // Auto-scroll to newest when following — but only from the tail. Chasing it
  // unconditionally means a reader who scrolled up into history is yanked back
  // to the bottom every 250 ms, which makes the log unreadable by keyboard.
  // The gate is where the reader stood before this batch (atTail), never a
  // measurement taken here: by now the new lines are laid out, so "distance
  // from the bottom" has already grown for someone who never moved. Ticking
  // Follow re-arms it, so the checkbox still means "take me to the end".
  useEffect(() => {
    const el = bodyRef.current
    if (!loaded || !follow || !el || !atTail.current) return
    el.scrollTop = el.scrollHeight
  }, [filtered, follow, loaded])

  const copyAll = () => copyText(filtered.map(fmtLine).join('\n'))
  const doExport = async () => {
    setExportMsg('')
    try {
      const path = await ExportDiagnostics()
      if (path) setExportMsg(`Saved to ${path}`)
    } catch (e) { setExportMsg(String(e)) }
  }

  return (
    // The console owns the viewport: header + a card that fills the rest,
    // with only the log body scrolling. 3rem = the canvas's py-6 total.
    <div className="pf-stagger flex h-[calc(100dvh-var(--chrome-top)-3rem)] min-h-0 flex-col">
      <PageHeader
        eyebrow="sediment"
        title="Activity"
        subtitle="A live tail of the engine's log."
        action={<Button variant="ghost" size="sm" onClick={doExport}><IconExternal size={14} /> Export diagnostics</Button>}
      />

      {exportMsg && <div className="pf-fade mb-3 select-text text-xs text-[var(--text-3)]">{exportMsg}</div>}

      <div className="pf-card flex min-h-0 flex-1 flex-col overflow-hidden">
        {/* Console toolbar: every filter and control on one band. */}
        <div className="pf-toolbar relative flex flex-wrap items-center gap-2 border-b border-[var(--border)] px-3 py-2">
          <div className="w-36"><Select value={level} onChange={setLevel} options={[
            {value: 'all', label: 'All levels'},
            ...LEVELS.map(l => ({value: l, label: l[0].toUpperCase() + l.slice(1)})),
          ]} /></div>
          <div className="min-w-40 flex-1"><TextInput value={query} onChange={setQuery} placeholder="Filter messages…" /></div>
          <Checkbox checked={follow} onChange={v => { setFollow(v); if (v) atTail.current = true }} label="Follow" />
          <Button variant="ghost" size="sm" onClick={copyAll}>Copy</Button>
          <span className="ml-1 hidden text-xs tabular-nums text-[var(--text-3)] sm:inline">
            {filtered.length} of {entries.length}
          </span>
          <Badge tone="neutral">ring · {CAP} max</Badge>
        </div>
        {/* The well bounces at its ends; the glow is its sibling so it neither
            scrolls nor rides the band's transform (rubberband.ts). */}
        <div className="relative flex min-h-0 flex-1 flex-col">
          {/* role="log": the well is an append-only record, so a screen reader
              announces arriving lines politely instead of re-reading the ring.
              onScroll records where the reader left off (see the follow gate). */}
          <div
            ref={bodyRef}
            role="log"
            aria-label="Engine log"
            aria-busy={!loaded}
            onScroll={() => {
              const el = bodyRef.current
              if (el) atTail.current = el.scrollHeight - el.scrollTop - el.clientHeight <= TAIL_SLACK
            }}
            className="pf-well-flush relative min-h-0 flex-1 select-text overflow-y-auto overscroll-y-contain px-3 py-2 font-mono text-body leading-relaxed"
          >
            <div data-band-content>
              {!loaded
                ? <LogSkeleton />
                : filtered.length === 0
                  ? (attached && entries.length === 0
                    ? <EmptyState icon={<IconTerminal size={26} />} title="The service keeps its own logs"
                        hint="This window views a background service, which logs to its own files. Export diagnostics to collect them." />
                    : <EmptyState icon={<IconLogs size={26} />} title="No log lines"
                        hint="Activity streams here as the engine runs." />)
                  : filtered.map(e => <LogLine key={e.seq} e={e} />)}
            </div>
          </div>
          <div className="pf-band-glow absolute inset-0 z-10" data-band-glow aria-hidden />
        </div>
      </div>
    </div>
  )
}

const levelColor: Record<string, string> = {
  debug: 'var(--text-3)', info: 'var(--text-2)', warn: 'var(--warn)', error: 'var(--bad)',
}

function LogLine({e}: {e: Entry}) {
  const time = new Date(e.timeMs).toLocaleTimeString(undefined, {hour12: false})
  const c = levelColor[e.level.toLowerCase()] ?? 'var(--text-2)'
  return (
    <div className="pf-fade flex items-baseline gap-2 whitespace-pre-wrap break-words py-0.5 transition-colors duration-150 hover:bg-[var(--panel)]/40">
      <span className="shrink-0 text-[var(--text-3)]">{time}</span>
      <span
        className="w-12 shrink-0 rounded-[var(--r-xs)] px-1 text-center text-micro font-semibold uppercase leading-4"
        style={{color: c, background: `color-mix(in srgb, ${c} 12%, transparent)`}}
      >{e.level}</span>
      <span className="min-w-0 flex-1 text-[var(--text)]">
        {e.msg}
        {e.attrs && <span className="text-[var(--text-3)]"> {e.attrs}</span>}
      </span>
    </div>
  )
}

// Message-bar widths, as a fraction of the message column. Irregular on
// purpose: equal bars read as a comb, not as sediment settling.
const SKELETON_BARS = [
  '58%', '81%', '43%', '92%', '36%', '70%', '64%',
  '88%', '49%', '77%', '33%', '85%', '55%', '73%',
  '40%', '95%', '61%', '46%', '79%', '52%',
]

/** LogSkeleton: the well before the first poll answers — LogLine's geometry,
 * to the row. Same flex box and py-0.5; a time column of 8 mono glyphs (ch, so
 * it is exactly the width of "12:34:56" in this face); the level chip at its
 * real 1rem × 3rem box and --r-xs corners; a flex-1 message bar.
 *
 * The row height is one line box of the well's own type — 1lh resolves against
 * the inherited text-body × leading-relaxed — plus py-0.5, which is what a real
 * LogLine measures (the chip's shorter box sits inside it, so the text line is
 * what sets the height). Relative units throughout, so --ui-scale carries it
 * and the rows never shift when the real lines land.
 *
 * The chip radius is an inline style. It no longer has to be — .pf-skeleton's
 * radius lives in @layer base now, so a rounded-* utility would win — but it
 * still says exactly which token this chip owes its shape to, which is the
 * point at 4px. */
function LogSkeleton() {
  return (
    <div aria-hidden>
      {SKELETON_BARS.map((w, i) => (
        <div key={i} className="flex h-[calc(1lh+0.25rem)] items-center gap-2 py-0.5">
          <Skeleton className="h-2.5 w-[8ch] shrink-0" />
          <Skeleton className="h-4 w-12 shrink-0" style={{borderRadius: 'var(--r-xs)'}} />
          <div className="min-w-0 flex-1"><Skeleton className="h-2.5" style={{width: w}} /></div>
        </div>
      ))}
    </div>
  )
}

function fmtLine(e: Entry): string {
  return `${new Date(e.timeMs).toISOString()} ${e.level.toUpperCase().padEnd(5)} ${e.msg} ${e.attrs}`.trimEnd()
}
