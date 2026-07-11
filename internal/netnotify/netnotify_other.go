//go:build !windows

package netnotify

import (
	"context"
	"log/slog"
)

// watchNetChange has no portable implementation; the resume detector still
// runs, and the agent's normal backoff handles the rest.
func watchNetChange(ctx context.Context, logger *slog.Logger, ch chan<- struct{}) {}
