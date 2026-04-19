package bootstrap

import (
	"fmt"
	"testing"
	"time"
)

func TestStore_Register(t *testing.T) {
	s := NewStore(30 * time.Second)

	s.Register(Node{ID: "n1", Addr: "1.2.3.4:443", Region: "us-east"})

	peers := s.Peers(PeerQuery{})
	if len(peers) != 1 {
		t.Fatalf("expected 1 peer, got %d", len(peers))
	}
	if peers[0].ID != "n1" {
		t.Errorf("expected id n1, got %s", peers[0].ID)
	}
	if peers[0].Addr != "1.2.3.4:443" {
		t.Errorf("expected addr 1.2.3.4:443, got %s", peers[0].Addr)
	}
}

func TestStore_Register_UpdatesLastSeen(t *testing.T) {
	s := NewStore(30 * time.Second)

	s.Register(Node{ID: "n1", Addr: "1.2.3.4:443"})

	s.mu.RLock()
	first := s.nodes["n1"].LastSeen
	s.mu.RUnlock()

	// Re-register to update LastSeen.
	s.Register(Node{ID: "n1", Addr: "5.6.7.8:443"})

	s.mu.RLock()
	second := s.nodes["n1"].LastSeen
	addr := s.nodes["n1"].Addr
	s.mu.RUnlock()

	if !second.After(first) && !second.Equal(first) {
		t.Error("expected LastSeen to be updated on re-register")
	}
	if addr != "5.6.7.8:443" {
		t.Errorf("expected addr to be updated, got %s", addr)
	}
}

func TestStore_Peers_FiltersByRegion(t *testing.T) {
	s := NewStore(30 * time.Second)

	s.Register(Node{ID: "n1", Addr: "1.1.1.1:443", Region: "us-east"})
	s.Register(Node{ID: "n2", Addr: "2.2.2.2:443", Region: "ap-northeast"})
	s.Register(Node{ID: "n3", Addr: "3.3.3.3:443", Region: "us-east"})

	peers := s.Peers(PeerQuery{PreferredRegion: "us-east"})
	if len(peers) != 2 {
		t.Fatalf("expected 2 peers, got %d", len(peers))
	}
	for _, p := range peers {
		if p.Region != "us-east" {
			t.Errorf("expected region us-east, got %s", p.Region)
		}
	}
}

func TestStore_Peers_AllowRemote(t *testing.T) {
	s := NewStore(30 * time.Second)

	s.Register(Node{ID: "n1", Addr: "1.1.1.1:443", Region: "us-east"})
	s.Register(Node{ID: "n2", Addr: "2.2.2.2:443", Region: "ap-northeast"})

	// Without AllowRemote: only preferred region.
	peers := s.Peers(PeerQuery{PreferredRegion: "us-east", AllowRemote: false})
	if len(peers) != 1 {
		t.Fatalf("without AllowRemote: expected 1 peer, got %d", len(peers))
	}

	// With AllowRemote: preferred region first, then remote.
	peers = s.Peers(PeerQuery{PreferredRegion: "us-east", AllowRemote: true})
	if len(peers) != 2 {
		t.Fatalf("with AllowRemote: expected 2 peers, got %d", len(peers))
	}
}

func TestStore_Peers_FiltersByRole(t *testing.T) {
	s := NewStore(30 * time.Second)

	s.Register(Node{ID: "n1", Addr: "1.1.1.1:443", Role: "edge"})
	s.Register(Node{ID: "n2", Addr: "2.2.2.2:443", Role: "hub"})
	s.Register(Node{ID: "n3", Addr: "3.3.3.3:443", Role: "edge"})

	peers := s.Peers(PeerQuery{Role: "hub"})
	if len(peers) != 1 {
		t.Fatalf("expected 1 hub, got %d", len(peers))
	}
	if peers[0].ID != "n2" {
		t.Errorf("expected n2 (hub), got %s", peers[0].ID)
	}

	edges := s.Peers(PeerQuery{Role: "edge"})
	if len(edges) != 2 {
		t.Fatalf("expected 2 edges, got %d", len(edges))
	}
}

func TestStore_Peers_RespectsLimit(t *testing.T) {
	s := NewStore(30 * time.Second)

	for i := 0; i < 10; i++ {
		s.Register(Node{ID: fmt.Sprintf("n%d", i), Addr: fmt.Sprintf("1.1.1.%d:443", i)})
	}

	peers := s.Peers(PeerQuery{Limit: 3})
	if len(peers) != 3 {
		t.Fatalf("expected 3 peers, got %d", len(peers))
	}
}

func TestStore_Peers_ExcludesExpired(t *testing.T) {
	s := NewStore(100 * time.Millisecond)

	s.Register(Node{ID: "n1", Addr: "1.1.1.1:443"})

	// Manually set LastSeen to the past.
	s.mu.Lock()
	s.nodes["n1"].LastSeen = time.Now().Add(-200 * time.Millisecond)
	s.mu.Unlock()

	peers := s.Peers(PeerQuery{})
	if len(peers) != 0 {
		t.Fatalf("expected 0 peers (expired), got %d", len(peers))
	}
}

func TestStore_Cleanup(t *testing.T) {
	s := NewStore(100 * time.Millisecond)

	s.Register(Node{ID: "active", Addr: "1.1.1.1:443"})
	s.Register(Node{ID: "expired", Addr: "2.2.2.2:443"})

	// Expire one node.
	s.mu.Lock()
	s.nodes["expired"].LastSeen = time.Now().Add(-200 * time.Millisecond)
	s.mu.Unlock()

	removed := s.cleanup()
	if removed != 1 {
		t.Fatalf("expected 1 removed, got %d", removed)
	}

	s.mu.RLock()
	_, exists := s.nodes["expired"]
	_, activeExists := s.nodes["active"]
	s.mu.RUnlock()

	if exists {
		t.Error("expired node should have been removed")
	}
	if !activeExists {
		t.Error("active node should still exist")
	}
}
