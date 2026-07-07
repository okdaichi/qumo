package relay

import "time"

// Config holds the relay server configuration.
type Config struct {
	// NodeID is a human-readable identifier for this relay node, used as a
	// prefix in routing/metric labels (e.g. "[node]/path/track"). It is the
	// ops-facing label, not the MoQT protocol hop identity (that is the
	// TrackMux's random HopID — see moqt.NewHopID).
	NodeID string

	// Role is this node's role in the topology ("edge" or "hub").
	// If empty, a simple flat peer discovery is used.
	Role string

	// AdvertiseAddr is the address this relay advertises to peers.
	// It should be the address that other nodes can use to connect to this relay.
	AdvertiseAddr string

	// GroupCacheSize is the number of completed groups each track's ring
	// retains for late/backfill subscribers. ≤0 falls back to
	// DefaultGroupCacheSize.
	GroupCacheSize int

	// FrameCapacity is the capacity (bytes) of recycled frame buffers. ≤0
	// falls back to DefaultFramePool (DefaultNewFrameCapacity).
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
