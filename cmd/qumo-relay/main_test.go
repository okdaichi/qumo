package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/okdaichi/qumo/relay"
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
  upstream_url: "https://upstream.example.com:4433"
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
	if cfg.UpstreamURL != "https://upstream.example.com:4433" {
		t.Errorf("Expected upstream_url 'https://upstream.example.com:4433', got '%s'", cfg.UpstreamURL)
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
relay:
  upstream_url: "https://upstream.example.com:4433"
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
  upstream_url: "https://relay.example.com:8443"
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
	if cfg.UpstreamURL != "https://relay.example.com:8443" {
		t.Errorf("UpstreamURL not properly mapped")
	}

	// Verify RelayConfig is properly populated
	if cfg.RelayConfig.Upstream != "https://relay.example.com:8443" {
		t.Errorf("RelayConfig.Upstream not properly mapped")
	}
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
  upstream_url: "https://upstream.example.com"
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
		Address:     "localhost:4433",
		CertFile:    "cert.pem",
		KeyFile:     "key.pem",
		UpstreamURL: "https://upstream.example.com",
		RelayConfig: relay.Config{
			Upstream:       "https://upstream.example.com",
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
  upstream_url: "https://upstream.example.com:4433"  # Upstream server
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
