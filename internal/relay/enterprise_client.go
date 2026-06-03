package relay

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

const enterpriseMaxBody = 1 << 20 // 1 MB

type introspectRequest struct {
	Token string `json:"token"`
}

type introspectResponse struct {
	Valid           bool   `json:"valid"`
	TokenID         string `json:"token_id"`
	ProjectID       string `json:"project_id"`
	TenantID        string `json:"tenant_id"`
	APIKeyID        string `json:"api_key_id"`
	Environment     string `json:"environment"`
	RevalidateAfter string `json:"revalidate_after"`
}

// IntrospectResult holds the validated details from a successful credential introspection.
type IntrospectResult struct {
	TokenID     string
	ProjectID   string
	TenantID    string
	APIKeyID    string
	Environment string
}

type cachedCredential struct {
	result  IntrospectResult
	expires time.Time
}

// UsageEvent is a single cumulative usage record for a broadcast session.
// All byte values are totals since session start; the server diffs consecutive reports.
type UsageEvent struct {
	BroadcastSessionID string            `json:"broadcast_session_id"`
	OwnerTokenID       string            `json:"owner_token_id"`
	Metrics            map[string]uint64 `json:"metrics"`
	Ts                 string            `json:"ts"` // RFC3339
}

// EnterpriseClient communicates with the qumo-enterprise backend for credential
// introspection and usage reporting.
//
// Configuration is read from environment variables:
//
//	QUMO_ENTERPRISE_URL  - base URL of the enterprise server
//	QUMO_RELAY_TOKEN     - shared bearer token (must match the server's config)
type EnterpriseClient struct {
	baseURL    string
	authToken  string
	httpClient *http.Client

	cacheMu sync.Mutex
	cache   map[string]cachedCredential
}

// NewEnterpriseClient creates an EnterpriseClient from environment variables.
// Returns nil when QUMO_ENTERPRISE_URL is not set (enterprise features disabled).
func NewEnterpriseClient() *EnterpriseClient {
	baseURL := os.Getenv("QUMO_ENTERPRISE_URL")
	if baseURL == "" {
		return nil
	}
	if !strings.HasPrefix(baseURL, "http://") && !strings.HasPrefix(baseURL, "https://") {
		baseURL = "https://" + baseURL
	}
	baseURL = strings.TrimRight(baseURL, "/")

	authToken := os.Getenv("QUMO_RELAY_TOKEN")
	if authToken == "" {
		slog.Warn("QUMO_ENTERPRISE_URL is set but QUMO_RELAY_TOKEN is empty")
	}

	slog.Info("enterprise client configured", "url", baseURL)
	return &EnterpriseClient{
		baseURL:   baseURL,
		authToken: authToken,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		cache: make(map[string]cachedCredential),
	}
}

// Introspect validates a publisher JWT against the enterprise credential endpoint.
// Returns (nil, nil) when the token is present but the server reports valid:false.
// Results are cached until the server-supplied revalidate_after time.
func (c *EnterpriseClient) Introspect(ctx context.Context, jwt string) (*IntrospectResult, error) {
	c.cacheMu.Lock()
	if cached, ok := c.cache[jwt]; ok && time.Now().Before(cached.expires) {
		result := cached.result
		c.cacheMu.Unlock()
		return &result, nil
	}
	c.cacheMu.Unlock()

	body, err := json.Marshal(introspectRequest{Token: jwt})
	if err != nil {
		return nil, fmt.Errorf("enterprise: marshal introspect request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/v1/credentials/introspect", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("enterprise: create introspect request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.authToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.authToken)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("enterprise: introspect: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil, fmt.Errorf("enterprise: introspect returned HTTP %d", resp.StatusCode)
	}

	var raw introspectResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, enterpriseMaxBody)).Decode(&raw); err != nil {
		return nil, fmt.Errorf("enterprise: decode introspect response: %w", err)
	}

	if !raw.Valid {
		return nil, nil // token present but not valid
	}

	expires, err := time.Parse(time.RFC3339, raw.RevalidateAfter)
	if err != nil || expires.IsZero() || !expires.After(time.Now()) {
		expires = time.Now().Add(5 * time.Minute) // safe fallback
	}

	result := IntrospectResult{
		TokenID:     raw.TokenID,
		ProjectID:   raw.ProjectID,
		TenantID:    raw.TenantID,
		APIKeyID:    raw.APIKeyID,
		Environment: raw.Environment,
	}

	c.cacheMu.Lock()
	c.cache[jwt] = cachedCredential{result: result, expires: expires}
	c.cacheMu.Unlock()

	return &result, nil
}

// ReportUsage POSTs a batch of cumulative usage events to the enterprise backend.
func (c *EnterpriseClient) ReportUsage(ctx context.Context, events []UsageEvent) error {
	if len(events) == 0 {
		return nil
	}

	body, err := json.Marshal(events)
	if err != nil {
		return fmt.Errorf("enterprise: marshal usage events: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/v1/usage/events", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("enterprise: create usage request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.authToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.authToken)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("enterprise: report usage: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("enterprise: usage events returned HTTP %d", resp.StatusCode)
	}
	return nil
}
