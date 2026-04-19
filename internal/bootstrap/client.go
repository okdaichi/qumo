package bootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// ClientConfig holds the settings for connecting to a bootstrap server.
type ClientConfig struct {
	// URL is the base URL of the bootstrap HTTP server (e.g. "http://bootstrap:8080").
	URL string

	// Interval is how often to re-register (heartbeat) and refresh the peer list.
	Interval time.Duration
}

// Client manages registration and peer discovery for a single bootstrap server.
// It periodically heartbeats (POST /register) and exposes FetchPeers for on-demand
// peer discovery. Topology decisions are left to the caller.
type Client struct {
	cfg    ClientConfig
	nodeID string
	addr   string // this node's advertised address
	region string
	role   string

	httpClient *http.Client
}

// NewClient creates a new bootstrap client.
func NewClient(cfg ClientConfig, nodeID, addr, region, role string) *Client {
	return &Client{
		cfg:    cfg,
		nodeID: nodeID,
		addr:   addr,
		region: region,
		role:   role,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// Run periodically registers this node with the bootstrap server (heartbeat).
// It blocks until ctx is cancelled.
func (c *Client) Run(ctx context.Context) {
	if err := c.register(ctx); err != nil {
		slog.Warn("bootstrap register failed", "url", c.cfg.URL, "error", err)
	}

	ticker := time.NewTicker(c.cfg.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := c.register(ctx); err != nil {
				slog.Warn("bootstrap register failed", "url", c.cfg.URL, "error", err)
			}
		}
	}
}

func (c *Client) register(ctx context.Context) error {
	body, err := json.Marshal(map[string]string{
		"id":     c.nodeID,
		"addr":   c.addr,
		"region": c.region,
		"role":   c.role,
	})
	if err != nil {
		return err
	}

	reqURL := c.cfg.URL + "/register"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body) //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("register: unexpected status %d", resp.StatusCode)
	}

	return nil
}

// FetchPeers queries the bootstrap server for candidate peers matching q.
// This node's own ID is filtered out from the results (client-side self-exclusion).
func (c *Client) FetchPeers(ctx context.Context, q PeerQuery) ([]Node, error) {
	u, err := url.Parse(c.cfg.URL + "/peers")
	if err != nil {
		return nil, err
	}

	qs := u.Query()
	if q.PreferredRegion != "" {
		qs.Set("region", q.PreferredRegion)
	}
	if q.Role != "" {
		qs.Set("role", q.Role)
	}
	if q.Limit > 0 {
		qs.Set("limit", strconv.Itoa(q.Limit))
	}
	if q.AllowRemote {
		qs.Set("allow_remote", "true")
	}
	u.RawQuery = qs.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, resp.Body) //nolint:errcheck
		return nil, fmt.Errorf("peers: unexpected status %d", resp.StatusCode)
	}

	const maxBody = 1 << 20 // 1 MB
	var wrapper struct {
		Peers []Node `json:"peers"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(&wrapper); err != nil {
		return nil, fmt.Errorf("peers: decode error: %w", err)
	}

	// Client-side self-exclusion.
	result := wrapper.Peers[:0]
	for _, n := range wrapper.Peers {
		if n.ID != c.nodeID {
			result = append(result, n)
		}
	}

	return result, nil
}
