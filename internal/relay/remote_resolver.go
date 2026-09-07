package relay

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// remotePeerResponse is the JSON response from the remote traffic resolver's /peers endpoint.
type remotePeerResponse struct {
	Peers []remotePeer `json:"peers"`
}

type remotePeer struct {
	ID     string `json:"id"`
	Addr   string `json:"addr"`
	Region string `json:"region,omitempty"`
	Role   string `json:"role,omitempty"`
}

// RemoteResolver discovers peers by querying the remote traffic resolver API.
// It is used for cross-cluster hub discovery when configured.
//
// Configuration is read from environment variables:
//
//	REMOTE_RESOLVER_URL     - base URL of the remote traffic resolver
//	                         (e.g. "https://traffic-resolver.example.com:8443")
//	REMOTE_AUTH_TOKEN       - optional bearer token for authentication
//	REMOTE_RESOLVE_INTERVAL - polling interval (default: "15s")
//	REMOTE_TLS_ENABLED      - "true" to use TLS for the remote resolver
//	                         connection (default: false)
//	CA_FILE                 - PEM CA file for mTLS (used when REMOTE_TLS_ENABLED=true)
type RemoteResolver struct {
	url        string
	hubID      string
	authToken  string
	interval   time.Duration
	httpClient *http.Client
}

// NewRemoteResolver creates a RemoteResolver from environment variables.
// It returns nil when REMOTE_RESOLVER_URL is not set (remote discovery disabled).
// The optional TLS config enables mTLS when the relay has a CA_FILE configured.
// hubID is this relay's node ID (RELAY_NAME); it is sent as the hub query
// parameter so the registry can skip the requester's own row and bound the
// mesh degree per peer instead of returning the full hub list. Empty disables
// the parameter.
func NewRemoteResolver(tlsConfig *tls.Config, hubID string) *RemoteResolver {
	rawURL := os.Getenv("REMOTE_RESOLVER_URL")
	if rawURL == "" {
		return nil
	}

	// Normalize URL (strip trailing slashes, ensure scheme).
	if !strings.HasPrefix(rawURL, "http://") && !strings.HasPrefix(rawURL, "https://") {
		rawURL = "https://" + rawURL
	}
	rawURL = strings.TrimRight(rawURL, "/")

	intervalStr := os.Getenv("REMOTE_RESOLVE_INTERVAL")
	interval := 15 * time.Second
	if intervalStr != "" {
		if d, err := time.ParseDuration(intervalStr); err == nil {
			interval = d
		}
	}

	authToken := os.Getenv("REMOTE_AUTH_TOKEN")

	transport := http.DefaultTransport.(*http.Transport).Clone() //nolint:forcetypeassert
	tlsEnabled, _ := strconv.ParseBool(os.Getenv("REMOTE_TLS_ENABLED"))
	if tlsEnabled && tlsConfig != nil {
		transport.TLSClientConfig = tlsConfig
	}

	slog.Info("remote resolver configured", "url", rawURL, "interval", interval)

	return &RemoteResolver{
		url:       rawURL,
		hubID:     hubID,
		authToken: authToken,
		interval:  interval,
		httpClient: &http.Client{
			Timeout:   10 * time.Second,
			Transport: transport,
		},
	}
}

// Interval returns the polling interval for this resolver.
func (r *RemoteResolver) Interval() time.Duration {
	return r.interval
}

// ResolvePeers queries the remote traffic resolver's /peers endpoint.
//
// The remote resolver is a hub-only registry: it is the cross-cluster hub
// discovery path, and every peer it returns is a hub. query.Role is therefore
// neither sent to the server (it is a hub-only registry and ignores it) nor
// used to re-filter the response. The control plane is collapsing /peers to a
// hub-only registry and dropping the per-peer role field (foalk-inc/qumo-deploy#535);
// re-filtering on it would silently drop every peer once role decodes as "".
// Instead we trust the server's hub-only contract and treat a missing per-peer
// role as the queried role.
func (r *RemoteResolver) ResolvePeers(ctx context.Context, query PeerQuery) ([]ResolvedPeer, error) {
	// Parse the base URL and append the path segment instead of string-
	// concatenating: a base URL that already carries a query (e.g. an
	// operator-supplied "?hub=") must not be mangled into the path, and its
	// other parameters are preserved below.
	base, err := url.Parse(r.url)
	if err != nil {
		return nil, fmt.Errorf("remote: parse URL: %w", err)
	}
	u := *base
	u.Path = strings.TrimSuffix(base.Path, "/") + "/peers"

	qs := base.Query()
	if r.hubID != "" {
		qs.Set("hub", r.hubID)
	}
	if query.Limit > 0 {
		qs.Set("limit", strconv.Itoa(query.Limit))
	}
	u.RawQuery = qs.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("remote: create request: %w", err)
	}
	if r.authToken != "" {
		req.Header.Set("Authorization", "Bearer "+r.authToken)
	}

	resp, err := r.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("remote: query %s: %w", u.String(), err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil, fmt.Errorf("remote: %s returned status %d", u.String(), resp.StatusCode)
	}

	const maxBody = 1 << 20 // 1 MB
	var wrapper remotePeerResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(&wrapper); err != nil {
		return nil, fmt.Errorf("remote: decode response: %w", err)
	}

	// Convert to ResolvedPeer. Do not re-filter on p.Role: the remote resolver
	// is a hub-only registry and may omit the role field (see method doc). When
	// it does, fall back to the queried role so downstream metadata stays set.
	results := make([]ResolvedPeer, 0, len(wrapper.Peers))
	for _, p := range wrapper.Peers {
		role := p.Role
		if role == "" {
			role = query.Role
		}
		results = append(results, ResolvedPeer{
			ID:      p.ID,
			Address: p.Addr,
			Region:  p.Region,
			Role:    role,
		})
	}

	if query.Limit > 0 && len(results) > query.Limit {
		results = results[:query.Limit]
	}

	return results, nil
}

// CloseIdleConnections closes idle HTTP connections on the underlying transport.
func (r *RemoteResolver) CloseIdleConnections() {
	if transport, ok := r.httpClient.Transport.(*http.Transport); ok {
		transport.CloseIdleConnections()
	}
}
