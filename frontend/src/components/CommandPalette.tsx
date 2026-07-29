import {useEffect, useId, useMemo, useRef, useState} from 'react'
import {createPortal} from 'react-dom'
import {Command, CommandCtx, COMMANDS, fuzzyScore, navCommands} from '../commands'
import {IconSearch} from './icons'
import {Kbd, Overline, useDialog} from './ui'

/**
 * CommandPalette (Ctrl+K): a glass overlay for keyboard-first navigation and
 * actions. Commands gate themselves by role/mode; ranking is fuzzy. Exits
 * animate via [data-closing] before unmount.
 *
 * It keeps its own chrome rather than wrapping `Modal`: it is a different
 * surface — hung 14vh from the top like a lure, dropping in on pf-drop instead
 * of blooming up on pf-pop, and headed by its own input rather than a title
 * bar. What it does NOT fork is dialog behaviour: `useDialog` (ui.tsx) owns the
 * role, the focus trap, initial focus, the restore and Escape for both.
 *
 * It portals for the same reason Modal and Select do. Nothing in Shell's grid
 * carries a backdrop-filter today, which is the only reason `fixed inset-0`
 * ever measured the viewport from in there — one .pf-* recipe applied to an
 * ancestor and the containing block becomes that ancestor instead.
 *
 * Keyboard model (the input keeps DOM focus throughout; results are a listbox
 * addressed by aria-activedescendant, never tab stops): ↑/↓ move the active
 * option, Enter runs it, Escape closes. Keys are handled on the panel, not the
 * input, so a stray click inside the palette cannot disarm them.
 */
export function CommandPalette({ctx, onClose}: {ctx: CommandCtx; onClose: () => void}) {
  const [q, setQ] = useState('')
  const [idx, setIdx] = useState(0)
  const [closing, setClosing] = useState(false)
  const listRef = useRef<HTMLDivElement>(null)
  const closeRef = useRef(onClose)
  closeRef.current = onClose
  const closingRef = useRef(false)

  // Latched: Escape and a backdrop release can both arrive, and two exit timers
  // would call onClose twice.
  const beginClose = () => {
    if (closingRef.current) return
    closingRef.current = true
    setClosing(true)
    window.setTimeout(() => closeRef.current(), 150)
  }

  // Explicit, not autoFocus: the palette is useless if the first keystroke
  // lands anywhere but the query field, and autoFocus fires exactly once.
  const inputRef = useRef<HTMLInputElement>(null)
  const {panelRef, dialogProps, backdropProps} = useDialog({
    onDismiss: beginClose, label: 'command palette', initialFocus: inputRef,
  })
  const uid = useId()
  const listId = `${uid}-results`
  const optionId = (i: number) => `${uid}-o${i}`

  // Navigate commands are role-derived (the gateway's Agents entry); prepend
  // them so the palette lists exactly the screens the current role can reach.
  const all = useMemo(() => [...navCommands(ctx.status.role), ...COMMANDS], [ctx.status.role])
  const available = useMemo(() => all.filter(c => !c.when || c.when(ctx)), [all, ctx])
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
        id={optionId(i)}
        role="option"
        aria-selected={active}
        // Not a tab stop: the input holds focus and points here with
        // aria-activedescendant. Making these tabbable would put Enter on two
        // owners at once — the focused button and the active option.
        tabIndex={-1}
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

  const empty = results.length === 0

  return createPortal(
    <div
      className="fixed inset-0 z-50 flex items-start justify-center px-6 pt-[14vh]"
      data-closing={closing || undefined}
      {...backdropProps}
    >
      <div className="pf-scrim absolute inset-0 bg-black/40" />
      <div
        ref={panelRef}
        {...dialogProps}
        onKeyDown={onKey}
        className="pf-drop pf-glass pf-fringe relative w-full max-w-[560px] overflow-hidden rounded-[var(--r-bloom)] outline-none will-change-[transform,opacity]"
      >
        <div className="flex items-center gap-2.5 border-b border-[var(--border)] px-4 py-3">
          <span className="shrink-0 text-[var(--text-3)]"><IconSearch size={16} /></span>
          <input
            ref={inputRef}
            value={q}
            onChange={e => setQ(e.target.value)}
            role="combobox"
            aria-label="search commands"
            aria-controls={listId}
            aria-expanded={!empty}
            aria-autocomplete="list"
            aria-activedescendant={empty ? undefined : optionId(idx)}
            placeholder="Type a command or search…"
            className="min-w-0 flex-1 bg-transparent text-sm text-[var(--text)] outline-none placeholder:text-[var(--text-3)]"
          />
          <Kbd>Esc</Kbd>
        </div>
        <div ref={listRef} className="max-h-[46vh] overflow-y-auto overscroll-y-contain p-1.5">
          <div data-band-content>
            {/* Mounted whether or not it has anything to say: a live region
                inserted along with its text is often missed, one whose text
                changes never is. */}
            <div role="status" aria-live="polite">
              {empty && (
                <div className="px-3 py-8 text-center text-sm text-[var(--text-3)]">Nothing matches "{q}".</div>
              )}
            </div>
            <div id={listId} role="listbox" aria-label="commands">
              {grouped
                ? grouped.map(([section, cmds]) => (
                    <div key={section} role="group" aria-label={section}>
                      {/* The group carries the section name; the printed one
                          would only say it twice. */}
                      <div aria-hidden><Overline className="px-2.5 pb-1 pt-2.5">{section}</Overline></div>
                      {cmds.map(renderItem)}
                    </div>
                  ))
                : results.map(renderItem)}
            </div>
          </div>
        </div>
        <div className="flex items-center gap-3 border-t border-[var(--border)] px-4 py-2 text-sub text-[var(--text-3)]">
          <span className="flex items-center gap-1"><Kbd>↑ ↓</Kbd> navigate</span>
          <span className="flex items-center gap-1"><Kbd>Enter</Kbd> run</span>
        </div>
      </div>
    </div>,
    document.body
  )
}
