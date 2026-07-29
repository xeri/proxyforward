import {ReactNode, useEffect, useId, useLayoutEffect, useRef, useState} from 'react'
import {createPortal} from 'react-dom'
import {ClipboardSetText} from '../../wailsjs/runtime/runtime'
import {useMotion} from '../motion'
import {IconAlert, IconCheck, IconChevronDown, IconCopy, IconEye, IconEyeOff, IconInfo, IconClose} from './icons'

type State = 'good' | 'warn' | 'bad' | 'unknown'

/** PageHeader: the masthead — an accent eyebrow murmuring the screen's theme,
 * one hero-size display title, a mono subtitle. One per screen, nothing
 * larger above the fold. The generous bottom margin is deliberate — silence
 * before the content. `truncate` on the h1 because 42px meets narrow windows
 * (and the Agents detail masthead is a hostname of arbitrary length). */
export function PageHeader({eyebrow, title, subtitle, action}: {
  eyebrow?: string; title: string; subtitle?: ReactNode; action?: ReactNode
}) {
  return (
    <div className="mb-8 flex items-end justify-between gap-4">
      <div className="min-w-0">
        {eyebrow && <Overline variant="eyebrow" className="mb-1.5">{eyebrow}</Overline>}
        <h1 className="truncate font-display text-hero font-bold leading-[var(--lh-tight)]">{title}</h1>
        {subtitle && <p className="mt-1.5 text-body text-[var(--text-2)]">{subtitle}</p>}
      </div>
      {action && <div className="shrink-0">{action}</div>}
    </div>
  )
}

/** Overline: the shared label recipe — caption-size, lowercase, tracked,
 * muted. Labels recede; values dominate. Lowercase is the app's register:
 * the instrument murmurs its labels, it does not bark them.
 * `variant="eyebrow"` is the louder cousin: wider-tracked and accent-inked,
 * reserved for mastheads and act numbers — never row labels. */
export function Overline({children, variant = 'label', className = ''}: {
  children: ReactNode; variant?: 'label' | 'eyebrow'; className?: string
}) {
  const cls = variant === 'eyebrow'
    ? 'text-caption font-medium lowercase tracking-[var(--tracking-eyebrow)] text-[var(--accent)]'
    : 'text-caption font-semibold lowercase tracking-[var(--tracking-label)] text-[var(--text-3)]'
  return <div className={`${cls} ${className}`}>{children}</div>
}

/** PanelTitle: the one panel-heading recipe (Cormorant, --fs-title). Loud
 * (700, full ink) when the panel is the page's subject; quiet
 * (--w-display-quiet, receded ink) when it serves. Hierarchy comes from the
 * casting, never from inventing a new size. */
export function PanelTitle({children, loud = false, className = ''}: {
  children: ReactNode; loud?: boolean; className?: string
}) {
  return (
    <h2 className={`font-display text-title ${
      loud ? 'font-bold text-[var(--text)]' : '[font-weight:var(--w-display-quiet)] text-[var(--text-2)]'
    } ${className}`}>{children}</h2>
  )
}

/** Card: the quiet tier-2 panel (glass.css .pf-card). It recedes — so its
 * `title` is the quiet casting by default; pass `loud` only when the panel is
 * the page's subject. Most quiet cards should carry `label` instead, a
 * lowercase overline that lets the content dominate. */
export function Card({title, loud, label, subtitle, action, children, className = '', pad = true}: {
  title?: ReactNode; loud?: boolean; label?: ReactNode; subtitle?: string; action?: ReactNode; children: ReactNode; className?: string; pad?: boolean
}) {
  return (
    <div className={`pf-card ${className}`}>
      {(title || label || action) && (
        <div className={`relative flex items-center justify-between gap-3 ${pad ? 'px-5 pt-4' : 'p-5 pb-4'}`}>
          <div className="min-w-0">
            {title && <PanelTitle loud={loud}>{title}</PanelTitle>}
            {!title && label && <Overline>{label}</Overline>}
            {subtitle && <p className="mt-0.5 text-xs text-[var(--text-2)]">{subtitle}</p>}
          </div>
          {action}
        </div>
      )}
      <div className={`relative ${pad ? 'p-5 pt-4' : ''}`}>{children}</div>
    </div>
  )
}

/** SignalCard: the identity surface — Signal Glass (glass.css .pf-signal).
 * One per screen, only on surfaces that represent live network activity.
 * Carries the caustic layer quiet cards gave up; the Shell lamp wakes it. */
export function SignalCard({title, subtitle, action, children, className = '', pad = true}: {
  title?: ReactNode; subtitle?: string; action?: ReactNode; children: ReactNode; className?: string; pad?: boolean
}) {
  return (
    <div className={`pf-signal ${className}`}>
      <span aria-hidden className="pf-caustic" />
      {(title || action) && (
        <div className={`relative flex items-center justify-between gap-3 ${pad ? 'px-5 pt-4' : 'p-5 pb-4'}`}>
          <div className="min-w-0">
            {title && <PanelTitle loud>{title}</PanelTitle>}
            {subtitle && <p className="mt-0.5 text-xs text-[var(--text-2)]">{subtitle}</p>}
          </div>
          {action}
        </div>
      )}
      <div className={`relative ${pad ? 'p-5 pt-4' : ''}`}>{children}</div>
    </div>
  )
}

/** StatTile: a tier-3 headline metric — type on whitespace, no container.
 * The hairline left rule gives metric groups a rhythm without boxes.
 * `value` takes a ReactNode so callers pass a NumberTicker when the numeral
 * should glide. `tone` tints the numeral with a status color; `accent` with
 * the mode accent. `hero` is the one --fs-metric-hero figure a page may
 * carry; `sm` is for dense 3-up-and-tighter grids that would clip 26px. */
export function StatTile({label, value, sub, accent, tone, size = 'md'}: {
  label: string
  value: ReactNode
  sub?: string
  accent?: boolean
  /** `unknown` dims the numeral to silt — a sentinel is never painted healthy. */
  tone?: 'good' | 'warn' | 'bad' | 'unknown'
  size?: 'sm' | 'md' | 'hero'
}) {
  const color = tone === 'unknown' ? 'var(--text-3)' : tone ? `var(--${tone})` : accent ? 'var(--accent)' : undefined
  const fs = size === 'hero' ? 'text-metric-hero' : size === 'sm' ? 'text-metric-sm' : 'text-metric'
  return (
    <div className="min-w-0 border-l border-[var(--hairline)] py-0.5 pl-4">
      <Overline>{label}</Overline>
      <div
        className={`font-display mt-1 font-bold leading-[var(--lh-tight)] tabular-nums ${fs}`}
        style={color ? {color} : undefined}
      >{value}</div>
      {sub && <div className="mt-1 truncate text-caption tabular-nums text-[var(--text-3)]" title={sub}>{sub}</div>}
    </div>
  )
}

/** Pill: a small toggleable filter chip (tunnel/country filters, list lenses).
 * The one shared treatment — screens must not fork their own copies. */
export function Pill({on, onClick, children}: {on: boolean; onClick: () => void; children: ReactNode}) {
  return (
    <button
      type="button" onClick={onClick} aria-pressed={on}
      className={`pf-press rounded-[var(--r-chip)] border px-2.5 py-1 text-xs transition-all duration-150 ${
        on
          ? 'border-[color-mix(in_srgb,var(--accent)_45%,transparent)] bg-[color-mix(in_srgb,var(--accent)_14%,transparent)] font-medium text-[var(--text)]'
          : 'border-[var(--border)] text-[var(--text-3)] hover:text-[var(--text)]'
      }`}
    >{children}</button>
  )
}

/** PillGroup: an exclusive Pill row where the active state is a traveling
 * highlight that glides between chips instead of teleporting. Pills share a
 * fixed height so mixed content (dots, flags, plain text) stays aligned.
 * Buttons keep transparent borders for identical geometry; the indicator
 * carries the border + tint. */
export function PillGroup<T extends string>({value, onChange, options}: {
  value: T
  onChange: (v: T) => void
  options: {value: T; label: ReactNode}[]
}) {
  const ref = useRef<HTMLDivElement>(null)
  const [ind, setInd] = useState<{x: number; y: number; w: number; h: number} | null>(null)
  // Transitions arm one frame after mount so the first measurement lands
  // without the indicator flying in from the origin.
  const [armed, setArmed] = useState(false)
  useEffect(() => { setArmed(true) }, [])

  const optionsKey = options.map(o => o.value).join(',')
  useLayoutEffect(() => {
    const btn = ref.current?.querySelector<HTMLElement>(`[data-pill="${CSS.escape(value)}"]`)
    if (btn) setInd({x: btn.offsetLeft, y: btn.offsetTop, w: btn.offsetWidth, h: btn.offsetHeight})
  }, [value, optionsKey])

  return (
    <div ref={ref} className="relative inline-flex flex-wrap items-center gap-1.5" role="radiogroup">
      <div
        aria-hidden
        className={`pf-control pointer-events-none absolute left-0 top-0 rounded-[var(--r-chip)] bg-[color-mix(in_srgb,var(--accent)_14%,transparent)] [--control-edge:color-mix(in_srgb,var(--accent)_45%,transparent)] [--control-rim-o:1] ${
          armed ? 'transition-[transform,width,height] duration-[var(--dur-slow)] [transition-timing-function:var(--ease-spring)]' : ''
        }`}
        style={{
          width: ind?.w ?? 0, height: ind?.h ?? 0,
          transform: `translate(${ind?.x ?? 0}px, ${ind?.y ?? 0}px)`,
          opacity: ind ? 1 : 0,
        }}
      />
      {options.map(o => {
        const on = o.value === value
        return (
          <button
            key={o.value} type="button" role="radio" aria-checked={on} data-pill={o.value}
            onClick={() => onChange(o.value)}
            className={`pf-press relative z-10 inline-flex h-[26px] items-center gap-1.5 whitespace-nowrap rounded-[var(--r-chip)] border border-transparent px-2.5 text-xs transition-colors duration-150 ${
              on ? 'font-medium text-[var(--text)]' : 'text-[var(--text-3)] hover:text-[var(--text)]'
            }`}
          >{o.label}</button>
        )
      })}
    </div>
  )
}

/** RoleWord: the name of a role as an outlined glass token — cyan for the
 * agent, violet for the gateway — so role mentions read at a glance in prose,
 * pills, and labels. */
export function RoleWord({role, children}: {role: 'agent' | 'gateway'; children?: ReactNode}) {
  return (
    <span
      className="pf-role-chip"
      style={{['--role-c' as string]: role === 'agent' ? 'var(--role-agent)' : 'var(--role-gateway)'}}
    >{children ?? role}</span>
  )
}

/** LiveDot: a small breathing dot that marks a surface as live and
 * self-updating (charts, live tables). Breathing gates on data-motion.
 *
 * The gap is --halo-gap, not a Tailwind rem: .pf-halo breathes a 5px ring out
 * of the dot, and gap-1.5 is ~5.06px at this root — the ring was landing on the
 * word "live". Anything haloed keeps that much air (tokens.css). */
export function LiveDot() {
  return (
    <span className="inline-flex items-center gap-[var(--halo-gap)] text-micro lowercase tracking-[var(--tracking-label)] text-[var(--text-3)]">
      <span className="inline-flex h-2 w-2 rounded-full pf-halo" style={{background: 'var(--good)', ['--halo' as string]: 'var(--good)'}} />
      live
    </span>
  )
}

/** SectionHeader: the mono emphasis tier (13/600) for in-card row groups —
 * the working voice raised, not the display voice borrowed. Use it where a
 * Cormorant title would be ceremony a list doesn't deserve. `count` renders
 * quietly beside the title; hints take the annotation italic. */
export function SectionHeader({title, hint, count, action}: {
  title: string; hint?: string; count?: ReactNode; action?: ReactNode
}) {
  return (
    <div className="flex items-center justify-between gap-3">
      <div className="min-w-0">
        <div className="flex items-center gap-2 text-emphasis font-semibold text-[var(--text)]">
          {title}
          {count !== undefined && <span className="font-normal tabular-nums text-[var(--text-3)]">{count}</span>}
        </div>
        {hint && <div className="mt-0.5 text-xs italic text-[var(--text-3)]">{hint}</div>}
      </div>
      {action && <div className="shrink-0">{action}</div>}
    </div>
  )
}

export function Button({children, onClick, variant = 'primary', size = 'md', disabled, className = '', title}: {
  children: ReactNode
  onClick?: () => void
  variant?: 'primary' | 'ghost' | 'danger' | 'subtle'
  size?: 'sm' | 'md'
  disabled?: boolean
  className?: string
  title?: string
}) {
  // The catch-light is --btn-lip, a layer of .pf-btn's rim ring — never an
  // `inset 0 1px 0` here: this element already wears the ring, and a crisp
  // inset bevel on top of it specks at the corners (glass.css, the rim
  // primitive). The hot variant just turns the lip up. Blurred glows stay
  // box-shadows; they have no crisp end.
  const styles = {
    primary: 'pf-btn-hot [--btn-lip:0.28] bg-[var(--btn-accent-fill)] text-[var(--accent-contrast)] shadow-[0_2px_12px_-2px_color-mix(in_srgb,var(--accent)_45%,transparent)] hover:bg-[var(--btn-accent-fill-hover)] hover:shadow-[0_4px_20px_-2px_color-mix(in_srgb,var(--accent)_60%,transparent)] disabled:opacity-50 disabled:shadow-none',
    ghost: 'border border-[color-mix(in_srgb,var(--text)_14%,transparent)] bg-transparent text-[var(--text)] hover:bg-[var(--btn-bg)] hover:border-[color-mix(in_srgb,var(--text)_24%,transparent)] disabled:opacity-50',
    subtle: 'bg-[var(--btn-bg)] text-[var(--text)] hover:bg-[var(--btn-bg-hover)] disabled:opacity-50',
    danger: 'border border-[color-mix(in_srgb,var(--bad)_55%,var(--border))] bg-transparent text-[var(--bad)] hover:bg-[var(--bad)] hover:border-[var(--bad)] hover:text-white disabled:opacity-50',
  }[variant]
  const sz = size === 'sm' ? 'px-2.5 py-1 text-xs' : 'px-3.5 py-2 text-sm'
  return (
    <button
      title={title}
      className={`pf-press pf-btn relative inline-flex items-center justify-center gap-1.5 rounded-[var(--r-btn)] font-medium transition-[background-color,border-color,box-shadow,color,opacity] duration-200 ${sz} ${styles} ${className}`}
      onClick={onClick} disabled={disabled}
    >{children}</button>
  )
}

export function IconButton({children, onClick, title, variant = 'ghost', disabled}: {
  children: ReactNode; onClick?: () => void; title: string; variant?: 'ghost' | 'danger'; disabled?: boolean
}) {
  const styles = variant === 'danger'
    ? 'text-[var(--text-3)] hover:text-[var(--bad)] hover:bg-[color-mix(in_srgb,var(--bad)_12%,transparent)]'
    : 'text-[var(--text-3)] hover:text-[var(--text)] hover:bg-[var(--panel-2)]'
  return (
    <button title={title} aria-label={title} onClick={onClick} disabled={disabled}
      className={`inline-flex h-8 w-8 items-center justify-center rounded-[var(--r-btn)] transition-all duration-200 active:scale-90 disabled:opacity-40 ${styles}`}>
      {children}
    </button>
  )
}

/** Pager: the "x–y of z · Previous/Next" row beneath a paged list — the player
 * wall and the connection history render the same one. The caller owns the
 * offset (it is usually the query key too); Pager draws nothing when one page
 * holds everything, so no consumer needs its own "can page" conditional unless
 * it suppresses paging for another reason. */
export function Pager({offset, pageSize, total, onOffset, className = ''}: {
  offset: number
  pageSize: number
  total: number
  onOffset: (next: number) => void
  /** Padding, for a pager sitting inside a padless card. */
  className?: string
}) {
  if (total <= pageSize) return null
  return (
    <div className={`flex items-center justify-between text-xs text-[var(--text-3)] ${className}`}>
      <span className="tabular-nums">{offset + 1}–{Math.min(offset + pageSize, total)} of {total}</span>
      <div className="flex gap-1.5">
        <Button variant="ghost" size="sm" disabled={offset === 0}
          onClick={() => onOffset(Math.max(0, offset - pageSize))}>Previous</Button>
        <Button variant="ghost" size="sm" disabled={offset + pageSize >= total}
          onClick={() => onOffset(offset + pageSize)}>Next</Button>
      </div>
    </div>
  )
}

export function Field({label, hint, children}: {label: string; hint?: ReactNode; children: ReactNode}) {
  return (
    <label className="block">
      <div className="mb-1.5 text-sm font-medium text-[var(--text)]">{label}</div>
      {children}
      {hint && <div className="mt-1.5 text-xs italic leading-relaxed text-[var(--text-3)]">{hint}</div>}
    </label>
  )
}

/** TextInput: the one text field. The WRAPPER wears .pf-control (an <input>
 * cannot host a pseudo-element, so the rim ring has nowhere to live on the
 * input itself) and :has() carries focus outward to it; the input is a
 * transparent pane inside. The pressed-in seat stays a blurred inset shadow —
 * blurred shadows have no crisp end and cannot speck (glass.css).
 *
 * `size="sm"` + `icon` is the compact form for list toolbars. Screens must not
 * hand-roll their own search box beside this one. */
export function TextInput({value, onChange, placeholder, type = 'text', mono, onEnter, autoFocus, size = 'md', icon, ariaLabel, disabled}: {
  value: string; onChange: (v: string) => void; placeholder?: string; type?: string; mono?: boolean
  onEnter?: () => void; autoFocus?: boolean; size?: 'sm' | 'md'; icon?: ReactNode; ariaLabel?: string
  disabled?: boolean
}) {
  const isPassword = type === 'password'
  const [reveal, setReveal] = useState(false)
  const effectiveType = isPassword && reveal ? 'text' : type
  const sm = size === 'sm'
  return (
    <div
      className={`pf-control relative flex items-center rounded-[var(--r-input)] bg-[var(--input-bg)] shadow-[inset_0_2px_4px_-1px_var(--bevel-bot)] transition-[background-color,box-shadow] duration-200 ${
        disabled ? 'pointer-events-none opacity-50' : 'hover:bg-[var(--input-bg-hover)] has-[:focus]:bg-[var(--input-bg-hover)] has-[:focus]:shadow-[inset_0_2px_4px_-1px_var(--bevel-bot),0_0_0_3px_color-mix(in_srgb,var(--accent)_22%,transparent),0_0_18px_-4px_color-mix(in_srgb,var(--accent)_40%,transparent)]'
      } ${sm ? 'h-8' : 'h-[var(--control-h)]'}`}
    >
      {icon && (
        <span className="pointer-events-none absolute left-2.5 flex text-[var(--text-3)]">{icon}</span>
      )}
      <input
        type={effectiveType} value={value} placeholder={placeholder} autoFocus={autoFocus} aria-label={ariaLabel}
        disabled={disabled}
        onChange={e => onChange(e.target.value)}
        onKeyDown={e => { if (e.key === 'Enter' && onEnter) onEnter() }}
        className={`h-full w-full min-w-0 rounded-[inherit] bg-transparent text-[var(--text)] outline-none placeholder:italic placeholder:text-[var(--text-3)] ${
          icon ? 'pl-8' : sm ? 'pl-2.5' : 'pl-3'
        } ${isPassword ? 'pr-10' : sm ? 'pr-2.5' : 'pr-3'} ${sm ? 'text-xs' : 'text-sm'} ${mono ? 'font-mono text-body' : ''}`}
      />
      {isPassword && (
        <button
          type="button" tabIndex={-1}
          onClick={() => setReveal(r => !r)}
          aria-label={reveal ? 'Hide' : 'Show'} title={reveal ? 'Hide' : 'Show'}
          className="absolute inset-y-0 right-0 flex w-9 items-center justify-center rounded-r-[var(--r-md)] text-[var(--text-3)] transition-colors duration-150 hover:text-[var(--text)]"
        >
          {reveal ? <IconEyeOff size={16} /> : <IconEye size={16} />}
        </button>
      )}
    </div>
  )
}

/** Checkbox: a custom, theme-aware checkbox (the native one does not match the
 * app and renders inconsistently in the WebView). */
export function Checkbox({checked, onChange, label}: {
  checked: boolean; onChange: (v: boolean) => void; label?: ReactNode
}) {
  return (
    <button
      type="button" role="checkbox" aria-checked={checked}
      onClick={() => onChange(!checked)}
      className="inline-flex items-center gap-1.5 text-xs text-[var(--text-2)] transition-colors hover:text-[var(--text)]"
    >
      <span
        className={`pf-control relative grid h-4 w-4 place-items-center rounded-[var(--r-xs)] transition-colors duration-150 ${
          checked
            ? 'bg-[var(--accent)] text-[var(--accent-contrast)] [--control-edge:var(--accent)] [--control-rim-o:1]'
            : 'bg-[var(--input-bg)] text-transparent'
        }`}
      >
        <IconCheck size={12} />
      </span>
      {label}
    </button>
  )
}

/** Select: a custom dropdown (the native <select>'s popup is unstyled and
 * OS-native — this matches the app in both themes). Keyboard: Enter/Space/↓
 * opens, Esc closes, click-outside closes.
 *
 * The list renders through a portal on opaque menu glass (pf-menu): every
 * card is a stacking context (backdrop-filter), so an in-card absolute menu
 * paints underneath the next card no matter its z-index — and floating
 * options need a near-solid fill to stay legible over whatever they cover.
 * It anchors to the trigger, tracks scroll/resize, and flips upward when the
 * viewport below is tight. */
export function Select({value, onChange, options, disabled}: {
  value: string; onChange: (v: string) => void; options: {value: string; label: string}[]; disabled?: boolean
}) {
  const [open, setOpen] = useState(false)
  const btnRef = useRef<HTMLButtonElement>(null)
  const listRef = useRef<HTMLDivElement>(null)
  const [anchor, setAnchor] = useState<{top: number; bottom: number; left: number; width: number; up: boolean} | null>(null)
  const current = options.find(o => o.value === value)

  const place = () => {
    const r = btnRef.current?.getBoundingClientRect()
    if (!r) return
    // max-h-60 ≈ 203px + margin; flip up only when below is short AND above fits better.
    const below = window.innerHeight - r.bottom
    const up = below < 220 && r.top > below
    setAnchor({top: r.top, bottom: r.bottom, left: r.left, width: r.width, up})
  }

  const toggle = () => {
    if (open) { setOpen(false); return }
    place()
    setOpen(true)
  }

  useEffect(() => {
    if (!open) return
    const onDoc = (e: MouseEvent) => {
      const t = e.target as Node
      if (!btnRef.current?.contains(t) && !listRef.current?.contains(t)) setOpen(false)
    }
    // stopPropagation so an enclosing Modal (window listener, bubbles later)
    // doesn't close alongside the menu.
    const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape') { e.stopPropagation(); setOpen(false) } }
    const onMove = () => place()
    document.addEventListener('mousedown', onDoc)
    document.addEventListener('keydown', onKey)
    window.addEventListener('resize', onMove)
    document.addEventListener('scroll', onMove, true)
    return () => {
      document.removeEventListener('mousedown', onDoc)
      document.removeEventListener('keydown', onKey)
      window.removeEventListener('resize', onMove)
      document.removeEventListener('scroll', onMove, true)
    }
  }, [open])

  return (
    <>
      <button
        ref={btnRef}
        type="button" onClick={toggle} disabled={disabled} aria-haspopup="listbox" aria-expanded={open}
        data-open={open}
        className={`pf-control relative flex h-[var(--control-h)] w-full items-center justify-between gap-2 rounded-[var(--r-input)] px-3 text-left text-sm text-[var(--text)] outline-none transition-[background-color,box-shadow] duration-200 hover:bg-[var(--input-bg-hover)] disabled:opacity-50 disabled:pointer-events-none ${
          open
            ? 'bg-[var(--input-bg-hover)] shadow-[inset_0_2px_4px_-1px_var(--bevel-bot),0_0_0_3px_color-mix(in_srgb,var(--accent)_22%,transparent),0_0_18px_-4px_color-mix(in_srgb,var(--accent)_40%,transparent)]'
            : 'bg-[var(--input-bg)] shadow-[inset_0_2px_4px_-1px_var(--bevel-bot)]'
        }`}
      >
        <span className="min-w-0 truncate">{current ? current.label : value}</span>
        <span className={`shrink-0 text-[var(--text-3)] transition-transform duration-200 ${open ? 'rotate-180' : ''}`}><IconChevronDown size={16} /></span>
      </button>
      {open && anchor && createPortal(
        <div
          ref={listRef}
          role="listbox"
          className="pf-fade pf-menu fixed z-[60] max-h-60 overflow-auto overscroll-y-contain rounded-[var(--r-btn)] p-1"
          style={{
            left: anchor.left,
            width: anchor.width,
            ...(anchor.up
              ? {bottom: window.innerHeight - anchor.top + 4}
              : {top: anchor.bottom + 4}),
          }}
        >
          <div data-band-content>
            {options.map(o => {
              const on = o.value === value
              return (
                <button
                  key={o.value} type="button" role="option" aria-selected={on}
                  onClick={() => { onChange(o.value); setOpen(false) }}
                  className={`flex w-full items-center justify-between gap-2 rounded-[var(--r-sm)] px-2.5 py-1.5 text-left text-sm transition-colors duration-100 ${
                    on ? 'bg-[color-mix(in_srgb,var(--accent)_16%,transparent)] text-[var(--text)]' : 'text-[var(--text-2)] hover:bg-[var(--panel-2)] hover:text-[var(--text)]'
                  }`}
                >
                  <span className="min-w-0 truncate">{o.label}</span>
                  {on && <span className="shrink-0 text-[var(--accent)]"><IconCheck size={14} /></span>}
                </button>
              )
            })}
          </div>
        </div>,
        document.body
      )}
    </>
  )
}

/** Menu: Select's popup mechanics without the value semantics — a portalled
 * float on menu glass, anchored to a trigger, tracking scroll and resize,
 * flipping up when the viewport below is tight, closing on click-outside and
 * Esc. Rows are arbitrary children (MenuItem), so they can seat an Emblem.
 *
 * It portals for the same reason Select's list does: every card, and every
 * chrome surface, is a backdrop-filter stacking context — an in-place absolute
 * float paints underneath the next one no matter its z-index. */
export function Menu({open, anchor, onClose, children, align = 'left', minWidth}: {
  open: boolean
  anchor: React.RefObject<HTMLElement | null>
  onClose: () => void
  children: ReactNode
  align?: 'left' | 'right'
  minWidth?: number
}) {
  const listRef = useRef<HTMLDivElement>(null)
  const [at, setAt] = useState<{top: number; bottom: number; left: number; right: number; up: boolean} | null>(null)

  const place = () => {
    const r = anchor.current?.getBoundingClientRect()
    if (!r) return
    const below = window.innerHeight - r.bottom
    setAt({
      top: r.top, bottom: r.bottom, left: r.left, right: window.innerWidth - r.right,
      up: below < 220 && r.top > below,
    })
  }

  // Place before paint, so the menu never flashes at the wrong anchor.
  useLayoutEffect(() => { if (open) place() }, [open])

  useEffect(() => {
    if (!open) return
    const onDoc = (e: MouseEvent) => {
      const t = e.target as Node
      if (!anchor.current?.contains(t) && !listRef.current?.contains(t)) onClose()
    }
    // stopPropagation so an enclosing Modal (window listener, bubbles later)
    // doesn't close alongside the menu.
    const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape') { e.stopPropagation(); onClose() } }
    const onMove = () => place()
    document.addEventListener('mousedown', onDoc)
    document.addEventListener('keydown', onKey)
    window.addEventListener('resize', onMove)
    document.addEventListener('scroll', onMove, true)
    return () => {
      document.removeEventListener('mousedown', onDoc)
      document.removeEventListener('keydown', onKey)
      window.removeEventListener('resize', onMove)
      document.removeEventListener('scroll', onMove, true)
    }
  }, [open, onClose])

  if (!open || !at) return null
  return createPortal(
    <div
      ref={listRef}
      role="menu"
      className="pf-fade pf-menu fixed z-[60] overflow-auto overscroll-y-contain rounded-[var(--r-btn)] p-1"
      style={{
        ...(align === 'right' ? {right: at.right} : {left: at.left}),
        minWidth,
        maxHeight: at.up ? at.top - 12 : window.innerHeight - at.bottom - 12,
        ...(at.up ? {bottom: window.innerHeight - at.top + 4} : {top: at.bottom + 4}),
      }}
    >
      <div data-band-content>{children}</div>
    </div>,
    document.body
  )
}

/** MenuItem: one row of a Menu — a lead slot (icon/Emblem), a title, a quiet
 * hint underneath, and a check when it is the current choice. */
export function MenuItem({lead, title, hint, on, disabled, onClick, onPointerEnter, onPointerLeave}: {
  lead?: ReactNode; title: ReactNode; hint?: ReactNode; on?: boolean; disabled?: boolean
  onClick?: () => void
  onPointerEnter?: () => void
  onPointerLeave?: () => void
}) {
  return (
    <button
      type="button" role="menuitem" disabled={disabled}
      onClick={onClick} onPointerEnter={onPointerEnter} onPointerLeave={onPointerLeave}
      className={`flex w-full items-center gap-2.5 rounded-[var(--r-sm)] px-2 py-1.5 text-left transition-colors duration-100 disabled:cursor-not-allowed disabled:opacity-60 ${
        on
          ? 'bg-[color-mix(in_srgb,var(--accent)_16%,transparent)] text-[var(--text)]'
          : 'text-[var(--text-2)] enabled:hover:bg-[var(--panel-2)] enabled:hover:text-[var(--text)]'
      }`}
    >
      {lead && <span className="shrink-0">{lead}</span>}
      <span className="min-w-0 flex-1">
        <span className="block truncate text-sm font-medium">{title}</span>
        {hint && <span className="mt-0.5 block text-sub leading-snug text-[var(--text-3)]">{hint}</span>}
      </span>
      {on && <span className="shrink-0 text-[var(--accent)]"><IconCheck size={14} /></span>}
    </button>
  )
}

/** Switch: the bare milled-glass switch — track, knob, drag. No label, no row:
 * `Toggle` is this plus a settings row, and a `Field` can stand one beside a
 * Select instead.
 *
 * Every length is a token (tokens.css --switch-*) and the drag reads the one
 * length it needs — --switch-travel, registered as a <length> so it resolves to
 * px — back out of the computed style. The knob's rest positions are CSS
 * calc()s off that same token, so the pointer math and the CSS cannot disagree.
 * They used to: a rem-sized track (h-5 w-9) with hard-px seats (2 / 18) left the
 * knob overhanging its track by ~1.4px at scale 1 and ~2.8px at 0.9259, and that
 * overhang riding the corner arc was the "pixelated corner". */
export function Switch({checked, onChange, disabled, label}: {
  checked: boolean; onChange: (v: boolean) => void; disabled?: boolean; label?: string
}) {
  // Drag-to-flip: the pointer is captured on press and the knob follows it
  // between its seats as a 0→1 fraction of the travel, committing to whichever
  // side it lands nearest. A sub-threshold press never becomes a drag — it
  // falls through to onClick, which also keeps keyboard (Enter/Space) working.
  const [dragT, setDragT] = useState<number | null>(null)
  const drag = useRef({startX: 0, travel: 0, moved: false, suppressClick: false})

  const onPointerDown = (e: React.PointerEvent<HTMLButtonElement>) => {
    if (disabled || e.button !== 0) return
    const travel = parseFloat(getComputedStyle(e.currentTarget).getPropertyValue('--switch-travel'))
    drag.current = {startX: e.clientX, travel: travel || 0, moved: false, suppressClick: false}
    e.currentTarget.setPointerCapture(e.pointerId)
  }
  const onPointerMove = (e: React.PointerEvent<HTMLButtonElement>) => {
    if (disabled || !e.currentTarget.hasPointerCapture(e.pointerId)) return
    const dx = e.clientX - drag.current.startX
    if (!drag.current.moved && Math.abs(dx) < 4) return
    drag.current.moved = true
    const from = checked ? 1 : 0
    const moved = drag.current.travel > 0 ? dx / drag.current.travel : 0
    setDragT(Math.min(1, Math.max(0, from + moved)))
  }
  const onPointerUp = (e: React.PointerEvent<HTMLButtonElement>) => {
    if (!e.currentTarget.hasPointerCapture(e.pointerId)) return
    e.currentTarget.releasePointerCapture(e.pointerId)
    if (!drag.current.moved) return
    // The click that follows a drag must not re-toggle the committed state.
    drag.current.suppressClick = true
    const next = (dragT ?? (checked ? 1 : 0)) > 0.5
    setDragT(null)
    if (next !== checked) onChange(next)
  }
  const onPointerCancel = () => { setDragT(null); drag.current.moved = false }
  const onClick = () => {
    if (drag.current.suppressClick) { drag.current.suppressClick = false; return }
    if (!disabled) onChange(!checked)
  }

  // Mid-drag the track previews the side the knob would commit to.
  const visualOn = dragT !== null ? dragT > 0.5 : checked

  return (
    <button
      role="switch" aria-checked={checked} aria-label={label} disabled={disabled}
      onClick={onClick}
      onPointerDown={onPointerDown} onPointerMove={onPointerMove}
      onPointerUp={onPointerUp} onPointerCancel={onPointerCancel}
      className={`pf-control relative h-[var(--switch-h)] w-[var(--switch-w)] shrink-0 touch-none rounded-[var(--switch-r)] transition-colors duration-300 ${
        visualOn
          ? 'bg-[var(--accent)] shadow-[0_1px_8px_-1px_color-mix(in_srgb,var(--accent)_60%,transparent)] [--control-edge:var(--accent)] [--control-rim-o:1]'
          : 'bg-[var(--btn-bg)] shadow-[inset_0_1px_2px_var(--bevel-bot)] hover:bg-[var(--btn-bg-hover)]'
      }`}
    >
      <span
        className={`pf-knob absolute top-1/2 h-[var(--switch-knob)] w-[var(--switch-knob)] -translate-y-1/2 rounded-[var(--switch-knob-r)] transition-[left,background-color] duration-300 [transition-timing-function:var(--ease-spring)] ${
          visualOn ? 'bg-[var(--accent-contrast)]' : 'bg-[var(--text-3)]'
        }`}
        style={dragT !== null
          ? {left: `calc(var(--switch-seat) + ${dragT} * var(--switch-travel))`, transition: 'none'}
          : {left: checked ? 'calc(var(--switch-seat) + var(--switch-travel))' : 'var(--switch-seat)'}}
      />
    </button>
  )
}

/** Toggle: the settings row — label + hint on the left, a Switch on the right. */
export function Toggle({checked, onChange, label, hint, disabled}: {
  checked: boolean; onChange: (v: boolean) => void; label: string; hint?: ReactNode; disabled?: boolean
}) {
  return (
    <div className={`flex items-start justify-between gap-4 py-2 ${disabled ? 'opacity-50' : ''}`}>
      <div className="min-w-0">
        <div className="text-sm font-medium text-[var(--text)]">{label}</div>
        {hint && <div className="mt-0.5 text-xs leading-relaxed text-[var(--text-3)]">{hint}</div>}
      </div>
      <div className="mt-0.5 shrink-0">
        <Switch checked={checked} onChange={onChange} disabled={disabled} label={label} />
      </div>
    </div>
  )
}

const dotColor: Record<State, string> = {
  good: 'var(--good)', warn: 'var(--warn)', bad: 'var(--bad)', unknown: 'var(--text-3)',
}

/** StatusDot: color + label, never color alone. Breathes a halo when live.
 *
 * The dot is sized in `em` so it tracks whatever type it leads, and it aligns
 * on the label's BASELINE, not the line box. Line-box centering is geometric:
 * it puts the dot on the midline of a box that includes the leading and the
 * descender space, which sits well off the optical centre of a run of
 * lowercase text — that mismatch is what read as "the circles don't line up
 * with the words" beside "No agent connected yet". Seated on the baseline and
 * nudged a hair down, the dot lands on the x-height midline instead, and it
 * stays there at every --ui-scale step because both lengths are em.
 *
 * An empty label emits no span and no gap: a zero-width span behind the gap
 * left ~7px of dead air in both Overview identity cards. */
export function StatusDot({state, label, pulse}: {state: State; label: string; pulse?: boolean}) {
  const live = pulse && state === 'good'
  return (
    <span className="inline-flex items-baseline gap-[var(--halo-gap)] text-sm">
      <span
        className={`inline-flex h-[0.62em] w-[0.62em] shrink-0 translate-y-[0.04em] rounded-full ${live ? 'pf-halo' : ''}`}
        style={{background: dotColor[state], ['--halo' as string]: dotColor[state]}}
      />
      {label && <span className="min-w-0 text-[var(--text-2)]">{label}</span>}
    </span>
  )
}

export function Badge({children, tone = 'neutral'}: {children: ReactNode; tone?: 'neutral' | 'good' | 'warn' | 'bad' | 'accent'}) {
  const tones = {
    neutral: 'bg-[var(--btn-bg)] text-[var(--text-2)] border-[var(--border)]',
    good: 'bg-[color-mix(in_srgb,var(--good)_14%,transparent)] text-[var(--good)] border-[color-mix(in_srgb,var(--good)_30%,transparent)]',
    warn: 'bg-[color-mix(in_srgb,var(--warn)_14%,transparent)] text-[var(--warn)] border-[color-mix(in_srgb,var(--warn)_30%,transparent)]',
    bad: 'bg-[color-mix(in_srgb,var(--bad)_14%,transparent)] text-[var(--bad)] border-[color-mix(in_srgb,var(--bad)_30%,transparent)]',
    accent: 'bg-[color-mix(in_srgb,var(--accent)_14%,transparent)] text-[var(--accent)] border-[color-mix(in_srgb,var(--accent)_30%,transparent)]',
  }[tone]
  return <span className={`inline-flex items-center gap-1 rounded-[var(--r-chip)] border px-1.5 py-0.5 text-sub font-semibold ${tones}`}>{children}</span>
}

/** MonoChip: compact monospaced chip for ports, counts, and addresses that
 * ride inline with table text. */
export function MonoChip({children, tone = 'neutral'}: {children: ReactNode; tone?: 'neutral' | 'accent'}) {
  const cls = tone === 'accent'
    ? 'border-[color-mix(in_srgb,var(--accent)_35%,transparent)] bg-[color-mix(in_srgb,var(--accent)_14%,transparent)] text-[var(--accent)]'
    : 'border-[var(--border)] bg-[var(--panel-2)] text-[var(--text-2)]'
  return (
    <span className={`rounded-[var(--r-chip)] border px-1.5 py-0.5 font-mono text-sub tabular-nums ${cls}`}>
      {children}
    </span>
  )
}

/** FormRow: label + hint on the left, control on the right — the shared
 * settings-row treatment. */
export function FormRow({label, hint, children}: {label: string; hint?: ReactNode; children: ReactNode}) {
  return (
    <div className="flex items-center justify-between gap-4 py-2">
      <div className="min-w-0">
        <div className="text-sm font-medium text-[var(--text)]">{label}</div>
        {hint && <div className="mt-0.5 text-xs italic text-[var(--text-3)]">{hint}</div>}
      </div>
      <div className="shrink-0">{children}</div>
    </div>
  )
}

/** WarnWash: an amber internal glow behind a row that needs eyes on it — the
 * light seeps into the glass right where the problem is. */
export function WarnWash({on, children}: {on: boolean; children: ReactNode}) {
  if (!on) return <>{children}</>
  return (
    <div
      className="relative -mx-2 rounded-[var(--r-md)] px-2"
      style={{
        background: 'color-mix(in srgb, var(--warn) 6%, transparent)',
        boxShadow: 'inset 0 0 24px -8px color-mix(in srgb, var(--warn) 25%, transparent)',
      }}
    >
      {children}
    </div>
  )
}

/** Kbd: keyboard shortcut chip. Pass keys like "Ctrl K" — spaces split chips. */
export function Kbd({children, className = ''}: {children: string; className?: string}) {
  return (
    <span className={`inline-flex items-center gap-0.5 ${className}`}>
      {children.split(' ').map((k, i) => (
        <kbd
          key={i}
          // px-locked on purpose: the 18px chip box doesn't ride --ui-scale,
          // so the glyph size can't either or it outgrows the chip at 1.33×.
          // Ink is --text-2, not the silt --text-3 the rest of the chrome uses:
          // at 10.5px this is small text, and --text-3 on --panel-2 measures
          // 4.14:1 — under the 4.5:1 AA floor. --text-2 clears it at 6.0:1.
          className="pf-control relative inline-flex h-[18px] min-w-[18px] items-center justify-center rounded-[var(--r-chip)] bg-[var(--panel-2)] px-1 font-sans text-[10.5px] font-medium text-[var(--text-2)]"
        >{k}</kbd>
      ))}
    </span>
  )
}

/** Skeleton: shimmer placeholder. Give it the geometry of what it masks. */
export function Skeleton({className = '', style}: {className?: string; style?: React.CSSProperties}) {
  return <div aria-hidden className={`pf-skeleton ${className}`} style={style} />
}

/** SegmentedControl: exclusive choice with a sliding thumb. Left unsized, the
 * grid gives every segment the width of the widest label (an auto-sized grid
 * resolves equal 1fr tracks to the largest max-content), so long labels like
 * "System" never squeeze — avoid fixed widths from callers.
 *
 * The thumb is draggable: pointer capture on press, the thumb follows the
 * pointer between segments, release commits the nearest one. A press without
 * movement stays a click on the segment buttons. */
export function SegmentedControl<T extends string>({value, onChange, options, className = ''}: {
  value: T
  onChange: (v: T) => void
  options: {value: T; label: ReactNode; title?: string}[]
  className?: string
}) {
  const idx = Math.max(0, options.findIndex(o => o.value === value))
  const n = options.length
  const ref = useRef<HTMLDivElement>(null)
  const [dragPx, setDragPx] = useState<number | null>(null)
  const drag = useRef({startX: 0, moved: false, suppress: false, thumbW: 0, origin: 0, pad: 0})

  // The track's content box, measured — never a px literal. clientWidth already
  // excludes the rim, and the padding is a rem utility that moves with
  // --ui-scale, so the old "content = width − 6" was only ever right at a 16px
  // root (it is 1.69px of padding here, not 2). Same family of bug as the
  // toggle's hard-px seats.
  const metrics = (el: HTMLDivElement) => {
    const pad = parseFloat(getComputedStyle(el).paddingLeft) || 0
    return {pad, thumbW: (el.clientWidth - pad * 2) / n}
  }

  const onPointerDown = (e: React.PointerEvent<HTMLDivElement>) => {
    if (e.button !== 0 || !ref.current) return
    const {pad, thumbW} = metrics(ref.current)
    drag.current = {startX: e.clientX, moved: false, suppress: false, thumbW, origin: idx * thumbW, pad}
    ref.current.setPointerCapture(e.pointerId)
  }
  const onPointerMove = (e: React.PointerEvent<HTMLDivElement>) => {
    if (!ref.current?.hasPointerCapture(e.pointerId)) return
    const dx = e.clientX - drag.current.startX
    if (!drag.current.moved && Math.abs(dx) < 4) return
    drag.current.moved = true
    const max = (n - 1) * drag.current.thumbW
    setDragPx(Math.min(max, Math.max(0, drag.current.origin + dx)))
  }
  const commit = (i: number) => {
    const opt = options[Math.max(0, Math.min(n - 1, i))]
    if (opt && opt.value !== value) onChange(opt.value)
  }
  const onPointerUp = (e: React.PointerEvent<HTMLDivElement>) => {
    if (!ref.current?.hasPointerCapture(e.pointerId)) return
    ref.current.releasePointerCapture(e.pointerId)
    // Drag or tap, the commit happens here, from the pointer position.
    // Pointer capture retargets the compatibility mouse events, so the
    // synthetic click that follows may land on the container or the button
    // depending on engine — suppress it either way, briefly (keyboard clicks
    // arrive with no pointer sequence and must keep working).
    drag.current.suppress = true
    window.setTimeout(() => { drag.current.suppress = false }, 0)
    if (drag.current.moved) {
      const px = dragPx ?? drag.current.origin
      setDragPx(null)
      commit(Math.round(px / drag.current.thumbW))
    } else {
      const r = ref.current.getBoundingClientRect()
      commit(Math.floor((e.clientX - r.left - drag.current.pad) / drag.current.thumbW))
    }
  }
  const onPointerCancel = () => { setDragPx(null); drag.current.moved = false }
  const pick = (v: T) => {
    if (drag.current.suppress) return
    onChange(v)
  }

  return (
    <div
      ref={ref}
      onPointerDown={onPointerDown} onPointerMove={onPointerMove}
      onPointerUp={onPointerUp} onPointerCancel={onPointerCancel}
      className={`pf-control relative grid touch-none rounded-[var(--r-seg)] bg-[var(--input-bg)] p-0.5 shadow-[inset_0_1px_3px_var(--bevel-bot)] ${className}`}
      style={{gridTemplateColumns: `repeat(${n}, 1fr)`}}
      role="radiogroup"
    >
      {/* The thumb is milled glass too, and its seats come off the track's own
          padding (0.5 = 0.125rem) — the same length the drag math measures. */}
      <div
        aria-hidden
        className="pf-control absolute bottom-0.5 left-0.5 top-0.5 rounded-[var(--r-seg-item)] bg-[var(--panel-3)] shadow-[var(--shadow-soft)] transition-transform duration-300 [transition-timing-function:var(--ease-out)]"
        style={{
          width: `calc((100% - 0.25rem) / ${n})`,
          ...(dragPx !== null
            ? {transform: `translateX(${dragPx}px)`, transition: 'none'}
            : {transform: `translateX(${idx * 100}%)`}),
        }}
      />
      {options.map(o => {
        const on = o.value === value
        return (
          <button
            key={o.value} type="button" role="radio" aria-checked={on} title={o.title}
            onClick={() => pick(o.value)}
            className={`relative z-10 flex items-center justify-center gap-1.5 whitespace-nowrap rounded-[var(--r-seg-item)] px-3 py-1.5 text-xs font-medium transition-colors duration-200 ${
              on ? 'text-[var(--text)]' : 'text-[var(--text-3)] hover:text-[var(--text-2)]'
            }`}
          >{o.label}</button>
        )
      })}
    </div>
  )
}

/** Disclosure: progressive-disclosure section; height animates, never snaps. */
export function Disclosure({label, hint, children, defaultOpen = false}: {
  label: ReactNode; hint?: ReactNode; children: ReactNode; defaultOpen?: boolean
}) {
  const [open, setOpen] = useState(defaultOpen)
  return (
    <div className="overflow-hidden rounded-[var(--r-md)] border border-[var(--border)] bg-[var(--input-bg)]">
      <button
        type="button" onClick={() => setOpen(o => !o)} aria-expanded={open}
        className="flex w-full items-center justify-between gap-3 px-3.5 py-2.5 text-left transition-colors hover:bg-[var(--input-bg-hover)]"
      >
        <span className="min-w-0">
          <span className="block text-sm font-medium text-[var(--text)]">{label}</span>
          {hint && <span className="mt-0.5 block text-xs text-[var(--text-3)]">{hint}</span>}
        </span>
        <span className={`shrink-0 text-[var(--text-3)] transition-transform duration-300 ${open ? 'rotate-180' : ''}`}>
          <IconChevronDown size={16} />
        </span>
      </button>
      <div className="pf-expand" data-open={open}>
        <div><div className="px-3.5 pb-3.5 pt-1">{children}</div></div>
      </div>
    </div>
  )
}

/** FatalStatement: distress in the display voice — type on whitespace behind a
 * single coral rule, never a boxed alert (a screen keeps one identity surface;
 * a fatal must not add a second). The eyebrow murmurs the register, Cormorant
 * carries the sentence, and the raw engine error sits under it in the working
 * voice, capped at prose width so a long Go error stays readable. Reserved for
 * EngineFatal-class states — a warning is still a Banner. */
export function FatalStatement({eyebrow = 'fatal', title, detail, action}: {
  eyebrow?: string; title: string; detail?: ReactNode; action?: ReactNode
}) {
  return (
    <div className="pf-fade border-l-2 border-[var(--bad)] py-1 pl-5">
      <div className="text-caption font-medium lowercase tracking-[var(--tracking-eyebrow)] text-[var(--bad)]">{eyebrow}</div>
      <h2 className="mt-1.5 font-display text-metric-hero font-bold leading-[var(--lh-display)] text-[var(--text)]">{title}</h2>
      {detail && <p className="mt-2 max-w-prose text-body text-[var(--text-2)]">{detail}</p>}
      {action && <div className="mt-3.5">{action}</div>}
    </div>
  )
}

/** Banner: inline callout for alerts and mode ribbons. */
export function Banner({tone = 'info', children, action, onDismiss}: {
  tone?: 'info' | 'good' | 'warn' | 'bad'
  children: ReactNode
  action?: ReactNode
  onDismiss?: () => void
}) {
  const tones = {
    info: {color: 'var(--accent)', icon: <IconInfo size={16} />},
    good: {color: 'var(--good)', icon: <IconCheck size={16} />},
    warn: {color: 'var(--warn)', icon: <IconAlert size={16} />},
    bad: {color: 'var(--bad)', icon: <IconAlert size={16} />},
  }[tone]
  // Alerts leak ambient light: a soft tone-colored bleed radiates from behind
  // the glass (pf-bleed) so a warning reads before it is read.
  const bleeds = tone === 'warn' || tone === 'bad'
  return (
    <div
      className={`pf-fade relative flex items-center gap-3 rounded-[var(--r-input)] border px-3.5 py-2.5 text-sm ${bleeds ? 'pf-bleed' : ''}`}
      style={{
        borderColor: `color-mix(in srgb, ${tones.color} 35%, var(--border))`,
        // The sheet's bleed banner: the tone feeds in from the leading edge
        // and gives way to plain trench — never a solid fill.
        background: `linear-gradient(90deg, color-mix(in srgb, ${tones.color} 12%, transparent), transparent 60%)`,
        ...(bleeds ? {['--bleed' as string]: tones.color, ['--bleed-strength' as string]: '22%'} : undefined),
      }}
    >
      <span className="shrink-0" style={{color: tones.color}}>{tones.icon}</span>
      <div className="min-w-0 flex-1 break-words text-[var(--text)]">{children}</div>
      {action}
      {onDismiss && (
        <button onClick={onDismiss} aria-label="Dismiss" className="shrink-0 text-[var(--text-3)] transition-colors hover:text-[var(--text)]">
          <IconClose size={14} />
        </button>
      )}
    </div>
  )
}

export function ErrorBanner({message, onDismiss}: {message: string; onDismiss?: () => void}) {
  return <Banner tone="bad" onDismiss={onDismiss}>{message}</Banner>
}

/** copyText prefers the Wails runtime clipboard (works in the WebView even
 * without a secure context), falling back to the browser API in dev. */
export async function copyText(text: string): Promise<void> {
  // Real Wails resolves true; the dev mock resolves undefined → fall back.
  const ok = await Promise.resolve()
    .then(() => ClipboardSetText(text))
    .catch(() => false)
  if (!ok) await navigator.clipboard.writeText(text).catch(() => {})
}

/** CopyButton with icon + brief confirmation. */
export function CopyButton({text, size = 'md', label = 'Copy'}: {text: string; size?: 'sm' | 'md'; label?: string}) {
  const [copied, setCopied] = useState(false)
  useEffect(() => {
    if (!copied) return
    const t = setTimeout(() => setCopied(false), 1500)
    return () => clearTimeout(t)
  }, [copied])
  return (
    <Button variant="subtle" size={size} onClick={() => { copyText(text); setCopied(true) }}>
      <span key={String(copied)} className="pf-fade inline-flex items-center gap-1.5">
        {copied ? <IconCheck size={15} /> : <IconCopy size={15} />}
        {copied ? 'Copied' : label}
      </span>
    </Button>
  )
}

/** CopyIcon: icon-only copy affordance for table rows and pipeline nodes. */
export function CopyIcon({text, title = 'Copy'}: {text: string; title?: string}) {
  const [copied, setCopied] = useState(false)
  useEffect(() => {
    if (!copied) return
    const t = setTimeout(() => setCopied(false), 1500)
    return () => clearTimeout(t)
  }, [copied])
  return (
    <button
      title={title} aria-label={title}
      onClick={() => { copyText(text); setCopied(true) }}
      className={`inline-flex h-5 w-5 shrink-0 items-center justify-center rounded-[var(--r-xs)] transition-all duration-150 active:scale-90 ${
        copied ? 'text-[var(--good)]' : 'text-[var(--text-3)] hover:bg-[var(--panel-2)] hover:text-[var(--text)]'
      }`}
    >
      <span key={String(copied)} className="pf-fade inline-flex">
        {copied ? <IconCheck size={13} /> : <IconCopy size={13} />}
      </span>
    </button>
  )
}

/** Spinner: an indeterminate wait. It carries its own accessible name because
 * it is sometimes the *entire* content of a screen (App's pre-first-tick boot),
 * and a nameless graphic there leaves nothing to read at all. Callers with
 * their own adjacent wording should name it for what is being waited on.
 *
 * The rotation gates on prefersReduced() like every other scripted motion: the
 * ring is the signal, the spin is only reassurance, and under reduced motion
 * the sea goes still. (The CSS kill switch would freeze it mid-turn anyway —
 * this stops it at rest instead of at a random angle.) */
export function Spinner({size = 16, label = 'loading'}: {size?: number; label?: string}) {
  const {reduced} = useMotion()
  return (
    <svg
      width={size} height={size} viewBox="0 0 24 24" fill="none"
      role="img" aria-label={label}
      className={reduced ? '' : 'animate-spin'}
    >
      <circle cx="12" cy="12" r="9" stroke="currentColor" strokeWidth="3" opacity="0.2" />
      <path d="M21 12a9 9 0 0 0-9-9" stroke="currentColor" strokeWidth="3" strokeLinecap="round" />
    </svg>
  )
}

/** EmptyState: an empty screen is an invitation, so it speaks in the quiet
 * display voice with an annotation-italic hint. `size="lg"` is the full-page
 * centerpiece for a screen with nothing else to show. */
export function EmptyState({icon, title, hint, action, size = 'md'}: {
  icon?: ReactNode; title: string; hint?: ReactNode; action?: ReactNode; size?: 'md' | 'lg'
}) {
  const lg = size === 'lg'
  return (
    <div className={`flex flex-col items-center justify-center gap-2 text-center ${lg ? 'py-16' : 'py-10'}`}>
      {icon && (
        <div className="pf-control pf-float relative grid h-12 w-12 place-items-center rounded-[var(--r-md)] bg-[var(--input-bg)] text-[var(--text-3)]">
          {icon}
        </div>
      )}
      <div className={`mt-1 font-display leading-[var(--lh-tight)] [font-weight:var(--w-display-quiet)] text-[var(--text-2)] ${lg ? 'text-hero' : 'text-title'}`}>
        {title}
      </div>
      {hint && <div className={`italic leading-relaxed text-[var(--text-3)] ${lg ? 'max-w-md text-body' : 'max-w-sm text-xs'}`}>{hint}</div>}
      {action && <div className="mt-2">{action}</div>}
    </div>
  )
}

/** Everything the browser will move focus to with Tab. Deliberately narrow:
 * anything exotic enough to miss this list is exotic enough to carry an
 * explicit tabindex. */
const FOCUSABLE =
  'a[href],button:not([disabled]),input:not([disabled]),select:not([disabled]),textarea:not([disabled]),[tabindex]:not([tabindex="-1"])'

/** Visible tab stops, in tab order. Two filters the selector cannot express:
 * `tabIndex >= 0` (the selector's `button` arm happily matches a
 * `tabindex="-1"` button — the command palette's options are exactly that, and
 * trapping against one parks the boundary on an element Tab will never reach),
 * and getClientRects() as the "is it rendered" test — not offsetParent, which
 * lies about fixed-position elements. */
function focusables(root: HTMLElement): HTMLElement[] {
  return [...root.querySelectorAll<HTMLElement>(FOCUSABLE)]
    .filter(el => el.tabIndex >= 0 && el.getClientRects().length > 0)
}

/**
 * useDialog: the half of a dialog that isn't drawn — ARIA identity, a focus
 * trap, initial focus, focus restored to whatever opened it, Escape, and a
 * backdrop dismiss that survives a text-selection drag. `Modal` and the
 * command palette share exactly this and nothing else; their chrome,
 * placement and motion are different surfaces on purpose.
 *
 * Three things are load-bearing:
 *
 * - **Escape listens on `window`, not `document`.** `Select` and `Menu` close
 *   themselves from `document` listeners that stopPropagation(), and document
 *   precedes window on the bubble path — so a Select open inside a dialog
 *   swallows the first Escape and the dialog stays put. Move this to
 *   `document` and one Escape closes both.
 * - **The trap only intercepts Tab.** Tab is the only way out: the scrim
 *   covers the viewport and dismisses on click. A focusin-based trap would
 *   also yank focus back out of `Select`'s portalled list, which lives outside
 *   the panel by design.
 * - **Initial focus goes to `initialFocus`, else to whatever autoFocus
 *   already took, else to the panel** — which announces the dialog and its
 *   title rather than the close button. The chosen element is remembered so a
 *   re-run of this effect reproduces it: React fires autoFocus once, in the
 *   commit phase, and StrictMode remounts effects while leaving the DOM in
 *   place, so without the memo dev and production would disagree about where
 *   an autoFocus dialog starts.
 */
export function useDialog({onDismiss, label, labelledBy, initialFocus}: {
  onDismiss: () => void
  label?: string
  labelledBy?: string
  initialFocus?: React.RefObject<HTMLElement | null>
}) {
  const panelRef = useRef<HTMLDivElement>(null)
  const dismissRef = useRef(onDismiss)
  dismissRef.current = onDismiss
  const wantRef = useRef(initialFocus)
  wantRef.current = initialFocus
  const downOutside = useRef(false)

  // The opener is read during the first render, not in the effect below:
  // React runs a child's autoFocus in the commit phase, so by the time any
  // effect here runs the dialog's own field is already the active element and
  // "restore" would restore focus to a node about to be destroyed.
  const opener = useRef<HTMLElement | null | undefined>(undefined)
  if (opener.current === undefined) opener.current = document.activeElement as HTMLElement | null

  const placed = useRef<HTMLElement | null>(null)

  useEffect(() => {
    const panel = panelRef.current
    const inside = (el: Element | null | undefined): el is HTMLElement => !!el && !!panel?.contains(el)
    const want = [wantRef.current?.current, placed.current, document.activeElement].find(inside)
    if (want) { placed.current = want; want.focus({preventScroll: true}) }
    else panel?.focus({preventScroll: true})

    const onKey = (e: KeyboardEvent) => {
      const el = panelRef.current
      if (!el) return
      if (e.key === 'Escape') { dismissRef.current(); return }
      if (e.key !== 'Tab') return
      const items = focusables(el)
      if (items.length === 0) { e.preventDefault(); el.focus({preventScroll: true}); return }
      const active = document.activeElement
      const within = el.contains(active)
      if (e.shiftKey) {
        // The panel itself is tabindex=-1 and sits before its contents, so
        // Shift+Tab from it would step out of the dialog entirely.
        if (!within || active === items[0] || active === el) {
          e.preventDefault()
          items[items.length - 1].focus()
        }
      } else if (!within || active === items[items.length - 1]) {
        e.preventDefault()
        items[0].focus()
      }
    }
    window.addEventListener('keydown', onKey)

    return () => {
      window.removeEventListener('keydown', onKey)
      const active = document.activeElement
      // Only reclaim focus if it is still ours to give: a command that ran on
      // the way out may have focused something deliberately.
      const ours = !active || active === document.body || !!panel?.contains(active)
      if (ours && opener.current?.isConnected) opener.current.focus({preventScroll: true})
    }
  }, [])

  const outside = (e: React.MouseEvent) => !panelRef.current?.contains(e.target as Node)

  return {
    panelRef,
    dialogProps: {
      role: 'dialog' as const,
      'aria-modal': true,
      'aria-label': label,
      'aria-labelledby': labelledBy,
      tabIndex: -1,
    },
    // Dismiss only when the press AND the release both land off the panel:
    // a selection dragged out of a dialog is a drag, not a click on the scrim.
    backdropProps: {
      onMouseDown: (e: React.MouseEvent) => { downOutside.current = outside(e) },
      onMouseUp: (e: React.MouseEvent) => {
        const dismiss = downOutside.current && outside(e)
        downOutside.current = false
        if (dismiss) dismissRef.current()
      },
    },
  }
}

/** Modal: centered glass dialog with a scrim; Esc / backdrop-click closes.
 * Exits animate — [data-closing] runs the mirrored keyframes, then unmounts.
 * It is a real dialog, not a picture of one: `useDialog` owns the semantics,
 * the focus trap and the restore, so all of its call sites get them at once.
 *
 * It renders through a portal for the same reason Select's list does: every
 * .pf-card is a backdrop-filter surface, which makes it a containing block for
 * fixed descendants. Analytics opens the session replay from inside its history
 * card — anchored there, `fixed inset-0` would size to the CARD, not the
 * viewport, and the dialog would be trapped in it. */
export function Modal({title, onClose, children, footer, wide}: {
  title: string; onClose: () => void; children: ReactNode; footer?: ReactNode; wide?: boolean
}) {
  const [closing, setClosing] = useState(false)
  const closeRef = useRef(onClose)
  closeRef.current = onClose
  const closingRef = useRef(false)
  // Latched: Escape and a backdrop release can both arrive, and two exit
  // timers would call onClose twice.
  const begin = () => {
    if (closingRef.current) return
    closingRef.current = true
    setClosing(true)
    window.setTimeout(() => closeRef.current(), 170)
  }
  const titleId = useId()
  const {panelRef, dialogProps, backdropProps} = useDialog({onDismiss: begin, labelledBy: titleId})

  return createPortal(
    <div
      className="fixed inset-0 z-50 flex items-center justify-center p-6"
      data-closing={closing || undefined}
      {...backdropProps}
    >
      <div className="pf-scrim absolute inset-0 bg-black/50" />
      <div
        ref={panelRef}
        {...dialogProps}
        className={`pf-pop pf-glass relative w-full ${wide ? 'max-w-2xl' : 'max-w-lg'} max-h-[85vh] overflow-hidden rounded-[var(--r-bloom)] outline-none`}
      >
        <div className="flex items-center justify-between px-5 py-3.5">
          <h2 id={titleId} className="font-display text-title font-bold text-[var(--text)]">{title}</h2>
          <IconButton title="Close" onClick={begin}><IconClose size={16} /></IconButton>
        </div>
        <div className="pf-sep" aria-hidden />
        <div className="max-h-[calc(85vh-8rem)] overflow-y-auto overscroll-y-contain px-5 py-4">
          <div data-band-content>{children}</div>
        </div>
        {footer && (
          <>
            <div className="pf-sep" aria-hidden />
            <div className="flex justify-end gap-2 px-5 py-3.5">{footer}</div>
          </>
        )}
      </div>
    </div>,
    document.body
  )
}

/** Codebox: a selectable, monospaced value with a copy affordance.
 *
 * `size="lg"` is the ceremonial artifact — the pairing code or address you
 * hand to another human renders as a plaque: larger mono, letterspaced,
 * generous padding. Never case-transformed at any size: codes are
 * credentials.
 *
 * `selectable={false}` for a MASKED value: the mask is real text, so a
 * select-all drag off a masked pairing code hands out a string of bullets that
 * looks like it came from the app. Copy still works — callers copy the real
 * string, not what is on screen. */
export function Codebox({text, action, selectable = true, size = 'md'}: {
  text: string; action?: ReactNode; selectable?: boolean; size?: 'md' | 'lg'
}) {
  const fs = size === 'lg'
    ? 'text-code font-medium tracking-[var(--tracking-code)] px-4 py-3.5'
    : 'text-emphasis px-3 py-2.5'
  return (
    <div className="flex items-stretch gap-2">
      <code className={`pf-well min-w-0 flex-1 break-all font-mono leading-relaxed text-[var(--text)] ${fs} ${
        selectable ? 'select-text' : 'select-none'
      }`}>
        {text}
      </code>
      {action}
    </div>
  )
}
