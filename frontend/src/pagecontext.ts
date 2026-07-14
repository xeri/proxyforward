import {useEffect, useSyncExternalStore} from 'react'

/**
 * Screen-local context for the title bar's understated left strip (e.g.
 * Analytics sets "Last 7 days · 128 sessions"). A module store — same
 * pattern as players.ts — so screens don't thread props through Shell.
 * The TitleBar falls back to a status-derived default when unset.
 */

let value: string | null = null
const subs = new Set<() => void>()

function set(next: string | null) {
  if (next === value) return
  value = next
  for (const fn of subs) fn()
}

/** Screens call this with their context line; it clears on unmount. */
export function usePageContext(text: string | null) {
  useEffect(() => {
    set(text)
    return () => set(null)
  }, [text])
}

/** TitleBar subscribes here. */
export function useTitleContext(): string | null {
  return useSyncExternalStore(
    fn => {
      subs.add(fn)
      return () => { subs.delete(fn) }
    },
    () => value,
  )
}
