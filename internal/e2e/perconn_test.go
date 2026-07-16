package e2e

import (
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"proxyforward/internal/config"
)

// connCountingProxy is a transparent TCP relay between the agent and the
// gateway. It terminates no TLS (so the agent's cert pin still validates against
// the real gateway) and only forwards bytes, tracking how many agent→gateway
// connections are open at once.
type connCountingProxy struct {
	ln     net.Listener
	gwAddr string
	wg     sync.WaitGroup

	mu   sync.Mutex
	open int
}

func newConnCountingProxy(t *testing.T, gwAddr string) *connCountingProxy {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &connCountingProxy{ln: ln, gwAddr: gwAddr}
	p.wg.Add(1)
	go p.acceptLoop()
	return p
}

func (p *connCountingProxy) addr() string { return p.ln.Addr().String() }

func (p *connCountingProxy) acceptLoop() {
	defer p.wg.Done()
	for {
		c, err := p.ln.Accept()
		if err != nil {
			return
		}
		p.wg.Add(1)
		go p.handle(c)
	}
}

func (p *connCountingProxy) handle(client net.Conn) {
	defer p.wg.Done()
	defer client.Close()
	up, err := net.Dial("tcp", p.gwAddr)
	if err != nil {
		return
	}
	defer up.Close()

	p.mu.Lock()
	p.open++
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		p.open--
		p.mu.Unlock()
	}()

	// Forward both directions; either side's EOF half-closes the other. handle
	// returns only once both copies finish, so close() (which waits the group)
	// is leak-free.
	var cwg sync.WaitGroup
	cwg.Add(2)
	go func() { defer cwg.Done(); io.Copy(up, client); halfClose(up) }()
	go func() { defer cwg.Done(); io.Copy(client, up); halfClose(client) }()
	cwg.Wait()
}

func halfClose(c net.Conn) {
	if tc, ok := c.(*net.TCPConn); ok {
		tc.CloseWrite()
	}
}

func (p *connCountingProxy) concurrent() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.open
}

// close stops accepting and waits for in-flight relays to drain. Must run after
// the agent and gateway have stopped so their conns close and the relays exit;
// registered via t.Cleanup inside the interpose hook, whose LIFO ordering
// places it after the agent stop and before the gateway shutdown.
func (p *connCountingProxy) close() {
	p.ln.Close()
	p.wg.Wait()
}

// TestPerConnOpensAConnectionPerPlayer is the structural head-of-line-blocking
// proof. TCP head-of-line blocking is, by definition, confined to a single
// connection: a lost segment stalls only the byte delivery of the connection it
// was on. Under mux transport every player shares one agent→gateway connection,
// so one player's loss stalls all of them; under per-conn transport each player
// rides a dedicated connection, so it cannot. We count the concurrent agent→
// gateway connections through a transparent proxy while two players are held
// open and assert per-conn opens one per player (plus the control conn) while
// mux stays at one.
func TestPerConnOpensAConnectionPerPlayer(t *testing.T) {
	muxConns := concurrentLinkConns(t, config.TransportMux)
	perConnConns := concurrentLinkConns(t, config.TransportPerConn)

	if muxConns != 1 {
		t.Errorf("mux transport used %d concurrent agent→gateway conns for 2 players, want 1 (all players share one — the HOL hazard)", muxConns)
	}
	if perConnConns < 3 {
		t.Errorf("per-conn transport used %d concurrent agent→gateway conns for 2 players, want ≥3 (control + one dedicated conn per player)", perConnConns)
	}
	if perConnConns <= muxConns {
		t.Errorf("per-conn (%d conns) must open more than mux (%d): that per-player separation is precisely what removes cross-player TCP head-of-line blocking", perConnConns, muxConns)
	}
}

// concurrentLinkConns brings up a tunnel through a counting proxy, holds two
// concurrent players (each with a confirmed echo, so its data path is truly
// established), and returns the concurrent agent→gateway connection count.
func concurrentLinkConns(t *testing.T, transport string) int {
	echoAddr, closeEcho := echoServer(t)
	defer closeEcho()

	var proxy *connCountingProxy
	h := newHarnessWith(t, echoAddr, harnessOpts{
		transport: transport,
		interpose: func(gwAddr string) string {
			proxy = newConnCountingProxy(t, gwAddr)
			t.Cleanup(proxy.close)
			return proxy.addr()
		},
	})
	addr := h.waitPublicPort()

	// Two concurrent players, each confirmed with an echo so its dedicated data
	// connection (under per-conn) is actually up before we count.
	for i := 0; i < 2; i++ {
		c, err := net.DialTimeout("tcp", addr, 5*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		c.SetDeadline(time.Now().Add(10 * time.Second))
		msg := []byte("player")
		if _, err := c.Write(msg); err != nil {
			t.Fatal(err)
		}
		got := make([]byte, len(msg))
		if _, err := io.ReadFull(c, got); err != nil {
			t.Fatalf("player %d echo: %v", i, err)
		}
	}

	// Poll briefly so a just-established data conn is counted; mux reaches 1
	// immediately, per-conn settles at 3 (control + two data).
	want := 1
	if transport == config.TransportPerConn {
		want = 3
	}
	deadline := time.Now().Add(3 * time.Second)
	for proxy.concurrent() < want && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	return proxy.concurrent()
}
