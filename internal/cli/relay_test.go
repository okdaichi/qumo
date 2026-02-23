package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/okdaichi/qumo/internal/relay"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLoadConfig tests the configuration loading
func TestLoadConfig(t *testing.T) {
	// Create a temporary config file
	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "test-config.yaml")

	validConfig := `
server:
  address: "localhost:4433"
  cert_file: "certs/cert.pem"
  key_file: "certs/key.pem"
relay:
  group_cache_size: 150
  frame_capacity: 2000
`

	if err := os.WriteFile(configFile, []byte(validConfig), 0644); err != nil {
		t.Fatalf("Failed to create test config: %v", err)
	}

	cfg, err := loadConfig(configFile)
	if err != nil {
		t.Fatalf("loadConfig() error: %v", err)
	}

	if cfg.Address != "localhost:4433" {
		t.Errorf("Expected address 'localhost:4433', got '%s'", cfg.Address)
	}
	if cfg.CertFile != "certs/cert.pem" {
		t.Errorf("Expected cert_file 'certs/cert.pem', got '%s'", cfg.CertFile)
	}
	if cfg.KeyFile != "certs/key.pem" {
		t.Errorf("Expected key_file 'certs/key.pem', got '%s'", cfg.KeyFile)
	}
	if cfg.RelayConfig.GroupCacheSize != 150 {
		t.Errorf("Expected GroupCacheSize 150, got %d", cfg.RelayConfig.GroupCacheSize)
	}
	if cfg.RelayConfig.FrameCapacity != 2000 {
		t.Errorf("Expected FrameCapacity 2000, got %d", cfg.RelayConfig.FrameCapacity)
	}
}

// TestLoadConfigDefaults tests default values
func TestLoadConfigDefaults(t *testing.T) {
	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "test-config.yaml")

	// Config without relay settings to test defaults
	minimalConfig := `
server:
  address: "localhost:4433"
  cert_file: "certs/cert.pem"
  key_file: "certs/key.pem"
relay: {}
`

	if err := os.WriteFile(configFile, []byte(minimalConfig), 0644); err != nil {
		t.Fatalf("Failed to create test config: %v", err)
	}

	cfg, err := loadConfig(configFile)
	if err != nil {
		t.Fatalf("loadConfig() error: %v", err)
	}

	if cfg.RelayConfig.FrameCapacity != 1500 {
		t.Errorf("Expected default FrameCapacity 1500, got %d", cfg.RelayConfig.FrameCapacity)
	}
	if cfg.RelayConfig.GroupCacheSize != 100 {
		t.Errorf("Expected default GroupCacheSize 100, got %d", cfg.RelayConfig.GroupCacheSize)
	}
}

// TestLoadConfigInvalidFile tests error handling for invalid file
func TestLoadConfigInvalidFile(t *testing.T) {
	_, err := loadConfig("/nonexistent/config.yaml")
	if err == nil {
		t.Error("Expected error for nonexistent file, got nil")
	}
}

// TestLoadConfigInvalidYAML tests error handling for invalid YAML
func TestLoadConfigInvalidYAML(t *testing.T) {
	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "invalid.yaml")

	invalidYAML := `
server:
  address: localhost:4433
  cert_file: "certs/cert.pem
  # Missing closing quote above
`

	if err := os.WriteFile(configFile, []byte(invalidYAML), 0644); err != nil {
		t.Fatalf("Failed to create test config: %v", err)
	}

	_, err := loadConfig(configFile)
	if err == nil {
		t.Error("Expected error for invalid YAML, got nil")
	}
}

// TestLoadConfigEmptyFile tests error handling for empty file
func TestLoadConfigEmptyFile(t *testing.T) {
	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "empty.yaml")

	if err := os.WriteFile(configFile, []byte(""), 0644); err != nil {
		t.Fatalf("Failed to create test config: %v", err)
	}

	_, err := loadConfig(configFile)
	// Empty file should return an error
	if err == nil {
		t.Error("Expected error for empty config file, got nil")
	}
}

// TestLoadConfigPartialData tests handling of partially filled config
func TestLoadConfigPartialData(t *testing.T) {
	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "partial.yaml")

	partialConfig := `
server:
  address: "localhost:4433"
relay:
  frame_capacity: 3000
`

	if err := os.WriteFile(configFile, []byte(partialConfig), 0644); err != nil {
		t.Fatalf("Failed to create test config: %v", err)
	}

	cfg, err := loadConfig(configFile)
	if err != nil {
		t.Fatalf("loadConfig() error: %v", err)
	}

	if cfg.Address != "localhost:4433" {
		t.Errorf("Expected address 'localhost:4433', got '%s'", cfg.Address)
	}
	if cfg.RelayConfig.FrameCapacity != 3000 {
		t.Errorf("Expected FrameCapacity 3000, got %d", cfg.RelayConfig.FrameCapacity)
	}
	// Should use default for unspecified GroupCacheSize
	if cfg.RelayConfig.GroupCacheSize != 100 {
		t.Errorf("Expected default GroupCacheSize 100, got %d", cfg.RelayConfig.GroupCacheSize)
	}
}

// TestLoadConfigStructMapping tests proper mapping to config struct
func TestLoadConfigStructMapping(t *testing.T) {
	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "mapping.yaml")

	configContent := `
server:
  address: "0.0.0.0:8443"
  cert_file: "/path/to/cert.pem"
  key_file: "/path/to/key.pem"
relay:
  group_cache_size: 500
  frame_capacity: 5000
`

	if err := os.WriteFile(configFile, []byte(configContent), 0644); err != nil {
		t.Fatalf("Failed to create test config: %v", err)
	}

	cfg, err := loadConfig(configFile)
	if err != nil {
		t.Fatalf("loadConfig() error: %v", err)
	}

	// Verify all fields are properly mapped
	if cfg.Address != "0.0.0.0:8443" {
		t.Errorf("Address not properly mapped")
	}
	if cfg.CertFile != "/path/to/cert.pem" {
		t.Errorf("CertFile not properly mapped")
	}
	if cfg.KeyFile != "/path/to/key.pem" {
		t.Errorf("KeyFile not properly mapped")
	}

	// Verify RelayConfig is properly populated
	if cfg.RelayConfig.GroupCacheSize != 500 {
		t.Errorf("RelayConfig.GroupCacheSize not properly mapped")
	}
	if cfg.RelayConfig.FrameCapacity != 5000 {
		t.Errorf("RelayConfig.FrameCapacity not properly mapped")
	}
}

// TestLoadConfigZeroValues tests handling of explicit zero values
func TestLoadConfigZeroValues(t *testing.T) {
	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "zero.yaml")

	zeroConfig := `
server:
  address: "localhost:4433"
relay:
  group_cache_size: 0
  frame_capacity: 0
`

	if err := os.WriteFile(configFile, []byte(zeroConfig), 0644); err != nil {
		t.Fatalf("Failed to create test config: %v", err)
	}

	cfg, err := loadConfig(configFile)
	if err != nil {
		t.Fatalf("loadConfig() error: %v", err)
	}

	// Zero values should be replaced with defaults
	if cfg.RelayConfig.FrameCapacity != 1500 {
		t.Errorf("Expected default FrameCapacity 1500 for zero value, got %d", cfg.RelayConfig.FrameCapacity)
	}
	if cfg.RelayConfig.GroupCacheSize != 100 {
		t.Errorf("Expected default GroupCacheSize 100 for zero value, got %d", cfg.RelayConfig.GroupCacheSize)
	}
}

// TestSetupTLS tests TLS configuration setup (basic validation only)
func TestSetupTLSInvalidFiles(t *testing.T) {
	_, err := setupTLS("/nonexistent/cert.pem", "/nonexistent/key.pem")
	if err == nil {
		t.Error("Expected error for nonexistent certificate files, got nil")
	}
}

// TestSetupTLSEmptyPaths tests error handling for empty paths
func TestSetupTLSEmptyPaths(t *testing.T) {
	_, err := setupTLS("", "")
	if err == nil {
		t.Error("Expected error for empty certificate paths, got nil")
	}
}

// TestConfigType tests the config type structure
func TestConfigType(t *testing.T) {
	cfg := &config{
		Address:  "localhost:4433",
		CertFile: "cert.pem",
		KeyFile:  "key.pem",
		RelayConfig: relay.Config{
			FrameCapacity:  1500,
			GroupCacheSize: 100,
		},
	}

	if cfg.Address == "" {
		t.Error("Address should not be empty")
	}
	if cfg.RelayConfig.FrameCapacity != 1500 {
		t.Error("FrameCapacity not properly set")
	}
}

// TestLoadConfigWithComments tests that YAML comments are properly ignored
func TestLoadConfigWithComments(t *testing.T) {
	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "commented.yaml")

	commentedConfig := `
# Server configuration
server:
  address: "localhost:4433"  # Listen address
  cert_file: "certs/cert.pem"  # TLS certificate
  key_file: "certs/key.pem"    # TLS key

# Relay configuration
relay:
  group_cache_size: 150  # Cache size
  frame_capacity: 2000   # Frame buffer size
`

	if err := os.WriteFile(configFile, []byte(commentedConfig), 0644); err != nil {
		t.Fatalf("Failed to create test config: %v", err)
	}

	cfg, err := loadConfig(configFile)
	if err != nil {
		t.Fatalf("loadConfig() error: %v", err)
	}

	if cfg.Address != "localhost:4433" {
		t.Errorf("Comments affected parsing")
	}
	if cfg.RelayConfig.GroupCacheSize != 150 {
		t.Errorf("Comments affected numeric parsing")
	}
}

func TestHealthHandler_ProbeLive_GETAndHEAD(t *testing.T) {
	h := &healthHandler{
		statusFunc: func() relay.Status {
			return relay.Status{Status: "healthy", ActiveConnections: 1, Timestamp: time.Now(), Uptime: "1s"}
		},
	}

	// GET
	req := httptest.NewRequest(http.MethodGet, "/health?probe=live", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)

	var resp map[string]string
	err := json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Equal(t, "alive", resp["status"])

	// HEAD should return no body
	req = httptest.NewRequest(http.MethodHead, "/health?probe=live", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, 0, rec.Body.Len())
}

func TestHealthHandler_ProbeReady_Cases(t *testing.T) {
	tests := map[string]struct {
		status     relay.Status
		wantCode   int
		wantReady  bool
		wantReason string
	}{
		"ready with healthy status": {
			status:    relay.Status{ActiveConnections: 0, Status: "healthy"},
			wantCode:  http.StatusOK,
			wantReady: true,
		},
		"invalid connection state": {
			status:     relay.Status{ActiveConnections: -1, Status: "healthy"},
			wantCode:   http.StatusServiceUnavailable,
			wantReady:  false,
			wantReason: "invalid_connection_state",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			h := &healthHandler{statusFunc: func() relay.Status { return tt.status }}
			req := httptest.NewRequest(http.MethodGet, "/health?probe=ready", nil)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			assert.Equal(t, tt.wantCode, rec.Code)

			var resp map[string]any
			err := json.NewDecoder(rec.Body).Decode(&resp)
			require.NoError(t, err)
			assert.Equal(t, tt.wantReady, resp["ready"])
			if !tt.wantReady && tt.wantReason != "" {
				assert.Equal(t, tt.wantReason, resp["reason"])
			}
		})
	}
}

func TestHealthHandler_DefaultStatusResponses(t *testing.T) {
	tests := map[string]struct {
		status   relay.Status
		wantCode int
	}{
		"unhealthy status code": {status: relay.Status{Status: "unhealthy", ActiveConnections: 0}, wantCode: http.StatusServiceUnavailable},
		"healthy status code":   {status: relay.Status{Status: "healthy", ActiveConnections: 0}, wantCode: http.StatusOK},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			h := &healthHandler{statusFunc: func() relay.Status { return tt.status }}
			req := httptest.NewRequest(http.MethodGet, "/health", nil)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			assert.Equal(t, tt.wantCode, rec.Code)

			var resp map[string]any
			err := json.NewDecoder(rec.Body).Decode(&resp)
			require.NoError(t, err)
			assert.Equal(t, tt.status.Status, resp["status"])
			assert.Contains(t, resp, "live")
			assert.Contains(t, resp, "ready")
		})
	}
}

func TestHealthHandler_InvalidMethod(t *testing.T) {
	h := &healthHandler{statusFunc: func() relay.Status {
		return relay.Status{Status: "healthy", ActiveConnections: 0}
	}}
	req := httptest.NewRequest(http.MethodPost, "/health", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}

// --- serveComponents tests ---

type mockServer struct {
	listenCalled   chan struct{}
	shutdownCalled chan struct{}
	listenErr      error
}

func newMockServer(listenErr error) *mockServer {
	return &mockServer{listenCalled: make(chan struct{}), shutdownCalled: make(chan struct{}), listenErr: listenErr}
}

func (m *mockServer) ListenAndServe() error {
	close(m.listenCalled)
	if m.listenErr != nil {
		return m.listenErr
	}
	// Block until Shutdown signals us to exit
	<-m.shutdownCalled
	return nil
}

func (m *mockServer) Shutdown(_ context.Context) error {
	// signal the listen goroutine to exit
	select {
	case <-m.shutdownCalled:
		// already closed
	default:
		close(m.shutdownCalled)
	}
	return nil
}

func TestServeComponents_ShutdownOnContextCancel(t *testing.T) {
	relayMock := newMockServer(nil)
	httpMock := newMockServer(nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Run serveComponents in background
	go serveComponents(ctx, relayMock, httpMock, 1*time.Second)

	// wait for both ListenAndServe to have been invoked
	<-relayMock.listenCalled
	<-httpMock.listenCalled

	// cancel context to trigger shutdown
	cancel()

	// verify Shutdown was called on both mocks
	select {
	case <-relayMock.shutdownCalled:
		// ok
	case <-time.After(500 * time.Millisecond):
		t.Fatal("relay shutdown was not called")
	}

	select {
	case <-httpMock.shutdownCalled:
		// ok
	case <-time.After(500 * time.Millisecond):
		t.Fatal("http shutdown was not called")
	}
}

func TestServeComponents_IgnoresImmediateListenError(t *testing.T) {
	// Relay.ListenAndServe returns an error immediately. serveComponents should
	// still wait for ctx cancellation and call Shutdown on the other server.
	relayMock := newMockServer(fmt.Errorf("listen failed"))
	httpMock := newMockServer(nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go serveComponents(ctx, relayMock, httpMock, 1*time.Second)

	// relayMock.listenCalled will be closed quickly even though it returned
	<-relayMock.listenCalled
	<-httpMock.listenCalled

	cancel()

	select {
	case <-httpMock.shutdownCalled:
		// ok
	case <-time.After(500 * time.Millisecond):
		t.Fatal("http shutdown was not called after context cancel")
	}
}
