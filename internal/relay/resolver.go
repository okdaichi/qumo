// Package relay provides the MoQT relay server for content distribution.
//
// Peer Resolution
//
// The relay discovers peers through one or more PeerResolver implementations.
//   - LocalResolver: discovers peers via the Nomad native service discovery API
//     (within-cluster, OSS). Configured via LOCAL_RESOLVER_ADDR and LOCAL_RESOLVER_SERVICE_NAME.
//   - RemoteResolver: discovers peers via a remote traffic resolver API
//     (cross-cluster). Configured via REMOTE_RESOLVER_URL.
package relay

import "context"

// PeerQuery defines criteria for discovering peers through a PeerResolver.
type PeerQuery struct {
	// Role filters peers by their configured role ("hub", "edge", or "" for any).
	Role string
	// Limit caps the number of peers returned. 0 means no limit.
	Limit int
}

// ResolvedPeer represents a peer discovered through a resolver, with metadata
// used by the topology logic for role-based and region-based connectivity.
type ResolvedPeer struct {
	ID      string
	Address string
	Region  string
	Role    string
}

// PeerResolver discovers peers based on query criteria.
type PeerResolver interface {
	// ResolvePeers returns a list of peers matching the query.
	ResolvePeers(ctx context.Context, query PeerQuery) ([]ResolvedPeer, error)
}
