//go:build !windows && !linux

package ipc

import "net"

// peerAllowed has no per-connection check off Linux: the 0700 socket
// directory (checked at listen) is the whole boundary there. macOS could add
// LOCAL_PEERCRED here if it ever ships.
func peerAllowed(*net.UnixConn) bool { return true }
