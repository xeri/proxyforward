<!-- Read-only production-readiness audit, 2026-07-29, run against
     polish/backend-truth @ 29c7cb5. Companion to polish-backlog.md: that file
     holds known UX debt, this one holds what a fresh adversarial pass found.
     Every finding was reproduced before it was written down; the evidence line
     says how. Move an item into polish-backlog.md if it is deferred, delete it
     when it is fixed. Symbol cites, not line cites — they survive edits. -->

# Production-readiness audit — 2026-07-29

## Verdict

The engine, the wire protocol, and the security model are production-grade and
were not the interesting part of this audit. What is *not* finished is the
**failure path**: what the product does when its own inputs are broken. Three of
the four highest-impact findings are places where a fault becomes silence — a
lost config, a blank window, a daemon nobody can reach — in a product whose
charter says the GUI is needed most when the tunnel is down.

Ordered by user-visible impact. Each entry: what happens, the evidence, and the
smallest change that closes it.

## P1 — a fault becomes data loss or a dead end

### 1. An unreadable config silently becomes the first-run wizard, and finishing it overwrites the original

`runGUI` (`main.go`) treats *any* config load failure as a first run: it
discards the error and continues with `config.Default()`. The comment promises
the error is surfaced "in the UI later"; it is not. `New` (`app/app.go`) is
never handed it, and nothing in the frontend can render it — no config-error
state exists anywhere in `frontend/src`.

The default document has `RoleUnset`, so `Startup` (`app/app.go`) skips the
engine and the app opens on the wizard. A user whose config was fine yesterday
is shown a first-run screen. Completing it calls `Save`
(`internal/config/config.go`), which atomically renames over the real file. No
backup is taken anywhere in `Save`, so what is gone is gone: `AgentID`, the
gateway host/port/fingerprint, every tunnel, groups, bandwidth caps, scope. The
Ed25519 keys survive in their own files (`internal/link/cred.go`), but the
config that points at the gateway does not.

Reaching it needs only a config that fails `Validate` — a hand-edit, or a file
written by a newer build that an older one's stricter validator rejects.

*Evidence*: `runGUI` drops `err` into `config.Default()`; `New` has no error
parameter; a repo-wide search for a config-error surface in the frontend returns
nothing; `Save` has no backup step.

*Contrast*: the CLI gets this right. `agent` and `gateway` (`main.go
newRoleCmd`) return the load error and refuse to start. Only the GUI degrades
silently, and only the GUI can overwrite.

*Smallest fix*: pass the load error into `New`, expose it on the tick, and render
a blocking "this config could not be read" state offering *retry / open the
folder / start over* — never the wizard. Belt and braces: have `Save` move an
unparseable file aside to `config.toml.corrupt-<stamp>` before its first
overwrite.

### 2. The GUI has three routes to a permanently blank window, and no diagnostics on any of them

- **No error boundary.** There is no `ErrorBoundary` or `componentDidCatch`
  anywhere in `frontend/src`. Any render throw unmounts the whole tree —
  in a WebView2 window with no devtools, that is a blank frame.
- **The first tick has no timeout and no failure state.** `useTick`
  (`frontend/src/state.ts`) calls `Status()` and swallows every rejection with an
  empty `.catch(() => {})`. If that call rejects, `status` stays `null` forever
  and `App` (`frontend/src/App.tsx`) renders a bare `Spinner` with no message, no
  retry, and no way out.
- **Same for the event stream.** If `EventsOn('tick', …)` never fires, the result
  is identical.

`useTickStale` exists and is good, but it only helps *after* a first tick has
landed — it cannot describe a session that never started.

*Evidence*: grep for `ErrorBoundary`/`componentDidCatch` in `frontend/src`
returns nothing; the empty catch is in `useTick`; the `!status` branch in `App`
is an unconditional spinner.

*Smallest fix*: an error boundary at the `Shell` level rendering the fault plus
the crash-log path (`installCrashLog` in `main.go` already writes one), and a
first-tick deadline — after ~5 s with no snapshot, replace the spinner with a
written "the engine is not answering" state carrying a retry.

## P2 — operability

### 3. The CLI cannot talk to a running daemon at all

`ipc.Dial` has exactly two callers, and both are the GUI: `runGUI` (`main.go`)
and `app/analytics.go`. **No subcommand in `newRootCmd` (`main.go`) opens the
pipe.** The whole control surface — roughly thirty ops across
`internal/engine/agents_api.go`, `analytics_api.go`, `consent_api.go`,
`lifecycle_api.go` and `pause_api.go` — is reachable only from the GUI.

The consequence lands hardest exactly where a gateway belongs, on a headless
Windows VPS. `RunStarted` (`internal/gateway/gateway.go`) prints a bootstrap
pairing code **only while the roster is empty**, by design. Once the first agent
enrolls, an operator with no desktop session cannot mint a second code
(`OpIssuePairing`), list/rename/revoke an agent, change a scope, read status,
answer a config-consent proposal, or stop and start the run — the daemon serves
all of it, and nothing can ask.

*Evidence*: `ipc.Dial` call sites; the subcommand list in `newRootCmd`; the
empty-roster guard in `RunStarted`.

*Smallest fix*: a thin IPC client group — `status`, `agents list|revoke|rename`,
`pair issue`, `run start|stop`, `pause` — reusing the ops the GUI already calls.
No engine work; it is a client and a command surface. `--json` on each makes the
product scriptable, which is the other half of headless.

### 4. Nothing recovers from a panic, so one bad connection takes down every agent

There is no `recover()` anywhere outside tests. `evict` (`internal/gateway/actor.go`)
and the per-agent listener model go to real lengths to make agents independent —
evicting one leaves the others' listeners and connections untouched — and a
single nil dereference on one player's connection goroutine discards all of it,
along with every tunnel on the box.

`debug.SetCrashOutput` (`installCrashLog` in `main.go`) means the crash is at
least *legible* afterwards, which is more than most projects manage. It does not
keep the other agents up.

*Judgment call, stated as one*: "crash on panic" is a defensible Go position when
process state may be corrupt. It is the weaker choice for a relay, where
per-connection state is isolated and the blast radius of continuing is one
connection — versus the blast radius of exiting, which is the entire fleet.

*Smallest fix*: a recover at the top of each per-connection goroutine that logs
with the connection's identifiers and closes only that connection. Leave the
lifecycle/actor goroutines to crash — a panic there genuinely is corrupt state.

### 5. Metrics show health but not harm

`writeMetrics` (`internal/engine/metrics.go`) is well built — honest about
unknowns, no PII in labels — but every series describes the happy path. There is
no counter for an auth-limiter refusal (`authLimiter` in
`internal/gateway/limits.go`), a `connGate` or `preAuthGate` rejection, a bind
failure, a health-probe transition, or a reconnect. An operator can see that the
gateway is up; they cannot see that it is being flooded, or that agents are
flapping. `proxyforward_link_sessions_total` is the only proxy for the latter.

*Smallest fix*: counters for the refusal paths that already exist as branches —
they are all in `limits.go` and the actor — plus per-tunnel bytes/connections.

## P3 — accessibility

Verified live in headless Brave against the devmock, not inferred.

### 6. Modal surfaces are visual only

No `role="dialog"` or `aria-modal` exists anywhere in `frontend/src`. Checked on
the live command palette (`frontend/src/components/CommandPalette.tsx`), both
attributes read `null`.

- **Focus is not contained.** Tabbing from the last palette result walks straight
  out into the page behind it — the sidebar, then the whole nav rail, with the
  palette still covering the screen.
- **Focus is not restored.** Opening the palette from the Traffic nav button and
  closing it with Escape leaves focus on a palette result that no longer exists,
  not on the button that opened it. A keyboard user loses their place on every
  open/close.

Escape *is* handled on every modal surface (`CommandPalette.tsx`,
`frontend/src/components/ui.tsx`), so the hard part is already done.

*Smallest fix*: one `Modal` wrapper in the `ui.tsx` kit owning `role="dialog"`,
`aria-modal`, a focus trap, and opener restore, adopted by the palette, the
close dialog, and the confirm panels.

### 7. Nothing is announced in a live-status application

There are no `aria-live` regions, `role="status"`, or `role="alert"` anywhere in
`frontend/src`. A player joining, the link dropping, a fatal error, a consent
proposal arriving — all change silently. For an instrument whose entire purpose
is live state, this is the largest single a11y gap.

*Smallest fix*: one polite live region for status transitions and one assertive
region for fatal/consent, fed from the tick.

### 8. One contrast near-miss

Measured every rendered text node on Overview against WCAG AA: **two failures,
one of them a false positive** (a jade primary button, whose `color(srgb …)`
fill my walker could not parse). The real one is the keyboard-hint chip — the
`Ctrl` `Kbd` at 4.15:1 against a required 4.5:1. Everything else passes, which
is a genuinely good result for a palette this dark.

## P4 — honesty, docs, and papercuts

9. **`Autostart` is still a phantom.** `Autostart`
   (`internal/config/config.go`) is stored, consumed by nothing, and shown
   nowhere. This is reality-check row 1 and polish-backlog item 1; it stays open
   because it needs a scope call — implement (registry Run key / startup folder)
   or delete the field.

10. **`docs/agent/commands.md` contradicts the linter config.** It says
    "`errcheck` is deliberately off — see the config header". `.golangci.yml`
    enables `errcheck` with a curated `exclude-functions` list, and both
    CLAUDE.md and `.claude/rules/go.md` describe it as a gate. The doccheck
    citation test cannot catch this — it verifies that symbols exist, not that
    claims are true.

11. **A dead CLI flag.** `--headless` on the `agent` and `gateway` subcommands
    (`main.go newRoleCmd`) is declared, defaulted to true, and never read. Delete
    it, or the next reader assumes a `--headless=false` path exists.

12. **No config schema version.** `internal/analytics/schema.go` has a proper
    ladder (`migrations`, `PRAGMA user_version`); the TOML config has neither.
    Additive fields are safe today because `Load` (`internal/config/config.go`)
    unmarshals over `Default()`, but nothing can detect a document written by a
    future build, and a field whose *meaning* changes has no migration hook.

13. **A confusing parse error.** `ParsePairingCode`
    (`internal/link/pairing.go`) answers a code with no scheme with
    `expected pxf://, got ""` — the empty string is `url.Parse`'s output, not
    anything the user typed. The other messages in this function are excellent;
    this one branch should say "that does not look like a pairing code — paste
    the whole `pxf://…` line from the gateway".

14. **`Activity` has no skeleton state.** Every other data screen carries all
    four states; `frontend/src/screens/Activity.tsx` has written empty states but
    no geometry-matched skeleton.

## Strategic — not bugs, but they decide whether v1 is shippable

15. **There is no update path.** No in-app version check, no update channel. The
    release pipeline is excellent — pinned action SHAs, provenance attestation,
    checksums, caches deliberately off — but a user running a vulnerable build
    has no way to learn a fix exists. For an internet-facing daemon that is the
    biggest gap on this list that is not a bug.

16. **Binaries are unsigned.** Already stated honestly in the release notes;
    SmartScreen warns on first run, on a product that then asks for UAC and
    installs a service. The cost is a certificate, not engineering.

17. **The identity key is unencrypted at rest.** Written 0600 (meaningless on
    Windows) and protected by the `internal/secureio` ACLs. Defensible — there is
    no passphrase UX and revocation is one click — but it means host compromise
    is agent-identity compromise, and that should be a stated property rather
    than an implicit one.

## Checked and found clean

Recording coverage, so the next pass does not re-derive it.

- **Full gate green**: `go test ./...` (817 tests, 103 files, 10 fuzz targets),
  `go vet` clean, `golangci-lint` **0 issues**, `npm run build` clean.
- **Every wire-controlled allocation is bounds-checked before `make`** —
  `ReadMsg` (`internal/control/control.go`), `ReadPacket`
  (`internal/mc/packet.go`), `internal/relay/datagram.go`,
  `internal/relay/flowgram.go`.
- **Abuse accounting** (`internal/gateway/limits.go`) matches its documented
  invariants: `sourceNet` reduction, an LRU-bounded token-bucket table, whole-token
  spending with a zero floor, `preAuthGate` released exactly once via
  `sync.OnceFunc`.
- **`connGate` is balanced on every path**, including UDP — `udpSession.Close`
  (`internal/gateway/udp.go`) is `sync.Once`-guarded, so a slot cannot be
  double-released.
- **Setup-bundle crypto** (`internal/setup/crypto.go`) is textbook: argon2id at
  RFC 9106 parameters, AES-256-GCM, the header bound as AAD, and decode-side caps
  on KDF parameters read from an untrusted file.
- **The avatar handler** (`app/avatars.go`) validates its path against a strict
  regex and neutralises the separator before touching disk — no traversal.
- **Analytics degradation is honest**: a failed `analytics.Open` in `New`
  (`internal/engine/engine.go`) warns and runs without persistence rather than
  failing the run. This is the pattern finding 1 should have followed.
- **CLI exit codes and validation**: non-zero on error, and `Validate`
  (`internal/config/config.go`) reports every problem at once via `errors.Join`
  rather than one per run.
- **Landmarks and `lang`** are correct (`frontend/src/layout/Shell.tsx`,
  `frontend/src/layout/Sidebar.tsx`); no click handlers on non-interactive
  elements anywhere.
- **The charter is faithfully executed.** Verified visually across the agent,
  gateway, and wizard states: one identity surface per screen, the bloom frame
  used exactly once, the two-voice cast, role re-tint, whitespace as structure.
  The aesthetics are not a production risk.
- **Bundle size is a non-issue**: 641 KB in one chunk, but assets are embedded in
  the binary and served from memory — code-splitting would buy nothing. The Vite
  warning can be silenced rather than acted on.
