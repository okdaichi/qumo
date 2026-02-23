package topology

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFileStore_SaveAndLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "topo.json")
	store := NewFileStore(path)

	g := newGraph()
	g.addNode(&Node{
		ID:     "relay-a",
		Region: "us-east-1",
		Edges: []Edge{
			{To: "relay-b", Cost: Cost(3.5)},
			{To: "relay-c", Cost: Cost(1)},
		},
	})
	g.addNode(&Node{
		ID:     "relay-b",
		Region: "eu-west-1",
		Edges:  []Edge{},
	})

	// Save
	if err := store.Save(g); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// File should exist
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Fatal("expected file to exist after Save")
	}

	// Load
	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if loaded == nil {
		t.Fatal("expected non-nil graph from Load")
	}

	// Verify nodes
	if len(loaded.Nodes) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(loaded.Nodes))
	}

	nodeA := loaded.Nodes["relay-a"]
	if nodeA == nil {
		t.Fatal("relay-a not found in loaded graph")
	}
	if nodeA.Region != "us-east-1" {
		t.Errorf("expected region us-east-1, got %s", nodeA.Region)
	}
	if len(nodeA.Edges) != 2 {
		t.Fatalf("expected 2 edges, got %d", len(nodeA.Edges))
	}

	// Verify edge costs preserved
	found := false
	for _, e := range nodeA.Edges {
		if e.To == "relay-b" && e.Cost == Cost(3.5) {
			found = true
		}
	}
	if !found {
		t.Error("expected edge to relay-b with cost 3.5")
	}
}

func TestFileStore_LoadNotExist(t *testing.T) {
	store := NewFileStore(filepath.Join(t.TempDir(), "nonexistent.json"))

	g, err := store.Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if g != nil {
		t.Error("expected nil graph for non-existent file")
	}
}

func TestFileStore_AtomicWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "topo.json")
	store := NewFileStore(path)

	g := newGraph()
	g.addNode(&Node{ID: "A", Edges: []Edge{{To: "B", Cost: Cost(1)}}})

	// First save
	if err := store.Save(g); err != nil {
		t.Fatalf("first Save failed: %v", err)
	}

	// Second save with different data
	g2 := newGraph()
	g2.addNode(&Node{ID: "X", Edges: []Edge{{To: "Y", Cost: Cost(10)}}})
	if err := store.Save(g2); err != nil {
		t.Fatalf("second Save failed: %v", err)
	}

	// No temp file should remain
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Error("temp file should not remain after successful save")
	}

	// Load should return second save
	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if _, ok := loaded.Nodes["X"]; !ok {
		t.Error("expected node X from second save")
	}
	if _, ok := loaded.Nodes["A"]; ok {
		t.Error("node A from first save should not exist")
	}
}

func TestTopology_WithStore(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "topo.json")
	store := NewFileStore(path)

	// Create topology with store, register a relay
	topo1 := &Topology{Store: store}
	topo1.Register(RelayInfo{
		Name:      "relay-1",
		Region:    "ap-northeast-1",
		Neighbors: map[string]float64{"relay-2": 5},
	})

	// Create a new topology from same store — should restore
	topo2 := &Topology{Store: store}
	if getNodeCount(topo2) != 2 {
		t.Fatalf("expected 2 nodes restored, got %d", getNodeCount(topo2))
	}

	g := topo2.Snapshot()
	node := g.Nodes["relay-1"]
	if node == nil {
		t.Fatal("relay-1 not found after restore")
	}
	if node.Region != "ap-northeast-1" {
		t.Errorf("expected region ap-northeast-1, got %s", node.Region)
	}
	if len(node.Edges) != 1 || node.Edges[0].Cost != Cost(5) {
		t.Errorf("expected edge to relay-2 with cost 5, got %v", node.Edges)
	}
}
