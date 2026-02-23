package relay

import (
	"context"
	"crypto/tls"
	"net/http"
	"testing"
	"time"

	"github.com/okdaichi/gomoqt/moqt"
	"github.com/okdaichi/gomoqt/quic"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestServer_Init tests the initialization logic
func TestServer_Init(t *testing.T) {
	t.Run("init with TLS config", func(t *testing.T) {
		server := &Server{
			Addr:      "localhost:4433",
			TLSConfig: &tls.Config{},
		}

		defer func() {
			if r := recover(); r != nil {
				t.Errorf("Expected no panic but got: %v", r)
			}
		}()
		server.init()
		require.NotNil(t, server.TrackMux)
	})

	t.Run("init without TLS config panics", func(t *testing.T) {
		server := &Server{
			Addr:      "localhost:4433",
			TLSConfig: nil,
		}

		defer func() {
			if r := recover(); r == nil {
				t.Error("Expected panic but got none")
			}
		}()
		server.init()
	})

	t.Run("init with custom config", func(t *testing.T) {
		server := &Server{
			Addr:      "localhost:4433",
			TLSConfig: &tls.Config{},
			Config: &Config{
				NodeID:         "node-1",
				Region:         "us-west",
				FrameCapacity:  2000,
				GroupCacheSize: 200,
			},
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
	server := &Server{
		Addr:      "localhost:4433",
		TLSConfig: &tls.Config{},
	}

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
	server := &Server{
		Addr:      "localhost:4433",
		TLSConfig: &tls.Config{},
	}

	err := server.Close()
	require.NoError(t, err, "Close should not error without init")
}

// TestServer_Close_AfterInit tests Close after initialization
func TestServer_Close_AfterInit(t *testing.T) {
	server := &Server{
		Addr:      "localhost:4433",
		TLSConfig: &tls.Config{},
	}
	server.init()

	err := server.Close()
	require.NoError(t, err, "Close should not error after init")
}

// TestServer_Shutdown_WithoutInit tests Shutdown without initialization
func TestServer_Shutdown_WithoutInit(t *testing.T) {
	server := &Server{
		Addr:      "localhost:4433",
		TLSConfig: &tls.Config{},
	}
	ctx := context.Background()

	err := server.Shutdown(ctx)
	require.NoError(t, err, "Shutdown should not error without init")
}

// TestServer_Shutdown_AfterInit tests Shutdown after initialization
func TestServer_Shutdown_AfterInit(t *testing.T) {
	server := &Server{
		Addr:      "localhost:4433",
		TLSConfig: &tls.Config{},
	}
	server.init()
	ctx := context.Background()

	err := server.Shutdown(ctx)
	require.NoError(t, err, "Shutdown should not error after init")
}

// TestServer_Shutdown_WithTimeout tests Shutdown with context timeout
func TestServer_Shutdown_WithTimeout(t *testing.T) {
	server := &Server{
		Addr:      "localhost:4433",
		TLSConfig: &tls.Config{},
	}
	server.init()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := server.Shutdown(ctx)
	require.NoError(t, err, "Shutdown with timeout should not error")
}

// TestServer_Muxes_SeparateInstances tests that server creates separate muxes
func TestServer_Muxes_SeparateInstances(t *testing.T) {
	server := &Server{
		Addr:      "localhost:4433",
		TLSConfig: &tls.Config{},
	}
	server.init()

	require.NotNil(t, server.TrackMux, "TrackMux should be initialized")
}

// TestServer_Init_WithNilTLSConfig tests that server panics without TLS config
func TestServer_Init_WithNilTLSConfig(t *testing.T) {
	server := &Server{
		Addr:      "localhost:4433",
		TLSConfig: nil,
	}

	defer func() {
		if r := recover(); r == nil {
			t.Error("Expected panic but got none")
		}
	}()
	server.init()
}

// TestServer_Config_Persistence tests that provided config is preserved
func TestServer_Config_Persistence(t *testing.T) {
	customConfig := &Config{
		NodeID:         "node-1",
		Region:         "us-west",
		FrameCapacity:  5000,
		GroupCacheSize: 500,
	}

	server := &Server{
		Addr:      "localhost:4433",
		TLSConfig: &tls.Config{},
		Config:    customConfig,
	}
	server.init()

	assert.Same(t, customConfig, server.Config, "Server should preserve custom config")
	assert.Equal(t, "node-1", server.Config.NodeID)
	assert.Equal(t, "us-west", server.Config.Region)
	assert.Equal(t, 5000, server.Config.FrameCapacity)
	assert.Equal(t, 500, server.Config.GroupCacheSize)
}

// TestServer_Init_Concurrent tests concurrent initialization
func TestServer_Init_Concurrent(t *testing.T) {
	server := &Server{
		Addr:      "localhost:4433",
		TLSConfig: &tls.Config{},
	}

	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func() {
			server.init()
			done <- true
		}()
	}

	for i := 0; i < 10; i++ {
		<-done
	}

	require.NotNil(t, server.TrackMux, "TrackMux should be initialized after concurrent init calls")
}

// TestServer_Close_Idempotent tests that Close can be called multiple times
func TestServer_Close_Idempotent(t *testing.T) {
	server := &Server{
		Addr:      "localhost:4433",
		TLSConfig: &tls.Config{},
	}
	server.init()

	require.NoError(t, server.Close(), "First Close should not error")
	require.NoError(t, server.Close(), "Second Close should not error")
	require.NoError(t, server.Close(), "Third Close should not error")
}

// TestServer_Shutdown_Idempotent tests that Shutdown can be called multiple times
func TestServer_Shutdown_Idempotent(t *testing.T) {
	server := &Server{
		Addr:      "localhost:4433",
		TLSConfig: &tls.Config{},
	}
	server.init()
	ctx := context.Background()

	require.NoError(t, server.Shutdown(ctx), "First Shutdown should not error")
	require.NoError(t, server.Shutdown(ctx), "Second Shutdown should not error")
	require.NoError(t, server.Shutdown(ctx), "Third Shutdown should not error")
}

// TestServer_Config_CustomValues tests custom config values
func TestServer_Config_CustomValues(t *testing.T) {
	customConfig := &Config{
		NodeID:         "node-1",
		Region:         "us-west",
		GroupCacheSize: 500,
		FrameCapacity:  4096,
	}

	server := &Server{
		Addr:      "localhost:4433",
		TLSConfig: &tls.Config{},
		Config:    customConfig,
	}
	server.init()

	assert.Same(t, customConfig, server.Config, "Custom config should be preserved")
	assert.Equal(t, 500, server.Config.GroupCacheSize, "GroupCacheSize should be preserved")
	assert.Equal(t, 4096, server.Config.FrameCapacity, "FrameCapacity should be preserved")
}

// TestServer_Mux_Initialization tests that muxes are properly initialized
func TestServer_Mux_Initialization(t *testing.T) {
	server := &Server{
		Addr:      "localhost:4433",
		TLSConfig: &tls.Config{},
	}
	server.init()

	require.NotNil(t, server.TrackMux, "TrackMux should be initialized")
}

// TestServer_Mux_CustomTrackMux tests providing custom TrackMux
func TestServer_Mux_CustomTrackMux(t *testing.T) {
	customMux := moqt.NewTrackMux()
	server := &Server{
		Addr:      "localhost:4433",
		TLSConfig: &tls.Config{},
	}
	server.TrackMux = customMux
	server.init()

	assert.Same(t, customMux, server.TrackMux, "Custom TrackMux should be preserved")
}

// TestServer_Close_WithNilComponents tests Close with uninitialized components
func TestServer_Close_WithNilComponents(t *testing.T) {
	server := &Server{
		Addr:      "localhost:4433",
		TLSConfig: &tls.Config{},
	}

	err := server.Close()
	require.NoError(t, err, "Close with nil components should not error")
}

// TestServer_Shutdown_WithNilComponents tests Shutdown with uninitialized components
func TestServer_Shutdown_WithNilComponents(t *testing.T) {
	server := &Server{
		Addr:      "localhost:4433",
		TLSConfig: &tls.Config{},
	}
	ctx := context.Background()

	err := server.Shutdown(ctx)
	require.NoError(t, err, "Shutdown with nil components should not error")
}

// TestServer_Init_WithQUICConfig tests initialization with QUIC config
func TestServer_Init_WithQUICConfig(t *testing.T) {
	quicConfig := &quic.Config{}
	server := &Server{
		Addr:      "localhost:4433",
		TLSConfig: &tls.Config{},
	}
	server.QUICConfig = quicConfig
	server.init()

	assert.Same(t, quicConfig, server.QUICConfig, "QUICConfig should be preserved")
}

// TestServer_Init_MultipleCallsWithDifferentConfigs tests init idempotency
func TestServer_Init_MultipleCallsWithDifferentConfigs(t *testing.T) {
	server := &Server{
		Addr:      "localhost:4433",
		TLSConfig: &tls.Config{},
		Config: &Config{
			NodeID: "node-1",
		},
	}

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

// TestServer_CheckHTTPOrigin tests CheckHTTPOrigin configuration
func TestServer_CheckHTTPOrigin(t *testing.T) {
	called := false
	originFunc := func(r *http.Request) bool {
		called = true
		return true
	}

	server := &Server{
		Addr:      "localhost:4433",
		TLSConfig: &tls.Config{},
	}
	server.CheckHTTPOrigin = originFunc
	server.init()

	require.NotNil(t, server.CheckHTTPOrigin, "CheckHTTPOrigin should be preserved")

	// Test that the function works
	result := server.CheckHTTPOrigin(nil)
	assert.True(t, called, "CheckHTTPOrigin function should be callable")
	assert.True(t, result, "CheckHTTPOrigin should return true")
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
			server := &Server{
				Addr:      tt.addr,
				TLSConfig: &tls.Config{},
			}
			server.init()

			assert.Equal(t, tt.addr, server.Addr, "Address should be preserved")
		})
	}
}
