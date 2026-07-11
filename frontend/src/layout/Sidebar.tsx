import {UIStatus} from '../state'
import {NAV_MAIN, NAV_SETTINGS, NavId, NavItem} from '../nav'
import {Badge, Kbd} from '../components/ui'
import {Emblem} from '../components/Emblem'
import {IconServer} from '../components/icons'

// Nav geometry shared by the buttons and the sliding indicator. ITEM_H must
// equal --nav-item-h (tokens.css); the indicator's translateY math needs the
// same number in px. The indicator's top-4 must match the nav's pt-4.
const ITEM_H = 36
const GAP = 2

/**
 * The left rail: brand, mode identity, main navigation with a sliding accent
 * pill, Settings pinned at the foot. Shares the title bar's glass sheet.
 */
export function Sidebar({status, nav, onNav}: {
  status: UIStatus
  nav: NavId
  onNav: (id: NavId) => void
}) {
  const isAgent = status.role === 'agent'
  const activeIdx = NAV_MAIN.findIndex(n => n.id === nav)

  return (
    <div className="flex h-full flex-col">
      {/* Brand row doubles as a drag handle; its height matches the chrome
          strip so the brand centers on the floating island's midline. */}
      <div className="pf-drag flex h-[var(--chrome-top)] shrink-0 items-center gap-2.5 px-4">
        <div className="grid h-6 w-6 shrink-0 place-items-center rounded-[var(--r-sm)] bg-[var(--accent)] text-[var(--accent-contrast)] shadow-[0_2px_10px_-2px_color-mix(in_srgb,var(--accent)_55%,transparent)]">
          <IconServer size={14} />
        </div>
        <span className="text-[13px] font-semibold tracking-tight">proxyforward</span>
      </div>

      {/* Mode identity anchor: the role's emblem, riding the live accent so
          a role swap washes through it with the rest of the chrome. */}
      <div className="mx-3 mt-2 flex items-center gap-2.5 rounded-[var(--r-md)] border border-[color-mix(in_srgb,var(--accent)_22%,var(--border))] bg-[color-mix(in_srgb,var(--accent)_7%,transparent)] px-2.5 py-2 transition-colors duration-500">
        <Emblem role={isAgent ? 'agent' : 'gateway'} size={26} glow />
        <span className="text-xs font-semibold tracking-tight">{isAgent ? 'Agent' : 'Gateway'}</span>
        <span className="ml-auto flex items-center gap-1">
          {status.mode === 'attached' && <Badge tone="good">Service</Badge>}
        </span>
      </div>

      <nav className="relative flex-1 px-2 pt-4">
        {/* Sliding active indicator: an internal accent glow, not a solid
            pill (main rail only; Settings styles itself). */}
        <div
          aria-hidden
          className="pf-nav-glow pointer-events-none absolute left-2 right-2 top-4 rounded-[var(--r-md)] transition-[transform,opacity] duration-300 [transition-timing-function:var(--ease-spring)]"
          style={{
            height: ITEM_H,
            opacity: activeIdx < 0 ? 0 : 1,
            transform: `translateY(${Math.max(0, activeIdx) * (ITEM_H + GAP)}px)`,
          }}
        />
        <div className="relative flex flex-col" style={{gap: GAP}}>
          {NAV_MAIN.map(item => (
            <NavButton key={item.id} item={item} on={nav === item.id} onNav={onNav} />
          ))}
        </div>
      </nav>

      <div className="px-2 pb-1">
        <NavButton item={NAV_SETTINGS} on={nav === 'settings'} onNav={onNav} standalone />
      </div>

      <div className="pf-sep mx-2" aria-hidden />
      <div className="space-y-1.5 px-4 py-3 text-[11px] text-[var(--text-3)]">
        {status.hostname && (
          <div className="flex flex-wrap items-center gap-1.5">
            <Badge tone="neutral">{status.hostname}</Badge>
            {status.peerHostname && <span aria-hidden>↔</span>}
            {status.peerHostname && <Badge tone="neutral">{status.peerHostname}</Badge>}
          </div>
        )}
        <div className="tabular-nums">v{status.version.replace(/ \(.*\)$/, '')} · pid {status.pid}</div>
      </div>
    </div>
  )
}

function NavButton({item, on, onNav, standalone = false}: {
  item: NavItem
  on: boolean
  onNav: (id: NavId) => void
  standalone?: boolean
}) {
  const Icon = item.icon
  return (
    <button
      onClick={() => onNav(item.id)}
      title={`${item.label} — Ctrl+${item.shortcut}`}
      style={{height: ITEM_H}}
      className={`group pf-press flex w-full items-center gap-2.5 rounded-[var(--r-md)] px-3 text-sm transition-colors duration-200 ${
        on
          ? `font-medium text-[var(--text)] ${standalone ? 'pf-nav-glow' : ''}`
          : 'pf-nav-bloom text-[var(--text-2)] hover:text-[var(--text)]'
      }`}
    >
      <span className={`transition-all duration-200 ${on ? 'scale-110 text-[var(--accent)]' : 'group-hover:scale-105'}`}>
        <Icon size={17} />
      </span>
      {item.label}
      <Kbd className="ml-auto opacity-0 transition-opacity duration-200 group-hover:opacity-100">{item.shortcut}</Kbd>
    </button>
  )
}
