package relay

import (
	"sync"
	"testing"
)

func TestPeerRegistry_RegisterDeregister(t *testing.T) {
	r := newPeerRegistry()

	// Simulate registration by directly inserting
	r.mu.Lock()
	r.peers["peer-test-1"] = &peerInfo{
		ID: "peer-test-1",
	}
	r.mu.Unlock()

	if r.peerCount() != 1 {
		t.Fatalf("expected 1 peer, got %d", r.peerCount())
	}

	peers := r.listPeers()
	if len(peers) != 1 {
		t.Fatalf("expected 1 peer in list, got %d", len(peers))
	}
	if peers[0].ID != "peer-test-1" {
		t.Errorf("expected peer ID 'peer-test-1', got '%s'", peers[0].ID)
	}

	// Deregister
	r.deregister("peer-test-1")
	if r.peerCount() != 0 {
		t.Fatalf("expected 0 peers after deregister, got %d", r.peerCount())
	}
}

func TestPeerRegistry_MultiplePeers(t *testing.T) {
	r := newPeerRegistry()

	ids := []string{"peer-a", "peer-b", "peer-c"}
	for _, id := range ids {
		r.mu.Lock()
		r.peers[id] = &peerInfo{ID: id}
		r.mu.Unlock()
	}

	if r.peerCount() != 3 {
		t.Fatalf("expected 3 peers, got %d", r.peerCount())
	}

	// Remove middle one
	r.deregister("peer-b")
	if r.peerCount() != 2 {
		t.Fatalf("expected 2 peers, got %d", r.peerCount())
	}

	peers := r.listPeers()
	for _, p := range peers {
		if p.ID == "peer-b" {
			t.Error("deregistered peer should not appear in list")
		}
	}
}

func TestPeerRegistry_DeregisterNonexistent(t *testing.T) {
	r := newPeerRegistry()
	// Should not panic
	r.deregister("nonexistent")
	if r.peerCount() != 0 {
		t.Fatalf("expected 0 peers, got %d", r.peerCount())
	}
}

func TestPeerRegistry_ConcurrentAccess(t *testing.T) {
	r := newPeerRegistry()

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := "peer-" + string(rune('a'+i%26))

			r.mu.Lock()
			r.peers[id] = &peerInfo{ID: id}
			r.mu.Unlock()

			_ = r.listPeers()
			_ = r.peerCount()

			r.deregister(id)
		}(i)
	}
	wg.Wait()
}

func TestPeerRegistry_ListPeersSnapshot(t *testing.T) {
	r := newPeerRegistry()

	r.mu.Lock()
	r.peers["peer-snap"] = &peerInfo{ID: "peer-snap"}
	r.mu.Unlock()

	peers := r.listPeers()

	// Modify the returned slice — should not affect registry
	if len(peers) > 0 {
		peers[0].ID = "modified"
	}

	original := r.listPeers()
	if original[0].ID == "modified" {
		t.Error("ListPeers should return a copy, not a reference")
	}
}
