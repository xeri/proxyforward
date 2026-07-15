// Package version holds build metadata injected via -ldflags.
package version

import "strings"

var (
	// Version is overridden at release build time with
	// -ldflags "-X proxyforward/internal/version.Version=v1.2.3".
	Version = "0.1.0-dev"
	Commit  = "unknown"
)

// String is the display form used everywhere the version reaches a human
// (UI status, --version, diagnostics). The commit is truncated to the short
// 7-hex form and dropped entirely when Version already embeds it (CI dev
// builds stamp "0.0.0-dev+<short-sha>") — a full 40-char SHA overflows every
// card and footer it lands in.
func String() string {
	c := Commit
	if len(c) > 7 {
		c = c[:7]
	}
	if c == "" || c == "unknown" || strings.Contains(Version, c) {
		return Version
	}
	return Version + " (" + c + ")"
}
