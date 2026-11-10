//go:build windows

package ipc

import (
	"errors"
	"fmt"
	"net"
	"os"

	"github.com/Microsoft/go-winio"
)

// pipeSecurity admits Administrators (BA), the interactive user (IU), and
// SYSTEM (SY) — the GUI runs as the logged-in user, the daemon may run as a
// service; remote access is impossible on local pipes by construction.
const pipeSecurity = "D:P(A;;GA;;;BA)(A;;GA;;;SY)(A;;GA;;;IU)"

func defaultPipeName() string { return `\\.\pipe\proxyforward` }

// PrivatePipeName is a process-private endpoint for tests that run a real
// engine or Serve, so parallel test binaries and a live daemon never share
// the production pipe.
func PrivatePipeName(tag string) string {
	return fmt.Sprintf(`\\.\pipe\proxyforward-%s-%d`, tag, os.Getpid())
}

// listen opens PipeName under pipeSecurity. A second listener on a live name
// fails, which is what makes a second engine fatal.
func listen() (net.Listener, error) {
	return winio.ListenPipe(PipeName, &winio.PipeConfig{
		SecurityDescriptor: pipeSecurity,
		MessageMode:        false,
	})
}

// winio reports a closed listener as net.ErrClosed (its ErrPipeListenerClosed is
// a deprecated alias of it).
func isListenerClosed(err error) bool { return errors.Is(err, net.ErrClosed) }
