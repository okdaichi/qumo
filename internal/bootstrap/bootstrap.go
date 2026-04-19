package bootstrap

import (
	"context"
	"log/slog"
	"math/rand/v2"
	"sync"
	"time"
)

// Node represents a registered node in the bootstrap network.
type Node struct {
	ID       string    `json:"id"`
	Addr     string    `json:"addr"`
	Region   string    `json:"region,omitempty"`
	Role     string    `json:"role,omitempty"`
	LastSeen time.Time `json:"-"`
}

// PeerQuery describes the criteria for selecting candidate peers from the store.
type PeerQuery struct {
	// PreferredRegion selects nodes in this region first.
	// When AllowRemote is true, nodes in other regions are appended as fallback.
	// When empty, all nodes are candidates regardless of region.
	PreferredRegion string

	// Role filters nodes by role (e.g. "edge", "hub"). Empty means any role.
	Role string

	// AllowRemote, when true, appends nodes from other regions after preferred-region nodes.
	// Ignored when PreferredRegion is empty.
	AllowRemote bool

	// Limit caps the result to at most this many nodes. 0 means no per-request cap.
	Limit int

	// MaxCap is a server-side hard cap applied before Limit. 0 means no cap.
	MaxCap int
}

// Store holds registered nodes in memory with TTL-based expiration.
type Store struct {
	mu    sync.RWMutex
	nodes map[string]*Node
	ttl   time.Duration
}

// NewStore creates a new node store with the given TTL.
func NewStore(ttl time.Duration) *Store {
	return &Store{
		nodes: make(map[string]*Node),
		ttl:   ttl,
	}
}

// Register inserts or updates a node, refreshing its LastSeen timestamp.
func (s *Store) Register(n Node) {
	s.mu.Lock()
	defer s.mu.Unlock()

	n.LastSeen = time.Now()
	s.nodes[n.ID] = &n
	slog.Info("node registered", "id", n.ID, "addr", n.Addr, "region", n.Region)
}

// Peers returns a filtered, shuffled, and capped list of active nodes using q.
// Preferred-region nodes are returned first; remote nodes are appended only when
// q.AllowRemote is true. Result is capped at min(q.Limit, q.MaxCap) where 0 means no cap.
func (s *Store) Peers(q PeerQuery) []Node {
	s.mu.RLock()
	defer s.mu.RUnlock()

	cutoff := time.Now().Add(-s.ttl)
	preferred := make([]Node, 0)
	remote := make([]Node, 0)

	for _, n := range s.nodes {
		if n.LastSeen.Before(cutoff) {
			continue
		}
		if q.Role != "" && n.Role != q.Role {
			continue
		}
		if q.PreferredRegion == "" || n.Region == q.PreferredRegion {
			preferred = append(preferred, *n)
		} else if q.AllowRemote {
			remote = append(remote, *n)
		}
	}

	rand.Shuffle(len(preferred), func(i, j int) { preferred[i], preferred[j] = preferred[j], preferred[i] })
	rand.Shuffle(len(remote), func(i, j int) { remote[i], remote[j] = remote[j], remote[i] })

	result := append(preferred, remote...)

	cap := q.MaxCap
	if q.Limit > 0 && (cap == 0 || q.Limit < cap) {
		cap = q.Limit
	}
	if cap > 0 && len(result) > cap {
		result = result[:cap]
	}

	return result
}

// cleanup removes nodes whose LastSeen is older than the TTL.
func (s *Store) cleanup() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	cutoff := time.Now().Add(-s.ttl)
	removed := 0

	for id, n := range s.nodes {
		if n.LastSeen.Before(cutoff) {
			delete(s.nodes, id)
			removed++
		}
	}

	return removed
}

// StartCleaner runs a background goroutine that removes expired nodes
// at the given interval. It stops when ctx is cancelled.
func (s *Store) StartCleaner(ctx context.Context, interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if removed := s.cleanup(); removed > 0 {
					slog.Info("expired nodes cleaned", "removed", removed)
				}
			}
		}
	}()
}
