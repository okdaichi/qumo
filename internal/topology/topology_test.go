package topology

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTopology_Register(t *testing.T) {
	topo := &Topology{}

	topo.Register(RelayInfo{
		Name:      "relay-a",
		Neighbors: map[string]float64{"relay-b": 1, "relay-c": 1},
	})

	assert.Equal(t, 3, getNodeCount(topo), "expected 3 nodes (relay-a + auto-created relay-b, relay-c)")

	g := topo.Snapshot()
	nodeA := g.Nodes["relay-a"]
	require.NotNil(t, nodeA, "relay-a not found")
	assert.Len(t, nodeA.Edges, 2, "expected 2 edges from relay-a")
}

func TestTopology_Register_Update(t *testing.T) {
	topo := &Topology{}

	topo.Register(RelayInfo{
		Name:      "relay-a",
		Neighbors: map[string]float64{"relay-b": 1},
	})
	// Update: replace neighbors
	topo.Register(RelayInfo{
		Name:      "relay-a",
		Neighbors: map[string]float64{"relay-c": 1},
	})

	g := topo.Snapshot()
	nodeA := g.Nodes["relay-a"]
	assert.Len(t, nodeA.Edges, 1, "expected 1 edge after update")
	assert.Equal(t, "relay-c", nodeA.Edges[0].To, "expected edge to relay-c")
}

func TestTopology_Register_WithCostAndLoad(t *testing.T) {
	topo := &Topology{}

	topo.Register(RelayInfo{
		Name:      "relay-a",
		Region:    "us-east-1",
		Neighbors: map[string]float64{"relay-b": 5.5, "relay-c": 0},
	})

	g := topo.Snapshot()
	nodeA := g.Nodes["relay-a"]
	require.NotNil(t, nodeA, "relay-a not found")

	// Check edge costs
	costs := map[string]Cost{}
	for _, e := range nodeA.Edges {
		costs[e.To] = e.Cost
	}
	assert.Equal(t, Cost(5.5), costs["relay-b"], "expected cost 5.5 to relay-b")
	assert.Equal(t, Cost(1), costs["relay-c"], "0 should default to 1")
}

func TestTopology_Deregister(t *testing.T) {
	topo := &Topology{}

	topo.Register(RelayInfo{
		Name:      "relay-a",
		Neighbors: map[string]float64{"relay-b": 1},
	})
	topo.Register(RelayInfo{
		Name:      "relay-b",
		Neighbors: map[string]float64{"relay-a": 1},
	})

	removed := topo.Deregister("relay-a")
	assert.True(t, removed, "expected relay-a to be removed")
	assert.Equal(t, 1, getNodeCount(topo), "expected 1 node remaining")

	// relay-b should have no edges to relay-a anymore
	g := topo.Snapshot()
	nodeB := g.Nodes["relay-b"]
	require.NotNil(t, nodeB, "relay-b should still exist")
	for _, e := range nodeB.Edges {
		assert.NotEqual(t, "relay-a", e.To, "dangling edge to relay-a should have been removed")
	}
}

func TestTopology_Deregister_NotFound(t *testing.T) {
	topo := &Topology{}

	removed := topo.Deregister("nonexistent")
	assert.False(t, removed, "expected false for nonexistent relay")
}

func TestTopology_Route(t *testing.T) {
	tests := map[string]struct {
		setup        func(*Topology)
		from         string
		to           string
		wantErr      bool
		wantNextHop  string
		wantPathLen  int
		wantCost     float64
		checkDiamond bool // For diamond test case where multiple valid paths exist
	}{
		"simple linear chain A->B->C": {
			setup: func(topo *Topology) {
				topo.Register(RelayInfo{Name: "A", Neighbors: map[string]float64{"B": 1}})
				topo.Register(RelayInfo{Name: "B", Neighbors: map[string]float64{"C": 1}})
				topo.Register(RelayInfo{Name: "C", Neighbors: map[string]float64{}})
			},
			from:        "A",
			to:          "C",
			wantErr:     false,
			wantNextHop: "B",
			wantPathLen: 3,
			wantCost:    2.0,
		},
		"weighted edges prefer cheaper path": {
			setup: func(topo *Topology) {
				// A -> B (cost 10), A -> C (cost 3), C -> B (cost 2)
				// Cheapest: A -> C -> B = 5
				topo.Register(RelayInfo{Name: "A", Neighbors: map[string]float64{"B": 10, "C": 3}})
				topo.Register(RelayInfo{Name: "C", Neighbors: map[string]float64{"B": 2}})
				topo.Register(RelayInfo{Name: "B", Neighbors: map[string]float64{}})
			},
			from:        "A",
			to:          "B",
			wantErr:     false,
			wantNextHop: "C",
			wantPathLen: 3,
			wantCost:    5.0,
		},
		"diamond topology": {
			setup: func(topo *Topology) {
				topo.Register(RelayInfo{Name: "A", Neighbors: map[string]float64{"B": 1, "C": 1}})
				topo.Register(RelayInfo{Name: "B", Neighbors: map[string]float64{"D": 1}})
				topo.Register(RelayInfo{Name: "C", Neighbors: map[string]float64{"D": 1}})
				topo.Register(RelayInfo{Name: "D", Neighbors: map[string]float64{}})
			},
			from:         "A",
			to:           "D",
			wantErr:      false,
			checkDiamond: true, // Either B or C is valid
			wantPathLen:  3,
			wantCost:     2.0,
		},
		"same node route": {
			setup: func(topo *Topology) {
				topo.Register(RelayInfo{Name: "A", Neighbors: map[string]float64{"B": 1}})
			},
			from:        "A",
			to:          "A",
			wantErr:     false,
			wantNextHop: "A",
			wantPathLen: 1,
			wantCost:    0,
		},
		"unreachable destination": {
			setup: func(topo *Topology) {
				topo.Register(RelayInfo{Name: "A", Neighbors: map[string]float64{}})
				topo.Register(RelayInfo{Name: "B", Neighbors: map[string]float64{}})
			},
			from:    "A",
			to:      "B",
			wantErr: true,
		},
		"nonexistent nodes": {
			setup: func(topo *Topology) {
				// Empty topology
			},
			from:    "X",
			to:      "Y",
			wantErr: true,
		},
		"bidirectional edges both directions": {
			setup: func(topo *Topology) {
				topo.Register(RelayInfo{Name: "A", Neighbors: map[string]float64{"B": 1}})
				topo.Register(RelayInfo{Name: "B", Neighbors: map[string]float64{"A": 1}})
			},
			from:        "B",
			to:          "A",
			wantErr:     false,
			wantNextHop: "A",
			wantPathLen: 2,
			wantCost:    1.0,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			topo := &Topology{}
			tt.setup(topo)

			result, err := topo.Route(tt.from, tt.to)

			if tt.wantErr {
				assert.Error(t, err, "expected error for this case")
				return
			}

			require.NoError(t, err, "unexpected error")
			assert.Equal(t, tt.wantCost, result.Cost, "cost mismatch")
			assert.Len(t, result.FullPath, tt.wantPathLen, "path length mismatch")

			if tt.checkDiamond {
				// For diamond topology, either B or C is valid next hop
				assert.Contains(t, []string{"B", "C"}, result.NextHop, "next hop should be B or C")
				// Verify determinism - second call should return same result
				result2, err2 := topo.Route(tt.from, tt.to)
				require.NoError(t, err2)
				assert.Equal(t, result.NextHop, result2.NextHop, "routing should be deterministic")
			} else if tt.wantNextHop != "" {
				assert.Equal(t, tt.wantNextHop, result.NextHop, "next hop mismatch")
			}
		})
	}
}

func TestTopology_WithRouter(t *testing.T) {
	// Verify Router interface is used by Topology.
	called := false
	mockRouter := routerFunc(func(g *Graph, from, to string) (RouteResult, error) {
		called = true
		return RouteResult{From: from, To: to, NextHop: "mock", FullPath: []string{from, "mock", to}, Cost: 42}, nil
	})

	topo := &Topology{Router: mockRouter}
	topo.Register(RelayInfo{Name: "A", Neighbors: map[string]float64{"B": 1}})
	topo.Register(RelayInfo{Name: "B", Neighbors: map[string]float64{}})

	result, err := topo.Route("A", "B")
	require.NoError(t, err, "unexpected error")
	assert.True(t, called, "custom Router was not called")
	assert.Equal(t, "mock", result.NextHop, "expected mock next_hop")
	assert.Equal(t, 42.0, result.Cost, "expected cost 42")
}

// routerFunc adapts a function to the Router interface.
type routerFunc func(g *Graph, from, to string) (RouteResult, error)

func (f routerFunc) Route(g *Graph, from, to string) (RouteResult, error) {
	return f(g, from, to)
}

func TestTopology_Restore(t *testing.T) {
	topo := &Topology{}

	// Build a graph externally and restore it.
	g := newGraph()
	g.addNode(&Node{ID: "X", Region: "eu-west-1", Edges: []Edge{{To: "Y", Cost: Cost(7)}}})
	g.addNode(&Node{ID: "Y", Edges: []Edge{}})

	topo.Restore(g)

	assert.Equal(t, 2, getNodeCount(topo), "expected 2 nodes after restore")

	result, err := topo.Route("X", "Y")
	require.NoError(t, err, "unexpected error")
	assert.Equal(t, 7.0, result.Cost, "expected cost 7")
}

// getNodeCount is a test helper to inspect topology size.
func getNodeCount(topo *Topology) int {
	topo.mu.RLock()
	defer topo.mu.RUnlock()
	topo.init()
	return len(topo.graph.Nodes)
}

// Concurrent access tests to verify thread safety

func TestTopology_ConcurrentRegister(t *testing.T) {
	topo := &Topology{}
	var wg sync.WaitGroup

	// Register multiple relays concurrently
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			topo.Register(RelayInfo{
				Name:      string(rune('A' + id)),
				Region:    "test-region",
				Neighbors: map[string]float64{},
			})
		}(i)
	}

	wg.Wait()

	// All 10 nodes should be registered
	assert.Equal(t, 10, getNodeCount(topo))
}

func TestTopology_ConcurrentRegisterAndDeregister(t *testing.T) {
	topo := &Topology{}

	// Pre-register some nodes
	for i := 0; i < 5; i++ {
		topo.Register(RelayInfo{
			Name:      string(rune('A' + i)),
			Neighbors: map[string]float64{},
		})
	}

	var wg sync.WaitGroup

	// Concurrently register new nodes and deregister existing ones
	for i := 0; i < 5; i++ {
		wg.Add(2)
		// Register new node
		go func(id int) {
			defer wg.Done()
			topo.Register(RelayInfo{
				Name:      string(rune('F' + id)),
				Neighbors: map[string]float64{},
			})
		}(i)
		// Deregister existing node
		go func(id int) {
			defer wg.Done()
			topo.Deregister(string(rune('A' + id)))
		}(i)
	}

	wg.Wait()

	// Should have 5 nodes (F-J)
	assert.Equal(t, 5, getNodeCount(topo))

	g := topo.Snapshot()
	for i := 0; i < 5; i++ {
		name := string(rune('F' + i))
		assert.NotNil(t, g.Nodes[name], "node %s should exist", name)
	}
	for i := 0; i < 5; i++ {
		name := string(rune('A' + i))
		assert.Nil(t, g.Nodes[name], "node %s should not exist", name)
	}
}

func TestTopology_ConcurrentRoute(t *testing.T) {
	topo := &Topology{}
	topo.Register(RelayInfo{Name: "A", Neighbors: map[string]float64{"B": 1}})
	topo.Register(RelayInfo{Name: "B", Neighbors: map[string]float64{"C": 1}})
	topo.Register(RelayInfo{Name: "C", Neighbors: map[string]float64{}})

	var wg sync.WaitGroup
	results := make([]RouteResult, 10)
	errors := make([]error, 10)

	// Multiple concurrent route queries
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			result, err := topo.Route("A", "C")
			results[idx] = result
			errors[idx] = err
		}(i)
	}

	wg.Wait()

	// All queries should succeed and return the same result
	for i := 0; i < 10; i++ {
		require.NoError(t, errors[i], "query %d should not error", i)
		assert.Equal(t, "A", results[i].From, "query %d: from mismatch", i)
		assert.Equal(t, "C", results[i].To, "query %d: to mismatch", i)
		assert.Equal(t, "B", results[i].NextHop, "query %d: next hop mismatch", i)
		assert.Equal(t, 2.0, results[i].Cost, "query %d: cost mismatch", i)
	}
}

func TestTopology_ConcurrentSnapshot(t *testing.T) {
	topo := &Topology{}
	topo.Register(RelayInfo{Name: "A", Neighbors: map[string]float64{"B": 1}})
	topo.Register(RelayInfo{Name: "B", Neighbors: map[string]float64{}})

	var wg sync.WaitGroup
	snapshots := make([]*Graph, 10)

	// Take multiple snapshots concurrently
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			snapshots[idx] = topo.Snapshot()
		}(i)
	}

	wg.Wait()

	// All snapshots should have the same nodes
	for i, g := range snapshots {
		assert.Len(t, g.Nodes, 2, "snapshot %d should have 2 nodes", i)
		assert.NotNil(t, g.Nodes["A"], "snapshot %d should have node A", i)
		assert.NotNil(t, g.Nodes["B"], "snapshot %d should have node B", i)
	}
}

func TestTopology_ConcurrentRegisterAndRoute(t *testing.T) {
	topo := &Topology{}
	topo.Register(RelayInfo{Name: "A", Neighbors: map[string]float64{"B": 1}})
	topo.Register(RelayInfo{Name: "B", Neighbors: map[string]float64{"C": 1}})
	topo.Register(RelayInfo{Name: "C", Neighbors: map[string]float64{}})

	var wg sync.WaitGroup

	// Some goroutines do routing
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			topo.Route("A", "C")
		}()
	}

	// Other goroutines register new nodes
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			topo.Register(RelayInfo{
				Name:      string(rune('D' + id)),
				Neighbors: map[string]float64{},
			})
		}(i)
	}

	wg.Wait()

	// Should have at least 3 original nodes + 10 new nodes
	assert.GreaterOrEqual(t, getNodeCount(topo), 13)
}

func TestTopology_Register_SetsLastSeen(t *testing.T) {
	topo := &Topology{}

	before := time.Now()
	topo.Register(RelayInfo{
		Name:      "relay-a",
		Neighbors: map[string]float64{},
	})
	after := time.Now()

	g := topo.Snapshot()
	node := g.Nodes["relay-a"]
	require.NotNil(t, node)
	assert.False(t, node.LastSeen.IsZero(), "LastSeen should be set")
	assert.True(t, !node.LastSeen.Before(before) && !node.LastSeen.After(after),
		"LastSeen should be between before and after")
}

func TestTopology_Register_UpdatesLastSeen(t *testing.T) {
	topo := &Topology{}

	topo.Register(RelayInfo{
		Name:      "relay-a",
		Neighbors: map[string]float64{},
	})

	g1 := topo.Snapshot()
	firstSeen := g1.Nodes["relay-a"].LastSeen

	time.Sleep(5 * time.Millisecond)

	topo.Register(RelayInfo{
		Name:      "relay-a",
		Neighbors: map[string]float64{},
	})

	g2 := topo.Snapshot()
	secondSeen := g2.Nodes["relay-a"].LastSeen
	assert.True(t, secondSeen.After(firstSeen), "LastSeen should be updated on re-register")
}

func TestTopology_SweepStaleNodes_NoTTL(t *testing.T) {
	topo := &Topology{}

	topo.Register(RelayInfo{Name: "relay-a", Neighbors: map[string]float64{}})
	removed := topo.SweepStaleNodes()
	assert.Nil(t, removed, "should not remove anything when NodeTTL is 0")
	assert.Equal(t, 1, getNodeCount(topo))
}

func TestTopology_SweepStaleNodes_RemovesExpired(t *testing.T) {
	topo := &Topology{NodeTTL: 50 * time.Millisecond}

	topo.Register(RelayInfo{Name: "relay-a", Neighbors: map[string]float64{"relay-b": 1}})
	topo.Register(RelayInfo{Name: "relay-b", Neighbors: map[string]float64{"relay-a": 1}})

	// Before TTL expires — nothing removed
	removed := topo.SweepStaleNodes()
	assert.Nil(t, removed)
	assert.Equal(t, 2, getNodeCount(topo))

	// Wait for TTL to expire
	time.Sleep(60 * time.Millisecond)

	removed = topo.SweepStaleNodes()
	assert.Len(t, removed, 2, "both nodes should be removed after TTL")
	assert.Equal(t, 0, getNodeCount(topo))
}

func TestTopology_SweepStaleNodes_KeepsFresh(t *testing.T) {
	topo := &Topology{NodeTTL: 100 * time.Millisecond}

	topo.Register(RelayInfo{Name: "relay-a", Neighbors: map[string]float64{"relay-b": 1}})
	topo.Register(RelayInfo{Name: "relay-b", Neighbors: map[string]float64{"relay-a": 1}})

	time.Sleep(60 * time.Millisecond)

	// Heartbeat relay-a
	topo.Register(RelayInfo{Name: "relay-a", Neighbors: map[string]float64{"relay-b": 1}})

	time.Sleep(50 * time.Millisecond)

	// relay-b should be stale, relay-a should still be fresh
	removed := topo.SweepStaleNodes()
	assert.Len(t, removed, 1)
	assert.Equal(t, "relay-b", removed[0])

	g := topo.Snapshot()
	require.NotNil(t, g.Nodes["relay-a"])
	// relay-a's edge to relay-b should be gone (dangling edge cleanup)
	assert.Empty(t, g.Nodes["relay-a"].Edges, "edges to removed relay-b should be cleaned up")
}

func TestTopology_SweepStaleNodes_SkipsAutoCreated(t *testing.T) {
	topo := &Topology{NodeTTL: 50 * time.Millisecond}

	// relay-a registers with neighbor relay-b; relay-b is auto-created with zero LastSeen
	topo.Register(RelayInfo{Name: "relay-a", Neighbors: map[string]float64{"relay-b": 1}})

	time.Sleep(60 * time.Millisecond)

	removed := topo.SweepStaleNodes()
	// relay-a is stale, but relay-b (auto-created, zero LastSeen) should NOT be swept
	assert.Len(t, removed, 1)
	assert.Equal(t, "relay-a", removed[0])
	assert.Equal(t, 1, getNodeCount(topo), "auto-created relay-b should remain")
}

func TestTopology_StartSweeper(t *testing.T) {
	topo := &Topology{NodeTTL: 50 * time.Millisecond}

	topo.Register(RelayInfo{Name: "relay-a", Neighbors: map[string]float64{}})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	topo.StartSweeper(ctx, 30*time.Millisecond)

	// Wait for TTL + sweeper interval
	time.Sleep(120 * time.Millisecond)

	assert.Equal(t, 0, getNodeCount(topo), "sweeper should have removed stale node")
}
