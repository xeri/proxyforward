import {ReactNode, useEffect, useRef, useState} from 'react'
import {ClipboardSetText} from '../../wailsjs/runtime/runtime'
import {IconAlert, IconCheck, IconChevronDown, IconCopy, IconEye, IconEyeOff, IconInfo, IconClose} from './icons'

type State = 'good' | 'warn' | 'bad' | 'unknown'

/** PageHeader: one display-size title per screen, with an optional tool slot. */
export function PageHeader({title, subtitle, action}: {
  title: string; subtitle?: ReactNode; action?: ReactNode
}) {
  return (
    <div className="mb-5 flex items-end justify-between gap-4">
      <div className="min-w-0">
        <h1 className="text-[22px] font-semibold leading-tight tracking-tight">{title}</h1>
        {subtitle && <p className="mt-1 text-[13px] text-[var(--text-3)]">{subtitle}</p>}
      </div>
      {action && <div className="shrink-0">{action}</div>}
    </div>
  )
}

/** Card: the workhorse "solid glass" surface. */
export function Card({title, subtitle, action, children, className = '', pad = true}: {
  title?: string; subtitle?: string; action?: ReactNode; children: ReactNode; className?: string; pad?: boolean
}) {
  return (
    <div className={`pf-card ${className}`}>
      {(title || action) && (
        <div className={`flex items-center justify-between gap-3 ${pad ? 'px-5 pt-4' : 'p-5 pb-4'}`}>
          <div className="min-w-0">
            {title && <h2 className="text-[15px] font-semibold tracking-tight text-[var(--text)]">{title}</h2>}
            {subtitle && <p className="mt-0.5 text-xs text-[var(--text-3)]">{subtitle}</p>}
          </div>
          {action}
        </div>
      )}
      <div className={pad ? 'p-5 pt-4' : ''}>{children}</div>
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
  const styles = {
    primary: 'bg-[var(--accent)] text-[var(--accent-contrast)] shadow-[0_2px_12px_-2px_color-mix(in_srgb,var(--accent)_45%,transparent)] hover:bg-[var(--accent-hover)] hover:shadow-[0_4px_20px_-2px_color-mix(in_srgb,var(--accent)_60%,transparent)] disabled:opacity-50 disabled:shadow-none',
    ghost: 'border border-[var(--border)] bg-transparent text-[var(--text)] hover:bg-[var(--panel-2)] hover:border-[var(--border-strong)] disabled:opacity-50',
    subtle: 'bg-[var(--panel-2)] text-[var(--text)] hover:bg-[var(--border)] disabled:opacity-50',
    danger: 'border border-[color-mix(in_srgb,var(--bad)_55%,var(--border))] bg-transparent text-[var(--bad)] hover:bg-[var(--bad)] hover:border-[var(--bad)] hover:text-white disabled:opacity-50',
  }[variant]
  const sz = size === 'sm' ? 'px-2.5 py-1 text-xs' : 'px-3.5 py-2 text-sm'
  return (
    <button
      title={title}
      className={`pf-press inline-flex items-center justify-center gap-1.5 rounded-[var(--r-md)] font-medium transition-[background-color,border-color,box-shadow,color,opacity] duration-200 ${sz} ${styles} ${className}`}
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
      className={`inline-flex h-8 w-8 items-center justify-center rounded-[var(--r-md)] transition-all duration-200 active:scale-90 disabled:opacity-40 ${styles}`}>
      {children}
    </button>
  )
}

export function Field({label, hint, children}: {label: string; hint?: ReactNode; children: ReactNode}) {
  return (
    <label className="block">
      <div className="mb-1.5 text-sm font-medium text-[var(--text)]">{label}</div>
      {children}
      {hint && <div className="mt-1.5 text-xs leading-relaxed text-[var(--text-3)]">{hint}</div>}
    </label>
  )
}

export function TextInput({value, onChange, placeholder, type = 'text', mono, onEnter, autoFocus}: {
  value: string; onChange: (v: string) => void; placeholder?: string; type?: string; mono?: boolean
  onEnter?: () => void; autoFocus?: boolean
}) {
  const isPassword = type === 'password'
  const [reveal, setReveal] = useState(false)
  const effectiveType = isPassword && reveal ? 'text' : type
  return (
    <div className="relative">
      <input
        type={effectiveType} value={value} placeholder={placeholder} autoFocus={autoFocus}
        onChange={e => onChange(e.target.value)}
        onKeyDown={e => { if (e.key === 'Enter' && onEnter) onEnter() }}
        className={`w-full rounded-[var(--r-md)] border border-[var(--border)] bg-[var(--panel-2)] py-2 pl-3 text-sm text-[var(--text)] shadow-[inset_0_1px_2px_var(--bevel-bot),inset_0_-1px_0_var(--bevel-top)] outline-none transition-all duration-200 placeholder:text-[var(--text-3)] hover:border-[var(--border-strong)] focus:border-[var(--accent)] focus:ring-2 focus:ring-[color-mix(in_srgb,var(--accent)_25%,transparent)] ${isPassword ? 'pr-10' : 'pr-3'} ${mono ? 'font-mono text-[12.5px]' : ''}`}
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
        className={`grid h-4 w-4 place-items-center rounded-[var(--r-xs)] transition-all duration-150 ${
          checked
            ? 'border border-transparent bg-[var(--accent)] text-[var(--accent-contrast)]'
            : 'border border-[var(--border-strong)] bg-[var(--panel-2)] text-transparent'
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
 * opens, Esc closes, click-outside closes. */
export function Select({value, onChange, options}: {
  value: string; onChange: (v: string) => void; options: {value: string; label: string}[]
}) {
  const [open, setOpen] = useState(false)
  const ref = useRef<HTMLDivElement>(null)
  const current = options.find(o => o.value === value)
  useEffect(() => {
    if (!open) return
    const onDoc = (e: MouseEvent) => { if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false) }
    const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape') setOpen(false) }
    document.addEventListener('mousedown', onDoc)
    document.addEventListener('keydown', onKey)
    return () => { document.removeEventListener('mousedown', onDoc); document.removeEventListener('keydown', onKey) }
  }, [open])
  return (
    <div ref={ref} className="relative">
      <button
        type="button" onClick={() => setOpen(o => !o)} aria-haspopup="listbox" aria-expanded={open}
        className={`flex w-full items-center justify-between gap-2 rounded-[var(--r-md)] border bg-[var(--panel-2)] px-3 py-2 text-left text-sm text-[var(--text)] shadow-[inset_0_1px_2px_var(--bevel-bot),inset_0_-1px_0_var(--bevel-top)] outline-none transition-all duration-200 hover:border-[var(--border-strong)] ${
          open ? 'border-[var(--accent)] ring-2 ring-[color-mix(in_srgb,var(--accent)_25%,transparent)]' : 'border-[var(--border)]'
        }`}
      >
        <span className="min-w-0 truncate">{current ? current.label : value}</span>
        <span className={`shrink-0 text-[var(--text-3)] transition-transform duration-200 ${open ? 'rotate-180' : ''}`}><IconChevronDown size={16} /></span>
      </button>
      {open && (
        <div
          role="listbox"
          className="pf-fade absolute z-30 mt-1 max-h-60 w-full overflow-auto rounded-[var(--r-lg)] border border-[var(--border)] bg-[var(--panel-3)] p-1 shadow-[var(--shadow-pop)]"
        >
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
      )}
    </div>
  )
}

export function Toggle({checked, onChange, label, hint, disabled}: {
  checked: boolean; onChange: (v: boolean) => void; label: string; hint?: ReactNode; disabled?: boolean
}) {
  return (
    <div className={`flex items-start justify-between gap-4 py-2 ${disabled ? 'opacity-50' : ''}`}>
      <div className="min-w-0">
        <div className="text-sm font-medium text-[var(--text)]">{label}</div>
        {hint && <div className="mt-0.5 text-xs leading-relaxed text-[var(--text-3)]">{hint}</div>}
      </div>
      <button
        role="switch" aria-checked={checked} disabled={disabled}
        onClick={() => !disabled && onChange(!checked)}
        className={`relative mt-0.5 h-5 w-9 shrink-0 rounded-full border transition-colors duration-300 ${
          checked
            ? 'border-transparent bg-[var(--accent)] shadow-[0_1px_8px_-1px_color-mix(in_srgb,var(--accent)_60%,transparent)]'
            : 'border-[var(--border-strong)] bg-[var(--panel-2)] hover:bg-[var(--border)]'
        }`}
      >
        <span className={`absolute top-1/2 h-3.5 w-3.5 -translate-y-1/2 rounded-full shadow-[0_1px_2px_rgba(0,0,0,0.35)] transition-all duration-300 [transition-timing-function:var(--ease-spring)] ${
          checked ? 'left-[19px] bg-[var(--accent-contrast)]' : 'left-[3px] bg-[var(--text-3)]'
        }`} />
      </button>
    </div>
  )
}

const dotColor: Record<State, string> = {
  good: 'var(--good)', warn: 'var(--warn)', bad: 'var(--bad)', unknown: 'var(--text-3)',
}

/** StatusDot: color + label, never color alone. Breathes a halo when live. */
export function StatusDot({state, label, pulse}: {state: State; label: string; pulse?: boolean}) {
  const live = pulse && state === 'good'
  return (
    <span className="inline-flex items-center gap-2 text-sm">
      <span
        className={`inline-flex h-2.5 w-2.5 rounded-full ${live ? 'pf-halo' : ''}`}
        style={{background: dotColor[state], ['--halo' as string]: dotColor[state]}}
      />
      <span className="text-[var(--text-2)]">{label}</span>
    </span>
  )
}

export function Badge({children, tone = 'neutral'}: {children: ReactNode; tone?: 'neutral' | 'good' | 'warn' | 'bad' | 'accent'}) {
  const tones = {
    neutral: 'bg-[var(--panel-2)] text-[var(--text-2)] border-[var(--border)]',
    good: 'bg-[color-mix(in_srgb,var(--good)_14%,transparent)] text-[var(--good)] border-[color-mix(in_srgb,var(--good)_30%,transparent)]',
    warn: 'bg-[color-mix(in_srgb,var(--warn)_14%,transparent)] text-[var(--warn)] border-[color-mix(in_srgb,var(--warn)_30%,transparent)]',
    bad: 'bg-[color-mix(in_srgb,var(--bad)_14%,transparent)] text-[var(--bad)] border-[color-mix(in_srgb,var(--bad)_30%,transparent)]',
    accent: 'bg-[color-mix(in_srgb,var(--accent)_14%,transparent)] text-[var(--accent)] border-[color-mix(in_srgb,var(--accent)_30%,transparent)]',
  }[tone]
  return <span className={`inline-flex items-center gap-1 rounded-[var(--r-sm)] border px-1.5 py-0.5 text-[11px] font-semibold ${tones}`}>{children}</span>
}

/** Kbd: keyboard shortcut chip. Pass keys like "Ctrl K" — spaces split chips. */
export function Kbd({children, className = ''}: {children: string; className?: string}) {
  return (
    <span className={`inline-flex items-center gap-0.5 ${className}`}>
      {children.split(' ').map((k, i) => (
        <kbd
          key={i}
          className="inline-flex h-[18px] min-w-[18px] items-center justify-center rounded-[var(--r-xs)] border border-[var(--border)] bg-[var(--panel-2)] px-1 font-sans text-[10.5px] font-medium text-[var(--text-3)] shadow-[inset_0_-1px_0_var(--border)]"
        >{k}</kbd>
      ))}
    </span>
  )
}

/** Skeleton: shimmer placeholder. Give it the geometry of what it masks. */
export function Skeleton({className = '', style}: {className?: string; style?: React.CSSProperties}) {
  return <div aria-hidden className={`pf-skeleton ${className}`} style={style} />
}

/** SegmentedControl: exclusive choice with a sliding thumb. */
export function SegmentedControl<T extends string>({value, onChange, options, className = ''}: {
  value: T
  onChange: (v: T) => void
  options: {value: T; label: ReactNode; title?: string}[]
  className?: string
}) {
  const idx = Math.max(0, options.findIndex(o => o.value === value))
  const n = options.length
  return (
    <div
      className={`relative grid rounded-[var(--r-md)] border border-[var(--border)] bg-[var(--panel-2)] p-0.5 ${className}`}
      style={{gridTemplateColumns: `repeat(${n}, 1fr)`}}
      role="radiogroup"
    >
      <div
        aria-hidden
        className="absolute bottom-0.5 top-0.5 rounded-[calc(var(--r-md)-2px)] border border-[var(--border-strong)] bg-[var(--panel-3)] shadow-[var(--shadow-soft)] transition-transform duration-300 [transition-timing-function:var(--ease-out)]"
        style={{left: 2, width: `calc((100% - 4px) / ${n})`, transform: `translateX(${idx * 100}%)`}}
      />
      {options.map(o => {
        const on = o.value === value
        return (
          <button
            key={o.value} type="button" role="radio" aria-checked={on} title={o.title}
            onClick={() => onChange(o.value)}
            className={`relative z-10 flex items-center justify-center gap-1.5 rounded-[calc(var(--r-md)-2px)] px-2.5 py-1.5 text-xs font-medium transition-colors duration-200 ${
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
    <div className="overflow-hidden rounded-[var(--r-md)] border border-[var(--border)] bg-[color-mix(in_srgb,var(--panel-2)_55%,transparent)]">
      <button
        type="button" onClick={() => setOpen(o => !o)} aria-expanded={open}
        className="flex w-full items-center justify-between gap-3 px-3.5 py-2.5 text-left transition-colors hover:bg-[var(--panel-2)]"
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
  return (
    <div
      className="pf-fade flex items-center gap-3 rounded-[var(--r-md)] border px-3.5 py-2.5 text-sm"
      style={{
        borderColor: `color-mix(in srgb, ${tones.color} 35%, var(--border))`,
        background: `color-mix(in srgb, ${tones.color} 8%, transparent)`,
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

export function Spinner({size = 16}: {size?: number}) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" className="animate-spin" fill="none">
      <circle cx="12" cy="12" r="9" stroke="currentColor" strokeWidth="3" opacity="0.2" />
      <path d="M21 12a9 9 0 0 0-9-9" stroke="currentColor" strokeWidth="3" strokeLinecap="round" />
    </svg>
  )
}

export function EmptyState({icon, title, hint, action}: {
  icon?: ReactNode; title: string; hint?: ReactNode; action?: ReactNode
}) {
  return (
    <div className="flex flex-col items-center justify-center gap-2 py-10 text-center">
      {icon && (
        <div className="pf-float grid h-12 w-12 place-items-center rounded-[var(--r-lg)] border border-[var(--border)] bg-[var(--panel-2)] text-[var(--text-3)] shadow-[inset_0_1px_0_var(--hairline)]">
          {icon}
        </div>
      )}
      <div className="mt-1 text-sm font-medium text-[var(--text-2)]">{title}</div>
      {hint && <div className="max-w-sm text-xs leading-relaxed text-[var(--text-3)]">{hint}</div>}
      {action && <div className="mt-2">{action}</div>}
    </div>
  )
}

/** Modal: centered glass dialog with a scrim; Esc / backdrop-click closes.
 * Exits animate — [data-closing] runs the mirrored keyframes, then unmounts. */
export function Modal({title, onClose, children, footer, wide}: {
  title: string; onClose: () => void; children: ReactNode; footer?: ReactNode; wide?: boolean
}) {
  const [closing, setClosing] = useState(false)
  const closeRef = useRef(onClose)
  closeRef.current = onClose
  const begin = () => {
    setClosing(true)
    window.setTimeout(() => closeRef.current(), 170)
  }
  useEffect(() => {
    const h = (e: KeyboardEvent) => { if (e.key === 'Escape') begin() }
    window.addEventListener('keydown', h)
    return () => window.removeEventListener('keydown', h)
  }, [])
  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center p-6"
      data-closing={closing || undefined}
      onMouseDown={begin}
    >
      <div className="pf-scrim absolute inset-0 bg-black/50" />
      <div
        onMouseDown={e => e.stopPropagation()}
        className={`pf-pop pf-glass relative w-full ${wide ? 'max-w-2xl' : 'max-w-lg'} max-h-[85vh] overflow-hidden rounded-[var(--r-xl)]`}
      >
        <div className="flex items-center justify-between border-b border-[var(--border)] px-5 py-3.5">
          <h2 className="text-base font-semibold tracking-tight">{title}</h2>
          <IconButton title="Close" onClick={begin}><IconClose size={16} /></IconButton>
        </div>
        <div className="max-h-[calc(85vh-8rem)] overflow-y-auto px-5 py-4">{children}</div>
        {footer && <div className="flex justify-end gap-2 border-t border-[var(--border)] px-5 py-3.5">{footer}</div>}
      </div>
    </div>
  )
}

/** Codebox: a selectable, monospaced value with a copy affordance. */
export function Codebox({text, action}: {text: string; action?: ReactNode}) {
  return (
    <div className="flex items-stretch gap-2">
      <code className="pf-well min-w-0 flex-1 select-text break-all px-3 py-2.5 font-mono text-[12.5px] leading-relaxed text-[var(--text)]">
        {text}
      </code>
      {action}
    </div>
  )
}
