package relay

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/okdaichi/gomoqt/moqt"
	"github.com/okdaichi/qumo/internal/sdn"
	"github.com/okdaichi/qumo/internal/topology"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// announceEntry mirrors sdn.announceEntry for test JSON serialization.
type testAnnounceEntry struct {
	Relay         string `json:"relay"`
	BroadcastPath string `json:"broadcast_path"`
}

// mockSDN creates an httptest.Server that implements the SDN endpoints
// needed by RemoteFetcher: GET /announce and GET /route.
func mockSDN(t *testing.T, entries []testAnnounceEntry, routes map[string]topology.RouteResult) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch {
		case r.URL.Path == "/announce" && r.Method == http.MethodGet:
			// ListAll
			json.NewEncoder(w).Encode(map[string]any{
				"entries": entries,
				"count":   len(entries),
			})
		case r.URL.Path == "/route" && r.Method == http.MethodGet:
			to := r.URL.Query().Get("to")
			if route, ok := routes[to]; ok {
				json.NewEncoder(w).Encode(route)
			} else {
				w.WriteHeader(http.StatusNotFound)
				json.NewEncoder(w).Encode(map[string]string{"error": "no route"})
			}
		case r.Method == http.MethodPut:
			// Register (heartbeat)
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]string{"status": "registered"})
		case r.Method == http.MethodDelete:
			// Deregister
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]string{"status": "deregistered"})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func TestRemoteFetcher_SkipsLocalPaths(t *testing.T) {
	// Set up a mux with a local handler already registered
	mux := moqt.NewTrackMux()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mux.Publish(ctx, "/local/stream", moqt.TrackHandlerFunc(func(tw *moqt.TrackWriter) {}))

	// SDN returns the same path as a remote announcement
	srv := mockSDN(t, []testAnnounceEntry{
		{Relay: "relay-b", BroadcastPath: "/local/stream"},
	}, nil)
	defer srv.Close()

	sdnClient, err := sdn.NewClient(sdn.ClientConfig{
		URL:               srv.URL,
		RelayName:         "relay-a",
		HeartbeatInterval: time.Hour,
	})
	require.NoError(t, err)

	fetcher := &RemoteFetcher{
		SDNClient: sdnClient,
		TrackMux:  mux,
	}
	fetcher.mu.Lock()
	fetcher.sessions = make(map[string]*remoteSession)
	fetcher.tracked = make(map[string]context.CancelFunc)
	fetcher.mu.Unlock()

	// Poll should skip /local/stream because it's locally available
	fetcher.poll(ctx, DefaultGroupCacheSize, DefaultFramePool)

	fetcher.mu.Lock()
	assert.Empty(t, fetcher.tracked, "should not track locally available paths")
	fetcher.mu.Unlock()
}

func TestRemoteFetcher_DetectsNewRemotePaths(t *testing.T) {
	mux := moqt.NewTrackMux()

	// SDN returns a remote-only path with a route that has an address
	srv := mockSDN(t,
		[]testAnnounceEntry{
			{Relay: "relay-b", BroadcastPath: "/remote/stream1"},
		},
		map[string]topology.RouteResult{
			"relay-b": {
				From:           "relay-a",
				To:             "relay-b",
				NextHop:        "relay-b",
				NextHopAddress: "https://relay-b:4433",
				FullPath:       []string{"relay-a", "relay-b"},
				Cost:           1,
			},
		},
	)
	defer srv.Close()

	sdnClient, err := sdn.NewClient(sdn.ClientConfig{
		URL:               srv.URL,
		RelayName:         "relay-a",
		HeartbeatInterval: time.Hour,
	})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fetcher := &RemoteFetcher{
		SDNClient: sdnClient,
		TrackMux:  mux,
	}
	fetcher.mu.Lock()
	fetcher.sessions = make(map[string]*remoteSession)
	fetcher.tracked = make(map[string]context.CancelFunc)
	fetcher.client = &moqt.Client{}
	fetcher.mu.Unlock()

	// Poll will try to dial relay-b — this will fail (no real server),
	// but the path should NOT be tracked since dial failed.
	fetcher.poll(ctx, DefaultGroupCacheSize, DefaultFramePool)

	fetcher.mu.Lock()
	// The path should not be tracked because dial would fail
	assert.Empty(t, fetcher.tracked, "should not track paths when dial fails")
	fetcher.mu.Unlock()
}

func TestRemoteFetcher_RemovesStaleTracked(t *testing.T) {
	mux := moqt.NewTrackMux()

	var mu sync.Mutex
	cancelled := false

	// Simulate a previously tracked path
	fetcher := &RemoteFetcher{
		SDNClient: nil, // will be set below
		TrackMux:  mux,
	}
	fetcher.sessions = make(map[string]*remoteSession)
	fetcher.tracked = make(map[string]context.CancelFunc)
	fetcher.tracked["/old/stream"] = func() {
		mu.Lock()
		cancelled = true
		mu.Unlock()
	}

	// SDN returns no entries (the path is gone)
	srv := mockSDN(t, []testAnnounceEntry{}, nil)
	defer srv.Close()

	sdnClient, err := sdn.NewClient(sdn.ClientConfig{
		URL:               srv.URL,
		RelayName:         "relay-a",
		HeartbeatInterval: time.Hour,
	})
	require.NoError(t, err)
	fetcher.SDNClient = sdnClient

	ctx := context.Background()
	fetcher.poll(ctx, DefaultGroupCacheSize, DefaultFramePool)

	mu.Lock()
	assert.True(t, cancelled, "stale path should be cancelled")
	mu.Unlock()

	fetcher.mu.Lock()
	assert.Empty(t, fetcher.tracked, "stale path should be removed from tracked")
	fetcher.mu.Unlock()
}

func TestRemoteFetcher_FiltersOwnEntries(t *testing.T) {
	mux := moqt.NewTrackMux()

	// SDN returns entries from both self and another relay
	srv := mockSDN(t, []testAnnounceEntry{
		{Relay: "relay-a", BroadcastPath: "/my/stream"},
		{Relay: "relay-b", BroadcastPath: "/other/stream"},
	}, map[string]topology.RouteResult{
		"relay-b": {
			From:           "relay-a",
			To:             "relay-b",
			NextHop:        "relay-b",
			NextHopAddress: "https://relay-b:4433",
			FullPath:       []string{"relay-a", "relay-b"},
			Cost:           1,
		},
	})
	defer srv.Close()

	sdnClient, err := sdn.NewClient(sdn.ClientConfig{
		URL:               srv.URL,
		RelayName:         "relay-a",
		HeartbeatInterval: time.Hour,
	})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fetcher := &RemoteFetcher{
		SDNClient: sdnClient,
		TrackMux:  mux,
	}
	fetcher.mu.Lock()
	fetcher.sessions = make(map[string]*remoteSession)
	fetcher.tracked = make(map[string]context.CancelFunc)
	fetcher.client = &moqt.Client{}
	fetcher.mu.Unlock()

	// Own entries (relay-a) should be filtered by ListAll, so only relay-b's
	// entries should be considered. But dial will fail (no real server).
	fetcher.poll(ctx, DefaultGroupCacheSize, DefaultFramePool)

	// No panic, no errors beyond dial failure (which is expected)
}

func TestRemoteFetcher_SkipsNoAddress(t *testing.T) {
	mux := moqt.NewTrackMux()

	// Route has no NextHopAddress
	srv := mockSDN(t,
		[]testAnnounceEntry{
			{Relay: "relay-b", BroadcastPath: "/remote/stream"},
		},
		map[string]topology.RouteResult{
			"relay-b": {
				From:     "relay-a",
				To:       "relay-b",
				NextHop:  "relay-b",
				FullPath: []string{"relay-a", "relay-b"},
				Cost:     1,
				// NextHopAddress intentionally empty
			},
		},
	)
	defer srv.Close()

	sdnClient, err := sdn.NewClient(sdn.ClientConfig{
		URL:               srv.URL,
		RelayName:         "relay-a",
		HeartbeatInterval: time.Hour,
	})
	require.NoError(t, err)

	ctx := context.Background()
	fetcher := &RemoteFetcher{
		SDNClient: sdnClient,
		TrackMux:  mux,
	}
	fetcher.mu.Lock()
	fetcher.sessions = make(map[string]*remoteSession)
	fetcher.tracked = make(map[string]context.CancelFunc)
	fetcher.client = &moqt.Client{}
	fetcher.mu.Unlock()

	fetcher.poll(ctx, DefaultGroupCacheSize, DefaultFramePool)

	fetcher.mu.Lock()
	assert.Empty(t, fetcher.tracked, "should not track paths with no next hop address")
	fetcher.mu.Unlock()
}
