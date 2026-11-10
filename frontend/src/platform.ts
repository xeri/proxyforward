import {Environment} from '../wailsjs/runtime/runtime'

/**
 * The host OS as the Wails runtime reports it ('windows' | 'linux' |
 * 'darwin'), learned once at startup. '' until it arrives and in a plain
 * browser (vite dev). Only the few native seams that genuinely differ per OS
 * read it — the window background (theme.ts) and the clipboard (ui.tsx
 * copyText) — never layout or copy.
 */
let platform = ''
const waiting = new Set<(p: string) => void>()

try {
  Environment().then(env => {
    platform = env.platform
    waiting.forEach(fn => fn(platform))
    waiting.clear()
  }, () => {})
} catch {
  /* plain browser (vite dev) */
}

export function hostPlatform(): string {
  return platform
}

/** Runs fn with the platform now if known, else once it arrives. */
export function onHostPlatform(fn: (p: string) => void) {
  if (platform) fn(platform)
  else waiting.add(fn)
}
