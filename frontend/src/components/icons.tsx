// Inline stroke icons (no dependency). Each inherits currentColor and takes an
// optional size; 1.75px strokes read crisply at 16–20px in the WebView.
import {SVGProps} from 'react'

type IconProps = SVGProps<SVGSVGElement> & {size?: number}

function Base({size = 18, children, ...rest}: IconProps & {children: React.ReactNode}) {
  return (
    <svg
      width={size} height={size} viewBox="0 0 24 24" fill="none"
      stroke="currentColor" strokeWidth="1.75" strokeLinecap="round" strokeLinejoin="round"
      {...rest}
    >{children}</svg>
  )
}

export const IconDashboard = (p: IconProps) => (
  <Base {...p}><rect x="3" y="3" width="7" height="9" rx="1.5"/><rect x="14" y="3" width="7" height="5" rx="1.5"/><rect x="14" y="12" width="7" height="9" rx="1.5"/><rect x="3" y="16" width="7" height="5" rx="1.5"/></Base>
)
export const IconTunnels = (p: IconProps) => (
  <Base {...p}><path d="M3 12a9 5 0 0 1 18 0"/><path d="M3 12v0M21 12v0"/><path d="M7 12v6M17 12v6M12 12v7"/></Base>
)
export const IconConnections = (p: IconProps) => (
  <Base {...p}><circle cx="6" cy="6" r="2.5"/><circle cx="18" cy="18" r="2.5"/><path d="M8 6h6a4 4 0 0 1 4 4v5.5"/></Base>
)
export const IconLogs = (p: IconProps) => (
  <Base {...p}><path d="M4 5h16M4 10h16M4 15h10M4 20h13"/></Base>
)
export const IconSettings = (p: IconProps) => (
  <Base {...p}><circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06a1.65 1.65 0 0 0 1.82.33H9a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z"/></Base>
)
export const IconCopy = (p: IconProps) => (
  <Base {...p}><rect x="9" y="9" width="12" height="12" rx="2"/><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/></Base>
)
export const IconCheck = (p: IconProps) => (
  <Base {...p}><path d="M20 6 9 17l-5-5"/></Base>
)
export const IconRefresh = (p: IconProps) => (
  <Base {...p}><path d="M21 12a9 9 0 1 1-2.64-6.36M21 3v6h-6"/></Base>
)
export const IconExternal = (p: IconProps) => (
  <Base {...p}><path d="M15 3h6v6M10 14 21 3M18 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h6"/></Base>
)
export const IconPlus = (p: IconProps) => (
  <Base {...p}><path d="M12 5v14M5 12h14"/></Base>
)
export const IconTrash = (p: IconProps) => (
  <Base {...p}><path d="M3 6h18M8 6V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2m3 0v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6"/></Base>
)
export const IconEdit = (p: IconProps) => (
  <Base {...p}><path d="M12 20h9M16.5 3.5a2.12 2.12 0 0 1 3 3L7 19l-4 1 1-4Z"/></Base>
)
export const IconMoon = (p: IconProps) => (
  <Base {...p}><path d="M21 12.79A9 9 0 1 1 11.21 3 7 7 0 0 0 21 12.79z"/></Base>
)
export const IconSun = (p: IconProps) => (
  <Base {...p}><circle cx="12" cy="12" r="4"/><path d="M12 2v2M12 20v2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M2 12h2M20 12h2M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4"/></Base>
)
export const IconShield = (p: IconProps) => (
  <Base {...p}><path d="M12 2 4 5v6c0 5 3.4 8.5 8 10 4.6-1.5 8-5 8-10V5z"/></Base>
)
export const IconBolt = (p: IconProps) => (
  <Base {...p}><path d="M13 2 3 14h8l-1 8 10-12h-8z"/></Base>
)
export const IconServer = (p: IconProps) => (
  <Base {...p}><rect x="3" y="4" width="18" height="7" rx="2"/><rect x="3" y="13" width="18" height="7" rx="2"/><path d="M7 7.5h.01M7 16.5h.01"/></Base>
)
export const IconGlobe = (p: IconProps) => (
  <Base {...p}><circle cx="12" cy="12" r="9"/><path d="M3 12h18M12 3a14 14 0 0 1 0 18 14 14 0 0 1 0-18z"/></Base>
)
export const IconLink = (p: IconProps) => (
  <Base {...p}><path d="M10 13a5 5 0 0 0 7 0l2-2a5 5 0 0 0-7-7l-1 1M14 11a5 5 0 0 0-7 0l-2 2a5 5 0 0 0 7 7l1-1"/></Base>
)
