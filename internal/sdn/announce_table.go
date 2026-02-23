package sdn

import (
	"context"
	"sync"
	"time"
)

// announceEntry records which relay announced a specific broadcast path.
type announceEntry struct {
	Relay         string    `json:"relay"`
	BroadcastPath string    `json:"broadcast_path"`
	RegisteredAt  time.Time `json:"registered_at"`
	ExpiresAt     time.Time `json:"expires_at,omitempty"`
}

// announceTable manages the central registry of which relays hold which broadcast paths.
// Thread-safe: all access goes through a RWMutex.
type announceTable struct {
	mu      sync.RWMutex
	entries map[string][]announceEntry // broadcastPath → list of relays

	// TTL is how long an entry stays valid after its last registration.
	// Zero means entries never expire.
	TTL time.Duration
}

// NewAnnounceTable creates an empty announce table.
// If ttl > 0, entries expire that long after their last registration/heartbeat.
func NewAnnounceTable(ttl time.Duration) *announceTable {
	return &announceTable{
		entries: make(map[string][]announceEntry),
		TTL:     ttl,
	}
}

// StartSweeper runs a background goroutine that removes expired entries
// at regular intervals. It stops when ctx is cancelled.
func (at *announceTable) StartSweeper(ctx context.Context, interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				at.Sweep()
			}
		}
	}()
}

// Register records that a relay holds the given broadcast path.
// If the same relay re-announces the same path, it updates the timestamp.
func (at *announceTable) Register(relay, broadcastPath string) {
	at.mu.Lock()
	defer at.mu.Unlock()

	now := time.Now()
	entries := at.entries[broadcastPath]

	expiresAt := time.Time{} // zero = never
	if at.TTL > 0 {
		expiresAt = now.Add(at.TTL)
	}

	// Update existing entry for this relay.
	for i, e := range entries {
		if e.Relay == relay {
			entries[i].RegisteredAt = now
			entries[i].ExpiresAt = expiresAt
			return
		}
	}

	// New entry.
	at.entries[broadcastPath] = append(entries, announceEntry{
		Relay:         relay,
		BroadcastPath: broadcastPath,
		RegisteredAt:  now,
		ExpiresAt:     expiresAt,
	})
}

// Deregister removes a specific broadcast path announcement from a relay.
// Returns true if the entry existed.
func (at *announceTable) Deregister(relay, broadcastPath string) bool {
	at.mu.Lock()
	defer at.mu.Unlock()

	entries := at.entries[broadcastPath]

	for i, e := range entries {
		if e.Relay == relay {
			at.entries[broadcastPath] = append(entries[:i], entries[i+1:]...)
			if len(at.entries[broadcastPath]) == 0 {
				delete(at.entries, broadcastPath)
			}
			return true
		}
	}
	return false
}

// DeregisterRelay removes all announcements from a specific relay.
// Used when a relay is deregistered from the topology.
func (at *announceTable) DeregisterRelay(relay string) int {
	at.mu.Lock()
	defer at.mu.Unlock()

	removed := 0
	for bp, entries := range at.entries {
		filtered := entries[:0]
		for _, e := range entries {
			if e.Relay != relay {
				filtered = append(filtered, e)
			} else {
				removed++
			}
		}
		if len(filtered) == 0 {
			delete(at.entries, bp)
		} else {
			at.entries[bp] = filtered
		}
	}
	return removed
}

// Lookup finds all relays that have announced the given broadcast path.
// Expired entries are excluded from results.
func (at *announceTable) Lookup(broadcastPath string) []announceEntry {
	at.mu.RLock()
	defer at.mu.RUnlock()

	entries := at.entries[broadcastPath]

	now := time.Now()
	var result []announceEntry
	for _, e := range entries {
		if !e.ExpiresAt.IsZero() && now.After(e.ExpiresAt) {
			continue // expired
		}
		result = append(result, e)
	}
	return result
}

// LookupResponse is the JSON response for announce lookup queries.
type LookupResponse struct {
	BroadcastPath string          `json:"broadcast_path"`
	Relays        []announceEntry `json:"relays"`
}

// AllEntries returns all announcements. Used for debugging / admin views.
func (at *announceTable) AllEntries() []announceEntry {
	at.mu.RLock()
	defer at.mu.RUnlock()

	var all []announceEntry
	for _, entries := range at.entries {
		all = append(all, entries...)
	}
	return all
}

// Count returns the total number of announce entries.
func (at *announceTable) Count() int {
	at.mu.RLock()
	defer at.mu.RUnlock()

	count := 0
	for _, entries := range at.entries {
		count += len(entries)
	}
	return count
}

// Sweep removes all expired entries from the table. Returns the number removed.
func (at *announceTable) Sweep() int {
	if at.TTL <= 0 {
		return 0
	}

	at.mu.Lock()
	defer at.mu.Unlock()

	now := time.Now()
	removed := 0

	for bp, entries := range at.entries {
		filtered := entries[:0]
		for _, e := range entries {
			if !e.ExpiresAt.IsZero() && now.After(e.ExpiresAt) {
				removed++
			} else {
				filtered = append(filtered, e)
			}
		}
		if len(filtered) == 0 {
			delete(at.entries, bp)
		} else {
			at.entries[bp] = filtered
		}
	}
	return removed
}
