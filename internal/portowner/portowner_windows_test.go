//go:build windows

package portowner

import (
	"net"
	"strings"
	"testing"
)

func TestLookupFindsOwnListener(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	owner, ok := Lookup(port)
	if !ok {
		t.Fatalf("Lookup(%d) found nothing; we are listening on it", port)
	}
	if owner.PID == 0 {
		t.Fatal("owner has PID 0")
	}
	// The test binary is <pkg>.test.exe.
	if !strings.Contains(strings.ToLower(owner.Name), ".test") {
		t.Errorf("owner name %q does not look like the test binary", owner.Name)
	}
}

func TestDecorateBindError(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	addr := ln.Addr().String()
	port := ln.Addr().(*net.TCPAddr).Port

	_, bindErr := net.Listen("tcp", addr)
	if bindErr == nil {
		t.Fatal("double bind unexpectedly succeeded")
	}
	dec := DecorateBindError(port, bindErr)
	if !strings.Contains(dec.Error(), ".test") || !strings.Contains(dec.Error(), "PID") {
		t.Errorf("decorated error missing process info: %v", dec)
	}
}

func TestLookupMissingPortFindsNothing(t *testing.T) {
	// Grab a port and release it so it is very likely free.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	if owner, ok := Lookup(port); ok {
		t.Errorf("Lookup(%d) on a freed port found %v", port, owner)
	}
}
