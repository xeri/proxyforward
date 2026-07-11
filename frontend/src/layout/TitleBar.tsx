import {UIStatus} from '../state'
import {toggleTheme, useTheme} from '../theme'
import {ConnectionPill} from '../components/ConnectionPill'
import {IconButton, Kbd} from '../components/ui'
import {IconCommand, IconMoon, IconServer, IconSun} from '../components/icons'
import {WindowControls} from './WindowControls'

/**
 * Frameless-window title bar. The bar is one continuous drag region; every
 * interactive child opts out with pf-no-drag. It fills the floating glass
 * island (Shell); in the wizard it carries the brand on its own.
 */
export function TitleBar({status, brand = false, onPalette}: {
  status?: UIStatus | null
  brand?: boolean
  onPalette?: () => void
}) {
  const {resolved} = useTheme()
  return (
    <div className="pf-drag flex h-full items-stretch" onContextMenu={e => e.preventDefault()}>
      <div className="flex min-w-0 flex-1 items-center gap-2.5 pl-4">
        {brand && (
          <>
            <div className="grid h-6 w-6 shrink-0 place-items-center rounded-[var(--r-sm)] bg-[var(--accent)] text-[var(--accent-contrast)]">
              <IconServer size={14} />
            </div>
            <span className="truncate text-[13px] font-semibold tracking-tight">proxyforward</span>
          </>
        )}
      </div>

      <div className="pf-no-drag flex items-center gap-1.5 pr-1.5">
        {status && <ConnectionPill status={status} />}
        {onPalette && (
          <button
            onClick={onPalette}
            title="Command palette"
            className="pf-press flex items-center gap-1.5 rounded-[var(--r-sm)] border border-transparent px-2 py-1 text-xs text-[var(--text-3)] transition-colors hover:border-[var(--border)] hover:bg-[var(--panel-2)] hover:text-[var(--text)]"
          >
            <IconCommand size={13} />
            <Kbd>Ctrl K</Kbd>
          </button>
        )}
        <IconButton title={resolved === 'dark' ? 'Switch to light' : 'Switch to dark'} onClick={toggleTheme}>
          <span key={resolved} className="pf-fade inline-flex">
            {resolved === 'dark' ? <IconSun size={16} /> : <IconMoon size={16} />}
          </span>
        </IconButton>
      </div>

      <div className="pf-no-drag ml-1 flex items-stretch">
        <div className="pf-sep-v my-2" aria-hidden />
        <WindowControls />
      </div>
    </div>
  )
}
