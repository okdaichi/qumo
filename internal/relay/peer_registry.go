package relay

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/okdaichi/gomoqt/moqt"
)

// peerInfo holds metadata about a connected peer.
type peerInfo struct {
	ID          string    `json:"peer_id"`
	ConnectedAt time.Time `json:"connected_at"`
	session     *moqt.Session
}

// peerCounter generates unique peer IDs.
var peerCounter atomic.Uint64

// peerRegistry tracks connected peers in a thread-safe manner.
type peerRegistry struct {
	mu    sync.RWMutex
	peers map[string]*peerInfo
}

// newPeerRegistry creates a new peer registry.
func newPeerRegistry() *peerRegistry {
	return &peerRegistry{
		peers: make(map[string]*peerInfo),
	}
}

// register adds a peer from the given session and returns the peer ID.
func (r *peerRegistry) register(sess *moqt.Session) string {
	id := fmt.Sprintf("peer-%d", peerCounter.Add(1))

	r.mu.Lock()
	defer r.mu.Unlock()

	r.peers[id] = &peerInfo{
		ID:          id,
		ConnectedAt: time.Now(),
		session:     sess,
	}

	return id
}

// deregister removes a peer by its ID.
func (r *peerRegistry) deregister(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	delete(r.peers, id)
}

// listPeers returns a snapshot of all currently connected peers.
func (r *peerRegistry) listPeers() []peerInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()

	peers := make([]peerInfo, 0, len(r.peers))
	for _, p := range r.peers {
		peers = append(peers, peerInfo{
			ID:          p.ID,
			ConnectedAt: p.ConnectedAt,
		})
	}
	return peers
}

// peerCount returns the number of currently connected peers.
func (r *peerRegistry) peerCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return len(r.peers)
}
