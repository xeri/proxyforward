import {ReactElement, useEffect, useState} from 'react'
import {SetTheme} from '../wailsjs/go/app/App'
import {Badge, IconButton} from './components/ui'
import {
  IconConnections, IconDashboard, IconLogs, IconMoon, IconServer,
  IconSettings, IconSun, IconTunnels,
} from './components/icons'
import {Dashboard} from './screens/Dashboard'
import {Connections} from './screens/Connections'
import {Logs} from './screens/Logs'
import {Settings} from './screens/Settings'
import {Tunnels} from './screens/Tunnels'
import {Wizard} from './screens/Wizard'
import {useTick, UIStatus} from './state'

type Nav = 'dashboard' | 'tunnels' | 'connections' | 'logs' | 'settings'
type Theme = 'dark' | 'light'

const NAV: {id: Nav; label: string; icon: (p: {size?: number}) => ReactElement}[] = [
  {id: 'dashboard', label: 'Dashboard', icon: IconDashboard},
  {id: 'tunnels', label: 'Tunnels', icon: IconTunnels},
  {id: 'connections', label: 'Connections', icon: IconConnections},
  {id: 'logs', label: 'Logs', icon: IconLogs},
  {id: 'settings', label: 'Settings', icon: IconSettings},
]

// Nav geometry shared by the buttons and the sliding indicator.
const NAV_ITEM_H = 40
const NAV_GAP = 4

function useTheme(): [Theme, () => void] {
  const [theme, setTheme] = useState<Theme>(
    () => (localStorage.getItem('pf-theme') === 'light' ? 'light' : 'dark'),
  )
  useEffect(() => {
    document.documentElement.setAttribute('data-theme', theme)
    localStorage.setItem('pf-theme', theme)
  }, [theme])
  const toggle = () => {
    // Cross-fade every surface while the token set swaps.
    const el = document.documentElement
    el.classList.add('pf-theme-anim')
    window.setTimeout(() => el.classList.remove('pf-theme-anim'), 420)
    setTheme(t => {
      const next = t === 'dark' ? 'light' : 'dark'
      // Persist to the backend (UI-only write); theme still applies on failure.
      SetTheme(next).catch(() => {})
      return next
    })
  }
  return [theme, toggle]
}

export default function App() {
  const status = useTick()
  const [nav, setNav] = useState<Nav>('dashboard')
  const [theme, toggleTheme] = useTheme()

  const isWizard = !status || status.mode === 'wizard' || status.role === ''

  if (isWizard) {
    return (
      <div className="h-full">
        <Wizard />
      </div>
    )
  }

  const s = status!
  const active = NAV.find(n => n.id === nav)!
  const activeIdx = NAV.findIndex(n => n.id === nav)

  return (
    <div className="flex h-full text-[var(--text)]">
      {/* Sidebar */}
      <aside className="flex w-56 shrink-0 flex-col border-r border-[var(--border)] bg-[var(--glass)] backdrop-blur-xl">
        <div className="flex items-center gap-2.5 px-4 py-4">
          <div
            className="grid h-9 w-9 shrink-0 place-items-center rounded-xl text-white shadow-[0_4px_16px_-2px_color-mix(in_srgb,var(--accent)_50%,transparent)]"
            style={{background: 'linear-gradient(135deg, var(--accent), var(--accent-2))'}}
          >
            <IconServer size={19} />
          </div>
          <div className="leading-tight">
            <div className="text-sm font-semibold tracking-tight">proxyforward</div>
            <div className="text-[11px] text-[var(--text-3)]">Minecraft tunnel</div>
          </div>
        </div>

        <nav className="relative flex-1 px-2 py-2">
          {/* Sliding active pill */}
          <div
            aria-hidden
            className="pointer-events-none absolute left-2 right-2 top-2 rounded-xl border border-[color-mix(in_srgb,var(--accent)_25%,var(--border))] transition-transform duration-300 [transition-timing-function:cubic-bezier(0.3,1.3,0.4,1)]"
            style={{
              height: NAV_ITEM_H,
              transform: `translateY(${activeIdx * (NAV_ITEM_H + NAV_GAP)}px)`,
              background: 'linear-gradient(90deg, color-mix(in srgb, var(--accent) 15%, transparent), color-mix(in srgb, var(--accent-2) 7%, transparent))',
            }}
          />
          <div className="relative flex flex-col" style={{gap: NAV_GAP}}>
            {NAV.map(item => {
              const Icon = item.icon
              const on = nav === item.id
              return (
                <button
                  key={item.id}
                  onClick={() => setNav(item.id)}
                  style={{height: NAV_ITEM_H}}
                  className={`group flex w-full items-center gap-3 rounded-xl px-3 text-sm transition-colors duration-200 ${
                    on
                      ? 'font-medium text-[var(--text)]'
                      : 'text-[var(--text-2)] hover:bg-[var(--panel)]/60 hover:text-[var(--text)]'
                  }`}
                >
                  <span className={`transition-all duration-200 ${on ? 'scale-110 text-[var(--accent)]' : 'group-hover:scale-105'}`}>
                    <Icon size={18} />
                  </span>
                  {item.label}
                </button>
              )
            })}
          </div>
        </nav>

        <div className="space-y-2 border-t border-[var(--border)] px-4 py-3 text-[11px] text-[var(--text-3)]">
          <div className="flex items-center gap-2">
            <Badge tone={s.role === 'gateway' ? 'accent' : 'neutral'}>
              {s.role === 'gateway' ? 'Gateway' : 'Agent'}
            </Badge>
            {s.mode === 'attached' && <Badge tone="good">Service</Badge>}
          </div>
          <div>v{s.version} · pid {s.pid}</div>
        </div>
      </aside>

      {/* Main column */}
      <div className="flex min-w-0 flex-1 flex-col">
        <header className="z-10 flex items-center justify-between border-b border-[var(--border)] bg-[var(--glass)] px-6 py-3.5 backdrop-blur-xl">
          <h1 key={nav} className="pf-fade text-lg font-semibold tracking-tight">{active.label}</h1>
          <div className="flex items-center gap-3">
            <GlobalStatusPill status={s} />
            <IconButton title={theme === 'dark' ? 'Switch to light' : 'Switch to dark'} onClick={toggleTheme}>
              <span key={theme} className="pf-fade inline-flex">
                {theme === 'dark' ? <IconSun size={17} /> : <IconMoon size={17} />}
              </span>
            </IconButton>
          </div>
        </header>

        <main key={nav} className="pf-page flex-1 overflow-y-auto p-6">
          <div className="mx-auto max-w-5xl">
            {nav === 'dashboard' && <Dashboard status={s} />}
            {nav === 'tunnels' && <Tunnels status={s} />}
            {nav === 'connections' && <Connections status={s} />}
            {nav === 'logs' && <Logs />}
            {nav === 'settings' && <Settings status={s} onThemeToggle={toggleTheme} theme={theme} />}
          </div>
        </main>
      </div>
    </div>
  )
}

function GlobalStatusPill({status}: {status: UIStatus}) {
  const isAgent = status.role === 'agent'
  const up = isAgent ? status.linkUp : status.agentConnected
  const [, force] = useState(0)
  useEffect(() => {
    const t = setInterval(() => force(x => x + 1), 1000)
    return () => clearInterval(t)
  }, [])
  // Backend-authoritative uptime, recomputed every render — the old useMemo
  // froze at "0s" because its deps never changed as time passed.
  const uptime = status.linkUpSinceMs ? fmtUptime(Date.now() - status.linkUpSinceMs) : null

  const label = isAgent
    ? up ? 'Connected' : 'Reconnecting…'
    : up ? 'Agent online' : 'Waiting for agent'
  const tone = up ? 'good' : isAgent ? 'bad' : 'warn'
  const color = {good: 'var(--good)', bad: 'var(--bad)', warn: 'var(--warn)'}[tone]

  return (
    <div className="flex items-center gap-2 rounded-full border border-[var(--border)] bg-[var(--panel)] px-3 py-1.5 text-xs shadow-[var(--shadow-soft)] transition-colors duration-300">
      <span
        className={`inline-flex h-2 w-2 rounded-full ${up ? 'pf-halo' : ''}`}
        style={{background: color, ['--halo' as string]: color}}
      />
      <span className="font-medium text-[var(--text-2)]">{label}</span>
      {isAgent && up && <span className="tabular-nums text-[var(--text-3)]">· {status.rttMillis} ms</span>}
      {up && uptime && <span className="tabular-nums text-[var(--text-3)]">· up {uptime}</span>}
    </div>
  )
}

function fmtUptime(ms: number): string {
  const s = Math.floor(ms / 1000)
  const d = Math.floor(s / 86400)
  const h = Math.floor((s % 86400) / 3600)
  const m = Math.floor((s % 3600) / 60)
  const sec = s % 60
  if (d > 0) return `${d}d ${h}h`
  if (h > 0) return `${h}h ${m}m`
  if (m > 0) return `${m}m ${sec}s`
  return `${sec}s`
}
