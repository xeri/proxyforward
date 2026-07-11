import {useEffect, useMemo, useRef, useState} from 'react'
import {ExportDiagnostics, LogsSince} from '../../wailsjs/go/app/App'
import {logging} from '../../wailsjs/go/models'
import {Badge, Button, Card, Checkbox, EmptyState, PageHeader, Select, TextInput, copyText} from '../components/ui'
import {IconExternal, IconLogs, IconTerminal} from '../components/icons'

type Entry = logging.Entry
const CAP = 2000
const LEVELS = ['debug', 'info', 'warn', 'error']

/** Activity: a live tail of the engine's log ring, filterable by level and
 * text, with copy and one-click diagnostics export. */
export function Activity({attached}: {attached: boolean}) {
  const [entries, setEntries] = useState<Entry[]>([])
  const [level, setLevel] = useState('info')
  const [query, setQuery] = useState('')
  const [follow, setFollow] = useState(true)
  const [exportMsg, setExportMsg] = useState('')
  const lastSeq = useRef(0)
  const bodyRef = useRef<HTMLDivElement>(null)

  // Poll the ring for new lines at 4 Hz.
  useEffect(() => {
    let alive = true
    const pull = async () => {
      try {
        const fresh = await LogsSince(lastSeq.current)
        if (alive && fresh.length) {
          lastSeq.current = fresh[fresh.length - 1].seq
          setEntries(prev => [...prev, ...fresh].slice(-CAP))
        }
      } catch { /* engine may be mid-restart */ }
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

  // Auto-scroll to newest when following.
  useEffect(() => {
    if (follow && bodyRef.current) bodyRef.current.scrollTop = bodyRef.current.scrollHeight
  }, [filtered, follow])

  const copyAll = () => copyText(filtered.map(fmtLine).join('\n'))
  const doExport = async () => {
    setExportMsg('')
    try {
      const path = await ExportDiagnostics()
      if (path) setExportMsg(`Saved to ${path}`)
    } catch (e) { setExportMsg(String(e)) }
  }

  return (
    <div className="pf-stagger space-y-3">
      <PageHeader
        title="Activity"
        subtitle="A live tail of the engine's log."
        action={<Button variant="ghost" size="sm" onClick={doExport}><IconExternal size={14} /> Export diagnostics</Button>}
      />

      <div className="flex flex-wrap items-center gap-2">
        <div className="w-36"><Select value={level} onChange={setLevel} options={[
          {value: 'all', label: 'All levels'},
          ...LEVELS.map(l => ({value: l, label: l[0].toUpperCase() + l.slice(1) + '+'})),
        ]} /></div>
        <div className="min-w-40 flex-1"><TextInput value={query} onChange={setQuery} placeholder="Filter messages…" /></div>
        <Checkbox checked={follow} onChange={setFollow} label="Follow" />
        <Button variant="ghost" size="sm" onClick={copyAll}>Copy</Button>
      </div>

      {exportMsg && <div className="pf-fade select-text text-xs text-[var(--text-3)]">{exportMsg}</div>}

      <Card pad={false} className="overflow-hidden">
        <div className="flex items-center justify-between border-b border-[var(--border)] px-3 py-2 text-xs text-[var(--text-3)]">
          <span>Showing {filtered.length} of {entries.length} lines</span>
          <Badge tone="neutral">ring · {CAP} max</Badge>
        </div>
        <div ref={bodyRef} className="pf-well-flush h-[calc(100vh-21rem)] select-text overflow-y-auto px-3 py-2 font-mono text-[12px] leading-relaxed">
          {filtered.length === 0
            ? (attached && entries.length === 0
              ? <EmptyState icon={<IconTerminal size={26} />} title="The service keeps its own logs"
                  hint="This window views a background service, which logs to its own files. Export diagnostics to collect them." />
              : <EmptyState icon={<IconLogs size={26} />} title="No log lines"
                  hint="Activity streams here as the engine runs." />)
            : filtered.map(e => <LogLine key={e.seq} e={e} />)}
        </div>
      </Card>
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
        className="w-12 shrink-0 rounded-[var(--r-xs)] px-1 text-center text-[10px] font-bold uppercase leading-4"
        style={{color: c, background: `color-mix(in srgb, ${c} 12%, transparent)`}}
      >{e.level}</span>
      <span className="min-w-0 flex-1 text-[var(--text)]">
        {e.msg}
        {e.attrs && <span className="text-[var(--text-3)]"> {e.attrs}</span>}
      </span>
    </div>
  )
}

function fmtLine(e: Entry): string {
  return `${new Date(e.timeMs).toISOString()} ${e.level.toUpperCase().padEnd(5)} ${e.msg} ${e.attrs}`.trimEnd()
}
