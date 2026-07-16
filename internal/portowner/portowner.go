// Package portowner answers "who is holding this TCP port?" so bind
// failures can say "port 25565 is in use by java.exe (PID 1234)" instead of
// a bare errno. Windows-only; other platforms report nothing found.
package portowner

import (
	"fmt"
)

// Owner identifies the process listening on a port.
type Owner struct {
	PID  uint32
	Name string // executable base name; may be empty when access is denied
}

func (o Owner) String() string {
	if o.Name == "" {
		return fmt.Sprintf("PID %d", o.PID)
	}
	return fmt.Sprintf("%s (PID %d)", o.Name, o.PID)
}

// Lookup finds the listener occupying a local TCP port (IPv4 or IPv6).
func Lookup(port int) (Owner, bool) {
	return lookup(port)
}

// IsAddrInUse reports whether err is a "port already in use" bind failure
// (EADDRINUSE / WSAEADDRINUSE), as opposed to a permission or other bind error —
// so a caller can reassign only genuine port clashes and surface the rest.
func IsAddrInUse(err error) bool {
	return isAddrInUse(err)
}

// DecorateBindError enriches a failed-bind error with the owning process
// when the failure is a port conflict and an owner can be found.
func DecorateBindError(port int, err error) error {
	if err == nil || !isAddrInUse(err) {
		return err
	}
	owner, ok := Lookup(port)
	if !ok {
		return err
	}
	return fmt.Errorf("port %d is in use by %s: %w", port, owner, err)
}
