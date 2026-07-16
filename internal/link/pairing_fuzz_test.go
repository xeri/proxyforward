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
		if !strings.HasPrefix(pc.Fingerprint, "sha256:") || len(pc.Fingerprint) != len("sha256:")+64 {
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
