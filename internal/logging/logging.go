// Package logging wires slog to three sinks: the console (headless mode), a
// size-rotated file, and an in-memory ring the GUI reads. All sinks hang off
// one slog.Logger so call sites stay ordinary slog calls.
package logging

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

type Options struct {
	Level   string // debug|info|warn|error (default info)
	Console bool   // write human-readable lines to stderr
	// FilePath enables size-rotated file logging when non-empty.
	FilePath string
	// Ring receives every record when non-nil (GUI log view).
	Ring *Ring
}

func ParseLevel(s string) slog.Level {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// New builds the fan-out logger and returns it with a close function for the
// file sink.
func New(opts Options) (*slog.Logger, func() error, error) {
	level := ParseLevel(opts.Level)
	var handlers []slog.Handler
	closeFn := func() error { return nil }

	if opts.Console {
		handlers = append(handlers, slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
	}
	if opts.FilePath != "" {
		if err := os.MkdirAll(filepath.Dir(opts.FilePath), 0o700); err != nil {
			return nil, nil, fmt.Errorf("create log dir: %w", err)
		}
		rw, err := newRotatingWriter(opts.FilePath, 10*1024*1024, 3)
		if err != nil {
			return nil, nil, fmt.Errorf("open log file: %w", err)
		}
		closeFn = rw.Close
		handlers = append(handlers, slog.NewTextHandler(rw, &slog.HandlerOptions{Level: level}))
	}
	if opts.Ring != nil {
		handlers = append(handlers, newRingHandler(opts.Ring, level))
	}
	if len(handlers) == 0 {
		return slog.New(slog.NewTextHandler(io.Discard, nil)), closeFn, nil
	}
	return slog.New(fanout(handlers)), closeFn, nil
}

// DefaultFilePath returns the log file path inside the given config dir.
func DefaultFilePath(configDir string) string {
	return filepath.Join(configDir, "logs", "proxyforward.log")
}
