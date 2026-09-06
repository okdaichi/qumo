package relay

import (
	"context"
	"crypto/tls"
	"sync"
	"testing"
	"time"

	"github.com/qumo-dev/gomoqt/moqt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestServer returns a Server with minimal MoQServer and MoQDialer set.
func newTestServer(addr string) *Server {
	return &Server{
		MOQServer: &moqt.Server{
			Addr:      addr,
			TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12},
		},
		MOQDialer: &moqt.Dialer{},
	}
}

// TestServer_Init tests the initialization logic
func TestServer_Init(t *testing.T) {
	t.Run("init with MoQServer", func(t *testing.T) {
		server := newTestServer("localhost:4433")

		defer func() {
			if r := recover(); r != nil {
				t.Errorf("Expected no panic but got: %v", r)
			}
		}()
		server.init()
		require.NotNil(t, server.TrackMux)
	})

	t.Run("ListenAndServe with nil MoQServer panics", func(t *testing.T) {
		server := &Server{}

		defer func() {
			if r := recover(); r == nil {
				t.Error("Expected panic but got none")
			}
		}()
		_ = server.ListenAndServe()
	})

	t.Run("ListenAndServe with nil MoQDialer panics", func(t *testing.T) {
		server := &Server{
			MOQServer: &moqt.Server{
				Addr:      "localhost:4433",
				TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12},
			},
		}

		defer func() {
			if r := recover(); r == nil {
				t.Error("Expected panic but got none")
			}
		}()
		_ = server.ListenAndServe()
	})

	t.Run("init with custom config", func(t *testing.T) {
		server := newTestServer("localhost:4433")
		server.Config = &Config{
			NodeID:         "node-1",
			FrameCapacity:  2000,
			GroupCacheSize: 200,
		}

		defer func() {
			if r := recover(); r != nil {
				t.Errorf("Expected no panic but got: %v", r)
			}
		}()
		server.init()
		require.NotNil(t, server.TrackMux)
	})
}

// TestServer_Init_Idempotent tests that init can be called multiple times safely
func TestServer_Init_Idempotent(t *testing.T) {
	server := newTestServer("localhost:4433")

	server.init()
	config1 := server.Config
	mux1 := server.TrackMux

	server.init()
	config2 := server.Config
	mux2 := server.TrackMux

	assert.Same(t, config1, config2, "Config should be the same after multiple init calls")
	assert.Same(t, mux1, mux2, "TrackMux should be the same after multiple init calls")
}

// TestServer_Close_WithoutInit tests Close without initialization
func TestServer_Close_WithoutInit(t *testing.T) {
	server := &Server{}

	err := server.Close()
	require.NoError(t, err, "Close should not error without init")
}

// TestServer_Close_AfterInit tests Close after initialization
func TestServer_Close_AfterInit(t *testing.T) {
	server := newTestServer("localhost:4433")
	server.init()

	err := server.Close()
	require.NoError(t, err, "Close should not error after init")
}

// TestServer_Shutdown_WithoutInit tests Shutdown without initialization
func TestServer_Shutdown_WithoutInit(t *testing.T) {
	server := &Server{}
	ctx := context.Background()

	err := server.Shutdown(ctx)
	require.NoError(t, err, "Shutdown should not error without init")
}

// TestServer_Shutdown_WithTimeout tests Shutdown with context timeout
func TestServer_Shutdown_WithTimeout(t *testing.T) {
	server := &Server{}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := server.Shutdown(ctx)
	require.NoError(t, err, "Shutdown with timeout should not error")
}

// TestServer_Muxes_SeparateInstances tests that server creates separate muxes
func TestServer_Muxes_SeparateInstances(t *testing.T) {
	server := newTestServer("localhost:4433")
	server.init()

	require.NotNil(t, server.TrackMux, "TrackMux should be initialized")
}

// TestServer_Config_Persistence tests that provided config is preserved
func TestServer_Config_Persistence(t *testing.T) {
	customConfig := &Config{
		NodeID:         "node-1",
		FrameCapacity:  5000,
		GroupCacheSize: 500,
	}

	server := newTestServer("localhost:4433")
	server.Config = customConfig
	server.init()

	assert.Same(t, customConfig, server.Config, "Server should preserve custom config")
	assert.Equal(t, "node-1", server.Config.NodeID)
	assert.Equal(t, 5000, server.Config.FrameCapacity)
	assert.Equal(t, 500, server.Config.GroupCacheSize)
}

// TestServer_Init_Concurrent tests concurrent initialization
func TestServer_Init_Concurrent(t *testing.T) {
	server := newTestServer("localhost:4433")

	done := make(chan bool)
	for range 10 {
		go func() {
			server.init()
			done <- true
		}()
	}

	for range 10 {
		<-done
	}

	require.NotNil(t, server.TrackMux, "TrackMux should be initialized after concurrent init calls")
}

// TestServer_Close_Idempotent tests that Close can be called multiple times
func TestServer_Close_Idempotent(t *testing.T) {
	server := newTestServer("localhost:4433")
	server.init()

	require.NoError(t, server.Close(), "First Close should not error")
	require.NoError(t, server.Close(), "Second Close should not error")
	require.NoError(t, server.Close(), "Third Close should not error")
}

// TestServer_Shutdown_Idempotent tests that Shutdown can be called multiple times
func TestServer_Shutdown_Idempotent(t *testing.T) {
	server := &Server{}
	ctx := context.Background()

	require.NoError(t, server.Shutdown(ctx), "First Shutdown should not error")
	require.NoError(t, server.Shutdown(ctx), "Second Shutdown should not error")
	require.NoError(t, server.Shutdown(ctx), "Third Shutdown should not error")
}

// TestServer_Config_CustomValues tests custom config values
func TestServer_Config_CustomValues(t *testing.T) {
	customConfig := &Config{
		NodeID:         "node-1",
		GroupCacheSize: 500,
		FrameCapacity:  4096,
	}

	server := newTestServer("localhost:4433")
	server.Config = customConfig
	server.init()

	assert.Same(t, customConfig, server.Config, "Custom config should be preserved")
	assert.Equal(t, 500, server.Config.GroupCacheSize, "GroupCacheSize should be preserved")
	assert.Equal(t, 4096, server.Config.FrameCapacity, "FrameCapacity should be preserved")
}

// TestServer_Mux_Initialization tests that muxes are properly initialized
func TestServer_Mux_Initialization(t *testing.T) {
	server := newTestServer("localhost:4433")
	server.init()

	require.NotNil(t, server.TrackMux, "TrackMux should be initialized")
}

// TestServer_Mux_CustomTrackMux tests providing custom TrackMux
func TestServer_Mux_CustomTrackMux(t *testing.T) {
	customMux := moqt.NewTrackMux(0)
	server := newTestServer("localhost:4433")
	server.TrackMux = customMux
	server.init()

	assert.Same(t, customMux, server.TrackMux, "Custom TrackMux should be preserved")
}

// TestServer_MarkConnected tests server-level peer deduplication.
func TestServer_MarkConnected(t *testing.T) {
	server := newTestServer("localhost:4433")
	server.init()

	addr := "moqt://relay-a:4433"

	assert.True(t, server.markConnected(addr), "first call should return true")
	assert.False(t, server.markConnected(addr), "second call for same addr should return false")
	assert.True(t, server.markConnected("moqt://relay-b:4433"), "different addr should return true")
}

// TestServer_MarkUnconnected tests that markUnconnected removes the address so it can be re-connected.
func TestServer_MarkUnconnected(t *testing.T) {
	server := newTestServer("localhost:4433")
	server.init()

	addr := "moqt://relay-a:4433"

	require.True(t, server.markConnected(addr))
	assert.False(t, server.markConnected(addr), "should be blocked while connected")

	server.markUnconnected(addr)
	assert.True(t, server.markConnected(addr), "should be connectable again after markUnconnected")
}

func TestFilterPeersByAddr(t *testing.T) {
	peers := []ResolvedPeer{
		{Address: "moqt://relay-a:4433"},
		{Address: "moqt://relay-b:4433"},
		{Address: "moqt://relay-c:4433"},
	}

	exclude := map[string]struct{}{
		"moqt://relay-b:4433": {},
		"moqt://relay-d:4433": {},
	}

	filtered := filterPeersByAddr(peers, exclude)

	assert.Len(t, filtered, 2)
	assert.Equal(t, "moqt://relay-a:4433", filtered[0].Address)
	assert.Equal(t, "moqt://relay-c:4433", filtered[1].Address)
}

// TestServer_MarkConnected_Concurrent tests that markConnected is safe for concurrent use
// and that only one caller wins for a given address.
func TestServer_MarkConnected_Concurrent(t *testing.T) {
	server := newTestServer("localhost:4433")
	server.init()

	addr := "moqt://relay-concurrent:4433"
	wins := make(chan bool, 20)

	for range 20 {
		go func() {
			wins <- server.markConnected(addr)
		}()
	}

	var trueCount int
	for range 20 {
		if <-wins {
			trueCount++
		}
	}
	assert.Equal(t, 1, trueCount, "exactly one goroutine should win the race")
}

// TestServer_Close_WithNilComponents tests Close with uninitialized components
func TestServer_Close_WithNilComponents(t *testing.T) {
	server := &Server{}

	err := server.Close()
	require.NoError(t, err, "Close with nil components should not error")
}

// TestServer_Shutdown_WithNilComponents tests Shutdown with uninitialized components
func TestServer_Shutdown_WithNilComponents(t *testing.T) {
	server := &Server{}
	ctx := context.Background()

	err := server.Shutdown(ctx)
	require.NoError(t, err, "Shutdown with nil components should not error")
}

// TestServer_Init_WithQUICConfig tests initialization with QUIC config on MoQServer/MoQDialer
func TestServer_Init_WithQUICConfig(t *testing.T) {
	server := newTestServer("localhost:4433")
	server.init()

	require.NotNil(t, server.TrackMux)
}

// TestServer_Init_MultipleCallsWithDifferentConfigs tests init idempotency
func TestServer_Init_MultipleCallsWithDifferentConfigs(t *testing.T) {
	server := newTestServer("localhost:4433")
	server.Config = &Config{NodeID: "node-1"}

	server.init()
	firstMux := server.TrackMux

	// Try to change config and init again
	// Because of sync.Once, init() does nothing on second call
	server.Config = &Config{
		NodeID: "node-2",
	}
	server.init()

	// The config pointer changes because we assigned a new one,
	// but init() didn't run again (sync.Once)
	assert.Same(t, firstMux, server.TrackMux, "TrackMux should not change on second init call")

	// Config field is not protected by init(), so it changes
	assert.Equal(t, "node-2", server.Config.NodeID, "Config assignment should work even after init")
}

// callTrackingResolver wraps a PeerResolver and records calls for assertion.
type callTrackingResolver struct {
	PeerResolver
	calls []PeerQuery
	mu    sync.Mutex
}

func (c *callTrackingResolver) ResolvePeers(ctx context.Context, q PeerQuery) ([]ResolvedPeer, error) {
	c.mu.Lock()
	c.calls = append(c.calls, q)
	c.mu.Unlock()
	return c.PeerResolver.ResolvePeers(ctx, q)
}

func (c *callTrackingResolver) CallCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.calls)
}

func (c *callTrackingResolver) LastCall() (PeerQuery, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.calls) == 0 {
		return PeerQuery{}, false
	}
	return c.calls[len(c.calls)-1], true
}

// TestDiscoverPeers_RoleBasedRouting verifies that discoverPeers
// calls the correct resolver based on the node's configured role.
func TestDiscoverPeers_RoleBasedRouting(t *testing.T) {
	hubPeers := []ResolvedPeer{
		{ID: "hub-1", Address: "moqt://hub-1:4433", Region: "us-east", Role: "hub"},
		{ID: "hub-2", Address: "moqt://hub-2:4433", Region: "us-east", Role: "hub"},
	}

	tests := []struct {
		name           string
		role           string
		useLocal       bool // whether to call discoverPeers with localResolver
		useRemote      bool // whether to call discoverPeers with remoteResolver
		wantLocalCall  bool // expect localResolver.ResolvePeers to be called
		wantRemoteCall bool // expect remoteResolver.ResolvePeers to be called
		wantQueryRole  string
		wantQueryLimit int
	}{
		{
			name:           "edge uses local resolver for hubs",
			role:           "edge",
			useLocal:       true,
			useRemote:      false,
			wantLocalCall:  true,
			wantRemoteCall: false,
			wantQueryRole:  "hub",
			wantQueryLimit: 0,
		},
		{
			name:           "edge ignores remote resolver",
			role:           "edge",
			useLocal:       false,
			useRemote:      true,
			wantLocalCall:  false,
			wantRemoteCall: false,
		},
		{
			name:           "hub uses remote resolver for hubs",
			role:           "hub",
			useLocal:       false,
			useRemote:      true,
			wantLocalCall:  false,
			wantRemoteCall: true,
			wantQueryRole:  "hub",
			wantQueryLimit: 0,
		},
		{
			name:           "hub ignores local resolver",
			role:           "hub",
			useLocal:       true,
			useRemote:      false,
			wantLocalCall:  false,
			wantRemoteCall: false,
		},
		{
			name:           "default role uses local resolver",
			role:           "",
			useLocal:       true,
			useRemote:      false,
			wantLocalCall:  true,
			wantRemoteCall: false,
			wantQueryRole:  "",
			wantQueryLimit: 5,
		},
		{
			name:           "default role ignores remote resolver",
			role:           "",
			useLocal:       false,
			useRemote:      true,
			wantLocalCall:  false,
			wantRemoteCall: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stubLocal := &stubResolver{peers: hubPeers}
			stubRemote := &stubResolver{peers: hubPeers}

			trackerLocal := &callTrackingResolver{PeerResolver: stubLocal}
			trackerRemote := &callTrackingResolver{PeerResolver: stubRemote}

			server := newTestServer("localhost:4433")
			server.Config = &Config{Role: tt.role}
			server.localResolver = trackerLocal
			server.remoteResolver = trackerRemote
			server.init() // initializes s.connected map

			var wg sync.WaitGroup
			ctx, cancel := context.WithCancel(context.Background())

			// discoverPeers blocks in its tick loop; run in a goroutine
			// and cancel after tick() executes synchronously.
			if tt.useLocal {
				go server.discoverPeers(ctx, &wg, 1*time.Hour, trackerLocal)
			}
			if tt.useRemote {
				go server.discoverPeers(ctx, &wg, 1*time.Hour, trackerRemote)
			}

			// Wait for tick() to run (it's synchronous and fast), then cancel.
			time.Sleep(20 * time.Millisecond)
			cancel()
			wg.Wait()

			// Check that the correct resolvers were called.
			if tt.wantLocalCall {
				assert.GreaterOrEqual(t, trackerLocal.CallCount(), 1, "expected local resolver to be called")
				lastCall, ok := trackerLocal.LastCall()
				if assert.True(t, ok, "local resolver should have been called") {
					assert.Equal(t, tt.wantQueryRole, lastCall.Role)
					assert.Equal(t, tt.wantQueryLimit, lastCall.Limit)
				}
			} else {
				assert.Equal(t, 0, trackerLocal.CallCount(), "expected local resolver NOT to be called")
			}

			if tt.wantRemoteCall {
				assert.GreaterOrEqual(t, trackerRemote.CallCount(), 1, "expected remote resolver to be called")
				lastCall, ok := trackerRemote.LastCall()
				if assert.True(t, ok, "remote resolver should have been called") {
					assert.Equal(t, tt.wantQueryRole, lastCall.Role)
					assert.Equal(t, tt.wantQueryLimit, lastCall.Limit)
				}
			} else {
				assert.Equal(t, 0, trackerRemote.CallCount(), "expected remote resolver NOT to be called")
			}
		})
	}
}

// TestDiscoverPeers_HubDialsAllRemoteHubs verifies that a hub node dials
// every resolved remote hub, not just the first. Addresses are recorded in
// server.connected synchronously by connect() before the dial goroutine
// starts, so the snapshot is taken before cancel() lets maintainPeer's
// cleanup remove them.
func TestDiscoverPeers_HubDialsAllRemoteHubs(t *testing.T) {
	peers := []ResolvedPeer{
		{ID: "hub-1", Address: "moqt://hub-1:4433", Region: "us-east", Role: "hub"},
		{ID: "hub-2", Address: "moqt://hub-2:4433", Region: "eu-west", Role: "hub"},
		{ID: "hub-3", Address: "moqt://hub-3:4433", Region: "ap-south", Role: "hub"},
	}

	server := newTestServer("localhost:4433")
	server.Config = &Config{Role: "hub"}
	server.remoteResolver = &stubResolver{peers: peers}
	server.init()

	var wg sync.WaitGroup
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go server.discoverPeers(ctx, &wg, 1*time.Hour, server.remoteResolver)

	// Not synctest: maintainPeer's dial goroutines run real address
	// resolution inside gomoqt, which a bubble cannot advance. Entries are
	// only ever added until cancel(), so a bounded wait on the count is safe.
	require.Eventually(t, func() bool {
		server.connectedMu.Lock()
		defer server.connectedMu.Unlock()
		return len(server.connected) == len(peers)
	}, 2*time.Second, 5*time.Millisecond, "expected all remote hubs to be marked dialing")

	server.connectedMu.Lock()
	got := make([]string, 0, len(server.connected))
	for addr := range server.connected {
		got = append(got, addr)
	}
	server.connectedMu.Unlock()

	cancel()
	wg.Wait()

	assert.ElementsMatch(t,
		[]string{"moqt://hub-1:4433", "moqt://hub-2:4433", "moqt://hub-3:4433"},
		got, "hub should dial all resolved remote hubs, not just the first")
}

// TestServer_Address_Formats tests various address formats
func TestServer_Address_Formats(t *testing.T) {
	tests := []struct {
		name string
		addr string
	}{
		{"port only", ":4433"},
		{"localhost", "localhost:4433"},
		{"127.0.0.1", "127.0.0.1:4433"},
		{"0.0.0.0", "0.0.0.0:4433"},
		{"IPv6", "[::1]:4433"},
		{"IPv6 all", "[::]:4433"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := newTestServer(tt.addr)
			server.init()

			assert.Equal(t, tt.addr, server.MOQServer.Addr, "Address should be preserved")
		})
	}
}
