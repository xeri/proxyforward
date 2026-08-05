import React from 'react'
import {createRoot} from 'react-dom/client'
// Self-hosted faces (unicode-range subsets; glyphs outside them fall back to
// the system stacks). No network fetch — the woff2 files ship inside the
// binary like every other asset. Spline Sans Mono is the working voice of the
// app (its italic is the annotation voice: hints, placeholders, provisional
// states); Cormorant Unicase surfaces only for display type (titles, metrics).
import '@fontsource-variable/spline-sans-mono/index.css'
import '@fontsource-variable/spline-sans-mono/wght-italic.css'
import '@fontsource/cormorant-unicase/500.css'
import '@fontsource/cormorant-unicase/700.css'
// The remaining four voices of locked direction 3a, each with exactly one job:
// Gruppo labels and nav, MuseoModerno helper sentences, Red Hat Mono the
// machine-verbatim voice (codes, hosts, commands, logs), Doto every
// free-standing number, Genos button labels. Self-hosted like the two above —
// the app ships offline, so a webfont CDN is not an option.
import '@fontsource/gruppo/400.css'
import '@fontsource/museomoderno/300.css'
import '@fontsource/museomoderno/400.css'
import '@fontsource/museomoderno/600.css'
import '@fontsource/red-hat-mono/400.css'
import '@fontsource/red-hat-mono/600.css'
import '@fontsource/doto/700.css'
import '@fontsource/doto/900.css'
import '@fontsource/genos/500.css'
import '@fontsource/genos/700.css'
import './style.css'
import App from './App'
import {initTheme} from './theme'
import {initFx} from './fx'
import {initMotion} from './motion'

// Resolve and apply the persisted theme before first paint (no flash). The
// backend config is the source of truth; localStorage mirrors it for startup.
initTheme()
initFx()
initMotion()

function mount() {
  createRoot(document.getElementById('root')!).render(
    <React.StrictMode>
      <App />
    </React.StrictMode>,
  )
}

// In a plain browser (vite dev) there is no WebView2 host: install a fake Wails
// bridge before mounting so bindings resolve. Excluded from production builds.
if (import.meta.env.DEV) {
  import('./devmock').then(m => { m.installDevMock(); mount() })
} else {
  mount()
}
