// AvatarImg: the one player-head <img>. The asset server answers a cold head
// instantly with a no-cache placeholder while it warms the real render in the
// background (app/avatars.go), so this component re-asks on a short backoff:
// placeholders refetch (no-cache), real heads come back from browser cache
// for free. onError falls to the inline placeholder SVG so a dead asset
// server never leaves a broken-image glyph on the wall.
import {useEffect, useState} from 'react'
import {OFFLINE_SVG, avatarUrl} from '../avatars'

// Heads whose real render already arrived once this session — no retry churn
// on remounts (the browser cache serves them instantly anyway).
const settled = new Set<string>()

// Retry delays after first paint; warms usually land well inside the first.
const RETRY_AT_MS = [4_000, 12_000]

export function AvatarImg({id, size, px, className = ''}: {
  id: string
  /** Requested render size (server-side pixels, 16–128). */
  size: number
  /** Displayed size in CSS pixels (defaults to size). */
  px?: number
  className?: string
}) {
  const [bump, setBump] = useState(0)
  const [failed, setFailed] = useState(false)

  useEffect(() => {
    setFailed(false)
    if (settled.has(id)) return
    const timers = RETRY_AT_MS.map((ms, i) =>
      window.setTimeout(() => setBump(i + 1), ms))
    return () => timers.forEach(clearTimeout)
  }, [id])

  const dim = px ?? size
  if (failed) {
    return (
      <img src={OFFLINE_SVG} alt="" width={dim} height={dim}
        className={`[image-rendering:pixelated] ${className}`} />
    )
  }
  return (
    <img
      // bump busts the element (not the URL): the placeholder is no-cache so
      // a remount refetches; a settled head re-serves from browser cache.
      key={bump}
      src={avatarUrl(id, size)}
      alt=""
      loading="lazy"
      decoding="async"
      width={dim}
      height={dim}
      className={`[image-rendering:pixelated] ${className}`}
      onLoad={() => { if (bump >= RETRY_AT_MS.length) settled.add(id) }}
      onError={() => setFailed(true)}
    />
  )
}
