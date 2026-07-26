# QUIC datagrams for udp tunnels

Date: 2026-07-26. Author: Claude (Opus 5). Status: design approved (frp-parity
tranche, item 5), implementation in progress on `feat/quic-datagrams`.

## Context

A udp tunnel today is a datagram flow wearing a stream's clothes. The gateway
presents a UDP port as a `net.Listener` (`gateway/udp.go`), and the tunnel leg is
a QUIC or yamux **stream** carrying length-framed datagrams (`relay/datagram.go
DatagramStream`), so `relay.Splice`, the conn gate, pause, eviction and the caps
all apply to UDP through the code that already implements them for TCP. That is
the invariant worth keeping.

What it costs, on the QUIC transport only, is head-of-line blocking *within one
player's flow*: a QUIC stream is reliable and ordered, so a lost packet carrying
datagram N stalls datagram N+1 until the retransmit lands. For a game protocol
that already tolerates loss and reorders on its own — Bedrock/RakNet, Simple
Voice Chat — that is exactly the wrong trade: the tunnel is adding latency to
recover data the application would rather have dropped.

QUIC has an unreliable datagram extension (RFC 9221) for precisely this, and
quic-go implements it. The other two transports do not: yamux is TCP, and a
per-conn data connection is TCP. So this is a QUIC-only fast path with a
stream-shaped fallback, not a new transport.

## Decisions

### 1. Three gates, all required

The fast path engages only when **all** of these hold:

1. `CapUDPDatagram` was negotiated. The agent offers it unconditionally (it is
   harmless on a TCP transport — nothing will ever satisfy gate 2), and the
   gateway lists it in `SupportedCapabilities` once both ends honor it.
2. The session's `transport.Session` implements the datagram seam
   (`DatagramSession`), which only the QUIC wrapper does.
3. The peer's QUIC advertised datagram support (`SupportsDatagrams()` — RFC 9221
   is negotiated per connection, so a peer built without it says no).

Any gate failing leaves the existing stream leg in place, byte for byte. A
mixed-version pair, a mux agent, or a QUIC peer that disabled datagrams all get
today's behaviour with no new wire bytes.

### 2. The stream stays the flow's lifecycle anchor

Every udp flow still opens a stream and still carries `open_conn` on it. The
datagram path is an *optimization on the payload*, never on the lifecycle:
teardown, eviction, `CloseWrite`, the idle reaper and conntrack all continue to
work off the stream, unchanged. A flow whose datagrams all go by the fast path
still has its stream open and idle.

This is what keeps the change small. Nothing in the gateway's session lifecycle,
the actor, or the agent's serve path learns about datagrams.

### 3. Flow identity: a 4-byte header, gateway-allocated

A QUIC connection's datagrams are per-connection, not per-stream, so a datagram
must say which flow it belongs to. Wire form:

```
[4-byte big-endian flowID][payload]
```

`flowgramHdr = 4`. The gateway allocates the flowID when it opens the flow and
puts it in `OpenConn.FlowID` (`omitempty`, so a stream-only flow adds no bytes
and a legacy peer sees an unchanged frame). 0 means "stream only" — no second
round trip, no new message type.

### 4. `FlowMux` demultiplexes; `FlowgramConn` is a `relay.Conn`

Per session, lazily on the first eligible flow:

- `FlowMux` runs **one** goroutine reading `ReceiveDatagram`, splitting the
  4-byte header and routing the payload to a per-flow bounded queue. Unknown
  flowIDs are dropped (a flow that just closed, or a peer's stale retransmit).
  Queue depth `flowQueueDepth = 64`, drop-newest — the same reasoning as
  `udpSessionQueue`: blocking here would let one player stall every player on
  the connection, and dropping is what a real socket does under the same
  pressure.
- `FlowgramConn` implements `relay.Conn`, so **`Splice` is untouched** — the udp
  invariant. `Read` pulls whole datagrams from the queue with real deadline
  support (the timer-over-channel pattern from `udpSession.Read`, so the idle
  reaper keeps working). `Write` sends a datagram when the payload fits, and
  falls back to the stream when it does not.
- `Close`/`CloseWrite` delegate to the stream, per decision 2.

### 5. Oversize falls back to the stream, driven by the error

A QUIC datagram must fit the path MTU; there is no fragmentation. So:

- `len(payload) ≤ quicDatagramTryMax` (1200) → try `SendDatagram`.
- Anything larger, **or** a `SendDatagram` that reports the datagram is too big,
  → write it on the stream leg as a length-framed datagram.

The constant is an optimization bound, not a correctness bound: correctness comes
from the error. quic-go's too-large error is translated to an exported
`transport.ErrDatagramTooLarge` so nothing outside `internal/transport` imports
quic-go.

A companion per-flow goroutine merges the stream leg's framed datagrams into the
same queue, so the receiver sees one ordered-ish sequence regardless of which
path each datagram took.

### 6. Nothing goes unreliably until the peer has proved it registered

Added during implementation, after an e2e flake made the hole obvious. The flow
id travels in `open_conn`, **on the stream**. Between the gateway writing that
header and the agent reading it and registering, a datagram naming the flow
arrives at a peer whose table does not contain it — and is dropped, silently
losing a player's packet. The stream path never loses one, so this would be a
regression dressed as an optimization.

The interlock is one bit per flow, and no new message:

- A `FlowgramConn` sends unreliably only once it has *received* a datagram for
  its flow. The peer cannot have sent one without registering first, so receipt
  is proof.
- On becoming routable, each side sends an **announce**: a datagram carrying its
  flow id and no payload. That is what lets the peer start without waiting for
  traffic. Losing it costs nothing — the first real datagram says the same thing.
- Until then the payload takes the stream. Correct, just not accelerated.

Cost: exactly one datagram per flow takes the stream (the e2e tests assert that
number rather than pretending it is zero). Deadlock-free, because the announces
are unconditional.

### 7. Cross-path reordering is accepted

A datagram sent on the stream can arrive after one sent unreliably later, because
the two paths have independent delivery. This is UDP: reordering is a property
the protocol already tolerates, and every protocol this tunnel carries sequences
its own packets. Per-path arrival order is preserved; global order is not
promised, and was never promised by UDP.

## What this does not do

- **No datagram path for tcp tunnels.** A TCP tunnel is a byte stream; unreliable
  delivery is not an option.
- **No datagram path on yamux or per-conn.** Both are TCP.
- **No MTU discovery.** `quicDatagramTryMax` is a conservative constant and the
  error is authoritative.
- **No reordering buffer.** See decision 7.

## Commit sequence

1. `control`: `CapUDPDatagram` + `OpenConn.FlowID`, un-advertised.
2. `transport`: the `DatagramSession` seam + `EnableDatagrams` (burst ×3 — must
   be a no-op on its own).
3. `relay`: `flowgram.go` + unit tests (burst ×3).
4. `gateway`: allocate the flowID, register the flow, splice through
   `FlowgramConn`.
5. `agent`: the symmetric path in its datagram serve.
6. Advertise + e2e.

## Verification

Unit: `TestFlowgramDemuxRoutesByFlowID`, `TestFlowgramOversizeFallsBackToStream`,
`TestFlowgramQueueDropsWhenFull`, `TestFlowgramReadDeadline`,
`TestFlowgramCloseUnregisters`, `TestQUICDatagramSessionSupport`.

e2e: `TestQUICDatagramRoundTrip` (fast-path proven by a counter, never by
timing), `TestQUICDatagramOversizeFallsBack` (a 1400-byte datagram arrives
intact), `TestQUICDatagramLegacyPeerRidesStream`, `TestQUICDatagramFlowIsolation`.
The existing udp e2e suite must stay green over QUIC, and the burst floor is run
best-of-3 before and after every `relay/` and `transport/` commit.
