---
name: release
description: Playbook for cutting a proxyforward release — a tag drives the pipeline, which stops at a draft you must smoke-test and publish by hand. Releases are an escalation trigger: confirm with the human first.
---

# Cut a release (playbook F)

Cutting or tagging a release is an escalation trigger — confirm with the human.

`.github/workflows/release.yml` does the build. It stops at a **draft** on purpose:
the smoke test below is the part that cannot be automated, and a draft is what
gives you the chance to run it before anyone can download the binary.

1. Clean tree on `master`; CI green (`go test ./...`, `cd frontend && npm run build`).
2. Tag and push: `git tag vX.Y.Z && git push origin vX.Y.Z`. The workflow builds the
   exe **and** the NSIS installer, stamps the version via `-ldflags` into
   `internal/version`, asserts `--version` reports the tag, writes `SHA256SUMS.txt`
   and an SPDX SBOM, and attaches a build-provenance attestation.
3. **Smoke the draft's artifacts** — download them, don't trust the build:
   - `proxyforward.exe --version` shows `vX.Y.Z`.
   - The exe launches to the wizard/console.
   - The installer installs, launches, and uninstalls.
   - A loopback pair (`gateway` + `agent` headless, `docs/agent/commands.md`) moves bytes.
4. Publish the draft in the GitHub Releases UI.
5. Update README + root CLAUDE.md if commands or claims changed.

## What can go wrong

- **A missing installer is silent.** `wails build -nsis` exits 0 and just skips the
  installer when `makensis` isn't on PATH, so the workflow installs NSIS itself and
  then asserts the file exists. If that assert fires, the toolchain broke — don't
  "fix" it by dropping `-nsis`.
- **The binaries are unsigned** (no certificate). SmartScreen warns on first run.
  The honest substitute is the attestation: `gh attestation verify <exe> -R xeri/proxyforward`.
- **Windows only.** The linux/macOS artifacts CI builds are never release assets —
  the engine can't start there (see the Reality check in CLAUDE.md).
