# Security invariants — the reasoning

CLAUDE.md states each of these as a rule and names the symbol that enforces it.
This file is the *why*: what the rule is defending, and — where one exists — the
bug that proved the rule was needed. Read the rule there; read this before you
change the thing the rule is about, because every one of these looked reasonable
right up until it didn't.

The numbers themselves (timeouts, caps, floors) live in `architecture.md` "The
numbers". Identity/enrollment mechanics live in `architecture.md` "Per-agent
identity, enrollment & revocation". Neither is restated here.

## The trust root

**TLS 1.3 both sides, no CA, ever.** Trust is the gateway's self-signed ECDSA
P-256 cert, pinned by SHA-256 fingerprint carried out-of-band in the pairing code
`pxf://host:port/v1/pair/<token>#sha256:<hex>` (`link/pairing.go`,
`cert.go GatewayTLSConfig`/`AgentTLSConfig`). `pxf` doubles as the OS deep-link
scheme; `/v1/pair/` is a format-version + role marker so a wrong-kind link fails
loudly rather than half-working. The pinned fingerprint compares in constant time
(`crypto/subtle` — `cert.go fingerprintEqual`).

**Per-agent identity is the trust model.** Each agent proves possession of a
long-term Ed25519 key the gateway allowlists. Its derived `agentID` (`agt_…`) is
unforgeable, individually scoped (ports/tunnels) and revocable. Revocation *and* a
scope change both evict the live session: a session captures its scope at
admission, so without that eviction, narrowing a scope contains nothing that is
already connected (`gateway.go SetAgentScope`). The next connect is fatal
`ErrCodeRevoked`.

A matching agentID **supersedes** (that is a reconnect); a distinct one is admitted
**alongside** (that is the multi-agent gateway). Supersede is anti-flap dampened,
so two live machines colliding on one agentID degrade to a slow contest with a log
line rather than a tight teardown/rebind loop burning CPU (`actor.go admit`,
`noteSupersede`).

**"Unforgeable" is a claim about width**, and therefore a security parameter rather
than a display choice. `agentID` is the label the gateway keys supersede, scope,
revocation, and config on. It must stay wide enough that no second key answering to
a victim's name can be searched for (`cred.go agentFingerprintBytes`, floored by
`TestAgentIDIsWideEnoughToBeUnforgeable`). `AgentStore.Enroll` refuses a colliding
join besides, so a collision that is somehow found still fails closed.

**Identity is the only authenticator** (`gateway/auth.go identityValidator`).
The legacy fleet-wide shared token is gone, and with it its residual risk: a
self-asserted `agentID` no longer exists on any path, so supersede, scope,
revocation, and config all key on a label only a verified key can mint
(`link/cred.go AgentID`). The bug class this closes was real — a token holder
could once name itself an enrolled agent and inherit its session, ports, and
scope — and it is closed by construction now, not by a reserved namespace.

## The pre-auth boundary

Everything here runs before the peer has proved anything, on a port the whole
internet can reach. That is the entire threat model of this section: the code is
cheap for a stranger to run, and it holds locks and allocates on their behalf.

**The unit of abuse accounting is the network, not the address** (`limits.go
sourceNet`). One IPv4 address, but one IPv6 **/64** — the smallest block a
subscriber is handed. A per-address limit on IPv6 is 2^64 wide and free to evade by
counting up. Both limiters reduce the address themselves; callers pass the raw IP,
because `remoteIP`'s other callers (the GUI link card, the cloned-identity warning)
need it whole. Any new per-source limit keys on `sourceNet` or it is decorative.

**A limiter's own table is attack surface.** It must be bounded by construction and
O(1) to update, because `allow` runs before auth on every connection and holds a
mutex every honest agent needs. Hence a token bucket per network (one scalar,
refilled on read) in an LRU with a hard cap (`authFailNets`) — never "grow, then
scan to clean up", which under a flood scans more the worse things get
(`TestAuthLimiterIsBoundedUnderSourceRotation`).

Bans stay bounded too: tokens floor at 0, so a burst cannot ban a shared address
for longer than the window (`TestAuthLimiterBanIsBoundedByTheWindow`). And the
bucket must be spent in *whole* tokens — with continuous refill, a block that
triggers at "score ≥ limit" ends a nanosecond after it begins
(`TestAuthLimiterBlockOutlastsATick`).

**The unauthenticated set must be bounded by a gate, not by the rate limiter**
(`limits.go preAuthGate`). `authLimiter.allow` only refuses a network once its
failures have *landed*, and a stalled connection does not fail until
`preAuthTimeout` — so the limiter alone leaves a ~10 s window of unbounded
goroutines and descriptors, which is precisely what makes Accept fail in the first
place. The slot is released the instant auth succeeds, or a handshake cap silently
becomes a session cap. The release is `sync.OnceFunc` because it runs on every
failure path *and* on success, and a double-release raises the ceiling forever
(`TestPreAuthGateBoundsUnauthenticatedConns`).

**The prologue's deadline is on the prologue, not on its reads.** Every rejection
branch answers an unauthenticated peer with a hello error, and a QUIC stream's
Write parks until the peer grants flow-control credit — credit a hostile peer never
grants. The TCP path got this right by accident of `conn.SetDeadline` bounding both
directions; the QUIC path armed only a read deadline, so a peer that sent a
rejectable hello and advertised a zero window parked the gateway in `WriteMsg`
forever. `sess.Close()` was never reached, the handler never returned, and its
deferred pre-auth release never ran — the gate defeated using its own accounting
(`TestQUICPreAuthReplyCannotBlockForever`).

**An accept loop stops only when its listener is closed** (`limits.go
isListenerClosed`, used by `gateway.go retryAccept`). Every other Accept error is
transient — descriptors, buffers, a reset between SYN and accept — and returning on
one is a *permanent* outage wearing a healthy face: process up, GUI green, players
still served, no agent able to reconnect ever again
(`TestControlAcceptSurvivesATransientError`). Retry with capped backoff, never a
bare `continue` — a broken listener would spin.

## What an authenticated agent may do

Authenticated is not trusted. A pairing code can be shared beyond its intended
machine, and an enrolled agent can be compromised.

**Port 0 is not a port, it is "any port in the ephemeral range"** — so it is
policy-*valid* only when nothing constrains the choice. A port-0 request names
nothing for `validateSpec` to check; it asks the gateway to choose, so policy is
enforced where the choice is made, by `bindLocked` picking only from `conflict.go
allowedPorts` (scope narrows, allowlist filters; an empty result means this agent
may have no port at all). Waiving the check for 0 instead — which is what it did —
let a scoped agent bind a public listener on a port nobody allowed, pointed at
whatever local address it liked, once per invented tunnel id
(`TestEphemeralRequestStaysInsideScope`). `reassignCandidates` already refused to do
this on the fallback path; the primary path just did not ask. Any new port-granting
path uses `allowedPorts` or it is not constrained.

**An authenticated agent is bounded in every dimension it can grow.** Connections
(`connGate`), unauthenticated sockets (`preAuthGate`) and bandwidth (`bwcap`) were
capped; listeners — the only resource an agent allocates by *asking* — were capped
only by its scope, and the default scope is empty ("any port, any tunnel id"). So
the default agent could hold a port per invented tunnel id until the host's
ephemeral range was gone, taking every other agent's tunnels and the machine's own
outbound connections with it (`MaxTunnelsPerAgent`, enforced in `actor.go
bindLocked`; `TestListenerCapBoundsAnUnscopedAgent`).

The cap is charged per agent — a global one would let a greedy agent lock out
honest ones, which is the same denial at a different address — and counts growth,
never rebinding: `bindLocked` implements a hot-apply as close-then-open, so counting
before the replacement would leave an agent at its cap unable to reconfigure a
tunnel it already holds. The cap would brick reconfiguration rather than bound it.

**The gateway never decides what the agent exposes.** A disabled tunnel keeps a
`LocalAddr` naming a real service, and ids are guessable slugs (`link.TunnelID`).
So a pushed spec must not enable one (`agent/hotapply.go mergeTunnels`) and a data
stream must not dial one (`agent/agent.go enabledTunnelByID`). `push_config`
auto-applies without consent, so both halves are load-bearing
(`TestMergeTunnelsKeepsDisabledDraftsDisabled`,
`TestEnabledTunnelByIDIgnoresDisabledTunnels`).

**Fatal auth errors stop the agent** (`bad_token`, `agent_conflict`, `version`,
`revoked`) instead of retry-hammering the gateway (`agent.go isFatal`), and surface
in the UI via `EngineFatal` on the tick.

## The local boundary (the IPC pipe)

**The IPC pipe is no easier to reach than the config dir it fronts.** The same three
principals `secureio` grants, deliberately: SYSTEM, Administrators, and the account
serving it (`ipc/security_windows.go pipeSecurity`) — never a *group* like `IU`.
`IU` is S-1-5-4, INTERACTIVE: *every* account logged on interactively, including a
standard non-admin one and every RDP session. It used to grant exactly that, under
a comment claiming it admitted "the interactive user".

The pipe is not a status feed: `OpIssuePairing`, `OpRevokeAgent` and
`OpSetAgentScope` ride the same envelope, and a pairing code carries a live
enrollment ticket. So on any machine with a second account, that account could
mint a pairing code, enroll its own agent, or revoke the real one — and against a service-mode
daemon (LocalSystem) that is a local privilege escalation to a SYSTEM service's
security policy. A wider ACL here is a hole straight through the one `secureio`
installs (`TestPipeDeniesOtherInteractiveUsers`).

Service mode runs as LocalSystem, so "the account we run as" is SYSTEM and the
installing admin's *unelevated* GUI would be locked out — UAC filters the
Administrators SID to deny-only in an unelevated token, so `BA` cannot let them in.
The installer therefore records its own SID (`ipc/owner.go PipeOwnerFile`), in the
secureio-protected config dir, where only SYSTEM and admins can write it. Any SID
formatted into the SDDL is parsed first, and a pipe whose ACL cannot be built is
not served.

**"Something answered the pipe" is not evidence an engine did.** Any local process
may create a pipe under a name nobody holds yet, so an unprivileged process that
gets there first — at boot, or after the engine dies — *becomes* the engine as far
as the GUI is concerned: Dial succeeds, Startup attaches as a thin client, and from
then on the GUI believes what it is told and tells it what it is asked. The real
engine's Serve then fails with "is another daemon running?", by which point the GUI
is already talking to the squatter and looks perfectly healthy. That is not merely
spoofed status — the GUI would display an attacker's pairing code as its own.

`ipc.Dial` verifies the pipe's **owner** is SYSTEM, Administrators, or this very
account before returning it (`ipc/security_windows.go verifyServer`,
`ErrUntrustedServer`). The owner is the right check because it is the one part a
squatter cannot fake: setting an object's owner to a SID you do not hold needs
`SeRestorePrivilege`. An elevated attacker can forge it, and it does not matter — at
that point there is no boundary left to defend.

## What leaves the machine

**Diagnostics bundles are made to be handed to strangers.** They redact every
secret, host, IP, and identity; peer IPs and hostnames become pseudonyms stable
*within one bundle* and meaningless outside it (`app/tools.go pseudonymiser`).

The salt is the property, not the hash. IPv4 is 2^32 addresses, so an unsalted
digest of one is a table anyone can build in minutes: `ip-3f2a…` was a reversible
encoding of the address wearing a hash's clothes
(`TestIPPseudonymIsNotJustAnEncoding`). Hostnames are worse — a digest of
"alices-laptop" is a dictionary away — and they are routinely a person's name or an
employer's internal naming (`TestDiagnosticsHealthPseudonymisesHostnames`). Minting
the salt is the one part of a bundle that is not best-effort: the fallback is not a
weaker salt, it is shipping the addresses.

**A leak test that leaves one channel empty passes vacuously.** Anything new that
exports data must seed *every* channel it ships. Two have already been caught this
way: the log ring and on-disk logs were never populated, so the sweep ran over an
empty set while a logged pairing code shipped in cleartext; and health.txt was
handed to `writeDiagnostics` pre-rendered, so the sweep never saw a byte the
renderer produced and it shipped both hostnames verbatim under a comment calling
them safe to share. `writeDiagnostics` now renders everything it redacts.

**Bundles ship logs**, so redacting only the config is theatre: log text is scrubbed
of the config's own secrets (`tools.go logScrubber`).

**Never log a secret.** The scrubber is a net, not a licence — it only knows the
values config holds. The pairing code embeds a live enrollment ticket, so it goes
to the console, never through `slog`: slog fans out to the rotating file, the GUI
ring, and every bundle (`gateway.go RunStarted`).
