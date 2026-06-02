package relay

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// localService is a Nomad API service registration response.
type localService struct {
	ID          string   `json:"ID"`
	ServiceName string   `json:"ServiceName"`
	Address     string   `json:"Address"`
	Port        int      `json:"Port"`
	Tags        []string `json:"Tags"`
	Datacenter  string   `json:"Datacenter"`
	JobID       string   `json:"JobID"`
	AllocID     string   `json:"AllocID"`
}

// LocalResolver discovers peers within the local cluster using Nomad's native
// service discovery API. It queries the Nomad HTTP API for services matching
// a configured service name, then filters by role tag.
//
// Configuration is read from environment variables:
//
//	LOCAL_RESOLVER_ADDR            - Nomad HTTP API address (default: "http://localhost:4646")
//	                                 Falls back to NOMAD_ADDR (auto-set by Nomad client)
//	                                 when not explicitly set.
//	LOCAL_RESOLVER_SERVICE_NAME    - Nomad service name to query (default: "qumo-relay")
//	LOCAL_RESOLVER_INTERVAL        - polling interval (default: "15s")
type LocalResolver struct {
	addr        string
	serviceName string
	interval    time.Duration
	httpClient  *http.Client
}

// NewLocalResolver creates a LocalResolver from environment variables.
// LOCAL_RESOLVER_ADDR takes precedence; if unset, falls back to NOMAD_ADDR
// (which Nomad auto-sets inside allocations), then to "http://localhost:4646".
func NewLocalResolver() *LocalResolver {
	addr := os.Getenv("LOCAL_RESOLVER_ADDR")
	if addr == "" {
		addr = os.Getenv("NOMAD_ADDR")
	}
	if addr == "" {
		addr = "http://localhost:4646"
	}
	serviceName := os.Getenv("LOCAL_RESOLVER_SERVICE_NAME")
	if serviceName == "" {
		serviceName = "qumo-relay"
	}
	intervalStr := os.Getenv("LOCAL_RESOLVER_INTERVAL")
	interval := 15 * time.Second
	if intervalStr != "" {
		if d, err := time.ParseDuration(intervalStr); err == nil {
			interval = d
		}
	}

	return &LocalResolver{
		addr:        addr,
		serviceName: serviceName,
		interval:    interval,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// Interval returns the polling interval for this resolver.
func (r *LocalResolver) Interval() time.Duration {
	return r.interval
}

// ResolvePeers queries the Nomad service API for all instances of the
// configured service name and filters them by the requested role.
// The role filter matches against service tags (e.g., a tag "role=hub" matches
// Role: "hub"). When Role is empty, all instances are returned.
func (r *LocalResolver) ResolvePeers(ctx context.Context, query PeerQuery) ([]ResolvedPeer, error) {
	u, err := url.JoinPath(r.addr, "/v1/service/", r.serviceName)
	if err != nil {
		return nil, fmt.Errorf("local resolver: build URL: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("local resolver: create request: %w", err)
	}

	resp, err := r.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("local resolver: query %s: %w", u, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil, fmt.Errorf("local resolver: %s returned status %d", u, resp.StatusCode)
	}

	const maxBody = 1 << 20 // 1 MB
	var services []localService
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(&services); err != nil {
		return nil, fmt.Errorf("local resolver: decode services: %w", err)
	}

	// Client-side filtering by role tag and limit.
	var results []ResolvedPeer
	for _, svc := range services {
		role := serviceTag(svc.Tags, "role")
		if query.Role != "" && role != query.Role {
			continue
		}
		region := serviceTag(svc.Tags, "region")
		if region == "" {
			region = svc.Datacenter
		}

		addr := netJoinHostPort(svc.Address, svc.Port)
		results = append(results, ResolvedPeer{
			ID:      svc.ID,
			Address: addr,
			Region:  region,
			Role:    role,
		})
	}

	if query.Limit > 0 && len(results) > query.Limit {
		results = results[:query.Limit]
	}

	return results, nil
}

// serviceTag extracts a tag value by prefix from a tag list.
// Tags are either bare values like "hub" or key=value pairs like "region=us-east".
// The key match is case-insensitive.
func serviceTag(tags []string, key string) string {
	prefix := strings.ToLower(key) + "="
	for _, t := range tags {
		lower := strings.ToLower(t)
		if lower == key {
			return t
		}
		if strings.HasPrefix(lower, prefix) {
			return t[len(prefix):]
		}
	}
	return ""
}

// netJoinHostPort is a helper to format host:port (avoids importing net for
// a one-liner used outside hot paths).
func netJoinHostPort(host string, port int) string {
	if port == 0 {
		return host
	}
	// Simple IPv6 check.
	if strings.Contains(host, ":") {
		return fmt.Sprintf("[%s]:%d", host, port)
	}
	return fmt.Sprintf("%s:%d", host, port)
}

