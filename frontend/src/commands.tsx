import {ReactNode} from 'react'
import {ExportDiagnostics, OpenConfigDir, PairingCode, RestartEngine} from '../wailsjs/go/app/App'
import {
  IconCopy, IconExternal, IconFolder, IconKey, IconMonitor, IconMoon, IconRefresh, IconSun,
} from './components/icons'
import {copyText} from './components/ui'
import {navFor, NavId} from './nav'
import {UIStatus} from './state'
import {setThemePref} from './theme'

export type CommandCtx = {
  status: UIStatus
  go: (id: NavId) => void
}

export type Command = {
  id: string
  title: string
  hint?: string
  icon?: ReactNode
  kbd?: string
  section: 'Navigate' | 'Appearance' | 'Actions'
  /** Gate by role / mode / link state; hidden when false. */
  when?: (ctx: CommandCtx) => boolean
  run: (ctx: CommandCtx) => void | Promise<void>
}

/** navCommands: the Navigate section, built from the role's live rail so the
 * gateway's Agents entry (and its shortcut) appear only for the gateway. */
export function navCommands(role: string): Command[] {
  return navFor(role).map((n): Command => ({
    id: `nav-${n.id}`,
    title: `Go to ${n.label}`,
    icon: <n.icon size={15} />,
    kbd: `Ctrl ${n.shortcut}`,
    section: 'Navigate',
    run: ctx => ctx.go(n.id),
  }))
}

// The static actions/appearance commands; the Navigate section is prepended per
// role by navCommands() at render time.
export const COMMANDS: Command[] = [
  {
    id: 'theme-light', title: 'Theme: Light', icon: <IconSun size={15} />, section: 'Appearance',
    run: () => setThemePref('light'),
  },
  {
    id: 'theme-dark', title: 'Theme: Dark', icon: <IconMoon size={15} />, section: 'Appearance',
    run: () => setThemePref('dark'),
  },
  {
    id: 'theme-system', title: 'Theme: System', hint: 'Follow Windows', icon: <IconMonitor size={15} />, section: 'Appearance',
    run: () => setThemePref('system'),
  },
  {
    id: 'copy-pairing', title: 'Copy pairing code', hint: 'Hand to your agent', icon: <IconKey size={15} />, section: 'Actions',
    when: ctx => ctx.status.role === 'gateway' && ctx.status.mode === 'engine',
    run: () => PairingCode().then(copyText).catch(() => {}),
  },
  {
    id: 'copy-public-ip', title: 'Copy public address', icon: <IconCopy size={15} />, section: 'Actions',
    when: ctx => !!ctx.status.publicIp,
    run: ctx => copyText(ctx.status.publicIp),
  },
  {
    id: 'copy-peer', title: 'Copy peer address', icon: <IconCopy size={15} />, section: 'Actions',
    when: ctx => !!ctx.status.peerAddr,
    run: ctx => copyText(ctx.status.peerAddr),
  },
  {
    id: 'restart-engine', title: 'Restart engine', hint: 'Reconnects with current settings', icon: <IconRefresh size={15} />, section: 'Actions',
    when: ctx => ctx.status.mode === 'engine',
    run: () => RestartEngine().catch(() => {}),
  },
  {
    id: 'export-diagnostics', title: 'Export diagnostics', hint: 'Redacted support bundle', icon: <IconExternal size={15} />, section: 'Actions',
    run: () => ExportDiagnostics().then(() => {}).catch(() => {}),
  },
  {
    id: 'open-config', title: 'Open config folder', icon: <IconFolder size={15} />, section: 'Actions',
    run: () => OpenConfigDir().catch(() => {}),
  },
]

/** Fuzzy subsequence scorer: consecutive-run and word-start bonuses, light
 * length penalty. Returns -1 when the query is not a subsequence. */
export function fuzzyScore(text: string, query: string): number {
  const t = text.toLowerCase()
  const q = query.toLowerCase().replace(/\s+/g, '')
  if (!q) return 0
  let ti = 0, total = 0, streak = 0
  for (const ch of q) {
    const at = t.indexOf(ch, ti)
    if (at < 0) return -1
    streak = at === ti ? streak + 1 : 1
    const wordStart = at === 0 || t[at - 1] === ' ' || t[at - 1] === ':'
    total += streak * 2 + (wordStart ? 3 : 0)
    ti = at + 1
  }
  return total - t.length * 0.01
}
