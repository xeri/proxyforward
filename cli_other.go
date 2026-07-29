//go:build !windows

package main

// untrustedPipe is always false off Windows: there are no named pipes there, so
// ipc.Dial fails with ErrUnsupported before anything could answer. Exists so
// cli.go can ask the question unconditionally.
func untrustedPipe(error) bool { return false }
