import {ReactNode, useEffect, useState} from 'react'
import {ClipboardSetText} from '../../wailsjs/runtime/runtime'
import {IconCheck, IconCopy} from './icons'

type State = 'good' | 'warn' | 'bad' | 'unknown'

export function Card({title, subtitle, action, children, className = '', pad = true}: {
  title?: string; subtitle?: string; action?: ReactNode; children: ReactNode; className?: string; pad?: boolean
}) {
  return (
    <div className={`rounded-2xl border border-[var(--border)] bg-[var(--panel)] shadow-[var(--shadow-soft)] ${className}`}>
      {(title || action) && (
        <div className={`flex items-center justify-between gap-3 ${pad ? 'px-5 pt-4' : 'p-5 pb-4'}`}>
          <div>
            {title && <h2 className="text-[13px] font-semibold uppercase tracking-wide text-[var(--text-2)]">{title}</h2>}
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
    primary: 'text-white shadow-[0_2px_12px_-2px_color-mix(in_srgb,var(--accent)_50%,transparent)] hover:shadow-[0_4px_20px_-2px_color-mix(in_srgb,var(--accent)_65%,transparent)] hover:brightness-110 disabled:opacity-50 disabled:shadow-none',
    ghost: 'border border-[var(--border)] bg-transparent text-[var(--text)] hover:bg-[var(--panel-2)] hover:border-[var(--border-strong)] disabled:opacity-50',
    subtle: 'bg-[var(--panel-2)] text-[var(--text)] hover:bg-[var(--border)] disabled:opacity-50',
    danger: 'border border-[var(--bad)] bg-transparent text-[var(--bad)] hover:bg-[var(--bad)] hover:text-white disabled:opacity-50',
  }[variant]
  const sz = size === 'sm' ? 'px-2.5 py-1 text-xs' : 'px-3.5 py-2 text-sm'
  return (
    <button
      title={title}
      style={variant === 'primary' ? {background: 'linear-gradient(135deg, var(--accent), color-mix(in srgb, var(--accent) 55%, var(--accent-2)))'} : undefined}
      className={`inline-flex items-center justify-center gap-1.5 rounded-lg font-medium transition-all duration-200 active:scale-[0.96] disabled:active:scale-100 ${sz} ${styles} ${className}`}
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
      className={`inline-flex h-8 w-8 items-center justify-center rounded-lg transition-all duration-200 active:scale-90 disabled:opacity-40 ${styles}`}>
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
  return (
    <input
      type={type} value={value} placeholder={placeholder} autoFocus={autoFocus}
      onChange={e => onChange(e.target.value)}
      onKeyDown={e => { if (e.key === 'Enter' && onEnter) onEnter() }}
      className={`w-full rounded-lg border border-[var(--border)] bg-[var(--panel-2)] px-3 py-2 text-sm text-[var(--text)] outline-none transition-all duration-200 placeholder:text-[var(--text-3)] hover:border-[var(--border-strong)] focus:border-[var(--accent)] focus:ring-2 focus:ring-[color-mix(in_srgb,var(--accent)_25%,transparent)] ${mono ? 'font-mono text-[12.5px]' : ''}`}
    />
  )
}

export function Select({value, onChange, options}: {
  value: string; onChange: (v: string) => void; options: {value: string; label: string}[]
}) {
  return (
    <select value={value} onChange={e => onChange(e.target.value)}
      className="w-full rounded-lg border border-[var(--border)] bg-[var(--panel-2)] px-3 py-2 text-sm text-[var(--text)] outline-none transition-all duration-200 hover:border-[var(--border-strong)] focus:border-[var(--accent)] focus:ring-2 focus:ring-[color-mix(in_srgb,var(--accent)_25%,transparent)]">
      {options.map(o => <option key={o.value} value={o.value}>{o.label}</option>)}
    </select>
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
        style={checked ? {background: 'linear-gradient(135deg, var(--accent), color-mix(in srgb, var(--accent) 55%, var(--accent-2)))'} : undefined}
        className={`relative mt-0.5 h-5 w-9 shrink-0 rounded-full transition-colors duration-300 ${checked ? 'shadow-[0_1px_8px_-1px_color-mix(in_srgb,var(--accent)_60%,transparent)]' : 'bg-[var(--border)]'}`}
      >
        <span className={`absolute top-0.5 h-4 w-4 rounded-full bg-white shadow transition-all duration-300 [transition-timing-function:cubic-bezier(0.34,1.56,0.64,1)] ${checked ? 'left-[18px]' : 'left-0.5'}`} />
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
  return <span className={`inline-flex items-center gap-1 rounded-full border px-2 py-0.5 text-[11px] font-semibold ${tones}`}>{children}</span>
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
      className={`inline-flex h-5 w-5 shrink-0 items-center justify-center rounded transition-all duration-150 active:scale-90 ${
        copied ? 'text-[var(--good)]' : 'text-[var(--text-3)] hover:bg-[var(--panel-2)] hover:text-[var(--text)]'
      }`}
    >
      <span key={String(copied)} className="pf-fade inline-flex">
        {copied ? <IconCheck size={13} /> : <IconCopy size={13} />}
      </span>
    </button>
  )
}

export function ErrorBanner({message, onDismiss}: {message: string; onDismiss?: () => void}) {
  return (
    <div className="pf-fade flex items-start justify-between gap-3 rounded-lg border border-[var(--bad)] bg-[color-mix(in_srgb,var(--bad)_10%,transparent)] px-3 py-2.5 text-sm text-[var(--bad)]">
      <span className="min-w-0 break-words">{message}</span>
      {onDismiss && <button onClick={onDismiss} className="shrink-0 text-[var(--bad)] opacity-60 transition-opacity hover:opacity-100">✕</button>}
    </div>
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

export function EmptyState({icon, title, hint}: {icon?: ReactNode; title: string; hint?: ReactNode}) {
  return (
    <div className="flex flex-col items-center justify-center gap-2 py-10 text-center">
      {icon && (
        <div className="pf-float grid h-12 w-12 place-items-center rounded-2xl border border-[var(--border)] bg-[var(--panel-2)] text-[var(--text-3)]">
          {icon}
        </div>
      )}
      <div className="mt-1 text-sm font-medium text-[var(--text-2)]">{title}</div>
      {hint && <div className="max-w-sm text-xs text-[var(--text-3)]">{hint}</div>}
    </div>
  )
}

/** Modal: centered dialog with a scrim; Esc / backdrop-click closes. */
export function Modal({title, onClose, children, footer, wide}: {
  title: string; onClose: () => void; children: ReactNode; footer?: ReactNode; wide?: boolean
}) {
  useEffect(() => {
    const h = (e: KeyboardEvent) => { if (e.key === 'Escape') onClose() }
    window.addEventListener('keydown', h)
    return () => window.removeEventListener('keydown', h)
  }, [onClose])
  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center p-6" onMouseDown={onClose}>
      <div className="pf-scrim absolute inset-0 bg-black/50 backdrop-blur-[3px]" />
      <div
        onMouseDown={e => e.stopPropagation()}
        className={`pf-pop relative w-full ${wide ? 'max-w-2xl' : 'max-w-lg'} max-h-[85vh] overflow-hidden rounded-2xl border border-[var(--border)] bg-[var(--panel)] shadow-[var(--shadow-pop)]`}
      >
        <div className="flex items-center justify-between border-b border-[var(--border)] px-5 py-3.5">
          <h2 className="text-base font-semibold">{title}</h2>
          <button onClick={onClose} className="text-[var(--text-3)] transition-colors hover:text-[var(--text)]">✕</button>
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
      <code className="min-w-0 flex-1 select-text break-all rounded-lg border border-[var(--border)] bg-[var(--panel-2)] px-3 py-2.5 font-mono text-[12.5px] leading-relaxed text-[var(--text)]">
        {text}
      </code>
      {action}
    </div>
  )
}
