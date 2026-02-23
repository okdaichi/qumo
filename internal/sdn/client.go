// Package sdn provides a client for registering announcements
// with the SDN controller. When a relay receives a moqt.Announcement,
// it pushes the BroadcastPath to the central SDN announce table so that
// other relays can discover which relay holds which content.
package sdn

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"sync"
	"time"

	"github.com/okdaichi/qumo/internal/topology"
)

// ClientConfig holds the settings for the SDN announce client.
type ClientConfig struct {
	// URL is the base URL of the SDN controller (e.g. "https://sdn:8090").
	// If empty, auto-announce is disabled.
	URL string

	// RelayName identifies this relay in the announce table.
	RelayName string

	// HeartbeatInterval is how often the relay re-PUTs its announces
	// to keep them alive. Default: 30s.
	HeartbeatInterval time.Duration

	// TLS configures mutual TLS for relay→SDN communication.
	// If nil, plain HTTP is used (suitable for internal networks).
	TLS *TLSConfig
}

// TLSConfig holds mTLS settings for relay→SDN communication.
type TLSConfig struct {
	CertFile string
	KeyFile  string
	CAFile   string // optional: CA certificate to verify SDN server
}

// Client manages automatic announce registration with the SDN controller.
// It is safe for concurrent use.
type Client struct {
	config ClientConfig
	client *http.Client

	mu      sync.Mutex
	entries map[string]struct{} // broadcastPath set
	cancel  context.CancelFunc
	done    chan struct{}
}

// NewClient creates a new SDN announce client. Call Run to start the
// heartbeat loop.
func NewClient(cfg ClientConfig) (*Client, error) {
	if cfg.URL == "" {
		return nil, fmt.Errorf("sdn client: URL is required")
	}
	if cfg.RelayName == "" {
		return nil, fmt.Errorf("sdn client: RelayName is required")
	}
	if cfg.HeartbeatInterval <= 0 {
		cfg.HeartbeatInterval = 30 * time.Second
	}

	transport := http.DefaultTransport.(*http.Transport).Clone()

	if cfg.TLS != nil {
		tlsCfg, err := buildTLSConfig(cfg.TLS)
		if err != nil {
			return nil, fmt.Errorf("sdn client TLS: %w", err)
		}
		transport.TLSClientConfig = tlsCfg
	}

	return &Client{
		config:  cfg,
		client:  &http.Client{Transport: transport, Timeout: 10 * time.Second},
		entries: make(map[string]struct{}),
		done:    make(chan struct{}),
	}, nil
}

func buildTLSConfig(cfg *TLSConfig) (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("load client cert: %w", err)
	}

	tlsCfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
	}

	if cfg.CAFile != "" {
		caCert, err := os.ReadFile(cfg.CAFile)
		if err != nil {
			return nil, fmt.Errorf("read CA cert: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caCert) {
			return nil, fmt.Errorf("failed to parse CA certificate")
		}
		tlsCfg.RootCAs = pool
	}

	return tlsCfg, nil
}

// Register adds a broadcast path and immediately pushes it to the SDN
// controller. Safe for concurrent use.
func (c *Client) Register(broadcastPath string) {
	c.mu.Lock()
	c.entries[broadcastPath] = struct{}{}
	c.mu.Unlock()

	// Fire-and-forget PUT; errors are logged, not propagated.
	go func() {
		if err := c.put(context.Background(), broadcastPath); err != nil {
			slog.Warn("sdn announce register failed", "error", err,
				"broadcast_path", broadcastPath)
		}
	}()
}

// Deregister removes a broadcast path and DELETEs it from the SDN
// controller. Safe for concurrent use.
func (c *Client) Deregister(broadcastPath string) {
	c.mu.Lock()
	delete(c.entries, broadcastPath)
	c.mu.Unlock()

	go func() {
		if err := c.delete(context.Background(), broadcastPath); err != nil {
			slog.Warn("sdn announce deregister failed", "error", err,
				"broadcast_path", broadcastPath)
		}
	}()
}

// Lookup queries the SDN controller for relays holding the given broadcast path.
func (c *Client) Lookup(ctx context.Context, broadcastPath string) ([]announceEntry, error) {
	url := fmt.Sprintf("%s/announce/lookup?broadcast_path=%s", c.config.URL, broadcastPath)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("lookup %s returned %d", url, resp.StatusCode)
	}

	var result LookupResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode lookup response: %w", err)
	}
	return result.Relays, nil
}

// Route queries the SDN controller for the shortest path from this relay to the target relay.
// Returns the RouteResult which includes NextHop and NextHopAddress.
func (c *Client) Route(ctx context.Context, to string) (topology.RouteResult, error) {
	u := fmt.Sprintf("%s/route?from=%s&to=%s",
		c.config.URL,
		url.QueryEscape(c.config.RelayName),
		url.QueryEscape(to),
	)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return topology.RouteResult{}, err
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return topology.RouteResult{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return topology.RouteResult{}, fmt.Errorf("route %s returned %d", u, resp.StatusCode)
	}

	var result topology.RouteResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return topology.RouteResult{}, fmt.Errorf("decode route response: %w", err)
	}
	return result, nil
}

// ListAll queries the SDN controller for all current announcements.
// Returns entries grouped by broadcast path. Only entries from other relays
// (excluding this client's own relay) are included.
func (c *Client) ListAll(ctx context.Context) ([]announceEntry, error) {
	u := fmt.Sprintf("%s/announce", c.config.URL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("list all %s returned %d", u, resp.StatusCode)
	}

	var result struct {
		Entries []announceEntry `json:"entries"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode list response: %w", err)
	}

	// Filter out our own entries
	filtered := make([]announceEntry, 0, len(result.Entries))
	for _, e := range result.Entries {
		if e.Relay != c.config.RelayName {
			filtered = append(filtered, e)
		}
	}
	return filtered, nil
}

// Run starts the heartbeat loop that periodically re-PUTs all registered
// announces. It blocks until ctx is cancelled.
func (c *Client) Run(ctx context.Context) {
	ctx, c.cancel = context.WithCancel(ctx)

	slog.Info("sdn announce client started",
		"url", c.config.URL,
		"relay", c.config.RelayName,
		"heartbeat", c.config.HeartbeatInterval)

	ticker := time.NewTicker(c.config.HeartbeatInterval)
	defer ticker.Stop()
	defer close(c.done)

	for {
		select {
		case <-ctx.Done():
			c.deregisterAll()
			return
		case <-ticker.C:
			c.heartbeat(ctx)
		}
	}
}

// Close cancels the heartbeat loop and waits for it to finish.
func (c *Client) Close() {
	if c.cancel != nil {
		c.cancel()
		<-c.done
	}
}

// snapshot returns a copy of the current broadcast paths.
func (c *Client) snapshot() []string {
	c.mu.Lock()
	defer c.mu.Unlock()

	paths := make([]string, 0, len(c.entries))
	for bp := range c.entries {
		paths = append(paths, bp)
	}
	return paths
}

func (c *Client) heartbeat(ctx context.Context) {
	paths := c.snapshot()
	for _, bp := range paths {
		if ctx.Err() != nil {
			return
		}
		if err := c.put(ctx, bp); err != nil {
			slog.Warn("sdn heartbeat failed", "error", err,
				"broadcast_path", bp)
		}
	}
	slog.Debug("sdn heartbeat completed", "entries", len(paths))
}

func (c *Client) deregisterAll() {
	paths := c.snapshot()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	for _, bp := range paths {
		if err := c.delete(ctx, bp); err != nil {
			slog.Warn("sdn deregister on shutdown failed", "error", err,
				"broadcast_path", bp)
		}
	}
	slog.Info("sdn announce client stopped", "deregistered", len(paths))
}

// announceURL builds the URL: /announce/<relay>/<broadcast_path>
func (c *Client) announceURL(broadcastPath string) string {
	bp := broadcastPath
	if len(bp) > 0 && bp[0] == '/' {
		bp = bp[1:]
	}
	return fmt.Sprintf("%s/announce/%s/%s", c.config.URL, c.config.RelayName, bp)
}

func (c *Client) put(ctx context.Context, broadcastPath string) error {
	body, _ := json.Marshal(map[string]string{
		"relay":          c.config.RelayName,
		"broadcast_path": broadcastPath,
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, c.announceURL(broadcastPath), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("PUT %s returned %d", req.URL, resp.StatusCode)
	}
	return nil
}

func (c *Client) delete(ctx context.Context, broadcastPath string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.announceURL(broadcastPath), nil)
	if err != nil {
		return err
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// 404 is acceptable (already removed)
	if resp.StatusCode >= 400 && resp.StatusCode != http.StatusNotFound {
		return fmt.Errorf("DELETE %s returned %d", req.URL, resp.StatusCode)
	}
	return nil
}
