//go:build !windows

package secureio

import "os"

// mkdirAll is plain os.MkdirAll off Windows: 0o700 is enforced by the OS there,
// so the mode says what it means and there is nothing to repair. proxyforward
// cannot run off Windows anyway (ipc.Serve returns ErrUnsupported); CI builds
// these targets so the stubs can't rot.
func mkdirAll(dir string) error { return os.MkdirAll(dir, 0o700) }
