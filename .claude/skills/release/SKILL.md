---
name: release
description: Playbook for cutting a proxyforward release — there is no pipeline; this checklist is the whole process. Releases are an escalation trigger: confirm with the human first.
---

# Cut a release (playbook F)

Cutting or tagging a release is an escalation trigger — confirm with the human.

1. Clean tree; `go test ./...` and `cd frontend && npm run build` green.
2. `wails build -ldflags "-X proxyforward/internal/version.Version=vX.Y.Z -X proxyforward/internal/version.Commit=$(git rev-parse --short HEAD)"`
3. Smoke: `build/bin/proxyforward.exe --version` shows the stamp; exe launches to
   wizard/console; loopback pair (`gateway` + `agent` headless,
   `docs/agent/commands.md`) moves bytes.
4. Tag; attach `build/bin/proxyforward.exe` by hand. The NSIS files under
   `build/windows/installer/` are stock Wails templates, unused; no signing exists.
5. Update README + root CLAUDE.md if commands or claims changed.
