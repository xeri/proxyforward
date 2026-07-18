import { ReactNode, useEffect } from 'react'

/** DSRoot: the preview/design root wrapper. The app's index.html hosts the
 * #pf-refract SVG displacement filter and main.tsx stamps data-theme /
 * data-motion / data-fx on <html>; outside the app shell (preview cards,
 * claude.ai/design renders) this component supplies both. Dark is the token
 * default — pass theme="light" for the light set. data-fx stays unset by
 * default (full glass); motion defaults to "on" like the app. */
export function DSRoot({ theme, fx, motion = 'on', children }: {
  theme?: 'dark' | 'light'
  fx?: 'low' | 'high'
  motion?: 'on' | 'off' | 'system'
  children?: ReactNode
}) {
  useEffect(() => {
    const el = document.documentElement
    if (theme === 'light') el.dataset.theme = 'light'
    else delete el.dataset.theme
    if (fx) el.dataset.fx = fx
    else delete el.dataset.fx
    if (motion === 'system') delete el.dataset.motion
    else el.dataset.motion = motion
    // Host pages (preview cards, design shells) often pin an opaque body
    // background; the app's canvas gradient lives on <html> (base.css), so
    // clear the body to let it show — same page anatomy as the real app.
    document.body.style.background = 'transparent'
  }, [theme, fx, motion])
  return (
    <div style={{ position: 'relative', minHeight: '100%' }}>
      {/* The app paints this gradient on <html> (base.css); outside the shell
          the host page owns the background (preview cards pin it white), so
          the wrapper re-paints the token background behind its children.
          Absolute, not fixed: preview cells are transform-contained, and a
          fixed element reads as escaped content to overflow checkers. */}
      <div
        aria-hidden
        style={{
          position: 'absolute',
          inset: 0,
          zIndex: -1,
          background: 'linear-gradient(155deg, var(--bg) 0%, var(--bg-base) 100%)',
        }}
      />
      <svg width="0" height="0" style={{ position: 'absolute' }} aria-hidden="true">
        <filter id="pf-refract">
          <feTurbulence type="fractalNoise" baseFrequency="0.012 0.02" numOctaves={1} />
          <feDisplacementMap in="SourceGraphic" scale={12} />
        </filter>
      </svg>
      {children}
    </div>
  )
}
