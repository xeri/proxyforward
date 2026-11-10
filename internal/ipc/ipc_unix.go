//go:build !windows

package ipc

// Off Windows the endpoint is a unix socket, and access control is the
// filesystem plus the kernel's peer credentials, admitting the same
// principals pipeSecurity does — this user, and root as the
// Administrators/SYSTEM analogue:
//
//   - the socket lives in a directory only this user can enter (0700 and
//     owned by us — checked at listen, never assumed, so a pre-created or
//     symlinked directory in a shared /tmp fails closed);
//   - the socket itself is 0600;
//   - on Linux every accepted peer's uid is checked as well (peercred_linux.go).
//
// The pipe carries gateway admin ops (issue_pairing, revoke_agent, …), so
// this is a security boundary, not a convenience.
//
// Single ownership: a pipe name refuses a second listener by itself; a socket
// path does not (a second engine could unlink the first's socket and bind
// over it). So listen takes a non-blocking flock on a sibling lock file first
// and holds it for the listener's lifetime: held → another engine is live →
// fatal, exactly like a pipe conflict. The kernel drops the lock when its
// holder dies, so a crashed engine's leftover socket is safe to remove.

import (
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// defaultPipeName prefers $XDG_RUNTIME_DIR (per-user, tmpfs, mode 0700,
// cleared at logout) and falls back to a per-uid directory under the temp dir
// where no runtime dir exists (e.g. a non-login service).
func defaultPipeName() string {
	if dir := os.Getenv("XDG_RUNTIME_DIR"); filepath.IsAbs(dir) {
		return filepath.Join(dir, "proxyforward", "ipc.sock")
	}
	return filepath.Join(os.TempDir(), fmt.Sprintf("proxyforward-%d", os.Geteuid()), "ipc.sock")
}

// PrivatePipeName is a process-private endpoint for tests that run a real
// engine or Serve, so parallel test binaries and a live daemon never share
// the production socket.
func PrivatePipeName(tag string) string {
	return filepath.Join(os.TempDir(), fmt.Sprintf("proxyforward-%s-%d", tag, os.Getpid()), "ipc.sock")
}

func listen() (net.Listener, error) {
	path := PipeName
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("ipc: create socket directory: %w", err)
	}
	if err := checkPrivateDir(dir); err != nil {
		return nil, err
	}

	lock, err := os.OpenFile(path+".lock", os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("ipc: open lock file: %w", err)
	}
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		lock.Close()
		if errors.Is(err, unix.EWOULDBLOCK) {
			return nil, fmt.Errorf("ipc: %s is already served by another proxyforward engine; close that one first", path)
		}
		return nil, fmt.Errorf("ipc: lock %s: %w", lock.Name(), err)
	}

	// The lock is ours, so anything at path is a dead engine's leftover.
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		lock.Close()
		return nil, fmt.Errorf("ipc: remove stale socket: %w", err)
	}
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		lock.Close()
		return nil, err
	}
	// The directory already keeps others out; 0600 keeps the socket private
	// even if the directory is later loosened.
	if err := os.Chmod(path, 0o600); err != nil {
		ln.Close()
		lock.Close()
		return nil, fmt.Errorf("ipc: restrict socket: %w", err)
	}
	return &unixListener{UnixListener: ln, lock: lock}, nil
}

// checkPrivateDir refuses a socket directory anyone but us could enter or
// swap: it must be a real directory (Lstat — not a symlink), owned by our
// euid, with no group/other permission bits.
func checkPrivateDir(dir string) error {
	fi, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("ipc: %w", err)
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !fi.IsDir() || !ok || int(st.Uid) != os.Geteuid() || fi.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("ipc: socket directory %s must be a directory you own with mode 0700 (got %v); fix it with chmod 700, or remove it", dir, fi.Mode())
	}
	return nil
}

// unixListener drops peers that fail the credential check and releases the
// ownership lock on Close — after the socket is unlinked, so a successor that
// takes the lock can never have its fresh socket deleted by us.
type unixListener struct {
	*net.UnixListener
	lock      *os.File
	closeOnce sync.Once
}

func (l *unixListener) Accept() (net.Conn, error) {
	for {
		c, err := l.AcceptUnix()
		if err != nil {
			return nil, err
		}
		if peerAllowed(c) {
			return c, nil
		}
		c.Close()
	}
}

func (l *unixListener) Close() error {
	err := l.UnixListener.Close() // unlinks the socket path
	l.closeOnce.Do(func() { l.lock.Close() })
	return err
}

func isListenerClosed(err error) bool { return errors.Is(err, net.ErrClosed) }

func dialPipe(timeout time.Duration) (net.Conn, error) {
	return net.DialTimeout("unix", PipeName, timeout)
}
