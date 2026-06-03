package relay

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestBackendClient wires a BackendClient to srv with an auth token pre-set.
func newTestBackendClient(srv *httptest.Server) *BackendClient {
	return &BackendClient{
		baseURL:    srv.URL,
		authToken:  "test-token",
		httpClient: srv.Client(),
		cache:      make(map[string]cachedCredential),
	}
}

// okIntrospectResponse builds a valid introspect response body.
func okIntrospectResponse(tokenID string, revalidateAfter time.Time) introspectResponse {
	return introspectResponse{
		Valid:           true,
		TokenID:         tokenID,
		ProjectID:       "proj-1",
		TenantID:        "tenant-1",
		APIKeyID:        "key-1",
		Environment:     "production",
		RevalidateAfter: revalidateAfter.UTC().Format(time.RFC3339),
	}
}

// writeIntrospectJSON encodes resp as JSON and writes it to w.
func writeIntrospectJSON(w http.ResponseWriter, resp introspectResponse) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// ── NewBackendClient ──────────────────────────────────────────────────────────

func TestNewBackendClient(t *testing.T) {
	t.Run("nil when QUMO_BACKEND_URL unset", func(t *testing.T) {
		t.Setenv("QUMO_BACKEND_URL", "")
		assert.Nil(t, NewBackendClient())
	})

	t.Run("returns client with correct fields", func(t *testing.T) {
		t.Setenv("QUMO_BACKEND_URL", "https://backend.example.com")
		t.Setenv("QUMO_RELAY_TOKEN", "my-secret")
		c := NewBackendClient()
		require.NotNil(t, c)
		assert.Equal(t, "https://backend.example.com", c.baseURL)
		assert.Equal(t, "my-secret", c.authToken)
	})

	t.Run("adds https scheme when missing", func(t *testing.T) {
		t.Setenv("QUMO_BACKEND_URL", "backend.example.com")
		t.Setenv("QUMO_RELAY_TOKEN", "tok")
		c := NewBackendClient()
		require.NotNil(t, c)
		assert.Equal(t, "https://backend.example.com", c.baseURL)
	})

	t.Run("preserves http scheme", func(t *testing.T) {
		t.Setenv("QUMO_BACKEND_URL", "http://backend.example.com")
		t.Setenv("QUMO_RELAY_TOKEN", "tok")
		c := NewBackendClient()
		require.NotNil(t, c)
		assert.Equal(t, "http://backend.example.com", c.baseURL)
	})

	t.Run("strips trailing slash", func(t *testing.T) {
		t.Setenv("QUMO_BACKEND_URL", "https://backend.example.com/")
		t.Setenv("QUMO_RELAY_TOKEN", "tok")
		c := NewBackendClient()
		require.NotNil(t, c)
		assert.Equal(t, "https://backend.example.com", c.baseURL)
	})
}

// ── Introspect – request shape ────────────────────────────────────────────────

func TestBackendClient_Introspect_RequestShape(t *testing.T) {
	revalidate := time.Now().Add(10 * time.Minute)
	var gotMethod, gotPath, gotAuth, gotCT string
	var gotBodyBytes []byte

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotCT = r.Header.Get("Content-Type")
		gotBodyBytes, _ = io.ReadAll(r.Body)
		writeIntrospectJSON(w, okIntrospectResponse("tok-1", revalidate))
	}))
	defer srv.Close()

	_, err := newTestBackendClient(srv).Introspect(context.Background(), "the-jwt")
	require.NoError(t, err)

	assert.Equal(t, http.MethodPost, gotMethod)
	assert.Equal(t, "/v1/credentials/introspect", gotPath)
	assert.Equal(t, "Bearer test-token", gotAuth)
	assert.Equal(t, "application/json", gotCT)
	assert.Contains(t, string(gotBodyBytes), "the-jwt")
}

func TestBackendClient_Introspect_NoAuthHeaderWhenTokenEmpty(t *testing.T) {
	revalidate := time.Now().Add(10 * time.Minute)
	var gotAuth string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		writeIntrospectJSON(w, okIntrospectResponse("tok", revalidate))
	}))
	defer srv.Close()

	c := newTestBackendClient(srv)
	c.authToken = ""
	_, err := c.Introspect(context.Background(), "jwt")
	require.NoError(t, err)
	assert.Empty(t, gotAuth)
}

// ── Introspect – result parsing ───────────────────────────────────────────────

func TestBackendClient_Introspect_ValidCredential(t *testing.T) {
	revalidate := time.Now().Add(5 * time.Minute)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeIntrospectJSON(w, okIntrospectResponse("tok-abc", revalidate))
	}))
	defer srv.Close()

	result, err := newTestBackendClient(srv).Introspect(context.Background(), "jwt")
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "tok-abc", result.TokenID)
	assert.Equal(t, "proj-1", result.ProjectID)
	assert.Equal(t, "tenant-1", result.TenantID)
	assert.Equal(t, "key-1", result.APIKeyID)
	assert.Equal(t, "production", result.Environment)
}

func TestBackendClient_Introspect_InvalidCredential(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeIntrospectJSON(w, introspectResponse{Valid: false})
	}))
	defer srv.Close()

	result, err := newTestBackendClient(srv).Introspect(context.Background(), "bad-jwt")
	require.NoError(t, err)
	assert.Nil(t, result, "valid:false must return nil result without an error")
}

// ── Introspect – caching ──────────────────────────────────────────────────────

func TestBackendClient_Introspect_CacheHit(t *testing.T) {
	var calls atomic.Int32
	revalidate := time.Now().Add(10 * time.Minute)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		writeIntrospectJSON(w, okIntrospectResponse("tok-cache", revalidate))
	}))
	defer srv.Close()

	c := newTestBackendClient(srv)
	ctx := context.Background()

	r1, err := c.Introspect(ctx, "jwt-same")
	require.NoError(t, err)
	require.NotNil(t, r1)

	r2, err := c.Introspect(ctx, "jwt-same")
	require.NoError(t, err)
	require.NotNil(t, r2)

	assert.Equal(t, int32(1), calls.Load(), "second call must be served from cache without an HTTP round-trip")
	assert.Equal(t, r1.TokenID, r2.TokenID)
}

func TestBackendClient_Introspect_DifferentJWTsAreCachedIndependently(t *testing.T) {
	var calls atomic.Int32
	revalidate := time.Now().Add(10 * time.Minute)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		writeIntrospectJSON(w, okIntrospectResponse("tok-"+string(rune('A'+n-1)), revalidate))
	}))
	defer srv.Close()

	c := newTestBackendClient(srv)
	ctx := context.Background()

	r1, err := c.Introspect(ctx, "jwt-alpha")
	require.NoError(t, err)

	r2, err := c.Introspect(ctx, "jwt-beta")
	require.NoError(t, err)

	assert.Equal(t, int32(2), calls.Load(), "distinct JWTs must each make their own HTTP request")
	assert.NotEqual(t, r1.TokenID, r2.TokenID)
}

func TestBackendClient_Introspect_CacheExpiry(t *testing.T) {
	var calls atomic.Int32
	// Returning revalidate_after in the past causes the fallback TTL; but even
	// with the 5-min fallback the entry won't be immediately re-fetched. Instead
	// we manually insert a pre-expired entry to test the expiry path.
	revalidate := time.Now().Add(10 * time.Minute)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		writeIntrospectJSON(w, okIntrospectResponse("tok-exp", revalidate))
	}))
	defer srv.Close()

	c := newTestBackendClient(srv)

	// Seed the cache with an already-expired entry.
	c.cacheMu.Lock()
	c.cache["jwt-exp"] = cachedCredential{
		result:  IntrospectResult{TokenID: "stale"},
		expires: time.Now().Add(-1 * time.Minute),
	}
	c.cacheMu.Unlock()

	result, err := c.Introspect(context.Background(), "jwt-exp")
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, int32(1), calls.Load(), "expired entry must trigger a fresh HTTP request")
	assert.Equal(t, "tok-exp", result.TokenID, "result must come from the fresh response, not the stale cache")
}

func TestBackendClient_Introspect_CacheEviction(t *testing.T) {
	revalidate := time.Now().Add(10 * time.Minute)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeIntrospectJSON(w, okIntrospectResponse("tok-new", revalidate))
	}))
	defer srv.Close()

	c := newTestBackendClient(srv)

	// Manually insert two stale entries.
	c.cacheMu.Lock()
	c.cache["stale-1"] = cachedCredential{expires: time.Now().Add(-2 * time.Minute)}
	c.cache["stale-2"] = cachedCredential{expires: time.Now().Add(-1 * time.Minute)}
	c.cacheMu.Unlock()

	// A successful introspection triggers the sweep.
	_, err := c.Introspect(context.Background(), "fresh-jwt")
	require.NoError(t, err)

	c.cacheMu.Lock()
	_, stale1 := c.cache["stale-1"]
	_, stale2 := c.cache["stale-2"]
	_, fresh := c.cache["fresh-jwt"]
	c.cacheMu.Unlock()

	assert.False(t, stale1, "expired entry stale-1 must be swept")
	assert.False(t, stale2, "expired entry stale-2 must be swept")
	assert.True(t, fresh, "fresh entry must remain in cache")
}

// ── Introspect – singleflight ─────────────────────────────────────────────────

func TestBackendClient_Introspect_SingleflightCoalescesConcurrentRequests(t *testing.T) {
	const n = 10
	var calls atomic.Int32
	revalidate := time.Now().Add(10 * time.Minute)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		// Hold the response long enough for all goroutines to pile up in singleflight.
		time.Sleep(50 * time.Millisecond)
		writeIntrospectJSON(w, okIntrospectResponse("tok-sf", revalidate))
	}))
	defer srv.Close()

	c := newTestBackendClient(srv)
	ctx := context.Background()

	var wg sync.WaitGroup
	wg.Add(n)
	results := make([]*IntrospectResult, n)
	for i := range n {
		go func(i int) {
			defer wg.Done()
			results[i], _ = c.Introspect(ctx, "shared-jwt")
		}(i)
	}
	wg.Wait()

	assert.Equal(t, int32(1), calls.Load(), "singleflight must coalesce all concurrent requests into one HTTP call")
	for i, r := range results {
		require.NotNil(t, r, "goroutine %d must receive a non-nil result", i)
		assert.Equal(t, "tok-sf", r.TokenID)
	}
}

// ── Introspect – error handling ───────────────────────────────────────────────

func TestBackendClient_Introspect_NonOKStatus(t *testing.T) {
	for _, status := range []int{
		http.StatusUnauthorized,
		http.StatusForbidden,
		http.StatusInternalServerError,
		http.StatusBadGateway,
	} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
			}))
			defer srv.Close()

			_, err := newTestBackendClient(srv).Introspect(context.Background(), "jwt")
			require.Error(t, err)
			assert.Contains(t, err.Error(), "backend:")
		})
	}
}

func TestBackendClient_Introspect_MalformedJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("{not valid json"))
	}))
	defer srv.Close()

	_, err := newTestBackendClient(srv).Introspect(context.Background(), "jwt")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "backend:")
}

func TestBackendClient_Introspect_ContextCancelled(t *testing.T) {
	// Pre-cancel the context so the HTTP client rejects the request immediately,
	// avoiding any reliance on the server-side request context being cancelled
	// when the client drops the connection (behaviour differs across platforms).
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK) // should never be reached
	}))
	defer srv.Close()

	_, err := newTestBackendClient(srv).Introspect(ctx, "jwt")
	require.Error(t, err)
}

// ── ReportUsage ───────────────────────────────────────────────────────────────

func TestBackendClient_ReportUsage_SendsCorrectPayload(t *testing.T) {
	var gotMethod, gotPath, gotAuth, gotCT string
	var gotEvents []UsageEvent

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotCT = r.Header.Get("Content-Type")
		_ = json.NewDecoder(r.Body).Decode(&gotEvents)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	events := []UsageEvent{
		{
			BroadcastSessionID: "sess-abc",
			OwnerTokenID:       "tok-1",
			Metrics: map[string]int64{
				"gateway.ingress_bytes": 1024,
				"gateway.egress_bytes":  4096,
			},
			Ts: time.Now().UTC().Format(time.RFC3339),
		},
	}

	err := newTestBackendClient(srv).ReportUsage(context.Background(), events)
	require.NoError(t, err)

	assert.Equal(t, http.MethodPost, gotMethod)
	assert.Equal(t, "/v1/usage/events", gotPath)
	assert.Equal(t, "Bearer test-token", gotAuth)
	assert.Equal(t, "application/json", gotCT)
	require.Len(t, gotEvents, 1)
	assert.Equal(t, "sess-abc", gotEvents[0].BroadcastSessionID)
	assert.Equal(t, int64(1024), gotEvents[0].Metrics["gateway.ingress_bytes"])
	assert.Equal(t, int64(4096), gotEvents[0].Metrics["gateway.egress_bytes"])
}

func TestBackendClient_ReportUsage_NoOpOnEmptySlice(t *testing.T) {
	var called bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	defer srv.Close()

	err := newTestBackendClient(srv).ReportUsage(context.Background(), nil)
	require.NoError(t, err)
	assert.False(t, called, "must not make an HTTP request for an empty event list")

	err = newTestBackendClient(srv).ReportUsage(context.Background(), []UsageEvent{})
	require.NoError(t, err)
	assert.False(t, called, "must not make an HTTP request for an empty event list")
}

func TestBackendClient_ReportUsage_NonOKStatus(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusInternalServerError} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
			}))
			defer srv.Close()

			err := newTestBackendClient(srv).ReportUsage(context.Background(), []UsageEvent{{BroadcastSessionID: "x"}})
			require.Error(t, err)
			assert.Contains(t, err.Error(), "backend:")
		})
	}
}

func TestBackendClient_ReportUsage_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel so the HTTP dial is rejected immediately

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK) // should never be reached
	}))
	defer srv.Close()

	err := newTestBackendClient(srv).ReportUsage(ctx, []UsageEvent{{BroadcastSessionID: "x"}})
	require.Error(t, err)
}
