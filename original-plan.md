# proxyforward — ngrok-style Minecraft tunnel for Windows

## Context

The user wants a sophisticated, overengineered reverse-proxy pair (like ngrok/frp) so a Minecraft server on **Server A** (no port forwarding possible) is reachable through **Server B** (which can port-forward). Server A dials *out* to Server B; Server B exposes the public TCP port (default 25565, configurable) and relays player traffic back through that outbound link. Both machines run Windows. A control channel keeps the two sides synced. The GUI must be very nice.

**Decisions confirmed with the user:**
- **Stack:** Go + Wails v2 (WebView2 GUI) — Go is the proven language for tunneling tools (ngrok, frp); Wails gives a modern web-tech GUI in a single ~10 MB `.exe` with no bundled browser.
- **Distribution:** ONE combined executable `proxyforward.exe` that runs in *agent* (Server A) or *gateway* (Server B) role, with GUI by default plus `--headless` console mode and Windows-service mode.
- **Features:** all of — Minecraft-aware dashboard, real client IPs (PROXY protocol v2), metrics + live traffic graphs (optional Prometheus), multi-tunnel — but **every advanced feature is optional/off-by-default**; the core tunnel must be dead-simple and reliable.

The plan went through two review rounds with the user; the review identified four structural risks now designed in: (1) transport abstraction to escape TCP head-of-line blocking, (2) a service↔GUI IPC split to prevent split-brain, (3) precise splice shutdown semantics, (4) fuzzing + hardening of every internet-facing parser.

The working directory `C:\Users\eth\Downloads\proxyforward` is empty — greenfield project.

## Architecture

```
 Minecraft client ──TCP :25565──▶ ┌────────────────────┐        ┌───────────────────┐
                                  │ Server B (gateway) │        │ Server A (agent)  │
                                  │  public listeners  │◀──TLS──│  dials OUT to B   │──▶ localhost:25565
                                  │  control :8474     │ (mux)  │  (no portforward) │    (Minecraft server)
                                  └────────────────────┘        └───────────────────┘
```

- **Transport is an interface, not yamux types.** `internal/transport` defines `Transport` / `Session` / `Stream` interfaces consumed by agent and gateway. Default implementation: one outbound TCP+TLS connection multiplexed with `hashicorp/yamux` (control stream + one stream per player connection). Because everything rides one TCP connection, a single lost WAN packet stalls *all* streams (head-of-line blocking) — this is the design's known performance ceiling, so two escape hatches are planned behind the same interface: **(a)** frp-style per-connection mode (each `OpenConn` answered by a fresh outbound TCP+TLS data connection; control stays on the mux) as a config toggle, and **(b)** QUIC via `quic-go` (per-stream flow control, no HOL blocking, connection migration survives NAT rebinds/IP changes) as a drop-in later. Nothing outside `internal/transport` may import yamux.
- **Auth & TLS:** on first run the gateway generates a self-signed cert + random token and displays a one-line **pairing code** `pf1://host:8474/<token>#<cert-fingerprint>` (IPv6 literals bracketed). The agent pastes it; the fingerprint pins the cert (no CA needed), the token authenticates (constant-time compare). Pairing codes carry a **hostname when available** (gateway GUI encourages DDNS/stable names for dynamic-IP setups); the agent stores host, token, and fingerprint separately so the gateway address is editable later **without re-pairing**, and DNS is re-resolved on every reconnect attempt.
- **Identity & duplicate policy:** the agent's `Hello` carries a persistent random **agent ID** from day one. Reconnect by the *same* agent ID supersedes the old session (generation counter); a *different* agent presenting the same token while one is connected is **rejected with a clear error** ("another agent is already connected") — never supersede-flap between two machines.
- **Tunnels are agent-defined** (ngrok-style): agent registers `{name, type: tcp|udp, localAddr, requestedPublicPort, options}`; gateway validates (port availability + optional allowlist) and opens the public listener. `type` is reserved now — v1 implements TCP only, but the field makes Bedrock/Geyser UDP relay a protocol non-event later. Listeners close when the agent disconnects (unless the offline responder holds the port).
- **Data path:** client connects to public port → gateway opens a transport stream with an `OpenConn{tunnelID, clientAddr}` header → agent dials the local Minecraft server → bidirectional splice with byte counters. No tunnel-level compression ever — Minecraft traffic is already compressed post-login.
- **Liveness (single owner, fast):** the **agent** pings on the control stream every **5 s**, gateway echoes (doubles as the dashboard RTT); **15 s** read deadline on both sides; yamux keepalive *disabled* so there is exactly one liveness mechanism. Any data-stream write error marks the session suspect and triggers an immediate control-ping probe. The agent also subscribes to **Windows network-change and resume-from-sleep notifications** to reconnect instantly instead of waiting out a deadline. Reconnect uses exponential backoff + jitter with fresh DNS resolution per attempt.
- **Optional features (off by default, per-tunnel or global toggles):**
  - *Minecraft awareness:* the agent's health-check **status ping is the authoritative source** for MOTD/player count/version (sniffed counts drift). Passive sniffing — implemented buffer-and-replay (TeeReader-style) so the backend always receives pristine bytes — is used *only* for per-connection usernames, and must tolerate the 1.20.2 login/config-phase rework and legacy 0xFE pings. Offline responder: when the agent or local server is down, the gateway answers status pings with a configurable "Server offline" MOTD instead of a dead port.
  - *PROXY protocol v2:* agent prepends the PP2 header (real client IP from the gateway) when dialing the local server — for Paper/Velocity setups. GUI + docs state that PP2 and BungeeCord/Velocity IP-forwarding are **mutually exclusive per tunnel** to prevent ghost errors.
  - *Metrics:* per-connection/per-tunnel byte + latency counters feeding live GUI graphs (ring-buffer samples); optional Prometheus `/metrics` endpoint; optional per-tunnel bandwidth cap (`x/time/rate`-wrapped conns) to protect the gateway's uplink.

## Hard problems & mitigations (designed in, not bolted on)

### Correctness of the data pump
- **Splice half-close:** when one leg EOFs, propagate as `CloseWrite` on the other leg (`*net.TCPConn` has it; transport streams expose an equivalent that sends FIN while reads continue) and wait for **both** directions to drain before teardown — otherwise Minecraft disconnect messages get truncated into raw resets.
- **Stalled peers:** progress-refreshed write deadlines on both legs of every splice (reset on successful write), so a dead client can never park a goroutine forever. The control link's deadline does not cover data streams.
- **yamux `ConnectionWriteTimeout`:** defaults to 10 s and kills the *entire session* when the underlying conn stalls — set it deliberately (generous, aligned with our liveness budget), never inherited by accident.
- **Buffer bloat / chunk bursts:** `io.CopyBuffer` with pooled 128 KiB buffers (not `io.Copy`'s 32 KiB), yamux `MaxStreamWindowSize` at 1 MiB, `TCP_NODELAY` on end-to-end, generous OS socket buffers on the WAN link.

### Gateway lifecycle
- **Ghost-listener race:** the gateway's listener manager is a **single-goroutine actor** owning all bind/unbind sequencing. A superseding session closes the old transport session and confirms each listener's accept-loop exited (done-channel) *before* processing new registrations — re-registering a port can never race its own dying listener.
- **Accept-during-death race:** a client that lands on a public listener while the session is dying gets a graceful path — offline MOTD if enabled, else a clean close — never a goroutine erroring into the logs.
- **State & thread safety:** tunnel/session registry lives behind the same actor (single writer); hot counters are `atomic.Int64` on wrapped conns; GUI and Prometheus read immutable snapshots. `-race` in CI.

### Internet-facing hardening
- **Fuzz everything that reads untrusted bytes:** `go test -fuzz` targets for the VarInt/handshake parsers, the status responder, and the length-prefixed control framing; hard caps on declared lengths *before* allocation.
- **Control port (8474 will get scanned):** tight pre-auth read deadlines, hard pre-auth message-size cap, constant-time token comparison, auth-attempt rate limiting per IP.
- **Public listeners (Minecraft gets botted):** per-IP connection rate limits and a global connection cap, enforced at the gateway choke point; both configurable.

### Windows integration
- **Firewall & permissions:** first-run/wizard and `service install` add inbound firewall rules via `netsh advfirewall`; headless/service mode never shows the interactive firewall popup, so rules must be explicit. Rule status shown in GUI with one-click repair; rules removed on uninstall.
- **Elevation as a helper subprocess:** the GUI never elevates itself. Privileged steps relaunch `proxyforward.exe --elevated-task <add-firewall|install-service|…>` via `ShellExecute` "runas", do the one thing, and exit — UAC prompt scoped, main process stays unelevated.
- **Port conflicts with a name attached:** on bind failure, look up the owning process (`GetExtendedTcpTable` via `x/sys/windows`) and report "Port 25565 is in use by java.exe (PID 1234)" in GUI and logs.
- **Service↔GUI split-brain (missing subsystem, now designed):** once the Windows service is installed, the GUI becomes a **thin client to the daemon over a named pipe** (`\\.\pipe\proxyforward`, JSON-RPC reusing the control framing). On launch the GUI probes the pipe: daemon present → attach (dashboard, config edits, logs all via IPC); absent → run the engine in-process as before. Exactly one process ever owns ports and config. Service-mode config lives in **`%ProgramData%\proxyforward`** (a `LocalService` account can't see the configuring user's `%APPDATA%`), passed as an explicit path in the service arguments; GUI-mode config stays in `%APPDATA%\proxyforward`.

### Environment realities
- **"Is Minecraft actually running?"** — the agent health-checks each tunnel's local target: periodic TCP dial, upgraded to a real MC status ping when MC-awareness is on. Dashboard shows a three-segment status (local server / tunnel link / public port) so "Minecraft isn't running" is never confused with "link is down". Health state syncs to the gateway and can trigger the offline responder.
- **End-to-end self-test:** a "Test public reachability" button — the agent dials `gateway_host:public_port` across the real internet and completes a status ping through the full path, validating DNS, gateway firewall, router forwarding, listener, tunnel, and local server in one actionable check.
- **Dynamic gateway IP:** hostname-based pairing + per-attempt DNS re-resolution + editable gateway address (above).
- **Java/Minecraft client DNS caching:** the JVM caches DNS aggressively — if the gateway's public IP moves, running clients keep dialing the stale IP; nothing server-side fixes that. README + gateway GUI hints: give players a stable low-TTL hostname (ideally an SRV record), keep the gateway address stable, expect running clients to need a restart after an IP change.

### GUI plumbing
- **Wails event-bridge coalescing:** nothing per-packet or per-connection crosses the Go→JS bridge. One `tick` snapshot event at 2 Hz (stats, connection-table diff, link state) plus batched log lines (4 Hz flush, ring-capped ~2 000 lines).
- **Wails v2 has no system tray:** tray needs `energye/systray` (or similar), which has main-thread/message-loop quirks alongside Wails on Windows — **prototyped in milestone 1**, because a bad result forces a framework decision (Wails v3 alpha vs v2 + third-party tray) that must happen before the GUI is built.

## Project layout

```
proxyforward/
├── main.go                  # entry: CLI (agent|gateway|service|--elevated-task|version), GUI vs --headless
├── go.mod / wails.json
├── app/                     # Wails app struct: Go APIs bound to frontend, coalesced event emitters
├── internal/
│   ├── config/              # TOML config (%APPDATA% for GUI, %ProgramData% for service), defaults, validation, atomic save
│   ├── control/             # message types (Hello{agentID}, AuthOK, RegisterTunnel{type,…}, OpenConn, Ping, Stats…), length-prefixed JSON framing + fuzz targets, protocol version negotiation
│   ├── transport/           # Transport/Session/Stream interfaces; yamux-over-TLS impl (tuned windows, keepalive off, deliberate write timeout); per-connection mode; QUIC slot later
│   ├── link/                # session lifecycle: pairing parse/generate, cert gen + pinning, reconnect w/ backoff + DNS re-resolve, heartbeats, network-change/resume subscriptions
│   ├── agent/               # agent role: register tunnels, accept streams, dial local, splice w/ half-close semantics, health checks, PP2 injection, reachability self-test
│   ├── gateway/             # gateway role: control listener (pre-auth hardening), listener-manager actor, rate limits/conn caps, offline responder, port-conflict process lookup
│   ├── tunnel/              # tunnel registry + per-tunnel options shared by both roles
│   ├── mc/                  # Minecraft protocol: VarInt, handshake/status/login sniffing (buffer-and-replay), status responder + fuzz targets, 1.20.2 + legacy-ping tolerance
│   ├── metrics/             # counting conn wrapper, sample ring buffers, optional Prometheus, optional rate-limit wrapper
│   ├── ipc/                 # named-pipe JSON-RPC server (daemon) + client (GUI thin mode), reuses control framing
│   ├── logging/             # slog: file rotation + in-memory ring feeding the GUI, diagnostics-bundle export (logs + redacted config + version)
│   └── svc/                 # Windows service install/uninstall/run (kardianos/service), firewall rule add/remove/status via elevated helper
└── frontend/                # Vite + React + TypeScript + Tailwind
    └── src/ … (wizard, dashboard, tunnels, connections, logs, settings, tray-aware shell)
```

Key deps (kept light): `wails/v2`, `hashicorp/yamux`, `pelletier/go-toml/v2`, `kardianos/service`, `energye/systray` (pending M1 prototype), `prometheus/client_golang` (behind toggle), `x/sys/windows`, `x/time/rate`, stdlib `crypto/tls|x509`, `log/slog`, `spf13/cobra`.

## GUI (the "very nice" part)

Wails v2 + React + TS + Tailwind, dark theme default with light option, system-tray icon (status-colored) with minimize-to-tray. Go pushes coalesced snapshot events to the frontend (see GUI plumbing above).

Screens:
1. **Setup wizard** (first run): pick role → gateway shows the pairing code with a copy button (and a nudge toward a stable hostname); agent has a single paste box → "Connected ✓". One tunnel (25565→25565) pre-filled for the agent.
2. **Dashboard:** big three-segment status (local server / tunnel link / public port), link uptime and RTT, live bandwidth graph, "Test public reachability" button, and — when MC-awareness is on — MOTD, player count, player list (poll-sourced).
3. **Tunnels:** card list; add/edit dialog (name, local address, public port, toggles for MC-aware / PP2 / offline-MOTD / bandwidth cap) with the PP2-vs-proxy-forwarding exclusivity explained inline.
4. **Connections:** live table of active connections (client IP, username if sniffed, duration, bytes up/down).
5. **Logs:** streaming, level-filterable, copyable, plus one-click **diagnostics bundle export** (logs + redacted config + version) for support.
6. **Settings:** control port, regenerate pairing token, gateway address (agent side, editable without re-pairing), theme, autostart, metrics/Prometheus toggle, firewall rule status + repair, "Install as Windows service" (via elevation helper), config file location.

Charts drawn dependency-light (`recharts` or hand-rolled SVG sparklines) — follow the `dataviz` skill when building them.

## Execution modes

- `proxyforward.exe` → GUI. On launch, probe `\\.\pipe\proxyforward`: daemon running → attach as thin client; else run engine in-process (wizard if unconfigured, else last role).
- `proxyforward.exe agent|gateway --headless [--config path]` → console mode with structured, colorized log output.
- `proxyforward.exe service install|uninstall|start|stop --role gateway` → Windows service (headless daemon exposing the IPC pipe), config in `%ProgramData%\proxyforward`, installed via the elevation helper.
- `proxyforward.exe --elevated-task <task>` → scoped UAC helper (firewall rules, service install), does one thing and exits.
- `wails build` emits the single Windows/amd64 `.exe` (WebView2 runtime preinstalled on Win 10/11); optional NSIS installer target later.

## Implementation milestones

1. **Scaffold + risk spikes:** `go mod init`, `wails init` (React-TS template), directory layout, `config` package (dual location logic) with TOML round-trip, `logging`, cobra CLI skeleton. **Spike: system tray alongside Wails v2** (`energye/systray`) — go/no-go on v2+tray vs Wails v3 alpha before any GUI work.
2. **Core tunnel, headless (the heart):** `control` framing + `transport` interface with the yamux-over-TLS implementation (tuned windows, keepalive off, deliberate write timeout) + `link` lifecycle (pairing, cert pinning, 5 s/15 s heartbeat, reconnect with DNS re-resolve) + minimal `agent`/`gateway` + listener-manager actor + splice with half-close/drain/write-deadline semantics + agent ID/duplicate-rejection. Milestone exit: TCP echo through the public port; rapid kill/restart loop clean (ghost-listener test); burst benchmark passes; goleak/fd-count clean after the loop.
3. **Hardening + Windows integration:** fuzz targets for `mc` and `control` framing + pre-auth deadlines/caps/rate limits + public-listener conn caps; port-conflict process lookup; firewall rule management + elevation helper; local-target health checks; network-change/resume-triggered reconnect; Windows service mode + **IPC pipe (daemon side)**; config hot-apply for tunnel edits.
4. **GUI:** Wails bindings in `app/` with coalesced events, all six screens, wizard flow, tray (per M1 decision), **GUI-as-thin-client attach mode over IPC**, reachability self-test button, diagnostics bundle export.
5. **Optional features (each an isolated toggle):** `mc` polling (authoritative) + buffer-and-replay sniffing (usernames) + offline responder; PP2 injection with exclusivity guard; `metrics` counters + graphs + Prometheus + per-tunnel bandwidth cap. Per-connection transport mode toggle.
6. **Polish:** icons/branding, `wails build` release config, README (two-machine walkthrough, SRV/DNS guidance, PP2 notes), chaos/soak suite finishing touches, optional GitHub Actions `windows-latest` CI for service/netsh smoke paths.

Reserved in the protocol now, implemented later: `type: udp` relay (Bedrock/Geyser), QUIC transport, multi-agent-per-gateway.

## Verification

- **Unit + fuzz tests:** table-driven tests for `mc` VarInt/handshake (real packet captures as fixtures, incl. 1.20.2 and legacy 0xFE pings), `control` framing round-trip, pairing-code parse/generate (incl. IPv6 brackets), config validation; `go test -fuzz` corpora for VarInt, handshake, status responder, and framing kept in-repo.
- **Loopback end-to-end (single machine, scripted, run constantly):** `gateway --headless` on :8474/:35565 + `agent --headless` against a local TCP echo; assert byte round-trip; rapid agent kill/restart loop proves ghost-listener sequencing and duplicate-agent rejection; 64 MB burst benchmark asserts throughput and low cross-stream latency; half-close test asserts the final bytes before EOF arrive intact.
- **Chaos + leak suite:** in-process toxiproxy-style shim between gateway and agent (latency, jitter, resets, stalls) driving reconnect and `ConnectionWriteTimeout` paths; `-race` everywhere; `uber-go/goleak` + fd-count assertions after every chaos scenario; a soak run (hours, scripted) watching goroutine/fd/memory counts — tunnel daemons die from slow leaks.
- **Real Minecraft test:** local Paper server + real client through the gateway port; MC-awareness on → username + poll-sourced MOTD/player count in dashboard; PP2 on (`proxy-protocol: true`) → real client IP in server logs; agent killed mid-session → offline MOTD served, disconnect message arrives intact (not a raw reset).
- **Windows-specific:** service install/uninstall smoke (config read from `%ProgramData%`, firewall rules present, GUI attaches over the pipe, no double-instance), elevation helper UAC flow, network-adapter disable/enable triggers instant reconnect; optionally in `windows-latest` CI.
- **GUI:** `wails dev` for iteration; walk wizard → dashboard → add-tunnel → reachability test → logs → diagnostics export; verify tray behavior and `--headless` parity.
