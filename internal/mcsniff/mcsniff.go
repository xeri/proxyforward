// Package mcsniff glues the Minecraft login sniffer (internal/mc) to the
// relay read-tap (internal/relay) and the connection registry
// (internal/conntrack). It lives in its own package because it depends on all
// three, while relay must stay dependency-light (conntrack imports relay, so
// relay may not import conntrack).
package mcsniff

import (
	"encoding/hex"

	"proxyforward/internal/conntrack"
	"proxyforward/internal/mc"
	"proxyforward/internal/relay"
)

// Tap wraps the client-facing leg of a splice so the login handshake is
// sniffed and, when a player is identified, recorded on entry via
// SetPlayer. The returned conn forwards bytes unchanged; pass it to
// relay.Splice in place of the raw leg. Call only for Minecraft-aware
// tunnels.
func Tap(clientLeg relay.Conn, entry *conntrack.Entry) relay.Conn {
	sn := mc.NewSniffer()
	return relay.NewTap(clientLeg, func(b []byte) bool {
		if !sn.Feed(b) {
			return false // need more bytes
		}
		if oc, ok := sn.Outcome(); ok && oc.Login != nil {
			entry.SetPlayer(conntrack.PlayerInfo{
				Name:     oc.Login.Name,
				UUID:     uuidString(oc.Login),
				Protocol: oc.Handshake.ProtocolVersion,
			})
		}
		return true // done sniffing, whatever the verdict
	})
}

// uuidString formats the client-declared UUID as dashed lowercase hex, or ""
// when the login packet carried none (older protocols, offline mode).
func uuidString(ls *mc.LoginStart) string {
	if !ls.HasUUID {
		return ""
	}
	var zero [16]byte
	if ls.UUID == zero {
		return ""
	}
	h := hex.EncodeToString(ls.UUID[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}
