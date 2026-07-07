package relay

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServiceTag(t *testing.T) {
	tests := []struct {
		name string
		tags []string
		key  string
		want string
	}{
		{
			name: "bare tag matches",
			tags: []string{"hub", "region=us-east"},
			key:  "hub",
			want: "hub",
		},
		{
			name: "key=value tag",
			tags: []string{"hub", "region=us-east"},
			key:  "region",
			want: "us-east",
		},
		{
			name: "case-insensitive key match",
			tags: []string{"Region=us-east"},
			key:  "region",
			want: "us-east",
		},
		{
			name: "case-insensitive tag value",
			tags: []string{"Hub"},
			key:  "hub",
			want: "Hub",
		},
		{
			name: "no match returns empty",
			tags: []string{"edge", "region=eu"},
			key:  "role",
			want: "",
		},
		{
			name: "empty tags returns empty",
			tags: []string{},
			key:  "role",
			want: "",
		},
		{
			name: "key that partially matches another tag",
			tags: []string{"role=hub", "region=us-east"},
			key:  "region",
			want: "us-east",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := serviceTag(tt.tags, tt.key)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestNetJoinHostPort(t *testing.T) {
	tests := []struct {
		name string
		host string
		port int
		want string
	}{
		{
			name: "IPv4 with port",
			host: "10.0.0.1",
			port: 4433,
			want: "10.0.0.1:4433",
		},
		{
			name: "IPv6 with port",
			host: "::1",
			port: 4433,
			want: "[::1]:4433",
		},
		{
			name: "hostname with port",
			host: "relay-1",
			port: 4433,
			want: "relay-1:4433",
		},
		{
			name: "port zero returns host only",
			host: "10.0.0.1",
			port: 0,
			want: "10.0.0.1",
		},
		{
			name: "longer IPv6 with port",
			host: "2001:db8::1",
			port: 4433,
			want: "[2001:db8::1]:4433",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := netJoinHostPort(tt.host, tt.port)
			assert.Equal(t, tt.want, got)
		})
	}
}

// localTestService is a minimal service response for testing.
type localTestService struct {
	ID          string   `json:"ID"`
	ServiceName string   `json:"ServiceName"`
	Address     string   `json:"Address"`
	Port        int      `json:"Port"`
	Tags        []string `json:"Tags"`
	Datacenter  string   `json:"Datacenter"`
	JobID       string   `json:"JobID"`
	AllocID     string   `json:"AllocID"`
}

func TestLocalResolver_ResolvePeers(t *testing.T) {
	t.Run("returns peers from Nomad API", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/v1/service/qumo-relay", r.URL.Path)
			assert.Equal(t, http.MethodGet, r.Method)

			services := []localTestService{
				{
					ID:          "alloc-1",
					ServiceName: "qumo-relay",
					Address:     "10.0.0.1",
					Port:        4433,
					Tags:        []string{"role=hub", "region=us-east"},
					Datacenter:  "dc1",
				},
				{
					ID:          "alloc-2",
					ServiceName: "qumo-relay",
					Address:     "10.0.0.2",
					Port:        4433,
					Tags:        []string{"role=edge", "region=us-east"},
					Datacenter:  "dc1",
				},
			}
			json.NewEncoder(w).Encode(services)
		}))
		defer srv.Close()

		r := &LocalResolver{
			addr:        srv.URL,
			serviceName: "qumo-relay",
			interval:    15 * time.Second,
			httpClient:  srv.Client(),
		}

		peers, err := r.ResolvePeers(context.Background(), PeerQuery{})
		require.NoError(t, err)
		require.Len(t, peers, 2)

		assert.Equal(t, "alloc-1", peers[0].ID)
		assert.Equal(t, "10.0.0.1:4433", peers[0].Address)
		assert.Equal(t, "us-east", peers[0].Region)
		assert.Equal(t, "hub", peers[0].Role)
	})

	t.Run("filters by role", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			services := []localTestService{
				{
					ID:         "hub-1",
					Address:    "10.0.0.1",
					Port:       4433,
					Tags:       []string{"role=hub"},
					Datacenter: "dc1",
				},
				{
					ID:         "edge-1",
					Address:    "10.0.0.2",
					Port:       4433,
					Tags:       []string{"role=edge"},
					Datacenter: "dc1",
				},
			}
			json.NewEncoder(w).Encode(services)
		}))
		defer srv.Close()

		r := &LocalResolver{
			addr:        srv.URL,
			serviceName: "qumo-relay",
			interval:    15 * time.Second,
			httpClient:  srv.Client(),
		}

		// Request only hubs.
		peers, err := r.ResolvePeers(context.Background(), PeerQuery{Role: "hub"})
		require.NoError(t, err)
		require.Len(t, peers, 1)
		assert.Equal(t, "hub-1", peers[0].ID)
	})

	t.Run("limits results", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			services := make([]localTestService, 10)
			for i := range 10 {
				services[i] = localTestService{
					ID:      fmt.Sprintf("node-%d", i),
					Address: "10.0.0.1",
					Port:    4433,
					Tags:    []string{"role=hub"},
				}
			}
			json.NewEncoder(w).Encode(services)
		}))
		defer srv.Close()

		r := &LocalResolver{
			addr:        srv.URL,
			serviceName: "qumo-relay",
			interval:    15 * time.Second,
			httpClient:  srv.Client(),
		}

		peers, err := r.ResolvePeers(context.Background(), PeerQuery{Limit: 3})
		require.NoError(t, err)
		require.Len(t, peers, 3)
	})

	t.Run("uses datacenter as fallback region", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			services := []localTestService{
				{
					ID:         "node-1",
					Address:    "10.0.0.1",
					Port:       4433,
					Tags:       []string{"role=hub"},
					Datacenter: "us-east-1",
				},
			}
			json.NewEncoder(w).Encode(services)
		}))
		defer srv.Close()

		r := &LocalResolver{
			addr:        srv.URL,
			serviceName: "qumo-relay",
			interval:    15 * time.Second,
			httpClient:  srv.Client(),
		}

		peers, err := r.ResolvePeers(context.Background(), PeerQuery{})
		require.NoError(t, err)
		require.Len(t, peers, 1)
		assert.Equal(t, "us-east-1", peers[0].Region)
	})

	t.Run("returns error on non-200", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer srv.Close()

		r := &LocalResolver{
			addr:        srv.URL,
			serviceName: "qumo-relay",
			interval:    15 * time.Second,
			httpClient:  srv.Client(),
		}

		_, err := r.ResolvePeers(context.Background(), PeerQuery{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "status 500")
	})

	t.Run("respects context cancellation", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			<-r.Context().Done()
		}))
		defer srv.Close()

		r := &LocalResolver{
			addr:        srv.URL,
			serviceName: "qumo-relay",
			interval:    15 * time.Second,
			httpClient:  srv.Client(),
		}

		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
		defer cancel()

		_, err := r.ResolvePeers(ctx, PeerQuery{})
		require.Error(t, err)
	})
}

func TestNewLocalResolver_Defaults(t *testing.T) {
	t.Setenv("LOCAL_RESOLVER_ADDR", "")
	t.Setenv("NOMAD_ADDR", "") // Clear fallback for hermetic test
	t.Setenv("LOCAL_RESOLVER_SERVICE_NAME", "")
	t.Setenv("LOCAL_RESOLVER_INTERVAL", "")

	r := NewLocalResolver()
	require.NotNil(t, r)
	assert.Equal(t, "http://localhost:4646", r.addr)
	assert.Equal(t, "qumo-relay", r.serviceName)
	assert.Equal(t, 15*time.Second, r.interval)
	require.NotNil(t, r.httpClient)
	assert.Equal(t, 10*time.Second, r.httpClient.Timeout)
}

func TestNewLocalResolver_CustomEnv(t *testing.T) {
	t.Setenv("LOCAL_RESOLVER_ADDR", "http://nomad.internal:4646")
	t.Setenv("LOCAL_RESOLVER_SERVICE_NAME", "my-relay")
	t.Setenv("LOCAL_RESOLVER_INTERVAL", "30s")

	r := NewLocalResolver()
	require.NotNil(t, r)
	assert.Equal(t, "http://nomad.internal:4646", r.addr)
	assert.Equal(t, "my-relay", r.serviceName)
	assert.Equal(t, 30*time.Second, r.interval)
	require.NotNil(t, r.httpClient)
	assert.Equal(t, 10*time.Second, r.httpClient.Timeout)
}

func TestNewLocalResolver_NOMADAddrFallback(t *testing.T) {
	t.Setenv("LOCAL_RESOLVER_ADDR", "")
	t.Setenv("NOMAD_ADDR", "http://nomad.service.consul:4646")
	t.Setenv("LOCAL_RESOLVER_SERVICE_NAME", "")
	t.Setenv("LOCAL_RESOLVER_INTERVAL", "")

	r := NewLocalResolver()
	require.NotNil(t, r)
	assert.Equal(t, "http://nomad.service.consul:4646", r.addr,
		"should fall back to NOMAD_ADDR when LOCAL_RESOLVER_ADDR is unset")
	require.NotNil(t, r.httpClient)
	assert.Equal(t, 10*time.Second, r.httpClient.Timeout)
}

func TestNewLocalResolver_LocalResolverOverridesNOMADAddr(t *testing.T) {
	t.Setenv("LOCAL_RESOLVER_ADDR", "http://custom:4646")
	t.Setenv("NOMAD_ADDR", "http://nomad:4646")

	r := NewLocalResolver()
	require.NotNil(t, r)
	assert.Equal(t, "http://custom:4646", r.addr,
		"LOCAL_RESOLVER_ADDR should take precedence over NOMAD_ADDR")
	require.NotNil(t, r.httpClient)
	assert.Equal(t, 10*time.Second, r.httpClient.Timeout)
}

func TestLocalResolver_Interval(t *testing.T) {
	r := &LocalResolver{interval: 10 * time.Second}
	assert.Equal(t, 10*time.Second, r.Interval())
}

func TestNewLocalResolver_InvalidInterval(t *testing.T) {
	t.Setenv("LOCAL_RESOLVER_INTERVAL", "invalid_duration")

	r := NewLocalResolver()
	require.NotNil(t, r)
	assert.Equal(t, 15*time.Second, r.interval, "should fall back to 15s default when duration is invalid")
	require.NotNil(t, r.httpClient)
	assert.Equal(t, 10*time.Second, r.httpClient.Timeout)
}
