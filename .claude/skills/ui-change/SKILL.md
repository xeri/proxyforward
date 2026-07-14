---
name: ui-change
description: Playbook for UI-only changes to proxyforward's React frontend — polish, new surfaces, screen edits. Use before styling or component work; defines the state-matrix walk that "done" requires.
---

# UI-only change (playbook D)

1. Read `frontend/DESIGN.md` first; pull from `docs/agent/polish-backlog.md` before
   inventing polish work. The per-screen judgment sequence (identity surface, box
   test, label recipe, color/motion audits, spacing) is
   `docs/agent/reasoning.md` §4 — apply it in order.
2. Iterate in `cd frontend && npm run dev` against the mock axes
   (`docs/agent/commands.md`); no Go needed.
3. Tokens/kit only; one identity surface per screen; metadata is type on
   whitespace, not another card; label recipe = `Overline`.
4. Design all four states (skeleton / data / written empty / honest unavailable);
   sentinels render as "—"; both themes × both roles × Animations Off must look
   intentional.
5. Never build UI atop a Reality-check stub (root CLAUDE.md table) — that's an
   escalation trigger.
6. Gate: `npm run build` (tsc is the only checker), walk the relevant mock axes,
   then spot-check in `wails dev` (WebView2 ≠ your browser: clipboard, native
   controls, input icons).
