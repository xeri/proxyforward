package app

import (
	"errors"
	"strings"

	"proxyforward/internal/link"
)

// PairingCheck is the verdict on a half-typed pairing code: enough for the paste
// box to say something true while someone is still typing, and to say the *right*
// thing when they stop.
//
// The judgment lives here rather than in TypeScript so there is one implementation
// of the format. A checksum reimplemented in the frontend would be a second one,
// and the first time they disagreed the box would confidently reject a code the
// engine accepts.
type PairingCheck struct {
	// Kind is what to say: "ok", "incomplete" (say nothing — they are still
	// typing), "typo" (the shape is right, a character is wrong), or "unusable"
	// (this is not a pairing code).
	Kind string `json:"kind"`
	// Problem is the parser's own words, empty when Kind is "ok" or "incomplete".
	Problem string `json:"problem"`
	// Host and Port name the gateway a valid code points at, so the box can show
	// which machine is about to be trusted.
	Host string `json:"host"`
	Port int    `json:"port"`
	// Legacy marks a v1 code: still usable, but minted by an older gateway, and
	// worth saying so before someone wonders why it looks nothing like the ones
	// their gateway prints.
	Legacy bool `json:"legacy"`
}

// CheckPairingCode judges a pairing code without acting on it. Pure computation —
// it answers whether or not an engine is running, which matters because the first
// place it is used is the setup wizard.
func (a *App) CheckPairingCode(code string) PairingCheck {
	if strings.TrimSpace(code) == "" {
		return PairingCheck{Kind: "incomplete"}
	}
	parsed, err := link.ParsePairingCode(code)
	switch {
	case err == nil:
		return PairingCheck{
			Kind: "ok", Host: parsed.Host, Port: parsed.Port,
			Legacy: !link.IsShortPin(parsed.Fingerprint),
		}
	case errors.Is(err, link.ErrCodeIncomplete):
		return PairingCheck{Kind: "incomplete"}
	case errors.Is(err, link.ErrCodeChecksum):
		return PairingCheck{Kind: "typo", Problem: err.Error()}
	default:
		return PairingCheck{Kind: "unusable", Problem: err.Error()}
	}
}
