// Copies the app's compiled CSS to a stable path for cfg.cssEntry.
// frontend has no library build, so the design-sync bundle rides the vite app
// build: frontend/dist/assets/index-<hash>.css (tailwind utilities + tokens +
// glass + motion + base). @font-face blocks are stripped — their urls are
// root-absolute (/assets/*.woff2) and would dangle once the file moves; fonts
// ship separately via cfg.extraFonts (@fontsource-variable/inter).
import { mkdirSync, readFileSync, readdirSync, writeFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

const here = dirname(fileURLToPath(import.meta.url)) // .design-sync/
const assets = join(here, '..', 'frontend', 'dist', 'assets')
const css = readdirSync(assets).filter((n) => n.endsWith('.css'))
if (css.length !== 1) {
  console.error(`expected exactly one .css in ${assets}, found ${css.length} — run the frontend build first`)
  process.exit(1)
}
let text = readFileSync(join(assets, css[0]), 'utf8')
text = text.replace(/@font-face\{[^{}]*\}/g, '')
// cssEntry is workspace-bounded to the package dir, so the copy lives under
// frontend/ (gitignored), not .design-sync/.cache.
const outDir = join(here, '..', 'frontend', '.ds-css')
mkdirSync(outDir, { recursive: true })
writeFileSync(join(outDir, 'app.css'), text)
console.log(`prepared ${css[0]} -> frontend/.ds-css/app.css (${text.length} bytes)`)
