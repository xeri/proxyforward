package transport

import (
	"time"

	"github.com/quic-go/quic-go"
)

// quicALPN is the ALPN protocol id offered on every QUIC handshake. QUIC
// mandates ALPN (unlike yamux-over-TCP, which sets none), so withALPN injects
// this when the caller's tls.Config leaves NextProtos empty. The "/1" tracks the
// framing on the wire, not ProtocolVersion — both peers of a given build agree.
const quicALPN = "pf-quic/1"

// Deliberate quic-go tuning — the QUIC analogue of muxConfig, same rationale:
//   - KeepAlivePeriod 0 (OFF): the application-level ping (agent every 5 s) is
//     the single liveness owner, exactly as with yamux keepalive. Two mechanisms
//     produce confusing failures.
//   - MaxIdleTimeout 30 s: QUIC tears down a connection after this much silence.
//     It must sit ABOVE the app liveness budget (15 s idle read deadline + margin)
//     so the heartbeat — not QUIC — decides when the link is dead. Mirrors yamux's
//     30 s ConnectionWriteTimeout.
//   - Receive windows: 1 MiB initial stream window matches yamux MaxStreamWindowSize
//     so a Minecraft chunk burst fits in flight before auto-tuning ramps up; the max
//     windows give auto-tune headroom on fat pipes.
//   - MaxIncomingStreams 1<<16: yamux imposes no per-session stream cap; quic-go
//     defaults to 100, which would block OpenStreamSync once a busy proxy crosses
//     100 concurrent players. One player = one bidi stream, so lift the ceiling.
//   - MaxIncomingUniStreams -1: we only use bidirectional streams; disallow uni.
//
// Congestion control is not pluggable in quic-go's public API (no BBR); we accept
// its CUBIC-family default.
func quicConfig() *quic.Config {
	return &quic.Config{
		KeepAlivePeriod: 0,
		MaxIdleTimeout:  30 * time.Second,
		// HandshakeIdleTimeout bounds how long a dial waits for the server's
		// handshake reply (the dial aborts at 2×). It is the auto ladder's
		// UDP-blocked detector: when UDP is dropped the QUIC dial fails after this
		// and the agent falls back to per-conn. Generous enough to tolerate a
		// slow/lossy but working link (a handshake is 1 RTT + PTO retransmits) so
		// QUIC isn't false-rejected; the cost is paid at most once per re-probe.
		HandshakeIdleTimeout:           5 * time.Second,
		InitialStreamReceiveWindow:     1 << 20,
		MaxStreamReceiveWindow:         6 << 20,
		InitialConnectionReceiveWindow: 2 << 20,
		MaxConnectionReceiveWindow:     12 << 20,
		MaxIncomingStreams:             1 << 16,
		MaxIncomingUniStreams:          -1,
		EnableDatagrams:                false,
	}
}
