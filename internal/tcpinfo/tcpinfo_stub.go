//go:build !windows && !linux

package tcpinfo

import (
	"net"
	"time"
)

// RTT is unsupported on this platform; latency simply stays unknown.
func RTT(conn *net.TCPConn) (time.Duration, bool) { return 0, false }
