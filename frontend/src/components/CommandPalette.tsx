import {useEffect, useMemo, useRef, useState} from 'react'
import {Command, CommandCtx, COMMANDS, fuzzyScore} from '../commands'
import {IconSearch} from './icons'
import {Kbd} from './ui'

/**
 * CommandPalette (Ctrl+K): a glass overlay for keyboard-first navigation and
 * actions. Commands gate themselves by role/mode; ranking is fuzzy. Exits
 * animate via [data-closing] before unmount.
 */
export function CommandPalette({ctx, onClose}: {ctx: CommandCtx; onClose: () => void}) {
  const [q, setQ] = useState('')
  const [idx, setIdx] = useState(0)
  const [closing, setClosing] = useState(false)
  const listRef = useRef<HTMLDivElement>(null)
  const closeRef = useRef(onClose)
  closeRef.current = onClose

  const beginClose = () => {
    setClosing(true)
    window.setTimeout(() => closeRef.current(), 150)
  }

  const available = useMemo(() => COMMANDS.filter(c => !c.when || c.when(ctx)), [ctx])
  const results = useMemo(() => {
    if (!q.trim()) return available
    return available
      .map(c => ({c, s: fuzzyScore(`${c.title} ${c.hint ?? ''}`, q)}))
      .filter(x => x.s >= 0)
      .sort((a, b) => b.s - a.s)
      .map(x => x.c)
  }, [available, q])

  useEffect(() => setIdx(0), [q])
  useEffect(() => {
    listRef.current?.querySelector('[data-active="true"]')?.scrollIntoView({block: 'nearest'})
  }, [idx])

  const run = (c: Command) => {
    beginClose()
    Promise.resolve(c.run(ctx)).catch(() => {})
  }

  const onKey = (e: React.KeyboardEvent) => {
    if (e.key === 'ArrowDown') { e.preventDefault(); setIdx(i => Math.min(results.length - 1, i + 1)) }
    else if (e.key === 'ArrowUp') { e.preventDefault(); setIdx(i => Math.max(0, i - 1)) }
    else if (e.key === 'Enter') { e.preventDefault(); if (results[idx]) run(results[idx]) }
    else if (e.key === 'Escape') { e.preventDefault(); beginClose() }
  }

  // Group into sections when browsing; flat ranked list while searching.
  const grouped = useMemo(() => {
    if (q.trim()) return null
    const bySection = new Map<string, Command[]>()
    for (const c of results) {
      const list = bySection.get(c.section) ?? []
      list.push(c)
      bySection.set(c.section, list)
    }
    return [...bySection.entries()]
  }, [results, q])

  let flatIndex = -1

  const renderItem = (c: Command) => {
    flatIndex++
    const i = flatIndex
    const active = i === idx
    return (
      <button
        key={c.id}
        data-active={active || undefined}
        onMouseEnter={() => setIdx(i)}
        onClick={() => run(c)}
        className={`flex w-full items-center gap-2.5 rounded-[var(--r-sm)] px-2.5 py-2 text-left text-sm transition-colors duration-100 ${
          active ? 'bg-[color-mix(in_srgb,var(--accent)_14%,transparent)] text-[var(--text)]' : 'text-[var(--text-2)]'
        }`}
      >
        <span className={`shrink-0 ${active ? 'text-[var(--accent)]' : 'text-[var(--text-3)]'}`}>{c.icon}</span>
        <span className="min-w-0 flex-1 truncate">{c.title}</span>
        {c.hint && <span className="hidden truncate text-xs text-[var(--text-3)] sm:block">{c.hint}</span>}
        {c.kbd && <Kbd>{c.kbd}</Kbd>}
      </button>
    )
  }

  return (
    <div
      className="fixed inset-0 z-50 flex items-start justify-center px-6 pt-[14vh]"
      data-closing={closing || undefined}
      onMouseDown={beginClose}
    >
      <div className="pf-scrim absolute inset-0 bg-black/40" />
      <div
        onMouseDown={e => e.stopPropagation()}
        className="pf-drop pf-glass pf-fringe relative w-full max-w-[560px] overflow-hidden rounded-[var(--r-xl)] will-change-[transform,opacity]"
      >
        <div className="flex items-center gap-2.5 border-b border-[var(--border)] px-4 py-3">
          <span className="shrink-0 text-[var(--text-3)]"><IconSearch size={16} /></span>
          <input
            autoFocus
            value={q}
            onChange={e => setQ(e.target.value)}
            onKeyDown={onKey}
            placeholder="Type a command or search…"
            className="min-w-0 flex-1 bg-transparent text-sm text-[var(--text)] outline-none placeholder:text-[var(--text-3)]"
          />
          <Kbd>Esc</Kbd>
        </div>
        <div ref={listRef} className="max-h-[46vh] overflow-y-auto p-1.5">
          {results.length === 0 && (
            <div className="px-3 py-8 text-center text-sm text-[var(--text-3)]">Nothing matches "{q}".</div>
          )}
          {grouped
            ? grouped.map(([section, cmds]) => (
                <div key={section}>
                  <div className="px-2.5 pb-1 pt-2.5 text-[10.5px] font-semibold uppercase tracking-wider text-[var(--text-3)]">{section}</div>
                  {cmds.map(renderItem)}
                </div>
              ))
            : results.map(renderItem)}
        </div>
        <div className="flex items-center gap-3 border-t border-[var(--border)] px-4 py-2 text-[11px] text-[var(--text-3)]">
          <span className="flex items-center gap-1"><Kbd>↑ ↓</Kbd> navigate</span>
          <span className="flex items-center gap-1"><Kbd>Enter</Kbd> run</span>
        </div>
      </div>
    </div>
  )
}
