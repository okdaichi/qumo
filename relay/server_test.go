package relay

import (
	"context"
	"crypto/tls"
	"testing"
	"time"
)

// TestServerInit tests the initialization logic
func TestServerInit(t *testing.T) {
	tests := []struct {
		name      string
		server    *Server
		expectErr bool
	}{
		{
			name: "init with TLS config",
			server: &Server{
				Addr:      "localhost:4433",
				TLSConfig: &tls.Config{},
			},
			expectErr: false,
		},
		{
			name: "init without TLS config panics",
			server: &Server{
				Addr: "localhost:4433",
			},
			expectErr: true,
		},
		{
			name: "init with custom config",
			server: &Server{
				Addr:      "localhost:4433",
				TLSConfig: &tls.Config{},
				Config: &Config{
					Upstream:       "https://upstream.example.com",
					FrameCapacity:  2000,
					GroupCacheSize: 200,
				},
			},
			expectErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				r := recover()
				if tt.expectErr && r == nil {
					t.Error("Expected panic but got none")
				}
				if !tt.expectErr && r != nil {
					t.Errorf("Unexpected panic: %v", r)
				}
			}()

			tt.server.init()

			if !tt.expectErr {
				if tt.server.Config == nil {
					t.Error("Config should be initialized")
				}
				if tt.server.TrackMux == nil {
					t.Error("TrackMux should be initialized")
				}
				if tt.server.clientTrackMux == nil {
					t.Error("clientTrackMux should be initialized")
				}
			}
		})
	}
}

// TestServerInitIdempotent tests that init can be called multiple times safely
func TestServerInitIdempotent(t *testing.T) {
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

	if config1 != config2 {
		t.Error("Config should be the same after multiple init calls")
	}
	if mux1 != mux2 {
		t.Error("TrackMux should be the same after multiple init calls")
	}
}

// TestServerClose tests the Close method
func TestServerClose(t *testing.T) {
	server := &Server{
		Addr:      "localhost:4433",
		TLSConfig: &tls.Config{},
	}

	// Should not panic even without initialization
	err := server.Close()
	if err != nil {
		t.Errorf("Close() returned error: %v", err)
	}

	// After init, should still work
	server.init()
	err = server.Close()
	if err != nil {
		t.Errorf("Close() after init returned error: %v", err)
	}
}

// TestServerShutdown tests the Shutdown method
func TestServerShutdown(t *testing.T) {
	server := &Server{
		Addr:      "localhost:4433",
		TLSConfig: &tls.Config{},
	}

	ctx := context.Background()

	// Should not panic even without initialization
	err := server.Shutdown(ctx)
	if err != nil {
		t.Errorf("Shutdown() returned error: %v", err)
	}

	// After init, should still work
	server.init()
	err = server.Shutdown(ctx)
	if err != nil {
		t.Errorf("Shutdown() after init returned error: %v", err)
	}
}

// TestServerShutdownWithTimeout tests Shutdown with context timeout
func TestServerShutdownWithTimeout(t *testing.T) {
	server := &Server{
		Addr:      "localhost:4433",
		TLSConfig: &tls.Config{},
	}

	server.init()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := server.Shutdown(ctx)
	if err != nil {
		t.Errorf("Shutdown() with timeout returned error: %v", err)
	}
}

// TestServerConfigDefaults tests that default config is created
func TestServerConfigDefaults(t *testing.T) {
	server := &Server{
		Addr:      "localhost:4433",
		TLSConfig: &tls.Config{},
	}

	server.init()

	if server.Config == nil {
		t.Fatal("Config should be initialized")
	}

	// Should be an empty config, not nil
	if server.Config.Upstream != "" {
		t.Error("Default config should have empty Upstream")
	}
	if server.Config.FrameCapacity != 0 {
		t.Error("Default config should have zero FrameCapacity")
	}
	if server.Config.GroupCacheSize != 0 {
		t.Error("Default config should have zero GroupCacheSize")
	}
}

// TestServerMultipleMuxes tests that server creates separate muxes for client and server
func TestServerMultipleMuxes(t *testing.T) {
	server := &Server{
		Addr:      "localhost:4433",
		TLSConfig: &tls.Config{},
	}

	server.init()

	if server.TrackMux == nil {
		t.Fatal("TrackMux should be initialized")
	}
	if server.clientTrackMux == nil {
		t.Fatal("clientTrackMux should be initialized")
	}

	// They should be different instances
	if server.TrackMux == server.clientTrackMux {
		t.Error("TrackMux and clientTrackMux should be different instances")
	}
}

// TestServerWithNilTLSConfig tests that server panics without TLS config
func TestServerWithNilTLSConfig(t *testing.T) {
	server := &Server{
		Addr: "localhost:4433",
	}

	defer func() {
		if r := recover(); r == nil {
			t.Error("Expected panic with nil TLS config")
		}
	}()

	server.init()
}

// TestServerConfigPersistence tests that provided config is preserved
func TestServerConfigPersistence(t *testing.T) {
	customConfig := &Config{
		Upstream:       "https://test.example.com",
		FrameCapacity:  5000,
		GroupCacheSize: 500,
	}

	server := &Server{
		Addr:      "localhost:4433",
		TLSConfig: &tls.Config{},
		Config:    customConfig,
	}

	server.init()

	if server.Config != customConfig {
		t.Error("Server should preserve custom config")
	}
	if server.Config.Upstream != "https://test.example.com" {
		t.Errorf("Expected Upstream to be preserved, got: %s", server.Config.Upstream)
	}
	if server.Config.FrameCapacity != 5000 {
		t.Errorf("Expected FrameCapacity to be preserved, got: %d", server.Config.FrameCapacity)
	}
	if server.Config.GroupCacheSize != 500 {
		t.Errorf("Expected GroupCacheSize to be preserved, got: %d", server.Config.GroupCacheSize)
	}
}

// TestServerConcurrentInit tests concurrent initialization
func TestServerConcurrentInit(t *testing.T) {
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

	// Should only initialize once
	if server.Config == nil {
		t.Error("Config should be initialized")
	}
}

// TestServerCloseIdempotent tests that Close can be called multiple times
func TestServerCloseIdempotent(t *testing.T) {
	server := &Server{
		Addr:      "localhost:4433",
		TLSConfig: &tls.Config{},
	}

	server.init()

	// Multiple closes should not panic
	if err := server.Close(); err != nil {
		t.Errorf("First Close() error: %v", err)
	}
	if err := server.Close(); err != nil {
		t.Errorf("Second Close() error: %v", err)
	}
	if err := server.Close(); err != nil {
		t.Errorf("Third Close() error: %v", err)
	}
}

// TestServerShutdownIdempotent tests that Shutdown can be called multiple times
func TestServerShutdownIdempotent(t *testing.T) {
	server := &Server{
		Addr:      "localhost:4433",
		TLSConfig: &tls.Config{},
	}

	server.init()
	ctx := context.Background()

	// Multiple shutdowns should not error
	if err := server.Shutdown(ctx); err != nil {
		t.Errorf("First Shutdown() error: %v", err)
	}
	if err := server.Shutdown(ctx); err != nil {
		t.Errorf("Second Shutdown() error: %v", err)
	}
	if err := server.Shutdown(ctx); err != nil {
		t.Errorf("Third Shutdown() error: %v", err)
	}
}
