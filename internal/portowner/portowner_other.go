//go:build !windows

package portowner

import (
	"errors"
	"syscall"
)

func isAddrInUse(err error) bool {
	return errors.Is(err, syscall.EADDRINUSE)
}

func lookup(port int) (Owner, bool) {
	return Owner{}, false
}
