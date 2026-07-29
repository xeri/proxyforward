<!-- Companion to /CLAUDE.md ("Where everything lives"). All commands executed &
     verified 2026-07-13. Update trigger: a command changed — run it first,
     then edit. -->

# Commands & dev iteration

## Build / test / run

```
go build ./...                        # clean
go vet ./...                          # clean
go test ./...                         # full suite green, ~35 s (unit + e2e + fuzz seeds
                                      #   + burst floor + internal/doccheck citations)
go test -short ./...                  # skips the 64 MiB burst
go test -run TestBurst -v ./internal/e2e/        # the perf gate; prints MiB/s + worst RTT
go test -fuzz FuzzParseHandshake -fuzztime 5s ./internal/mc/   # any Fuzz* target works
cd frontend && npm install && npm run build      # tsc + vite, ~2 s
cd frontend && npm run dev            # browser dev on :5173 (PORT env overrides)
wails dev                             # real app with hot reload (opens a window)
wails build                           # → build/bin/proxyforward.exe (~15 s); ALSO the
                                      #   binding regeneration step ("Generating bindings")
wails build -ldflags "-X proxyforward/internal/version.Version=vX.Y.Z -X proxyforward/internal/version.Commit=<sha>"
build/bin/proxyforward.exe --version  # verify the stamp
```

## UI-only iteration (the mock state matrix)

`http://localhost:5173/?mock=agent` — scenarios `agent|gateway|wizard`, plus
composable axes `&link=down &mode=attached &fatal=1 &engine=stopped
&service=running|stopped &fresh=1 &analytics=off &paired=0
&geo=off|empty|error|pending &fx=low|high &fleet=multi|old|clamped
&conflict=port|clone|both &deeplink=1 &pending=in|out|both &consent=off
&invites=friend|single|both &tray=off|notice &pause=mine|theirs|both
&mcstatus=on|empty &local=unset`. Axis semantics are documented in
`docs/agent/architecture.md` ("devmock axes") and the `devmock.ts` header. This is
the UI state-matrix test harness — walk the relevant axes before calling UI work
done, then spot-check in `wails dev` (WebView2 ≠ your browser).

## Two-process / headless repro

```
go run . gateway --config <tmp>\gw.toml
go run . pair <code> --config <tmp>\ag.toml
go run . agent --config <tmp>\ag.toml
```

## Service lifecycle (Windows, elevation-gated)

```
proxyforward service install     # + start | stop | uninstall | run
```

`install`/`uninstall`/`start`/`stop` self-elevate when not already admin (one UAC
prompt per action); the GUI's Settings → System row calls the same verbs. `run` is
what the SCM invokes. A stopped run persists (`[engine] stopped`), so a service that
starts with it set serves the pipe and holds the tunnel down until Start.

Private configs keep your real setup untouched (`docs/agent/reasoning.md` step 0).

## Gotchas

- **Fresh clone**: every Go command fails until the frontend has been built once —
  `main.go` embeds `all:frontend/dist`, which is gitignored, and a `go:embed` matching
  zero files is a compile error. Run `cd frontend && npm ci && npm run build` first.
  CI does this in one job and shares `frontend/dist` with every Go job.
- `GOOS=linux go build ./...` and `GOOS=darwin` now **compile** (CI builds both). They
  do not *run*: `ipc.Serve` returns `ErrUnsupported` off Windows, so the engine never
  starts. Cross-compiling darwin from Windows still fails on `energye/systray`, which
  needs cgo — that's the cross-compile, not the code; the native macOS runner is fine.
- `go test -race` needs cgo (a C toolchain); unavailable on machines without one — the
  `race` job in CI is the only place it actually runs.
- gofmt cleanliness is enforced (edit hook + CI) and depends on `.gitattributes`
  (`eol=lf`) — without it a Windows checkout is CRLF and gofmt rejects every file.
  Don't mass-reformat in an unrelated change.
- `golangci-lint run ./...` (`.golangci.yml`, curated: govet/staticcheck/ineffassign/
  unused/misspell/errcheck) is a CI gate. `errcheck` is **on**: every ignored error is
  written `_ = call()`, and the config names each type whose `Close` error is genuinely
  not actionable rather than blanket-excluding `.*Close` — the exclusion that once hid a
  corrupt support bundle being reported as written.
- **windowsgui has no stderr**: startup failures land in
  `%APPDATA%\proxyforward\logs\crash.log` and `wails.log` (`main.go installCrashLog`);
  a silent non-appearing window means *look there*, not "add prints".
