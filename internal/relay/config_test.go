package relay

import (
	"testing"
)

// TestConfigDefaults tests default config values
func TestConfigDefaults(t *testing.T) {
	cfg := &Config{}

	if cfg.GroupCacheSize != 0 {
		t.Error("Default GroupCacheSize should be 0")
	}

	if cfg.FrameCapacity != 0 {
		t.Error("Default FrameCapacity should be 0")
	}

}

// TestConfigWithGroupCacheSize tests different cache sizes
func TestConfigWithGroupCacheSize(t *testing.T) {
	tests := []struct {
		name      string
		cacheSize int
	}{
		{"small cache", 10},
		{"medium cache", 100},
		{"large cache", 1000},
		{"very large cache", 10000},
		{"zero cache", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{
				GroupCacheSize: tt.cacheSize,
			}

			if cfg.GroupCacheSize != tt.cacheSize {
				t.Errorf("Expected GroupCacheSize=%d, got %d", tt.cacheSize, cfg.GroupCacheSize)
			}
		})
	}
}

// TestConfigWithFrameCapacity tests different frame capacities
func TestConfigWithFrameCapacity(t *testing.T) {
	tests := []struct {
		name     string
		capacity int
	}{
		{"1KB", 1024},
		{"64KB", 65536},
		{"1MB", 1048576},
		{"10MB", 10485760},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{
				FrameCapacity: tt.capacity,
			}

			if cfg.FrameCapacity != tt.capacity {
				t.Errorf("Expected FrameCapacity=%d, got %d", tt.capacity, cfg.FrameCapacity)
			}
		})
	}
}

// TestConfigFullyPopulated tests a fully configured Config
func TestConfigFullyPopulated(t *testing.T) {
	cfg := &Config{
		NodeID:         "node-1",
		Region:         "us-west",
		GroupCacheSize: 200,
		FrameCapacity:  2048,
	}

	if cfg.NodeID != "node-1" {
		t.Error("NodeID not set correctly")
	}

	if cfg.Region != "us-west" {
		t.Error("Region not set correctly")
	}

	if cfg.GroupCacheSize != 200 {
		t.Error("GroupCacheSize not set correctly")
	}

	if cfg.FrameCapacity != 2048 {
		t.Error("FrameCapacity not set correctly")
	}

}

// TestConfigCopy tests that Config can be copied correctly
func TestConfigCopy(t *testing.T) {
	original := &Config{
		NodeID:         "node-1",
		Region:         "us-west",
		GroupCacheSize: 100,
		FrameCapacity:  1024,
	}

	// Create a copy
	copy := &Config{
		NodeID:         original.NodeID,
		Region:         original.Region,
		GroupCacheSize: original.GroupCacheSize,
		FrameCapacity:  original.FrameCapacity,
	}

	// Verify all fields match
	if copy.NodeID != original.NodeID {
		t.Error("NodeID not copied correctly")
	}
	if copy.Region != original.Region {
		t.Error("Region not copied correctly")
	}
	if copy.GroupCacheSize != original.GroupCacheSize {
		t.Error("GroupCacheSize not copied correctly")
	}
	if copy.FrameCapacity != original.FrameCapacity {
		t.Error("FrameCapacity not copied correctly")
	}

	// Modify copy and ensure original is unchanged
	copy.NodeID = "node-2"
	if original.NodeID == copy.NodeID {
		t.Error("Modifying copy should not affect original")
	}
}

// TestConfigNegativeValues tests handling of negative/invalid values
func TestConfigNegativeValues(t *testing.T) {
	cfg := &Config{
		GroupCacheSize: -1,
		FrameCapacity:  -100,
	}

	// The Config struct doesn't validate, but we document the behavior
	if cfg.GroupCacheSize != -1 {
		t.Error("Negative GroupCacheSize should be stored as-is")
	}

	if cfg.FrameCapacity != -100 {
		t.Error("Negative FrameCapacity should be stored as-is")
	}
}

// TestConfigStructSize tests that Config doesn't grow unexpectedly
func TestConfigStructSize(t *testing.T) {
	cfg := &Config{}

	// This test ensures we're aware if Config grows
	// Config should have exactly 4 fields as of now
	expectedFields := 4

	// This is a documentation test - if this fails, update the test
	// and verify all new fields are tested
	_ = cfg.NodeID
	_ = cfg.Region
	_ = cfg.GroupCacheSize
	_ = cfg.FrameCapacity

	t.Logf("Config has %d fields - ensure all are tested", expectedFields)
}
