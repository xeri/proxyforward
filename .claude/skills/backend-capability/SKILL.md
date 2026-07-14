---
name: backend-capability
description: Playbook for exposing a new backend capability to the proxyforward UI (engine feature → IPC → Wails binding → React hook). Use when adding any Go-side data or action the GUI will consume.
---

# Backend capability exposed to the UI (playbook A)

1. Implement in an `internal/` package; wire lifecycle in `engine.New` / `Run` if
   long-lived.
2. Read-side queries: add an `Op*` const + case in `engine/analytics_api.go` — never
   a new IPC message type. Live status: extend `ipc.Status` → `engine.Status()` →
   `app.UIStatus` + `applyIPCStatus` (mirror types, unix-ms ints — the Wails
   generator can't model cross-package embedded structs or `time.Time`).
3. Add the `app.App` method → `wails build` regenerates `frontend/wailsjs`. Never
   hand-edit the generated bindings; rebuild before touching the frontend.
4. Frontend: typed hook over `usePolled` + module cache; clamp server-side to fit
   the `MaxFrame` 64 KiB IPC frame; degrade for old daemons (empty result or latch —
   see the `analyticsUnsupported` rules in `app/analytics.go`; served `ipc.OpError`s
   are transient and must never latch it).
5. Add the devmock stub (`devmock.ts`) + a mock axis if the feature has distinct
   states — the Proxy fallback returns `() => {}`, so anything `.then()`ed throws
   without a stub.
6. Gate: `go test ./...`, `cd frontend && npm run build`, walk the relevant mock
   axes, then `wails dev`.
