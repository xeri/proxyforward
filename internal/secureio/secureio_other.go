//go:build !windows

package secureio

import (
	"errors"
	"os"
)

// CurrentUserSID has no meaning off Windows; nothing calls it there (the pipe it
// serves is Windows-only), and the stub exists so the package builds for the
// targets CI compiles to keep these files from rotting.
func CurrentUserSID() (string, error) {
	return "", errors.New("secureio: SIDs are a Windows concept")
}

// mkdirAll is plain os.MkdirAll off Windows: 0o700 is enforced by the OS there,
// so the mode says what it means and there is nothing to repair. proxyforward
// cannot run off Windows anyway (ipc.Serve returns ErrUnsupported); CI builds
// these targets so the stubs can't rot.
func mkdirAll(dir string) error { return os.MkdirAll(dir, 0o700) }
