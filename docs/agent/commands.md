<!-- Companion to /CLAUDE.md ("Where everything lives"). All commands executed &
     verified 2026-07-13 @ 4a8b0c9. Update trigger: a command changed — run it first,
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

## Linux

The engine and GUI run on Linux; the IPC is a unix socket at
`$XDG_RUNTIME_DIR/proxyforward/ipc.sock`. The GUI needs gtk3 + webkit2gtk-4.1 (dev
packages to build, runtime libraries to run). On an atomic distro (Silverblue/Kinoite)
keep both in a toolbox rather than layering them onto the host — the toolbox shares
`$HOME`, the host network, and `$XDG_RUNTIME_DIR`, so ports bind on the host and the
config lands in the usual `~/.config/proxyforward`:

```
toolbox create proxyforward
toolbox run -c proxyforward sudo dnf install -y golang nodejs npm gcc \
    pkgconf-pkg-config gtk3-devel webkit2gtk4.1-devel
go install github.com/wailsapp/wails/v2/cmd/wails@v2.13.0      # inside the toolbox
wails build -platform linux/amd64 -tags webkit2_41 -s -skipbindings
toolbox run -c proxyforward build/bin/proxyforward             # run the GUI
```

- `-tags webkit2_41` is required: Wails 2.13 links webkit2gtk-4.0 unless told
  otherwise, and current distros ship only 4.1.
- `-skipbindings` keeps `frontend/wailsjs` untouched (CI's stale-bindings check is
  the Windows build's job).
- A blank or black window on NVIDIA is WebKitGTK's DMA-BUF renderer: run with
  `WEBKIT_DISABLE_DMABUF_RENDERER=1`.
- `wails build -platform windows/amd64` cross-compiles the Windows exe from Linux
  (no cgo on that target).
- Go newer than the Wails CLI's `golang.org/x/tools` breaks `wails build` with
  "package … without types was imported". Build the CLI against a current x/tools
  (`go get golang.org/x/tools@latest` in a scratch module, then `go build` the
  `cmd/wails` package) or use the go.mod Go version.

## UI-only iteration (the mock state matrix)

`http://localhost:5173/?mock=agent` — scenarios `agent|gateway|wizard`, plus
composable axes `&link=down &mode=attached &fatal=1 &fresh=1 &analytics=off
&paired=0 &geo=off|empty|error|pending &fx=low|high &fleet=multi|old`. Axis semantics are documented in
`docs/agent/architecture.md` ("devmock axes") and the `devmock.ts` header. This is
the UI state-matrix test harness — walk the relevant axes before calling UI work
done, then spot-check in `wails dev` (WebView2 ≠ your browser).

## Two-process / headless repro

```
go run . gateway --config <tmp>\gw.toml
go run . pair <code> --config <tmp>\ag.toml
go run . agent --config <tmp>\ag.toml
```

Private configs keep your real setup untouched (`docs/agent/reasoning.md` step 0).

## Gotchas

- **Fresh clone**: every Go command fails until the frontend has been built once —
  `main.go` embeds `all:frontend/dist`, which is gitignored, and a `go:embed` matching
  zero files is a compile error. Run `cd frontend && npm ci && npm run build` first.
  CI does this in one job and shares `frontend/dist` with every Go job.
- `GOOS=linux` and `GOOS=darwin` builds compile (CI builds both) and Linux runs —
  see "Linux" above. Cross-compiling darwin from Windows still fails on
  `energye/systray`, which needs cgo — that's the cross-compile, not the code; the
  native macOS runner is fine.
- `go test -race` needs cgo (a C toolchain); unavailable on machines without one — the
  `race` job in CI is the only place it actually runs.
- gofmt cleanliness is enforced (edit hook + CI) and depends on `.gitattributes`
  (`eol=lf`) — without it a Windows checkout is CRLF and gofmt rejects every file.
  Don't mass-reformat in an unrelated change.
- `golangci-lint run ./...` (`.golangci.yml`, curated: govet/staticcheck/ineffassign/
  unused/misspell) is a CI gate. `errcheck` is deliberately off — see the config header.
- **windowsgui has no stderr**: startup failures land in
  `%APPDATA%\proxyforward\logs\crash.log` and `wails.log` (`main.go installCrashLog`);
  a silent non-appearing window means *look there*, not "add prints".
