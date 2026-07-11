import {ReactElement} from 'react'
import {
  IconActivity, IconConnections, IconDashboard, IconSettings, IconTunnels,
} from './components/icons'

export type NavId = 'overview' | 'traffic' | 'tunnels' | 'activity' | 'settings'

export type NavItem = {
  id: NavId
  label: string
  icon: (p: {size?: number}) => ReactElement
  shortcut: string // digit paired with Ctrl
}

/** Main rail. Settings is pinned to the sidebar foot, outside this list. */
export const NAV_MAIN: NavItem[] = [
  {id: 'overview', label: 'Overview', icon: IconDashboard, shortcut: '1'},
  {id: 'traffic', label: 'Traffic', icon: IconConnections, shortcut: '2'},
  {id: 'tunnels', label: 'Tunnels', icon: IconTunnels, shortcut: '3'},
  {id: 'activity', label: 'Activity', icon: IconActivity, shortcut: '4'},
]

export const NAV_SETTINGS: NavItem = {
  id: 'settings', label: 'Settings', icon: IconSettings, shortcut: '5',
}
