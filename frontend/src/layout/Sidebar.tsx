import {UIStatus} from '../state'
import {NAV_MAIN, NAV_SETTINGS, NavId, NavItem} from '../nav'
import {Kbd} from '../components/ui'
import {RoleSwitcher} from '../components/RoleSwitcher'
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
export function Sidebar({status, nav, onNav, onPair}: {
  status: UIStatus
  nav: NavId
  onNav: (id: NavId) => void
  onPair: () => void
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

      {/* Mode identity anchor: the role's emblem, riding the live accent so a
          role swap washes through it with the rest of the chrome — and the
          control that performs that swap (RoleSwitcher). */}
      <RoleSwitcher status={status} onPair={onPair} />

      <nav className="relative flex-1 px-2 pt-4">
        {/* Sliding active indicator: an internal accent glow, not a solid
            pill (main rail only; Settings styles itself). */}
        <div
          aria-hidden
          className="pf-nav-glow pointer-events-none absolute left-2 right-2 top-4 rounded-[var(--r-md)] transition-[transform,opacity] duration-[var(--dur-slow)] [transition-timing-function:var(--ease-spring)]"
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
      <div className="space-y-2 px-3 py-3 text-[11px] text-[var(--text-3)]">
        {status.hostname && (
          <HostPair isAgent={isAgent} self={status.hostname} peer={status.peerHostname || ''} />
        )}
        {/* Belt and braces: the display version is short since the ldflags
            stamp got trimmed, but never let a surprise string wrap the rail. */}
        <div className="truncate px-1 tabular-nums" title={`v${status.version} · pid ${status.pid}`}>
          v{status.version.replace(/ \(.*\)$/, '')} · pid {status.pid}
        </div>
      </div>
    </div>
  )
}

/** HostPair: this machine over its peer — two quiet rows, each led by a
 * small role-hued status dot (cyan agent, violet gateway). Tier-3: type on
 * whitespace; the dots are the only color the footer carries. */
function HostPair({isAgent, self, peer}: {isAgent: boolean; self: string; peer: string}) {
  return (
    <div className="space-y-1.5 px-1" title="The agent connects to the gateway">
      <HostRow role={isAgent ? 'agent' : 'gateway'} name={self} sub="this machine" />
      <HostRow
        role={isAgent ? 'gateway' : 'agent'}
        name={peer || (isAgent ? 'gateway offline' : 'no agent yet')}
        dim={!peer}
      />
    </div>
  )
}

function HostRow({role, name, sub, dim}: {role: 'agent' | 'gateway'; name: string; sub?: string; dim?: boolean}) {
  const c = role === 'agent' ? 'var(--role-agent)' : 'var(--role-gateway)'
  return (
    <div className={`flex items-center gap-2 ${dim ? 'opacity-55' : ''}`}>
      <span aria-hidden className="h-1.5 w-1.5 shrink-0 rounded-full" style={{background: c}} />
      <span className={`min-w-0 truncate font-medium text-[var(--text-2)] ${dim ? 'italic' : ''}`} title={name}>
        {name}
      </span>
      {sub && <span className="ml-auto shrink-0 text-[9.5px] uppercase tracking-wide text-[var(--text-3)]">{sub}</span>}
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
          : 'text-[var(--text-2)] hover:bg-[color-mix(in_srgb,var(--text)_5%,transparent)] hover:text-[var(--text)]'
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
