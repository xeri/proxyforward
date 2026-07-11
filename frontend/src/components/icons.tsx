// Inline stroke icons (no dependency). One system: 24-unit grid, 1.75px round
// strokes, currentColor. Reads crisply at 16–20px in the WebView.
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

/* --- Navigation ---------------------------------------------------------- */
export const IconDashboard = (p: IconProps) => (
  <Base {...p}><rect x="3" y="3" width="7" height="9" rx="1.5"/><rect x="14" y="3" width="7" height="5" rx="1.5"/><rect x="14" y="12" width="7" height="9" rx="1.5"/><rect x="3" y="16" width="7" height="5" rx="1.5"/></Base>
)
export const IconTunnels = (p: IconProps) => (
  <Base {...p}><path d="M4 20v-7a8 8 0 0 1 16 0v7"/><path d="M3 20h18"/><path d="M12 20v-4.5"/></Base>
)
export const IconConnections = (p: IconProps) => (
  <Base {...p}><path d="M4 7h13"/><path d="M14 3.5 17.5 7 14 10.5"/><path d="M20 17H7"/><path d="M10 13.5 6.5 17 10 20.5"/></Base>
)
export const IconLogs = (p: IconProps) => (
  <Base {...p}><path d="M4 5h16M4 10h16M4 15h10M4 20h13"/></Base>
)
export const IconActivity = (p: IconProps) => (
  <Base {...p}><path d="M22 12h-4l-3 9L9 3l-3 9H2"/></Base>
)
export const IconSettings = (p: IconProps) => (
  <Base {...p}><circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06a1.65 1.65 0 0 0 1.82.33H9a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z"/></Base>
)

/* --- Window controls ------------------------------------------------------ */
export const IconMinimize = (p: IconProps) => (
  <Base {...p}><path d="M5 12h14"/></Base>
)
export const IconMaximize = (p: IconProps) => (
  <Base {...p}><rect x="5" y="5" width="14" height="14" rx="1.5"/></Base>
)
export const IconRestore = (p: IconProps) => (
  <Base {...p}><rect x="5" y="8.5" width="10.5" height="10.5" rx="1.5"/><path d="M8.5 5.5h8a2 2 0 0 1 2 2v8"/></Base>
)
export const IconClose = (p: IconProps) => (
  <Base {...p}><path d="M6 6l12 12M18 6 6 18"/></Base>
)

/* --- Actions -------------------------------------------------------------- */
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
export const IconSearch = (p: IconProps) => (
  <Base {...p}><circle cx="11" cy="11" r="7"/><path d="M20.5 20.5 16 16"/></Base>
)
export const IconCommand = (p: IconProps) => (
  <Base {...p}><path d="M15 6v12a3 3 0 1 0 3-3H6a3 3 0 1 0 3 3V6a3 3 0 1 0-3 3h12a3 3 0 1 0-3-3"/></Base>
)
export const IconArrowRight = (p: IconProps) => (
  <Base {...p}><path d="M5 12h14"/><path d="M13 6l6 6-6 6"/></Base>
)
export const IconChevronDown = (p: IconProps) => (
  <Base {...p}><path d="M6 9l6 6 6-6"/></Base>
)
export const IconChevronRight = (p: IconProps) => (
  <Base {...p}><path d="M9 6l6 6-6 6"/></Base>
)

/* --- Theme ---------------------------------------------------------------- */
export const IconMoon = (p: IconProps) => (
  <Base {...p}><path d="M21 12.79A9 9 0 1 1 11.21 3 7 7 0 0 0 21 12.79z"/></Base>
)
export const IconSun = (p: IconProps) => (
  <Base {...p}><circle cx="12" cy="12" r="4"/><path d="M12 2v2M12 20v2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M2 12h2M20 12h2M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4"/></Base>
)
export const IconMonitor = (p: IconProps) => (
  <Base {...p}><rect x="3" y="4" width="18" height="13" rx="2"/><path d="M9 21h6M12 17v4"/></Base>
)

/* --- Domain --------------------------------------------------------------- */
export const IconServer = (p: IconProps) => (
  <Base {...p}><rect x="3" y="4" width="18" height="7" rx="2"/><rect x="3" y="13" width="18" height="7" rx="2"/><path d="M7 7.5h.01M7 16.5h.01"/></Base>
)
export const IconGlobe = (p: IconProps) => (
  <Base {...p}><circle cx="12" cy="12" r="9"/><path d="M3 12h18M12 3a14 14 0 0 1 0 18 14 14 0 0 1 0-18z"/></Base>
)
export const IconLink = (p: IconProps) => (
  <Base {...p}><path d="M10 13a5 5 0 0 0 7 0l2-2a5 5 0 0 0-7-7l-1 1M14 11a5 5 0 0 0-7 0l-2 2a5 5 0 0 0 7 7l1-1"/></Base>
)
export const IconBroadcast = (p: IconProps) => (
  <Base {...p}><circle cx="12" cy="10" r="2"/><path d="M8.1 13.9a5.5 5.5 0 0 1 0-7.8M15.9 6.1a5.5 5.5 0 0 1 0 7.8"/><path d="M5.3 16.7a9.5 9.5 0 0 1 0-13.4M18.7 3.3a9.5 9.5 0 0 1 0 13.4"/><path d="M12 12v9"/></Base>
)
export const IconChip = (p: IconProps) => (
  <Base {...p}><rect x="7" y="7" width="10" height="10" rx="1.5"/><path d="M4 10h3M4 14h3M17 10h3M17 14h3M10 4v3M14 4v3M10 17v3M14 17v3"/></Base>
)
export const IconShield = (p: IconProps) => (
  <Base {...p}><path d="M12 2 4 5v6c0 5 3.4 8.5 8 10 4.6-1.5 8-5 8-10V5z"/></Base>
)
export const IconShieldCheck = (p: IconProps) => (
  <Base {...p}><path d="M12 2 4 5v6c0 5 3.4 8.5 8 10 4.6-1.5 8-5 8-10V5z"/><path d="M9 11.5l2 2 4-4.5"/></Base>
)
export const IconBolt = (p: IconProps) => (
  <Base {...p}><path d="M13 2 3 14h8l-1 8 10-12h-8z"/></Base>
)
export const IconKey = (p: IconProps) => (
  <Base {...p}><circle cx="8" cy="15" r="4"/><path d="M10.8 12.2 19 4M15.5 7.5 18 10M19 4l1 1"/></Base>
)
export const IconLock = (p: IconProps) => (
  <Base {...p}><rect x="5" y="11" width="14" height="9" rx="2"/><path d="M8 11V7a4 4 0 0 1 8 0v4"/></Base>
)
export const IconGauge = (p: IconProps) => (
  <Base {...p}><path d="M20.5 16.5a9 9 0 1 0-17 0"/><path d="M12 15l3.5-4.5"/><circle cx="12" cy="15.5" r="1.2"/></Base>
)
export const IconUsers = (p: IconProps) => (
  <Base {...p}><circle cx="9" cy="8" r="3.5"/><path d="M2.5 20a6.5 6.5 0 0 1 13 0"/><path d="M16 4.7a3.5 3.5 0 0 1 0 6.6M17.2 13.9A6.5 6.5 0 0 1 21.5 20"/></Base>
)
export const IconTerminal = (p: IconProps) => (
  <Base {...p}><path d="M5 7l5 5-5 5"/><path d="M12.5 17H19"/></Base>
)
export const IconFolder = (p: IconProps) => (
  <Base {...p}><path d="M3 7a2 2 0 0 1 2-2h4l2 2.5h8a2 2 0 0 1 2 2V17a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z"/></Base>
)
export const IconClock = (p: IconProps) => (
  <Base {...p}><circle cx="12" cy="12" r="9"/><path d="M12 7v5l3 2"/></Base>
)
export const IconDownload = (p: IconProps) => (
  <Base {...p}><path d="M12 4v11M6.5 10.5 12 16l5.5-5.5"/><path d="M5 20h14"/></Base>
)
export const IconUpload = (p: IconProps) => (
  <Base {...p}><path d="M12 20V9M6.5 13.5 12 8l5.5 5.5"/><path d="M5 4h14"/></Base>
)
export const IconSpark = (p: IconProps) => (
  <Base {...p}><path d="M12 3l1.9 5.6L19.5 10.5l-5.6 1.9L12 18l-1.9-5.6L4.5 10.5l5.6-1.9z"/></Base>
)
export const IconOffline = (p: IconProps) => (
  <Base {...p}><circle cx="12" cy="12" r="9"/><path d="M5.8 5.8l12.4 12.4"/></Base>
)
export const IconAlert = (p: IconProps) => (
  <Base {...p}><path d="M10.3 3.8 1.9 18a2 2 0 0 0 1.7 3h16.8a2 2 0 0 0 1.7-3L13.7 3.8a2 2 0 0 0-3.4 0z"/><path d="M12 9.5v4M12 17.2h.01"/></Base>
)
export const IconInfo = (p: IconProps) => (
  <Base {...p}><circle cx="12" cy="12" r="9"/><path d="M12 8h.01M12 11.5V16"/></Base>
)

/* --- Inputs --------------------------------------------------------------- */
export const IconEye = (p: IconProps) => (
  <Base {...p}><path d="M2 12s3.5-7 10-7 10 7 10 7-3.5 7-10 7-10-7-10-7z"/><circle cx="12" cy="12" r="3"/></Base>
)
export const IconEyeOff = (p: IconProps) => (
  <Base {...p}><path d="M9.9 5.2A9.5 9.5 0 0 1 12 5c6.5 0 10 7 10 7a13.2 13.2 0 0 1-2.2 2.9M6.1 6.1A13.2 13.2 0 0 0 2 12s3.5 7 10 7a9.5 9.5 0 0 0 4-.9M9.9 9.9a3 3 0 0 0 4.2 4.2"/><path d="M3 3l18 18"/></Base>
)
