---
name: dep-bump
description: Playbook for bumping a proxyforward dependency (Go module or npm package), including the yamux and Wails special cases. Use for any go.mod or package.json version change.
---

# Dependency bump (playbook E)

1. `go get <mod>@<v> && go mod tidy`.
   - For **yamux**: re-verify the `muxConfig` assumptions (`transport/yamux.go` —
     keepalive OFF, window size, long write timeout) against the upstream changelog;
     the heartbeat-owns-liveness invariant depends on them.
   - For **wails**: diff the regenerated `frontend/wailsjs`, re-test the frameless
     window + `tray_spike.go`.
2. Frontend: bump in `frontend/package.json`, `npm install`, `npm run build`, walk
   the mock axes.
3. Gate: full Go suite + burst (`go test -run TestBurst -v ./internal/e2e/`) +
   `wails build` boots.
