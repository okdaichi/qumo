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

	// Peers is the list of upstream relay peers to connect to.
	// The relay will dial each peer, discover announcements via
	// ANNOUNCE_PLEASE, and register them on the local TrackMux.
	Peers []Peer
}

// Peer represents a remote relay to connect to for announce discovery.
type Peer struct {
	// Address is the dial address (e.g. "moqt://relay-tokyo:4433" or "https://relay-tokyo:4433").
	Address string
}
