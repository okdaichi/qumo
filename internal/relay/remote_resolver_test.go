package relay

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewRemoteResolver(t *testing.T) {
	t.Run("returns nil when URL unset", func(t *testing.T) {
		t.Setenv("REMOTE_RESOLVER_URL", "")
		t.Setenv("REMOTE_AUTH_TOKEN", "")
		t.Setenv("REMOTE_RESOLVE_INTERVAL", "")
		t.Setenv("REMOTE_TLS_ENABLED", "")

		r := NewRemoteResolver(nil)
		assert.Nil(t, r)
	})

	t.Run("parses env vars correctly", func(t *testing.T) {
		t.Setenv("REMOTE_RESOLVER_URL", "https://resolver.example.com:8443")
		t.Setenv("REMOTE_AUTH_TOKEN", "my-secret-token")
		t.Setenv("REMOTE_RESOLVE_INTERVAL", "30s")
		t.Setenv("REMOTE_TLS_ENABLED", "true")

		r := NewRemoteResolver(&tls.Config{})
		require.NotNil(t, r)
		assert.Equal(t, "https://resolver.example.com:8443", r.url)
		assert.Equal(t, "my-secret-token", r.authToken)
		assert.Equal(t, 30*time.Second, r.interval)
	})

	t.Run("normalizes URL with missing scheme", func(t *testing.T) {
		t.Setenv("REMOTE_RESOLVER_URL", "resolver.example.com:8443")

		r := NewRemoteResolver(nil)
		require.NotNil(t, r)
		assert.Equal(t, "https://resolver.example.com:8443", r.url)
	})

	t.Run("normalizes URL with trailing slash", func(t *testing.T) {
		t.Setenv("REMOTE_RESOLVER_URL", "https://resolver.example.com/")

		r := NewRemoteResolver(nil)
		require.NotNil(t, r)
		assert.Equal(t, "https://resolver.example.com", r.url)
	})

	t.Run("default interval 15s", func(t *testing.T) {
		t.Setenv("REMOTE_RESOLVER_URL", "https://resolver.example.com")
		t.Setenv("REMOTE_RESOLVE_INTERVAL", "")

		r := NewRemoteResolver(nil)
		require.NotNil(t, r)
		assert.Equal(t, 15*time.Second, r.interval)
	})
}

func TestRemoteResolver_Interval(t *testing.T) {
	r := &RemoteResolver{interval: 10 * time.Second}
	assert.Equal(t, 10*time.Second, r.Interval())
}

func TestRemoteResolver_ResolvePeers(t *testing.T) {
	t.Run("returns peers from remote API", func(t *testing.T) {
		var gotAuthHeader string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/peers", r.URL.Path)
			assert.Equal(t, http.MethodGet, r.Method)
			gotAuthHeader = r.Header.Get("Authorization")

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(remotePeerResponse{
				Peers: []remotePeer{
					{
						ID:     "hub-1",
						Addr:   "10.0.0.1:4433",
						Region: "us-east",
						Role:   "hub",
					},
					{
						ID:     "hub-2",
						Addr:   "10.0.0.2:4433",
						Region: "europe",
						Role:   "hub",
					},
				},
			})
		}))
		defer srv.Close()

		r := &RemoteResolver{
			url:        srv.URL,
			authToken:  "",
			interval:   15 * time.Second,
			httpClient: srv.Client(),
		}

		peers, err := r.ResolvePeers(context.Background(), PeerQuery{})
		require.NoError(t, err)
		require.Len(t, peers, 2)

		assert.Equal(t, "hub-1", peers[0].ID)
		assert.Equal(t, "10.0.0.1:4433", peers[0].Address)
		assert.Equal(t, "us-east", peers[0].Region)
		assert.Equal(t, "hub", peers[0].Role)
		assert.Empty(t, gotAuthHeader, "no auth header when token unset")
	})

	t.Run("sends auth token", func(t *testing.T) {
		var gotAuth string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotAuth = r.Header.Get("Authorization")
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(remotePeerResponse{Peers: []remotePeer{}})
		}))
		defer srv.Close()

		r := &RemoteResolver{
			url:        srv.URL,
			authToken:  "secret-token",
			interval:   15 * time.Second,
			httpClient: srv.Client(),
		}

		_, err := r.ResolvePeers(context.Background(), PeerQuery{})
		require.NoError(t, err)
		assert.Equal(t, "Bearer secret-token", gotAuth)
	})

	t.Run("does not send role query param", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.False(t, r.URL.Query().Has("role"), "role must not be sent to the hub-only registry")
			assert.False(t, r.URL.Query().Has("limit"), "limit must not be sent when unset")
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(remotePeerResponse{
				Peers: []remotePeer{
					{ID: "hub-1", Addr: "10.0.0.1:4433", Role: "hub"},
					{ID: "hub-2", Addr: "10.0.0.2:4433", Role: "hub"},
				},
			})
		}))
		defer srv.Close()

		r := &RemoteResolver{
			url:        srv.URL,
			interval:   15 * time.Second,
			httpClient: srv.Client(),
		}

		peers, err := r.ResolvePeers(context.Background(), PeerQuery{Role: "hub"})
		require.NoError(t, err)
		require.Len(t, peers, 2)
		assert.Equal(t, "hub-1", peers[0].ID)
		assert.Equal(t, "hub-2", peers[1].ID)
	})

	t.Run("does not re-filter when response omits role (hub-only registry)", func(t *testing.T) {
		// Simulates the control plane collapsing /peers to a hub-only registry
		// and dropping the per-peer role field (foalk-inc/qumo-deploy#535). The
		// resolver must trust the server's contract and return every peer rather
		// than silently filtering them all out.
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(remotePeerResponse{
				Peers: []remotePeer{
					{ID: "hub-1", Addr: "10.0.0.1:4433", Region: "us-east"},
					{ID: "hub-2", Addr: "10.0.0.2:4433", Region: "europe"},
				},
			})
		}))
		defer srv.Close()

		r := &RemoteResolver{
			url:        srv.URL,
			interval:   15 * time.Second,
			httpClient: srv.Client(),
		}

		peers, err := r.ResolvePeers(context.Background(), PeerQuery{Role: "hub"})
		require.NoError(t, err)
		require.Len(t, peers, 2)
		// Missing per-peer role falls back to the queried role.
		assert.Equal(t, "hub", peers[0].Role)
		assert.Equal(t, "hub", peers[1].Role)
	})

	t.Run("returns every peer regardless of role, never filtering", func(t *testing.T) {
		// The pre-#93 resolver re-filtered on p.Role and would have dropped any
		// peer whose role != the queried role. Under the hub-only contract we
		// trust the server: every returned peer is kept, whatever its role —
		// and explicit roles are preserved while a blank role falls back to the
		// queried role. This is the exact regression #93 guards against.
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(remotePeerResponse{
				Peers: []remotePeer{
					{ID: "hub-1", Addr: "10.0.0.1:4433", Role: "hub"},
					{ID: "edge-1", Addr: "10.0.0.2:4433", Role: "edge"},
					{ID: "blank-1", Addr: "10.0.0.3:4433"},
				},
			})
		}))
		defer srv.Close()

		r := &RemoteResolver{
			url:        srv.URL,
			interval:   15 * time.Second,
			httpClient: srv.Client(),
		}

		peers, err := r.ResolvePeers(context.Background(), PeerQuery{Role: "hub"})
		require.NoError(t, err)
		require.Len(t, peers, 3, "no peer may be filtered out by role")
		assert.Equal(t, "hub", peers[0].Role, "explicit role preserved")
		assert.Equal(t, "edge", peers[1].Role, "non-hub role preserved, not dropped")
		assert.Equal(t, "hub", peers[2].Role, "blank role falls back to queried role")
	})

	t.Run("role fallback mapping", func(t *testing.T) {
		cases := []struct {
			name      string
			queryRole string
			peerRole  string
			wantRole  string
		}{
			{"explicit role preserved", "hub", "hub", "hub"},
			{"explicit non-hub role preserved", "hub", "edge", "edge"},
			{"blank peer role falls back to query role", "hub", "", "hub"},
			{"blank peer role and blank query role stays blank", "", "", ""},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					json.NewEncoder(w).Encode(remotePeerResponse{
						Peers: []remotePeer{{ID: "p1", Addr: "10.0.0.1:4433", Role: tc.peerRole}},
					})
				}))
				defer srv.Close()

				r := &RemoteResolver{
					url:        srv.URL,
					interval:   15 * time.Second,
					httpClient: srv.Client(),
				}

				peers, err := r.ResolvePeers(context.Background(), PeerQuery{Role: tc.queryRole})
				require.NoError(t, err)
				require.Len(t, peers, 1)
				assert.Equal(t, tc.wantRole, peers[0].Role)
			})
		}
	})

	t.Run("returns empty non-nil slice for empty peer list", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(remotePeerResponse{Peers: []remotePeer{}})
		}))
		defer srv.Close()

		r := &RemoteResolver{
			url:        srv.URL,
			interval:   15 * time.Second,
			httpClient: srv.Client(),
		}

		peers, err := r.ResolvePeers(context.Background(), PeerQuery{Role: "hub"})
		require.NoError(t, err)
		require.NotNil(t, peers, "callers iterate the result; it must never be nil")
		assert.Empty(t, peers)
	})

	t.Run("returns error on malformed JSON", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"peers": [ this is not json`))
		}))
		defer srv.Close()

		r := &RemoteResolver{
			url:        srv.URL,
			interval:   15 * time.Second,
			httpClient: srv.Client(),
		}

		_, err := r.ResolvePeers(context.Background(), PeerQuery{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "decode")
	})

	t.Run("sends limit query param", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "3", r.URL.Query().Get("limit"))
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(remotePeerResponse{
				Peers: []remotePeer{
					{ID: "hub-1", Addr: "10.0.0.1:4433", Role: "hub"},
					{ID: "hub-2", Addr: "10.0.0.2:4433", Role: "hub"},
					{ID: "hub-3", Addr: "10.0.0.3:4433", Role: "hub"},
					{ID: "hub-4", Addr: "10.0.0.4:4433", Role: "hub"},
				},
			})
		}))
		defer srv.Close()

		r := &RemoteResolver{
			url:        srv.URL,
			interval:   15 * time.Second,
			httpClient: srv.Client(),
		}

		peers, err := r.ResolvePeers(context.Background(), PeerQuery{Limit: 3})
		require.NoError(t, err)
		require.Len(t, peers, 3)
	})

	t.Run("returns error on non-200", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
		}))
		defer srv.Close()

		r := &RemoteResolver{
			url:        srv.URL,
			interval:   15 * time.Second,
			httpClient: srv.Client(),
		}

		_, err := r.ResolvePeers(context.Background(), PeerQuery{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "status 401")
	})

	t.Run("respects context cancellation", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			<-r.Context().Done()
		}))
		defer srv.Close()

		r := &RemoteResolver{
			url:        srv.URL,
			interval:   15 * time.Second,
			httpClient: srv.Client(),
		}

		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
		defer cancel()

		_, err := r.ResolvePeers(ctx, PeerQuery{})
		require.Error(t, err)
	})
}

func TestRemoteResolver_CloseIdleConnections(t *testing.T) {
	// Should not panic when called on a nil transport or a non-*http.Transport.
	r := &RemoteResolver{
		url:        "http://localhost:9999",
		interval:   15 * time.Second,
		httpClient: &http.Client{},
	}
	r.CloseIdleConnections() // should not panic
}

// stubResolver is a test stub that implements PeerResolver with canned responses.
type stubResolver struct {
	peers []ResolvedPeer
	err   error
	mu    sync.Mutex
}

func (s *stubResolver) ResolvePeers(_ context.Context, query PeerQuery) ([]ResolvedPeer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.err != nil {
		return nil, s.err
	}

	// Filter by role if query specifies one.
	if query.Role != "" {
		var filtered []ResolvedPeer
		for _, p := range s.peers {
			if p.Role == query.Role {
				filtered = append(filtered, p)
			}
		}
		// Apply limit
		if query.Limit > 0 && len(filtered) > query.Limit {
			filtered = filtered[:query.Limit]
		}
		return filtered, nil
	}

	// Apply limit
	result := s.peers
	if query.Limit > 0 && len(result) > query.Limit {
		result = result[:query.Limit]
	}
	return result, nil
}
