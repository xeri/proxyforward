//go:build windows

package ipc

import (
	"context"
	"errors"
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

// pipeSecurity admits Administrators (BA), the interactive user (IU), and
// SYSTEM (SY) — the GUI runs as the logged-in user, the daemon may run as a
// service; remote access is impossible on local pipes by construction.
const pipeSecurity = "D:P(A;;GA;;;BA)(A;;GA;;;SY)(A;;GA;;;IU)"

// Serve owns the named pipe until ctx is cancelled. Each client connection
// gets its own goroutine; requests are strictly request/response.
func Serve(ctx context.Context, logger *slog.Logger, src Sources) error {
	ln, err := winio.ListenPipe(PipeName, &winio.PipeConfig{
		SecurityDescriptor: pipeSecurity,
		MessageMode:        false,
	})
	if err != nil {
		return err
	}
	// Closing the listener unblocks Accept when ctx dies.
	stopped := make(chan struct{})
	defer func() { <-stopped }()
	watchCtx, cancelWatch := context.WithCancel(ctx)
	defer cancelWatch()
	go func() {
		defer close(stopped)
		<-watchCtx.Done()
		ln.Close()
	}()

	logger.Info("ipc pipe up", "pipe", PipeName)
	var (
		wg    sync.WaitGroup
		conns []net.Conn // only the accept loop appends; no lock needed
	)
	defer wg.Wait() // runs after the conns close below (defers are LIFO)
	defer func() {
		for _, c := range conns {
			c.Close() // unblocks each handler's read
		}
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
		conns = append(conns, conn)
		wg.Add(1)
		go func() {
			defer wg.Done()
			serveConn(ctx, logger, conn, src)
		}()
	}
}

func serveConn(ctx context.Context, logger *slog.Logger, conn net.Conn, src Sources) {
	defer conn.Close()
	for ctx.Err() == nil {
		conn.SetReadDeadline(time.Now().Add(idleTimeout))
		env, err := control.ReadMsg(conn, control.MaxFrame)
		if err != nil {
			return // client went away or idled out
		}
		conn.SetWriteDeadline(time.Now().Add(requestTimeout))
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
