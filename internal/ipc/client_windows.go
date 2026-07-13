//go:build windows

package ipc

import (
	"encoding/json"
	"fmt"
	"net"
	"time"

	"github.com/Microsoft/go-winio"

	"proxyforward/internal/control"
	"proxyforward/internal/stats"
)

// Client is one pipe connection to the daemon. Not safe for concurrent use;
// the GUI polls from a single loop.
type Client struct {
	conn net.Conn
}

// Dial connects to a running daemon's pipe. A quick failure means no daemon
// is running — the GUI's cue to start its own engine in-process.
func Dial(timeout time.Duration) (*Client, error) {
	conn, err := winio.DialPipe(PipeName, &timeout)
	if err != nil {
		return nil, err
	}
	return &Client{conn: conn}, nil
}

func (c *Client) Close() error { return c.conn.Close() }

// roundTrip sends one request and decodes the expected response type.
func roundTrip[T any](c *Client, reqType string, req any, respType string) (*T, error) {
	c.conn.SetDeadline(time.Now().Add(requestTimeout))
	defer c.conn.SetDeadline(time.Time{})
	if err := control.WriteMsg(c.conn, reqType, req); err != nil {
		return nil, fmt.Errorf("ipc: send %s: %w", reqType, err)
	}
	env, err := control.ReadMsg(c.conn, control.MaxFrame)
	if err != nil {
		return nil, fmt.Errorf("ipc: read reply to %s: %w", reqType, err)
	}
	if env.Type != respType {
		return nil, fmt.Errorf("ipc: unexpected reply %q to %s", env.Type, reqType)
	}
	return control.Decode[T](env)
}

// Ping verifies the daemon is alive.
func (c *Client) Ping() error {
	_, err := roundTrip[struct{}](c, TypePing, struct{}{}, TypePong)
	return err
}

// Status fetches the daemon's current snapshot.
func (c *Client) Status() (*Status, error) {
	return roundTrip[Status](c, TypeStatusReq, struct{}{}, TypeStatusResp)
}

// History fetches bandwidth history from the daemon. An old daemon that
// predates this request never replies, so the call fails with a read timeout
// after requestTimeout — callers should degrade, not retry.
func (c *Client) History(windowMs int64, maxBuckets int) (*stats.HistoryResult, error) {
	return roundTrip[stats.HistoryResult](c, TypeHistoryReq,
		HistoryReq{WindowMs: windowMs, MaxBuckets: maxBuckets}, TypeHistoryResp)
}

// Peers fetches the daemon's per-client lifetime records.
func (c *Client) Peers() ([]stats.PeerStat, error) {
	resp, err := roundTrip[PeersResp](c, TypePeersReq, struct{}{}, TypePeersResp)
	if err != nil {
		return nil, err
	}
	return resp.Peers, nil
}

// Analytics runs one generic analytics op against the daemon. An old daemon
// that predates the analytics envelope never replies, so the call fails with
// a read timeout — the caller latches "unsupported" and stops asking. A
// daemon that answered but reported a failure returns *OpError, which the
// caller must treat as transient (the protocol works) — never latch on it.
func (c *Client) Analytics(op string, body json.RawMessage) (json.RawMessage, error) {
	resp, err := roundTrip[AnalyticsResp](c, TypeAnalyticsReq,
		AnalyticsReq{Op: op, Body: body}, TypeAnalyticsResp)
	if err != nil {
		return nil, err
	}
	if resp.Err != "" {
		return nil, &OpError{Op: op, Msg: resp.Err}
	}
	return resp.Body, nil
}
