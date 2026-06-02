package relay

import "time"

// Config holds the relay server configuration.
type Config struct {
	// NodeID is the unique identifier for this relay node.
	NodeID string

	// Region is the geographic region this node belongs to.
	Region string

	// Role is this node's role in the topology ("edge" or "hub").
	// If empty, a simple flat peer discovery is used.
	Role string

	// AdvertiseAddr is the address this relay advertises to peers.
	// It should be the address that other nodes can use to connect to this relay.
	AdvertiseAddr string

	// GroupCacheSize is the maximum number of group caches to keep.
	GroupCacheSize int

	// FrameCapacity is the frame buffer size in bytes.
	FrameCapacity int

	// Peers is the list of upstream relay peers to connect to.
	// The relay will dial each peer, discover announcements via
	// ANNOUNCE_PLEASE, and register them on the local TrackMux.
	Peers []Peer

	// LocalResolverInterval is the polling interval for Nomad service discovery.
	// If zero, local discovery is disabled.
	LocalResolverInterval time.Duration

	// RemoteResolverInterval is the polling interval for the remote
	// traffic resolver. If zero, remote discovery is disabled.
	RemoteResolverInterval time.Duration
}

// Peer represents a remote relay to connect to for announce discovery.
type Peer struct {
	// Address is the dial address used to connect to a remote relay.
	// It can be a full URL such as "moqt://relay-tokyo:4433"
	// or a raw host:port string such as "relay-tokyo:4433".
	// Raw host:port addresses default to the moqt:// scheme.
	Address string
}
