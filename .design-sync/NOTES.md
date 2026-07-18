# design-sync notes — proxyforward

- `frontend/` is a private Wails app, not a packaged library: no dist entry, no
  shipped `.d.ts`. The converter runs in **synth-entry mode** — `cfg.entry`
  deliberately points at a nonexistent path (`frontend/dist/.no-lib-entry.js`),
  which anchors `PKG_DIR` at `frontend/` and soft-falls into src synthesis from
  `srcDir: src/components`. The `[NO_DIST] --entry ... doesn't exist` warn on
  every build is **expected**, not a failure.
- The 46 carded components are all pinned in `componentSrcMap` (37 live in
  `src/components/ui.tsx`). App-bound components (CommandPalette, PowerControls,
  RoleSwitcher, SetupBackup) and the ~53 icons ride the bundle via the synth
  entry but get no cards.
- **CSS**: no styles build exists apart from the vite app build. `buildCmd`
  builds the app, then `prepare-css.mjs` copies `frontend/dist/assets/*.css` to
  `frontend/.ds-css/app.css` (gitignored; cssEntry is workspace-bounded to the
  package dir, so it cannot live under `.design-sync/`) with `@font-face`
  stripped (their urls are root-absolute `/assets/*.woff2` and would dangle). Fonts ship via
  `extraFonts` → `@fontsource-variable/inter`. Re-run `buildCmd` whenever
  frontend styles change.
- **DSRoot** (`.design-sync/ds-root.tsx`, wired via `extraEntries` +
  `provider`): outside the app shell nothing hosts the `#pf-refract` SVG filter
  (index.html) or stamps `data-theme`/`data-motion`/`data-fx` (main.tsx).
  DSRoot supplies both, and clears the host page's opaque `body` background so
  the app's canvas gradient (on `html`, base.css) shows. Dark is the token
  default; light is `theme="light"`.
- **Windows install gotchas**: `npm ci` in `frontend/` gets blocked by running
  dev-server node processes holding files in `node_modules` — and a died-midway
  `npm ci` leaves a partial tree whose symptom is `'tsc' is not recognized`
  (vite never runs, and piping the build output hides the real exit code).
  Repair with `npm install`, verify `frontend/node_modules/.bin/tsc` exists.
  The converter deps' `npm i` warns about esbuild's blocked postinstall
  (allow-scripts policy); the prebuilt binary ships via optionalDependencies,
  so esbuild works regardless.
- **Known render warns** (triaged, expected on every sync):
  - `[FONT_MISSING] "Cascadia Mono"` — accepted substitute, user's explicit OK
    (2026-07-18). The app treats it as a Windows system font and never ships
    it; the fallback chain (`ui-monospace`, `Consolas`, `monospace`) is the
    deliberate convention. Do not re-ask unless the user reopens it.
  - `[NO_DIST] --entry frontend/dist/.no-lib-entry.js doesn't exist` — the
    synth-entry anchor trick, see the first bullet.
- The render check runs with **Brave** as the chromium:
  `DS_CHROMIUM_PATH="C:\Program Files\BraveSoftware\Brave-Browser\Application\brave.exe"`
  (no ms-playwright cache on this machine; playwright + typescript@5 live in
  `.ds-sync/node_modules` — typescript must stay v5, v7's API breaks the
  validator's .d.ts check silently).
- The preview harness pins `body{background:#fff}`; DSRoot paints the token
  gradient behind children and clears the body background. Without it every
  dark-theme card reads as empty ([RENDER_THIN] × 13) — near-white text on white.

## Preview harness geometry (folded from wave 1)

- Every story cell (and the `?story=` single wrapper) carries
  `transform: translateZ(0)` — cells are containing blocks for `fixed`
  descendants and clip floated overlays. Consequences, all confirmed:
  - DSRoot's `position:fixed; inset:0` backdrop shrinks to a content-height
    band per cell; the canvas below is app.css's own `html` gradient (which
    DSRoot exposes by clearing `body`). Cosmetic; every graded sheet shipped
    with it. If DSRoot is revised: paint the backdrop `position:absolute` on a
    full-height wrapper instead of `fixed`.
  - WorldMap-style anchored tooltips clip at cell edges — hover a country near
    the map's center (BR), not the edge (US).
  - A component whose portal escapes to `document.body` (Menu, Modal) needs
    `cfg.overrides.<Name> = {cardMode:"single", viewport:"WxH"}`.
- **Changing `cfg.overrides`/`cfg.titleMap` for a component after a full build
  trips `[CONFIG_STALE]`** in every scoped `preview-rebuild` that targets it,
  and only a full `package-build.mjs` (orchestrator-only) re-stamps. Add
  overrides BEFORE launching waves that touch those components.
- Cell screenshots are fixed-viewport: short content sits above a tall gradient
  run. Harness framing, not a collapsed layout — never "fix" with min-heights.

## Authoring previews (folded from wave 1)

- Glue styling is inline styles only: the shipped CSS is the app's Tailwind JIT
  output, so any utility combo the app never used silently doesn't exist
  (Skeleton geometry etc.).
- No color-emoji font on this capture host: flags render as bare letter pairs
  ("DE") — use the app's own 🌐 fallback glyph in previews. Same family as the
  Cascadia Mono substitute; grade through it.
- SegmentedControl is block-level grid — wrap in `display:flex` or it stretches
  full width. StatTile groups use the app's real 2-column rhythm (Overview.tsx),
  not invented 4-across sweeps.
- Component truth that looks like a defect (caption, don't fight): Switch has no
  disabled dimming of its own (Toggle's row supplies it); IconButton's danger
  red is hover-only; StatusDot's empty-label form reads as color-alone in
  isolation (left out of sweeps deliberately).
- MenuItem previews on a static `pf-menu`-classed div (material recipes carry no
  positioning) — the real Menu portals to body and escapes the cell clip.
- Tick-fed components (ConnectionPill via `useTickStale`) judge liveness by
  status-prop identity: re-mint the status object ~1 Hz in previews or the
  capture shows "Syncing…". ConnectionPill never touches the Wails runtime;
  BandwidthPanel's default `useBandwidthHistory` DOES throw without one —
  always inject a stub `useHistory` (canned `HistoryResult`, or `null` for the
  honest unavailable state), and size canned buckets to the pinned range's
  `RANGES[range].windowMs` (charts right-anchor their domain; a short series
  renders as a sliver).
- AvatarImg previews exercise the real `/pf/avatar/` 404 → `onError` → offline
  pixel-head SVG path. That IS the honest render (devmock ids only work under
  `__pfDevMock`).
- BandwidthPanel reads `pf-chart-range` + series prefs from localStorage; a
  clean capture profile gets defaults ('1m', dl+ul). Other ranges need the pref
  seeded.
- Interaction/animation-only states are recorded as uncapturable, never
  simulated: hovers, focus glows, drags, indicator glides, shimmer/breathing/
  spin, CopyButton's "Copied" flash, Signal Glass pointer wake.
- Go-style raw error strings (`connect gateway: dial tcp …: connectex: …`) make
  ErrorBanner read like the real screens (they render `String(e)`).

## Session history

- 2026-07-18: project recreated once — the original Claude Design project
  vanished mid-run (404 after a session-limit interruption; cause unknown,
  possibly login change). All artifacts are local, so recreate + re-push is
  cheap; the config pin is the current project.
