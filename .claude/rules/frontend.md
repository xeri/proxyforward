---
paths:
  - "frontend/**"
---

# Frontend conventions (moved from CLAUDE.md "Conventions in force" + Footguns)

Read `frontend/DESIGN.md` (the charter) before any UI work; pull polish work from
`docs/agent/polish-backlog.md` before inventing it. UI-change procedure: the
`ui-change` skill.

## React
- React 19 with classic hooks — no router (nav is `useState` in `App.tsx`), no state
  library (module stores + `useSyncExternalStore`, e.g. `pagecontext.ts`), no
  Actions/`use()`/compiler; don't introduce them piecemeal.
- Data layer = typed hooks over `usePolled` + module caches (`hooks.ts`,
  `players.ts`). View Transitions for nav and theme (`App.tsx go`,
  `theme.ts animateSwap`).
- Reuse the `ui.tsx` kit; screens must not fork their own pills/inputs/tables
  (`Pill` doc comment).
- Sentinels render as "—" (`state.ts hasRtt`); all four states (skeleton / data /
  written empty / honest unavailable) exist on every data surface; all motion gates
  on `prefersReduced()` (`motion.ts`).

## CSS
- Tokens in `tokens.css` are the only sizes/colors/durations new UI may use.
- `pf-*` recipes in `glass.css` / `motion.css` own *material and motion only* —
  positioning stays with the consumer. They beat Tailwind utilities at equal
  specificity.
- Load-bearing couplings — change both sides or nothing: `--sidebar-w` ↔ `Shell`
  grid, `--nav-item-h` ↔ `Sidebar.tsx ITEM_H`, `--dur-theme` ↔
  `theme.ts THEME_SWEEP_MS`. Root font 13.5 px scales every rem.

## Footguns
- `frontend/wailsjs/` is GENERATED — never hand-edit (a PreToolUse hook blocks it);
  it only updates via `wails build` / `wails dev`. After changing any bound Go
  type/method, rebuild before touching the frontend.
- WebView2 ≠ your browser: native `<select>`/checkbox don't theme (custom `Select` /
  `Checkbox` in `ui.tsx`); Edge injects reveal/clear icons into inputs (suppressed
  in `base.css`); clipboard needs the Wails runtime (`copyText` fallback chain).
- devmock's Proxy fallback returns `() => {}` — any runtime call whose result is
  `.then()`ed needs an explicit stub, and every new binding needs its stub added
  (`devmock.ts` header).
