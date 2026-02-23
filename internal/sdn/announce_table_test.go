package sdn

import (
	"testing"
	"time"
)

func TestAnnounceTable_Register(t *testing.T) {
	at := NewAnnounceTable(0)

	at.Register("relay-a", "/live/stream1")
	at.Register("relay-b", "/live/stream1")

	entries := at.Lookup("/live/stream1")
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}

	relays := map[string]bool{}
	for _, e := range entries {
		relays[e.Relay] = true
	}
	if !relays["relay-a"] || !relays["relay-b"] {
		t.Errorf("expected both relay-a and relay-b, got %v", relays)
	}
}

func TestAnnounceTable_RegisterIdempotent(t *testing.T) {
	at := NewAnnounceTable(0)

	at.Register("relay-a", "/live/stream1")
	at.Register("relay-a", "/live/stream1") // duplicate

	entries := at.Lookup("/live/stream1")
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry (idempotent), got %d", len(entries))
	}
}

func TestAnnounceTable_Deregister(t *testing.T) {
	at := NewAnnounceTable(0)

	at.Register("relay-a", "/live/stream1")
	at.Register("relay-b", "/live/stream1")

	removed := at.Deregister("relay-a", "/live/stream1")
	if !removed {
		t.Error("expected removal to succeed")
	}

	entries := at.Lookup("/live/stream1")
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry after deregister, got %d", len(entries))
	}
	if entries[0].Relay != "relay-b" {
		t.Errorf("expected relay-b remaining, got %s", entries[0].Relay)
	}
}

func TestAnnounceTable_Deregister_NotFound(t *testing.T) {
	at := NewAnnounceTable(0)

	removed := at.Deregister("nonexistent", "/live/stream1")
	if removed {
		t.Error("expected false for nonexistent entry")
	}
}

func TestAnnounceTable_DeregisterRelay(t *testing.T) {
	at := NewAnnounceTable(0)

	at.Register("relay-a", "/live/stream1")
	at.Register("relay-a", "/live/stream2")
	at.Register("relay-a", "/live/stream3")
	at.Register("relay-b", "/live/stream1")

	removed := at.DeregisterRelay("relay-a")
	if removed != 3 {
		t.Errorf("expected 3 removals, got %d", removed)
	}

	if at.Count() != 1 {
		t.Errorf("expected 1 entry remaining, got %d", at.Count())
	}

	entries := at.Lookup("/live/stream1")
	if len(entries) != 1 || entries[0].Relay != "relay-b" {
		t.Errorf("expected only relay-b remaining for stream1")
	}
}

func TestAnnounceTable_LookupNotFound(t *testing.T) {
	at := NewAnnounceTable(0)

	entries := at.Lookup("/nonexistent")
	if len(entries) != 0 {
		t.Errorf("expected empty result, got %d entries", len(entries))
	}
}

func TestAnnounceTable_AllEntries(t *testing.T) {
	at := NewAnnounceTable(0)

	at.Register("relay-a", "/live/stream1")
	at.Register("relay-b", "/live/stream2")
	at.Register("relay-c", "/live/stream3")

	all := at.AllEntries()
	if len(all) != 3 {
		t.Errorf("expected 3 total entries, got %d", len(all))
	}
}

func TestAnnounceTable_Count(t *testing.T) {
	at := NewAnnounceTable(0)
	if at.Count() != 0 {
		t.Errorf("expected 0 for empty table, got %d", at.Count())
	}

	at.Register("relay-a", "/live/stream1")
	at.Register("relay-b", "/live/stream2")

	if at.Count() != 2 {
		t.Errorf("expected 2, got %d", at.Count())
	}
}

func TestAnnounceTable_DeregisterCleanup(t *testing.T) {
	at := NewAnnounceTable(0)

	at.Register("relay-a", "/live/stream1")
	at.Deregister("relay-a", "/live/stream1")

	if at.Count() != 0 {
		t.Errorf("expected 0 entries after full deregister, got %d", at.Count())
	}
}

func TestAnnounceTable_TTL_LookupFiltersExpired(t *testing.T) {
	at := NewAnnounceTable(50 * time.Millisecond)

	at.Register("relay-a", "/live/stream1")
	at.Register("relay-b", "/live/stream1")

	entries := at.Lookup("/live/stream1")
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries before expiry, got %d", len(entries))
	}

	time.Sleep(100 * time.Millisecond)

	entries = at.Lookup("/live/stream1")
	if len(entries) != 0 {
		t.Errorf("expected 0 entries after expiry, got %d", len(entries))
	}
}

func TestAnnounceTable_TTL_HeartbeatRenews(t *testing.T) {
	at := NewAnnounceTable(100 * time.Millisecond)

	at.Register("relay-a", "/live/stream1")

	time.Sleep(60 * time.Millisecond)
	at.Register("relay-a", "/live/stream1") // heartbeat

	time.Sleep(60 * time.Millisecond)
	entries := at.Lookup("/live/stream1")
	if len(entries) != 1 {
		t.Errorf("expected 1 entry (refreshed), got %d", len(entries))
	}
}

func TestAnnounceTable_Sweep(t *testing.T) {
	at := NewAnnounceTable(50 * time.Millisecond)

	at.Register("relay-a", "/live/stream1")
	at.Register("relay-b", "/live/stream2")

	time.Sleep(100 * time.Millisecond)

	removed := at.Sweep()
	if removed != 2 {
		t.Errorf("expected 2 removed, got %d", removed)
	}

	if at.Count() != 0 {
		t.Errorf("expected 0 entries after sweep, got %d", at.Count())
	}
}

func TestAnnounceTable_Sweep_NoTTL(t *testing.T) {
	at := NewAnnounceTable(0)

	at.Register("relay-a", "/live/stream1")

	removed := at.Sweep()
	if removed != 0 {
		t.Errorf("expected 0 removed with no TTL, got %d", removed)
	}
}

func TestAnnounceTable_TTL_ExpiresAtField(t *testing.T) {
	at := NewAnnounceTable(10 * time.Second)

	at.Register("relay-a", "/live/stream1")

	at.mu.RLock()
	entries := at.entries["/live/stream1"]
	at.mu.RUnlock()

	if len(entries) != 1 {
		t.Fatal("expected 1 entry")
	}
	if entries[0].ExpiresAt.IsZero() {
		t.Error("expected non-zero ExpiresAt with TTL configured")
	}
}

func TestAnnounceTable_NoTTL_ExpiresAtZero(t *testing.T) {
	at := NewAnnounceTable(0)

	at.Register("relay-a", "/live/stream1")

	at.mu.RLock()
	entries := at.entries["/live/stream1"]
	at.mu.RUnlock()

	if len(entries) != 1 {
		t.Fatal("expected 1 entry")
	}
	if !entries[0].ExpiresAt.IsZero() {
		t.Error("expected zero ExpiresAt with no TTL")
	}
}
