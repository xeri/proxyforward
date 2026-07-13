package tcpinfo

import (
	"net"
	"testing"
	"time"
)

// TestRTTLoopback exercises the real syscall over a loopback connection. The
// kernel may not have an RTT sample immediately, so it retries briefly; if RTT
// never reports ok (unsupported platform, or a kernel that withholds a sample
// for loopback) the test skips rather than fails — the probe is best-effort by
// contract.
func TestRTTLoopback(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	accepted := make(chan net.Conn, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			accepted <- nil
			return
		}
		accepted <- c
	}()

	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	srv := <-accepted
	if srv == nil {
		t.Fatal("accept failed")
	}
	defer srv.Close()

	// Push a little traffic both ways so the kernel forms an RTT estimate.
	tcp := conn.(*net.TCPConn)
	for i := 0; i < 20; i++ {
		if _, err := conn.Write([]byte("ping")); err != nil {
			t.Fatalf("write: %v", err)
		}
		buf := make([]byte, 4)
		srv.SetReadDeadline(time.Now().Add(time.Second))
		if _, err := srv.Read(buf); err != nil {
			t.Fatalf("server read: %v", err)
		}
		if rtt, ok := RTT(tcp); ok {
			if rtt < 0 || rtt > time.Second {
				t.Fatalf("implausible loopback RTT: %v", rtt)
			}
			t.Logf("loopback RTT = %v", rtt)
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Skip("RTT unavailable on this platform/kernel; probe is best-effort")
}
