//go:build !windows

package ipc

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"proxyforward/internal/stats"
)

func Serve(ctx context.Context, logger *slog.Logger, src Sources) error {
	return ErrUnsupported
}

type Client struct{}

func Dial(timeout time.Duration) (*Client, error) { return nil, ErrUnsupported }

func (c *Client) Close() error             { return ErrUnsupported }
func (c *Client) Ping() error              { return ErrUnsupported }
func (c *Client) Status() (*Status, error) { return nil, ErrUnsupported }
func (c *Client) History(windowMs int64, maxBuckets int) (*stats.HistoryResult, error) {
	return nil, ErrUnsupported
}
func (c *Client) Peers() ([]stats.PeerStat, error) { return nil, ErrUnsupported }
func (c *Client) Analytics(op string, body json.RawMessage) (json.RawMessage, error) {
	return nil, ErrUnsupported
}
