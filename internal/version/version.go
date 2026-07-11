// Package version holds build metadata injected via -ldflags.
package version

var (
	// Version is overridden at release build time with
	// -ldflags "-X proxyforward/internal/version.Version=v1.2.3".
	Version = "0.1.0-dev"
	Commit  = "unknown"
)

func String() string {
	return Version + " (" + Commit + ")"
}
