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
	LastSeen time.Time `json:"-"`
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

// Peers returns active (non-expired) nodes, excluding selfID if provided,
// optionally filtered by region, randomly shuffled, and capped at max.
// If max <= 0, no cap is applied.
func (s *Store) Peers(selfID, region string, max int) []Node {
	s.mu.RLock()
	defer s.mu.RUnlock()

	cutoff := time.Now().Add(-s.ttl)
	result := make([]Node, 0)

	for _, n := range s.nodes {
		if n.LastSeen.Before(cutoff) {
			continue
		}
		if selfID != "" && n.ID == selfID {
			continue
		}
		if region != "" && n.Region != region {
			continue
		}
		result = append(result, *n)
	}

	rand.Shuffle(len(result), func(i, j int) {
		result[i], result[j] = result[j], result[i]
	})

	if max > 0 && len(result) > max {
		result = result[:max]
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
