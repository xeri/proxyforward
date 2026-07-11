import React from 'react'
import {createRoot} from 'react-dom/client'
import './style.css'
import App from './App'

// Apply the persisted theme before first paint to avoid a flash. The backend
// config is the source of truth; localStorage mirrors it for instant startup.
const saved = localStorage.getItem('pf-theme')
document.documentElement.setAttribute('data-theme', saved === 'light' ? 'light' : 'dark')

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
