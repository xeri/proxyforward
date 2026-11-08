//go:build windows

package ipc

import (
	"net"
	"time"

	"github.com/Microsoft/go-winio"
)

func dialPipe(timeout time.Duration) (net.Conn, error) {
	return winio.DialPipe(PipeName, &timeout)
}
