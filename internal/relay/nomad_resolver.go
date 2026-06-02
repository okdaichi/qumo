package relay

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// nomadService is a Nomad API service registration response.
type nomadService struct {
	ID          string   `json:"ID"`
	ServiceName string   `json:"ServiceName"`
	Address     string   `json:"Address"`
	Port        int      `json:"Port"`
	Tags        []string `json:"Tags"`
	Datacenter  string   `json:"Datacenter"`
	JobID       string   `json:"JobID"`
	AllocID     string   `json:"AllocID"`
}

// NomadResolver discovers peers within a Nomad cluster using the Nomad
// native service discovery API. It queries the Nomad HTTP API for services
// matching a configured service name, then filters by role tag.
//
// Configuration is read from environment variables:
//
//	NOMAD_ADDR          - Nomad HTTP API address (default: "http://localhost:4646")
//	                     When running inside a Nomad allocation, NOMAD_ADDR is
//	                     automatically set by the Nomad client.
//	NOMAD_SERVICE_NAME  - Nomad service name to query (default: "qumo-relay")
//	NOMAD_RESOLVE_INTERVAL - polling interval (default: "15s")
type NomadResolver struct {
	addr        string
	serviceName string
	interval    time.Duration
	httpClient  *http.Client
}

// NewNomadResolver creates a NomadResolver from environment variables.
// When NOMAD_ADDR is unset, it defaults to "http://localhost:4646" (or the
// Nomad-out-of-cluster default). When running inside a Nomad allocation,
// NOMAD_ADDR is set automatically by the Nomad client.
func NewNomadResolver() *NomadResolver {
	addr := os.Getenv("NOMAD_ADDR")
	if addr == "" {
		addr = "http://localhost:4646"
	}
	serviceName := os.Getenv("NOMAD_SERVICE_NAME")
	if serviceName == "" {
		serviceName = "qumo-relay"
	}
	intervalStr := os.Getenv("NOMAD_RESOLVE_INTERVAL")
	interval := 15 * time.Second
	if intervalStr != "" {
		if d, err := time.ParseDuration(intervalStr); err == nil {
			interval = d
		}
	}

	return &NomadResolver{
		addr:        addr,
		serviceName: serviceName,
		interval:    interval,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// Interval returns the polling interval for this resolver.
func (r *NomadResolver) Interval() time.Duration {
	return r.interval
}

// ResolvePeers queries the Nomad service API for all instances of the
// configured service name and filters them by the requested role.
// The role filter matches against service tags (e.g., a tag "hub" matches
// Role: "hub"). When Role is empty, all instances are returned.
func (r *NomadResolver) ResolvePeers(ctx context.Context, query PeerQuery) ([]ResolvedPeer, error) {
	u, err := url.JoinPath(r.addr, "/v1/service/", r.serviceName)
	if err != nil {
		return nil, fmt.Errorf("nomad: build URL: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("nomad: create request: %w", err)
	}

	resp, err := r.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("nomad: query %s: %w", u, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil, fmt.Errorf("nomad: %s returned status %d", u, resp.StatusCode)
	}

	const maxBody = 1 << 20 // 1 MB
	var services []nomadService
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(&services); err != nil {
		return nil, fmt.Errorf("nomad: decode services: %w", err)
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

// envIntOr is a helper for reading integer env vars used by both resolvers.
func envIntOr(key string, defaultVal int) (int, error) {
	v := os.Getenv(key)
	if v == "" {
		return defaultVal, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, err
	}
	return n, nil
}
