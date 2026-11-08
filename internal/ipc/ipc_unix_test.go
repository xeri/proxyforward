//go:build !windows

package ipc

import (
	"context"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// useSocket points PipeName at path for one test, restoring it after.
func useSocket(t *testing.T, path string) {
	t.Helper()
	old := PipeName
	PipeName = path
	t.Cleanup(func() { PipeName = old })
}

// serveUntilUp starts Serve and waits until a Ping succeeds; the returned
// stop cancels it and waits for Serve to return.
func serveUntilUp(t *testing.T) (stop func() error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Serve(ctx, slog.New(slog.DiscardHandler), Sources{Status: func() Status { return Status{} }})
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		c, err := Dial(200 * time.Millisecond)
		if err == nil {
			err = c.Ping()
			c.Close()
			if err == nil {
				break
			}
		}
		select {
		case serr := <-done:
			cancel()
			t.Fatalf("Serve exited before coming up: %v", serr)
		default:
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatalf("socket never came up: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	return func() error {
		cancel()
		select {
		case err := <-done:
			return err
		case <-time.After(5 * time.Second):
			t.Fatal("Serve did not return after cancel")
			return nil
		}
	}
}

// The socket directory and the socket are private to this user, and a clean
// shutdown removes the socket.
func TestUnixSocketIsPrivate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "pxf")
	sock := filepath.Join(dir, "ipc.sock")
	useSocket(t, sock)
	stop := serveUntilUp(t)

	di, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := di.Mode().Perm(); got != 0o700 {
		t.Errorf("socket dir mode = %v, want 0700", got)
	}
	si, err := os.Stat(sock)
	if err != nil {
		t.Fatal(err)
	}
	if si.Mode()&os.ModeSocket == 0 {
		t.Errorf("%s is not a socket: %v", sock, si.Mode())
	}
	if got := si.Mode().Perm(); got != 0o600 {
		t.Errorf("socket mode = %v, want 0600", got)
	}

	if err := stop(); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	if _, err := os.Stat(sock); !os.IsNotExist(err) {
		t.Errorf("socket left behind after shutdown (stat err = %v)", err)
	}
}

// A crashed engine leaves its socket file behind with nobody listening; the
// next engine must take over instead of failing "address already in use".
func TestUnixStaleSocketReplaced(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "pxf")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(dir, "ipc.sock")
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: sock, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	ln.SetUnlinkOnClose(false)
	ln.Close()
	if _, err := os.Stat(sock); err != nil {
		t.Fatalf("stale socket not left in place: %v", err)
	}

	useSocket(t, sock)
	stop := serveUntilUp(t)
	if err := stop(); err != nil {
		t.Fatalf("Serve: %v", err)
	}
}

// A socket directory others can enter is refused outright, never used.
func TestUnixInsecureDirRefused(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "pxf")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o755); err != nil { // explicit: Mkdir is umask-filtered
		t.Fatal(err)
	}
	useSocket(t, filepath.Join(dir, "ipc.sock"))

	done := make(chan error, 1)
	go func() {
		done <- Serve(context.Background(), slog.New(slog.DiscardHandler), Sources{Status: func() Status { return Status{} }})
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Serve accepted a group/world-accessible socket directory")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve is serving from a group/world-accessible socket directory")
	}
}
