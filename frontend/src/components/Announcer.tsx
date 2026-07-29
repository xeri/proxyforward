import {useEffect, useSyncExternalStore} from 'react'

/**
 * The app's two ARIA live regions, and the module store that feeds them — the
 * same pattern as `pagecontext.ts`, so nothing has to thread props through
 * Shell to speak.
 *
 * Why a store and not props: a live region only announces text that changes
 * *while it is already mounted*, so the regions must outlive every screen and
 * be mounted before anything has something to say. `Announcer` is rendered once
 * at the top of `App`, above the error boundary; `announce()` is how any
 * surface reaches it.
 *
 * Two channels, and the choice between them is not stylistic:
 *   polite    — status transitions. Queued behind whatever the reader is
 *               currently saying; never interrupts.
 *   assertive — a fatal, or a decision waiting on the operator (a config
 *               consent proposal). Interrupts. Anything that can wait is polite.
 *
 * This is a mechanism, not a policy: App drives the polite channel from the
 * tick and the assertive one from `engineFatal`. Consent proposals do not ride
 * the tick (`UIStatus` carries no consent field — they are polled by the
 * surfaces that own them), so those surfaces call `announce(…, true)`
 * themselves when they adopt it.
 */

// An invisible marker, toggled on every write, so an identical repeat is still
// a DOM change. Without it a second "the link is down" — a genuinely new event
// — would be silently skipped, because the text node did not move.
const MARK = '\u200b' // zero-width space: a change to the DOM, nothing to a voice

let politeOut = ''
let assertiveOut = ''
const subs = new Set<() => void>()

function next(prev: string, text: string): string {
  const t = text.trim()
  if (!t) return '' // an emptied region says nothing, and is not announced
  return prev.endsWith(MARK) ? t : t + MARK
}

/**
 * Say something. Empty text clears that channel — which is how a resolved alarm
 * stops sitting in the accessibility tree behind a healthy console.
 */
export function announce(text: string, urgent = false) {
  if (urgent) {
    const v = next(assertiveOut, text)
    if (v === assertiveOut) return
    assertiveOut = v
  } else {
    const v = next(politeOut, text)
    if (v === politeOut) return
    politeOut = v
  }
  for (const fn of subs) fn()
}

function subscribe(fn: () => void): () => void {
  subs.add(fn)
  return () => { subs.delete(fn) }
}

/**
 * Hold an assertive announcement for as long as this component is mounted, and
 * clear it on the way out.
 *
 * For the surfaces that *are* the alarm — the error-boundary fallbacks, the
 * first-tick dead end. Marking those whole surfaces `role="alert"` would read
 * their evidence aloud too: a Disclosure's folded body is clipped
 * (`grid-template-rows: 0fr`), not hidden, so a component stack sits in the
 * accessibility tree and an alert would recite every line of it. One sentence
 * through here instead.
 */
export function useAlert(text: string) {
  useEffect(() => {
    announce(text, true)
    return () => announce('', true)
  }, [text])
}

/** Mounted once, at the top of App. Renders nothing a sighted user can see. */
export function Announcer() {
  const polite = useSyncExternalStore(subscribe, () => politeOut)
  const assertive = useSyncExternalStore(subscribe, () => assertiveOut)
  return (
    <>
      <div className="sr-only" role="status" aria-live="polite" aria-atomic="true">{polite}</div>
      <div className="sr-only" role="alert" aria-live="assertive" aria-atomic="true">{assertive}</div>
    </>
  )
}
