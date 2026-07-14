<div align="center">

# proxyforward

**Make your Minecraft server public — no port-forwarding required.**

An ngrok-style reverse tunnel: the Minecraft machine dials *out* to a machine that can
accept inbound connections, and that machine relays player traffic back through the tunnel.

[![CI](https://github.com/xeri/proxyforward/actions/workflows/ci.yml/badge.svg)](https://github.com/xeri/proxyforward/actions/workflows/ci.yml)
[![Security](https://github.com/xeri/proxyforward/actions/workflows/security.yml/badge.svg)](https://github.com/xeri/proxyforward/actions/workflows/security.yml)
[![Go](https://img.shields.io/badge/Go-1.25-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![Platform](https://img.shields.io/badge/platform-Windows-0078D4)](#)
[![GUI](https://img.shields.io/badge/GUI-Wails%20%2B%20React%2019-61DAFB?logo=react&logoColor=black)](https://wails.io)
[![TLS](https://img.shields.io/badge/TLS-1.3%20only-2E7D32)](#-security-model)
[![License](https://img.shields.io/badge/license-GPL--3.0-blue)](LICENSE)

<img src="preview.png" alt="proxyforward dashboard" width="820">

**📖 [Read the wiki](https://github.com/xeri/proxyforward/wiki)** — installation, the pairing
walkthrough, firewall and DNS setup, config and CLI reference, troubleshooting, and an honest
list of [what isn't built yet](https://github.com/xeri/proxyforward/wiki/Not-Yet-Implemented).

</div>

---

## How it works

```
Minecraft players                 Gateway (Server B)                  Agent (Server A)
      │                        public IP, can port-forward          behind NAT, dials OUT
      │                                   │                                  │
      └──── TCP :25565 ──────────▶  public listener                          │
                                          │                                  │
                                          ◀═══ one TLS 1.3 conn, yamux ═════ ┤
                                          │    (control + 1 stream/player)   │
                                          │                                  │
                                          │                                  └──▶ localhost:25565
                                          │                                       your Minecraft server
```

One `proxyforward.exe` runs in **either role** — a WebView2 GUI (Wails + React), a
`--headless` console mode, or a Windows service. The agent keeps a single outbound
TCP+TLS connection open to the gateway, multiplexed with yamux into a control stream
plus one stream per player. Everything is programmed against a `transport.Session`
interface, so a per-connection mode (sidesteps TCP head-of-line blocking) and a future
QUIC transport drop in without touching agent or gateway code.

## Download

Grab the installer or the portable exe from the
[latest release](https://github.com/xeri/proxyforward/releases/latest). Windows 10/11 x64;
you need it on **both** machines. There is no Linux or macOS build — the engine is
Windows-only (named pipes, service, firewall integration).

The binaries are **not code-signed**, so SmartScreen will warn on first run
("unknown publisher"). Rather than asking you to trust that, every release is built by
[this workflow](.github/workflows/release.yml) and carries a provenance attestation you
can verify:

```
gh attestation verify proxyforward-<version>-windows-amd64.exe -R xeri/proxyforward
```

`SHA256SUMS.txt` and an SPDX SBOM ship with each release.

## Quick start (two machines)

**On the public machine — the gateway:**

1. **Launch** `proxyforward.exe` and choose **"This faces the internet."**
2. **Enter** your public hostname (a stable DNS/DDNS name is strongly recommended — see
   [DNS and dynamic IPs](#dns-and-dynamic-ips)) and click **Start gateway**.
3. **Copy** the pairing code it shows: `pf1://host:8474/…#sha256:…`
4. **Forward** port **25565** (or your chosen public port) on the router to this machine,
   and allow the inbound firewall rule when prompted
   (Settings → Windows integration → *Add rule*).

**On the Minecraft machine — the agent:**

1. **Launch** `proxyforward.exe` and choose **"This hosts Minecraft."**
2. **Paste** the pairing code. It validates instantly (`✓ certificate pinned`).
3. **Confirm** the local address (`127.0.0.1:25565`) and public port, then click **Connect**.

The agent's dashboard turns green and players join at `your-host:25565`. Use
**Dashboard → Test public reachability** to validate the whole path
(DNS → firewall → router → tunnel → server) in one click.

## ⚡ Engineered for the hot path

The relay is a purpose-built splice, not an `io.Copy` wrapper — every default was
questioned and most were replaced:

| | Optimization | Why it matters |
|---|---|---|
| 📦 | **128 KiB pooled buffers** (`sync.Pool`) | `io.Copy`'s 32 KiB default throttles chunk-load bursts on fat pipes — [`relay.go`](internal/relay/relay.go) |
| 🪟 | **1 MiB yamux stream windows** (4× default) | a full Minecraft chunk burst fits in flight on one stream without stalling — [`yamux.go`](internal/transport/yamux.go) |
| 💓 | **One liveness owner** | yamux keepalive is *off*; the app-level 5 s ping (which also feeds the dashboard RTT) is the single source of truth, with a 30 s conn-write timeout as backstop |
| ⏱️ | **`TCP_NODELAY` end-to-end** | no Nagle-induced latency on either leg, and player data never enters the control path |
| 🤝 | **FIN-preserving half-close** | EOF on one leg becomes `CloseWrite` on the other while the opposite direction keeps draining — a disconnect message written just before close arrives intact instead of becoming a raw reset |
| 🛡️ | **2-minute write-stall deadline** | a peer that stops draining can never park a splice goroutine forever; byte counters are atomic, snapshotted lock-free by the GUI and metrics |
| 🎭 | **Single-goroutine gateway actor** | all session/listener lifecycle mutations are naturally serialized — a re-registered port can never race its own dying listener (the *ghost-listener guarantee*: the port is provably free before handoff) — [`actor.go`](internal/gateway/actor.go) |

And it's **enforced in CI**, not just claimed: the loopback e2e suite pushes a 64 MiB
burst through the full agent → gateway → client path and fails if round-trip throughput
drops below **20 MiB/s** or a concurrent stream's RTT exceeds **500 ms** mid-burst — a
regression guard against head-of-line blocking ([`e2e_test.go`](internal/e2e/e2e_test.go)).

## 🔒 Security model

- **TLS 1.3 only.** The gateway generates a self-signed **ECDSA P-256** certificate on
  first run; the pairing code pins its **SHA-256 fingerprint** — no CA, no third party,
  nothing to leak.
- **Constant-time comparisons** for both the auth token and the certificate fingerprint
  (`crypto/subtle`).
- **Pre-auth hardening:** the entire unauthenticated prologue (TCP accept → TLS
  handshake → hello frame) must finish within **10 s**, and pre-auth frames are capped at
  **4 KiB** (vs 64 KiB post-auth) — internet scanners get nothing to chew on.
- **fail2ban-style auth limiter:** 10 failed attempts per minute per IP; successes never
  count, so a legitimately flapping agent is never locked out while a token brute-forcer is.
- **Connection gates:** 4096 global / 32 per-IP, plus a public-port allowlist.

## 🔁 Built to stay up

- **Full-jitter exponential backoff** — 1 s → 60 s cap, sequence resets after 60 s of
  stable connection. Full jitter means a gateway restart doesn't trigger a
  thundering-herd of reconnects.
- **Instant reconnect on network change** — Windows `NotifyAddrChange` and a
  wall-clock-jump resume-from-sleep detector short-circuit the backoff instead of
  waiting out a read deadline.
- **Identity, not just auth** — the agent carries a persistent random ID. A reconnect by
  the same ID supersedes the old session; a *different* agent on the same token gets a
  clear rejection instead of the two flapping forever.
- **Fresh DNS on every attempt** — dynamic IPs just work; the gateway address is even
  editable later without re-pairing.
- **Health you can see** — 5 s local server probes, plus RFC 3550-style jitter EWMA and
  packet-loss tracking derived from the heartbeat, rolled into one dashboard health score.

## Features

Per-tunnel options (**Tunnels → edit**, off by default):

| Feature | What it does |
|---|---|
| **Minecraft-aware** | polls the server for MOTD / player count / version and sniffs usernames for the Connections view |
| **PROXY protocol v2** | prepends a PP2 header when dialing the local server so Paper/Velocity see the real player IP (set `proxy-protocol: true` in `paper-global.yml`). ⚠️ **Mutually exclusive** with BungeeCord/Velocity IP-forwarding on the same server — enabling both causes ghost errors |
| **Offline MOTD** | a message the gateway serves when the agent or server is down, instead of a dead port. Leave blank for a clean disconnect |
| **Bandwidth cap** | per-tunnel throughput limit to protect the gateway uplink |

Global toggles live in **Settings**: transport mode, Prometheus `/metrics`, abuse limits
(max connections global/per-IP, auth attempts/min), logging.

### DNS and dynamic IPs

<details>
<summary>Why a stable hostname matters (and what to do on a dynamic IP)</summary>

<br>

Minecraft's JVM caches DNS aggressively. If the gateway's public IP changes,
already-connected clients keep dialing the stale IP until they restart — nothing
server-side can fix that. So:

- Give players a **stable, low-TTL hostname** (ideally a `_minecraft._tcp` SRV record so
  you can also hide the port).
- On a dynamic residential IP, use DDNS and put that hostname in the gateway's public
  address — pairing codes and reconnect logic re-resolve it every time.

</details>

## 📊 Observability & Windows citizenship

- **RRD-style traffic history** — five resolution tiers from **100 ms** buckets (live
  graph) up to **1-day** buckets (~3 years retention), with rate OHLC candles per bucket.
  Persistent tiers are saved atomically, so a crash never corrupts history.
- **Logging that respects your disk** — 10 MiB × 3 rotating files plus an in-memory ring
  the GUI reads live.
- **Locked-down IPC** — the GUI attaches to the engine over a named pipe whose ACL admits
  only Administrators, SYSTEM, and the interactive user.
- **One UAC prompt, ever** — the firewall rule is added via `netsh advfirewall`, scoped
  to the program (not a port), with one-click status/repair in the GUI.
- **Diagnostics with names attached** — a port conflict reports
  *"Port 25565 is in use by java.exe (PID 1234)"* via `GetExtendedTcpTable`, not a bare
  `bind: address already in use`.

## CLI

```
proxyforward                      # GUI (attaches to a running service, else runs the engine)
proxyforward gateway --headless   # run the gateway in the console
proxyforward agent   --headless   # run the agent in the console
proxyforward pair <code>          # configure this machine as an agent from a pairing code
proxyforward firewall <status|add|remove>
proxyforward service <install|uninstall|start|stop> --role gateway
```

When installed as a Windows service the engine runs headless (config in
`%ProgramData%\proxyforward`) and the GUI attaches to it over the named pipe as a thin
client — exactly one process ever owns the ports.

## Building

```
wails build              # produces the single Windows/amd64 proxyforward.exe
```

## Development

```
cd frontend && npm install
wails dev                # full app with hot-reload frontend

# UI-only iteration in a plain browser (mocked Go bridge):
cd frontend && npm run dev
#   http://localhost:5173/?mock=agent      (or ?mock=gateway / ?mock=wizard)
```

## Testing

```
go test ./...
```

8 fuzz targets on the internet-facing parsers (control frames, Minecraft
handshake/VarInt/packet, login sniffer, offline responder) and an in-process loopback
e2e suite that is goroutine-leak-checked with `goleak` and enforces the
throughput/latency floor described above.

`go test` runs the fuzz **seed corpora**; the targets are actually fuzzed nightly
([`fuzz.yml`](.github/workflows/fuzz.yml)). Every push also runs the race detector, the
burst floor, CodeQL, and `govulncheck` — see [`ci.yml`](.github/workflows/ci.yml) and
[`security.yml`](.github/workflows/security.yml).

A fresh clone must build the frontend once before any Go command
(`cd frontend && npm ci && npm run build`) — `main.go` embeds `frontend/dist`.

## License

[GPL-3.0](LICENSE)
