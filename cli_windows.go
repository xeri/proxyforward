//go:build windows

package main

import (
	"errors"

	"proxyforward/internal/ipc"
)

// untrustedPipe reports whether a dial failed because something is answering the
// pipe that this account should not trust — a squatter, not an absent daemon.
//
// It lives behind a build tag because ipc.ErrUntrustedServer does, and must: the
// check it names (reading the pipe's owner SID) exists only on Windows, and
// main.go is compiled for linux and darwin in CI so the *_other.go stubs cannot
// rot.
func untrustedPipe(err error) bool { return errors.Is(err, ipc.ErrUntrustedServer) }
