//go:build linux

package ipc

import (
	"net"
	"os"

	"golang.org/x/sys/unix"
)

// peerAllowed admits a connection only from this user or root — the unix
// reading of pipeSecurity. The 0700 socket directory already keeps everyone
// else from connecting; SO_PEERCRED is the kernel's word on who actually did,
// so the boundary holds even if that directory is loosened after listen.
func peerAllowed(c *net.UnixConn) bool {
	raw, err := c.SyscallConn()
	if err != nil {
		return false
	}
	var (
		cred    *unix.Ucred
		credErr error
	)
	if err := raw.Control(func(fd uintptr) {
		cred, credErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	}); err != nil || credErr != nil {
		return false
	}
	return cred.Uid == uint32(os.Geteuid()) || cred.Uid == 0
}
