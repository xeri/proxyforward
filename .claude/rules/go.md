---
paths:
  - "**/*.go"
---

# Go conventions (moved from CLAUDE.md "Conventions in force" + Footguns)

- Package docs state the ownership/concurrency model up top. Comments explain *why*
  and justify numbers — they are the spec; keep their density. If a change
  invalidates a why-comment, updating it is part of the change.
- `log/slog` key-value logging, lowercase messages, no per-packet logs.
- Errors wrap with `%w` and tell the user what to *do* ("re-pair with the gateway's
  current pairing code"). `errors.Join` for multi-error validation.
- Concurrency idioms: single-writer actor for lifecycle, `atomic.Pointer[T]` + box
  structs for interface atomics, `sync.Map` for id-keyed live maps, channels for
  done-signaling.
- Zero TODO/FIXME markers in code — debt is recorded in `docs/agent/polish-backlog.md`,
  not inline.
- New code must be gofmt-clean (a PostToolUse hook and CI both check); don't
  mass-reformat in an unrelated change.
- Sentinels: 0 RTT means "unknown" on the wire; jitter/loss/uptime use -1. Never
  break the ≥ 1 ms RTT clamp (`agent.go handleControlMsg`) or render sentinels as
  real numbers — that produces convincing lies.
- Analytics ops must not hold `app.mu` — they ride their own mutex + pipe conn so a
  slow query can't stall the 2 Hz tick (`app/analytics.go` header). Served
  `ipc.OpError`s are transient and must never latch `analyticsUnsupported`.
- conntrack `ConnKey` is immutable after `Open` — it's read unlocked concurrently
  (regression: `TestConnKeyPlumbing` under -race).
- Windows file locking: stop the engine (final stats flush included) before
  overwriting `analytics.db` / config (`app/setup.go importSetupFromPath`);
  `setup.atomicWrite` retries rename once for AV scanners.
- netsh output is localized — detect firewall rule state by exit code only
  (`svc/firewall_windows.go`); `explorer.exe` returns nonzero on success
  (`app/tools.go openInFileManager`).
- Cobra mousetrap is disabled on purpose (`main.go`) — re-enabling it breaks
  double-click launch from Explorer. New CLI subcommands call
  `wincon.AttachParent()` at the top.
- Commits: lowercase, terse, scope-prefixed ("v2 motion: …"); one concern per
  commit; protocol and implementation never change in the same commit.
