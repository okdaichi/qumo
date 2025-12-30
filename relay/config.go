package relay

import "github.com/okdaichi/qumo/relay/health"

type Config struct {
	// Upstream server URL (optional)
	Upstream string

	// GroupCacheSize is the maximum number of group caches to keep.
	GroupCacheSize int

	// FrameCapacity is the frame buffer size in bytes.
	FrameCapacity int

	// HealthCheckAddr is the address for the health check HTTP server
	// Example: ":8080" or "0.0.0.0:8080"
	HealthCheckAddr string
}

func (c *Config) groupCacheSize() int {
	if c != nil && c.GroupCacheSize > 0 {
		return c.GroupCacheSize
	}
	return DefaultGroupCacheSize
}

func (c *Config) frameCapacity() int {
	if c != nil && c.FrameCapacity > 0 {
		return c.FrameCapacity
	}
	return DefaultNewFrameCapacity
}

func (c *Config) healthCheckAddr() string {
	if c != nil && c.HealthCheckAddr != "" {
		return c.HealthCheckAddr
	}
	return health.DefaultAddress
}
