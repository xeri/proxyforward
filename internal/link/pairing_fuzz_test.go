package link

import (
	"strings"
	"testing"
)

// FuzzParsePairingCode throws arbitrary strings at the pairing/deep-link parser. A
// pxf:// code reaches this parser straight from an OS deep link (a web page can fire
// one), so it must never panic, must never accept a code that violates its own
// invariants, and — the sharp property — must faithfully re-emit anything it
// accepts: parse∘String is a fixed point, or a clicked link could round-trip into a
// different gateway/token than the one shown.
func FuzzParsePairingCode(f *testing.F) {
	fp := "sha256:" + strings.Repeat("ab", 32)
	f.Add("pxf://gw.example.com:8474/v1/pair/deadbeef#" + fp)
	f.Add("pxf://gw.example.com:8474/v1/pair/anothertok#" + fp)
	f.Add("pxf://[2001:db8::1]:8474/v1/pair/tok#" + fp)
	f.Add("pxf://gw:8474/v1/join/tok#" + fp)
	f.Add("pxf://a%20b:8474/v1/pair/t%2Fok#" + fp)
	f.Add("not-a-url")
	f.Add("")
	// v2: one grouped payload, no fragment. The seeds cover a valid code, the same
	// code as a human might retype it, and one with a character damaged — the three
	// shapes the parser must tell apart.
	v2 := PairingCode{Host: "gw.example.com", Port: 8474,
		Fingerprint: "sha256:" + strings.Repeat("1c", v2PinBytes),
		Token:       "tkt_" + strings.Repeat("a5", v2TicketBytes)}.String()
	f.Add(v2)
	f.Add(strings.ToUpper(v2))
	f.Add(strings.ReplaceAll(v2, "-", ""))
	f.Add(v2[:len(v2)-1] + "q")
	f.Fuzz(func(t *testing.T, s string) {
		pc, err := ParsePairingCode(s)
		if err != nil {
			return
		}
		if pc.Host == "" {
			t.Fatalf("accepted empty host from %q", s)
		}
		if pc.Port < 1 || pc.Port > 65535 {
			t.Fatalf("accepted out-of-range port %d from %q", pc.Port, s)
		}
		if pc.Token == "" || strings.Contains(pc.Token, "/") {
			t.Fatalf("accepted bad token %q from %q", pc.Token, s)
		}
		// A v1 code carries the whole hash; a v2 code carries the pin the payload
		// packs. Anything else means the parser invented a width.
		if !strings.HasPrefix(pc.Fingerprint, "sha256:") ||
			(len(pc.Fingerprint) != len("sha256:")+64 && len(pc.Fingerprint) != len("sha256:")+v2PinBytes*2) {
			t.Fatalf("accepted bad fingerprint %q from %q", pc.Fingerprint, s)
		}
		got, err := ParsePairingCode(pc.String())
		if err != nil {
			t.Fatalf("re-parse of emitted code %q (from %q) failed: %v", pc.String(), s, err)
		}
		if got != pc {
			t.Fatalf("parse∘String not idempotent: %+v -> %+v (from %q)", pc, got, s)
		}
	})
}
