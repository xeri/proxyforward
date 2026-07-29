<!-- Read-only production-readiness audit, 2026-07-29, run against
     polish/backend-truth @ 29c7cb5, then worked through in the same branch.
     Companion to polish-backlog.md: that file holds known UX debt, this one
     holds what a fresh adversarial pass found. Every finding was reproduced
     before it was written down. A finding is DELETED when it is closed — the
     commit is the record, and a doc that keeps its own tally of finished work
     goes stale the first time someone re-opens one. What survives below is
     only what is still true. Symbol cites, not line cites. -->

# Production-readiness audit — 2026-07-29

## Verdict

The engine, the wire protocol, and the security model are production-grade and
were not the interesting part of this audit. What was *not* finished was the
**failure path**: what the product does when its own inputs are broken. Three of
the four highest-impact findings were places where a fault became silence — a
lost config, a blank window, a daemon nobody could reach — in a product whose
charter says the GUI is needed most when the tunnel is down.

Seventeen findings were raised, plus six more that the work itself turned up.
All but the three below are closed on this branch. The failure path now has a
config-fault state that never becomes the wizard, two error boundaries and a
first-tick deadline, an IPC client group for headless boxes, and per-connection
panic recovery.

## Still open

### 5 (part). Metrics show harm, but not flapping

`writeMetrics` (`internal/engine/metrics.go`) now exports the refusal half via
`RefusalCounters` (`internal/gateway/counters.go`) — auth failures, both
`connGate` caps apart, the `preAuthGate`, every `bindLocked` failure arm, port
reassignment, rebind recovery. An operator can see that the gateway is being
flooded. They still cannot see that agents are **flapping**:
`proxyforward_link_sessions_total` remains the only proxy for it, and there is
no per-tunnel bytes/connections series.

*Smallest fix*: a counter on the agent's health-probe transition and on
`runSession`'s reconnect, plus per-tunnel bytes and connections.

### 7 (part). Consent changes are still announced by nothing

The live regions exist and carry status transitions and fatals
(`frontend/src/components/Announcer.tsx`; `announce(text, true)` is exported for
the assertive channel). What has not adopted them is consent: a proposal
arriving, being approved, or being rejected still changes the screen silently.
`UIStatus` carries no consent field, so this is a one-line `announce` call at
each of the places that already know — `Agents.tsx` and `Tunnels.tsx`.

### 16. Binaries are unsigned

**Accepted, not fixed — the cost is a certificate, not engineering.** SmartScreen
warns on first run, on a product that then asks for UAC and installs a service.
The release notes already say so honestly, which is the only part that was ever
in this repo's control. Closing it means buying an OV/EV code-signing certificate
and adding the signing step to `.github/workflows/release.yml`.

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
  failing the run. This is the pattern finding 1 should have followed, and now does.
- **CLI exit codes and validation**: non-zero on error, and `Validate`
  (`internal/config/config.go`) reports every problem at once via `errors.Join`
  rather than one per run.
- **No click handlers on non-interactive elements** anywhere in `frontend/src`.
- **The charter is faithfully executed.** Verified visually across the agent,
  gateway, and wizard states: one identity surface per screen, the bloom frame
  used exactly once, the two-voice cast, role re-tint, whitespace as structure.
  The aesthetics are not a production risk.
- **Bundle size is a non-issue**: 641 KB in one chunk, but assets are embedded in
  the binary and served from memory — code-splitting would buy nothing. The Vite
  warning can be silenced rather than acted on.

### Two things this section originally got wrong

Kept as a caution about how this kind of pass fails, since both were written
down as verified.

- **"Landmarks and `lang` are correct"** was half true. The three landmarks
  existed but were **unnamed**, and there was no skip link — a screen-reader user
  got "navigation, navigation, navigation" and had to tab the whole rail to reach
  content. Existence was checked; usefulness was not. Both are fixed
  (`frontend/src/layout/Shell.tsx`).
- **A contrast "failure" that was not one.** The Overview sweep reported a jade
  primary button at 1:1. It was a `color(srgb …)` fill the measuring walker could
  not parse, not a real miss. Every automated contrast result needs the failing
  pair read back by eye before it is written down.
