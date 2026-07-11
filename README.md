# proxyforward

An ngrok-style reverse tunnel that makes a Minecraft server reachable from the
internet **even when it can't port-forward**. The Minecraft machine dials *out*
to a second machine that can accept inbound connections; that machine exposes
the public port and relays player traffic back through the outbound link.

```
 Minecraft client ──TCP :25565──▶  Server B (gateway)  ◀──TLS mux──  Server A (agent)  ──▶ localhost:25565
                                   public, port-forwards       dials out, behind NAT      (your Minecraft server)
```

One `proxyforward.exe` runs in either role and ships a modern WebView2 GUI
(Wails + React), a `--headless` console mode, and a Windows-service mode.

## Quick start (two machines)

**On the public machine (gateway — Server B):**

1. Launch `proxyforward.exe`, choose **“This faces the internet.”**
2. Enter your public hostname (a stable DNS/DDNS name is strongly recommended —
   see [DNS](#dns-and-dynamic-ips)) and click **Start gateway**.
3. Copy the **pairing code** it shows (`pf1://host:8474/…#sha256:…`).
4. Make sure port **25565** (or your chosen public port) is forwarded on the
   router to this machine, and allow the inbound firewall rule when prompted
   (Settings → Windows integration → *Add rule*).

**On the Minecraft machine (agent — Server A):**

1. Launch `proxyforward.exe`, choose **“This hosts Minecraft.”**
2. Paste the pairing code. It validates instantly (`✓ certificate pinned`).
3. Confirm the local address (`127.0.0.1:25565`) and public port, click
   **Connect**.

The agent’s dashboard turns green and players can join at
`your-host:25565`. Use **Dashboard → Test public reachability** to validate the
whole path (DNS → firewall → router → tunnel → server) in one click.

## How it works

- **Transport:** one outbound TCP+TLS connection multiplexed with yamux — a
  control stream plus one stream per player. Everything is programmed against a
  `transport.Session`/`Stream` interface, so a per-connection mode (avoids TCP
  head-of-line blocking) and a future QUIC transport drop in without touching
  agent/gateway code.
- **Pairing & security:** the gateway generates a self-signed cert and random
  token on first run. The pairing code pins the cert fingerprint (no CA needed)
  and carries the token (constant-time compared). The agent stores host, token
  and fingerprint separately, so the gateway address is editable later **without
  re-pairing**, and DNS is re-resolved on every reconnect.
- **Identity:** the agent carries a persistent random ID. A reconnect by the
  same ID supersedes the old session; a *different* agent on the same token is
  rejected with a clear error rather than flapping.
- **Liveness:** the agent pings every 5 s (also the dashboard RTT); a 15 s read
  deadline on both sides is the single liveness mechanism (yamux keepalive is
  off). Windows network-change and resume-from-sleep events trigger an instant
  reconnect instead of waiting out the deadline.
- **Data pump:** bidirectional splice with half-close semantics — when one leg
  EOFs it becomes a `CloseWrite` (FIN) on the other and the opposite direction
  keeps draining, so a Minecraft disconnect message arrives intact instead of a
  raw reset. Pooled 128 KiB buffers and `TCP_NODELAY` end-to-end.

## Optional features (off by default, per tunnel)

Configured in **Tunnels → edit**:

- **Minecraft-aware** — poll the server for MOTD / player count / version and
  sniff usernames for the Connections view.
- **PROXY protocol v2** — prepend a PP2 header when dialing the local server so
  Paper/Velocity see the real player IP. **Mutually exclusive** with
  BungeeCord/Velocity IP-forwarding on the same server — enabling both causes
  ghost errors. (Set `proxy-protocol: true` in Paper’s `paper-global.yml`.)
- **Offline MOTD** — a message the gateway serves when the agent or server is
  down, instead of a dead port. Leave blank for a clean disconnect.
- **Bandwidth cap** — per-tunnel throughput limit to protect the gateway uplink.

Global toggles live in **Settings**: transport mode, Prometheus `/metrics`,
abuse limits (max connections global/per-IP, auth attempts/min), logging.

## DNS and dynamic IPs

Minecraft’s JVM caches DNS aggressively. If the gateway’s public IP changes,
already-connected clients keep dialing the stale IP until they restart — nothing
server-side can fix that. So:

- Give players a **stable, low-TTL hostname** (ideally a `_minecraft._tcp` SRV
  record so you can also hide the port).
- On a dynamic residential IP, use DDNS and put that hostname in the gateway’s
  public address — pairing codes and reconnect logic re-resolve it every time.

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
`%ProgramData%\proxyforward`) and the GUI attaches to it over a named pipe as a
thin client — exactly one process ever owns the ports.

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

`go test ./...` runs unit, fuzz, and loopback end-to-end tests.
