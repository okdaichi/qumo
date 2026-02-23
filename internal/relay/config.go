package relay

type Config struct {
	// NodeID is the unique identifier for this relay node.
	NodeID string

	// Region is the geographic region this node belongs to.
	Region string

	// GroupCacheSize is the maximum number of group caches to keep.
	GroupCacheSize int

	// FrameCapacity is the frame buffer size in bytes.
	FrameCapacity int
}

// AnnounceRegistrar is implemented by sdn.Client and allows the relay
// server to push announcement state to the SDN controller.
type AnnounceRegistrar interface {
	Register(broadcastPath string)
	Deregister(broadcastPath string)
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
