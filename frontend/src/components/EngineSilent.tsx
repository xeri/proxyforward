import {Button, FatalStatement} from './ui'
import {useAlert} from './Announcer'
import {CRASH_LOG_PATH} from './ErrorBoundary'
import {FIRST_TICK_MS} from '../state'
import {OpenConfigDir} from '../../wailsjs/go/app/App'

/**
 * The first-tick dead end, written down. `useTick` waits FIRST_TICK_MS for a
 * status snapshot; past that the spinner is a lie — it says "loading" about a
 * session that never started, and there is nothing behind it to wait for.
 *
 * This is the *before* case only. `useTickStale` (the title bar's "Syncing…")
 * covers snapshots that stop arriving, and it is the better instrument there —
 * but it can only describe a stream that once existed.
 *
 * `reason` is whatever the rejected `Status()` call said, verbatim; it is empty
 * when the call simply never settled, because silence has no reason and
 * inventing one would be worse than the "—" rule it echoes.
 */
export function EngineSilent({reason, onRetry}: {reason: string; onRetry: () => void}) {
  useAlert(`The engine is not answering. ${reason}`)
  return (
    <div className="mx-auto flex h-full w-full max-w-[var(--content-max)] items-center px-[var(--page-pad)]">
      <div className="pf-rise w-full">
        <FatalStatement
          eyebrow="no answer"
          title="The engine is not answering"
          detail={
            <>
              The window is up, but no status has arrived in {Math.round(FIRST_TICK_MS / 1000)} seconds.
              The engine may still be starting, or it may have stopped before this window reached it.
              {reason && <span className="mt-2 block text-sub text-[var(--text-3)]">{reason}</span>}
            </>
          }
          action={
            <div className="space-y-5">
              <div className="flex flex-wrap items-center gap-2">
                <Button variant="primary" size="sm" onClick={onRetry}>Try again</Button>
                <Button variant="ghost" size="sm" onClick={() => OpenConfigDir().catch(() => {})}>
                  Open the config folder
                </Button>
              </div>
              <p className="max-w-prose text-caption italic text-[var(--text-3)]">
                A crash on startup is written to {CRASH_LOG_PATH}.
              </p>
            </div>
          }
        />
      </div>
    </div>
  )
}
