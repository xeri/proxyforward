import {useEffect, useState} from 'react'
import {Quit, WindowIsMaximised, WindowMinimise, WindowToggleMaximise} from '../../wailsjs/runtime/runtime'
import {IconClose, IconMaximize, IconMinimize, IconRestore} from '../components/icons'

/**
 * Windows-style caption controls for the frameless window. Full-height hit
 * targets, quiet glyphs that sharpen on hover, the canonical red close wash.
 * Isolated from the drag region by the parent's pf-no-drag.
 */
export function WindowControls() {
  const [maximised, setMaximised] = useState(false)

  const refresh = () => {
    // Promise.resolve guards the dev mock, whose stub returns undefined.
    Promise.resolve(WindowIsMaximised())
      .then(v => setMaximised(!!v))
      .catch(() => {})
  }
  useEffect(() => {
    refresh()
    window.addEventListener('resize', refresh)
    return () => window.removeEventListener('resize', refresh)
  }, [])

  const base =
    'inline-flex h-full w-[46px] items-center justify-center text-[var(--text-3)] transition-colors duration-150'
  return (
    <div className="flex h-full items-stretch">
      <button
        aria-label="Minimize" title="Minimize"
        onClick={() => { try { WindowMinimise() } catch { /* dev */ } }}
        className={`${base} hover:bg-[var(--panel-2)] hover:text-[var(--text)]`}
      >
        <IconMinimize size={13} />
      </button>
      <button
        aria-label={maximised ? 'Restore' : 'Maximize'} title={maximised ? 'Restore' : 'Maximize'}
        onClick={() => {
          try { WindowToggleMaximise() } catch { /* dev */ }
          window.setTimeout(refresh, 120)
        }}
        className={`${base} hover:bg-[var(--panel-2)] hover:text-[var(--text)]`}
      >
        {maximised ? <IconRestore size={13} /> : <IconMaximize size={12} />}
      </button>
      <button
        aria-label="Close" title="Close"
        onClick={() => { try { Quit() } catch { /* dev */ } }}
        className={`${base} hover:bg-[#d13438] hover:text-white`}
      >
        <IconClose size={14} />
      </button>
    </div>
  )
}
