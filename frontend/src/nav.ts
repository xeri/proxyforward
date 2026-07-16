import {ReactElement} from 'react'
import {
  IconActivity, IconAgents, IconAnalytics, IconConnections, IconDashboard, IconPlayers, IconSettings, IconTunnels,
} from './components/icons'

export type NavId = 'overview' | 'agents' | 'traffic' | 'players' | 'analytics' | 'tunnels' | 'activity' | 'settings'

export type NavItem = {
  id: NavId
  label: string
  icon: (p: {size?: number}) => ReactElement
  shortcut: string // digit paired with Ctrl
}

// The rail definition. `roles` limits an item to certain roles (omitted = both).
// Shortcuts are NOT stored here — they are assigned by position once the list is
// filtered for a role, so a hidden item never leaves a gap in the digit run.
type Role = 'agent' | 'gateway'
type NavDef = Omit<NavItem, 'shortcut'> & {roles?: Role[]}

// Agents is the gateway's fleet view; it sits right after Overview because the
// roster is the gateway operator's home base — the machines dialed into them.
const MAIN: NavDef[] = [
  {id: 'overview', label: 'Overview', icon: IconDashboard},
  {id: 'agents', label: 'Agents', icon: IconAgents, roles: ['gateway']},
  {id: 'traffic', label: 'Traffic', icon: IconConnections},
  {id: 'players', label: 'Players', icon: IconPlayers},
  {id: 'analytics', label: 'Analytics', icon: IconAnalytics},
  {id: 'tunnels', label: 'Tunnels', icon: IconTunnels},
  {id: 'activity', label: 'Activity', icon: IconActivity},
]

const SETTINGS: Omit<NavItem, 'shortcut'> = {id: 'settings', label: 'Settings', icon: IconSettings}

/** mainNav: the visible rail for a role, with Ctrl-digit shortcuts assigned by
 * position (1..n). Settings is pinned to the sidebar foot, outside this list. */
export function mainNav(role: string): NavItem[] {
  return MAIN
    .filter(n => !n.roles || n.roles.includes(role as Role))
    .map((n, i) => ({...n, shortcut: String(i + 1)}))
}

/** settingsNav: the foot-pinned Settings item; its shortcut follows the rail. */
export function settingsNav(role: string): NavItem {
  return {...SETTINGS, shortcut: String(mainNav(role).length + 1)}
}

/** navFor: the full ordered list (rail + settings) for a role — the keyboard
 * map and the command palette's Navigate section. */
export function navFor(role: string): NavItem[] {
  return [...mainNav(role), settingsNav(role)]
}

/** labelOf: a screen's display name, role-agnostic — for the title-bar context
 * strip, which only needs the label, never the shortcut. */
export function labelOf(id: NavId): string {
  return [...MAIN, SETTINGS].find(n => n.id === id)?.label ?? ''
}
