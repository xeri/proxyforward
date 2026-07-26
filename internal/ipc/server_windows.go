//go:build windows

package ipc

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/Microsoft/go-winio"

	"proxyforward/internal/control"
	"proxyforward/internal/stats"
)

const (
	// requestTimeout bounds one request/response cycle; idleTimeout is how
	// long a quiet client may hold its pipe connection.
	requestTimeout = 5 * time.Second
	idleTimeout    = 2 * time.Minute
)

// Serve owns the named pipe until ctx is cancelled. Each client connection
// gets its own goroutine; requests are strictly request/response.
//
// owners are additional SIDs allowed to reach the pipe beyond SYSTEM,
// Administrators, and the account this process runs as — see pipeSecurity. A
// GUI-hosted engine passes none; a service-mode engine passes the SID the
// installer recorded.
func Serve(ctx context.Context, logger *slog.Logger, src Sources, owners ...string) error {
	sddl, err := pipeSecurity(owners)
	if err != nil {
		// Refusing to serve is the only safe answer: the alternative is a pipe
		// whose ACL we could not build, and this ACL is the authorization model.
		return fmt.Errorf("ipc pipe security: %w", err)
	}
	ln, err := winio.ListenPipe(PipeName, &winio.PipeConfig{
		SecurityDescriptor: sddl,
		MessageMode:        false,
	})
	if err != nil {
		return err
	}
	// watchCtx is cancelled when ctx dies *or* when Serve returns for any other
	// reason (a failed Accept), so both exits tear down identically: the listener
	// closes, and with it every client connection still being served.
	stopped := make(chan struct{})
	watchCtx, cancelWatch := context.WithCancel(ctx)
	go func() {
		defer close(stopped)
		<-watchCtx.Done()
		ln.Close()
	}()

	logger.Info("ipc pipe up", "pipe", PipeName)
	var wg sync.WaitGroup
	// Teardown in the only order that works: cancel first, so the listener closes
	// and every handler's read is unblocked; then wait for the handlers that
	// unblocked; then for the closer itself. Spelled out rather than left to three
	// defers and LIFO, because the order is the whole correctness argument.
	defer func() {
		cancelWatch()
		wg.Wait()
		<-stopped
	}()
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if errors.Is(err, winio.ErrPipeListenerClosed) {
				return nil
			}
			return err
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Shutdown has to hang up on a client parked in a blocking read, or Serve
			// waits out idleTimeout before its handlers return — that is the only
			// reason the server takes any interest in a live connection. AfterFunc
			// ties that close to this handler's lifetime, and stop() forgets the
			// connection the moment it leaves. What this replaces was a slice the
			// accept loop appended to and nothing ever pruned: every client ever
			// served stayed reachable for the life of a process built to run for
			// months, and a GUI that idles out and reconnects is a client every two
			// minutes, forever.
			stop := context.AfterFunc(watchCtx, func() { conn.Close() })
			defer stop()
			serveConn(watchCtx, logger, conn, src)
		}()
	}
}

func serveConn(ctx context.Context, logger *slog.Logger, conn net.Conn, src Sources) {
	defer conn.Close()
	for ctx.Err() == nil {
		_ = conn.SetReadDeadline(time.Now().Add(idleTimeout))
		env, err := control.ReadMsg(conn, control.MaxFrame)
		if err != nil {
			return // client went away or idled out
		}
		_ = conn.SetWriteDeadline(time.Now().Add(requestTimeout))
		switch env.Type {
		case TypePing:
			err = control.WriteMsg(conn, TypePong, struct{}{})
		case TypeStatusReq:
			err = control.WriteMsg(conn, TypeStatusResp, src.Status())
		case TypeHistoryReq:
			req, decErr := control.Decode[HistoryReq](env)
			if decErr != nil {
				logger.Debug("ipc: bad history request", "err", decErr)
				return
			}
			resp := stats.HistoryResult{Buckets: []stats.Bucket{}}
			if src.History != nil {
				// Clamp so the reply always fits control.MaxFrame.
				resp = src.History(req.WindowMs, min(req.MaxBuckets, maxIPCEntries))
			}
			err = control.WriteMsg(conn, TypeHistoryResp, resp)
		case TypePeersReq:
			var peers []stats.PeerStat
			if src.Peers != nil {
				peers = src.Peers()
			}
			if len(peers) > maxIPCEntries {
				peers = peers[:maxIPCEntries]
			}
			err = control.WriteMsg(conn, TypePeersResp, PeersResp{Peers: peers})
		case TypeAnalyticsReq:
			req, decErr := control.Decode[AnalyticsReq](env)
			if decErr != nil {
				logger.Debug("ipc: bad analytics request", "err", decErr)
				return
			}
			resp := AnalyticsResp{Err: "analytics unavailable"}
			if src.Analytics != nil {
				resp = src.Analytics(*req)
			}
			err = control.WriteMsg(conn, TypeAnalyticsResp, resp)
			if errors.Is(err, control.ErrFrameTooLarge) {
				// WriteMsg size-checks before writing a byte, so the pipe is
				// still clean: substitute a served error instead of tearing
				// the connection down. The client surfaces it as an OpError
				// (transient), never an unsupported-daemon latch.
				logger.Warn("ipc: analytics reply exceeds frame limit", "op", req.Op)
				err = control.WriteMsg(conn, TypeAnalyticsResp, AnalyticsResp{Err: "reply exceeds frame limit"})
			}
		default:
			logger.Debug("ipc: ignoring unknown request", "type", env.Type)
			continue
		}
		if err != nil {
			return
		}
	}
}
