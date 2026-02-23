package topology

import (
	"encoding/json"
	"testing"
)

func TestNewGraph(t *testing.T) {
	g := newGraph()
	if g == nil {
		t.Fatal("NewGraph returned nil")
	}
	if len(g.Nodes) != 0 {
		t.Errorf("expected 0 nodes, got %d", len(g.Nodes))
	}
}

func TestGraph_AddNode(t *testing.T) {
	g := newGraph()
	g.addNode(&Node{ID: "n1", Region: "us-east-1"})
	g.addNode(&Node{ID: "n2", Region: "us-west-2"})

	if len(g.Nodes) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(g.Nodes))
	}
	if g.Nodes["n1"].Region != "us-east-1" {
		t.Errorf("expected region 'us-east-1', got '%s'", g.Nodes["n1"].Region)
	}
}

func TestGraph_AddNode_Update(t *testing.T) {
	g := newGraph()
	g.addNode(&Node{ID: "n1", Region: "us-east-1"})
	g.addNode(&Node{ID: "n1", Region: "us-west-2"}) // overwrite

	if len(g.Nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(g.Nodes))
	}
	if g.Nodes["n1"].Region != "us-west-2" {
		t.Errorf("expected updated region 'us-west-2', got '%s'", g.Nodes["n1"].Region)
	}
}

func TestGraph_AddEdge(t *testing.T) {
	g := newGraph()
	g.addNode(&Node{ID: "n1", Region: "us-east-1", Edges: []Edge{}})
	g.addNode(&Node{ID: "n2", Region: "us-west-2", Edges: []Edge{}})

	g.addEdge("n1", "n2", 10.0)

	if len(g.Nodes["n1"].Edges) != 1 {
		t.Fatalf("expected 1 edge from n1, got %d", len(g.Nodes["n1"].Edges))
	}
	if g.Nodes["n1"].Edges[0].To != "n2" {
		t.Errorf("expected edge to 'n2', got '%s'", g.Nodes["n1"].Edges[0].To)
	}
	if g.Nodes["n1"].Edges[0].Cost != Cost(10.0) {
		t.Errorf("expected cost 10.0, got %f", float64(g.Nodes["n1"].Edges[0].Cost))
	}
	// n2 should have no edges (directed)
	if len(g.Nodes["n2"].Edges) != 0 {
		t.Errorf("expected 0 edges from n2, got %d", len(g.Nodes["n2"].Edges))
	}
}

func TestGraph_AddEdge_Duplicate(t *testing.T) {
	g := newGraph()
	g.addNode(&Node{ID: "n1", Edges: []Edge{}})
	g.addNode(&Node{ID: "n2", Edges: []Edge{}})

	g.addEdge("n1", "n2", Cost(10.0))
	g.addEdge("n1", "n2", Cost(5.0)) // update cost

	if len(g.Nodes["n1"].Edges) != 1 {
		t.Fatalf("expected 1 edge (no duplicate), got %d", len(g.Nodes["n1"].Edges))
	}
	if g.Nodes["n1"].Edges[0].Cost != Cost(5.0) {
		t.Errorf("expected updated cost 5.0, got %f", float64(g.Nodes["n1"].Edges[0].Cost))
	}
}

func TestGraph_AddEdge_NonexistentSource(t *testing.T) {
	g := newGraph()
	g.addNode(&Node{ID: "n1", Edges: []Edge{}})

	// Should not panic
	g.addEdge("nonexistent", "n1", Cost(10.0))
}

func TestGraph_ToResponse(t *testing.T) {
	g := newGraph()
	g.addNode(&Node{
		ID:     "n1",
		Region: "us-east-1",
		Edges:  []Edge{{To: "n2", Cost: Cost(10.0)}},
	})
	g.addNode(&Node{
		ID:     "n2",
		Region: "us-west-2",
		Edges:  []Edge{},
	})

	resp := g.ToResponse()

	if len(resp.Nodes) != 2 {
		t.Fatalf("expected 2 nodes in response, got %d", len(resp.Nodes))
	}

	// Verify adjacency map
	if len(resp.Adjacency) != 1 {
		t.Fatalf("expected 1 entry in adjacency map, got %d", len(resp.Adjacency))
	}
	if _, ok := resp.Adjacency["n1"]; !ok {
		t.Error("expected n1 in adjacency map")
	}
	if cost, ok := resp.Adjacency["n1"]["n2"]; !ok || cost != 10.0 {
		t.Errorf("expected adjacency[n1][n2] = 10.0, got %v", cost)
	}
}

func TestGraphResponse_JSON(t *testing.T) {
	g := newGraph()
	g.addNode(&Node{
		ID:     "nodeA",
		Region: "us-east-1",
		Edges:  []Edge{{To: "nodeB", Cost: Cost(15.5)}, {To: "nodeC", Cost: Cost(8.0)}},
	})
	g.addNode(&Node{
		ID:     "nodeB",
		Region: "us-west-2",
		Edges:  []Edge{{To: "nodeC", Cost: Cost(3.2)}},
	})
	g.addNode(&Node{
		ID:     "nodeC",
		Region: "eu-west-1",
		Edges:  []Edge{},
	})

	resp := g.ToResponse()

	// Marshal to JSON
	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("failed to marshal response: %v", err)
	}

	// Unmarshal back
	var decoded GraphResponse
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	// Verify adjacency structure
	if len(decoded.Adjacency) != 2 {
		t.Errorf("expected 2 entries in adjacency map after round-trip, got %d", len(decoded.Adjacency))
	}
	if decoded.Adjacency["nodeA"]["nodeB"] != 15.5 {
		t.Errorf("expected adjacency[nodeA][nodeB] = 15.5, got %v", decoded.Adjacency["nodeA"]["nodeB"])
	}
	if decoded.Adjacency["nodeA"]["nodeC"] != 8.0 {
		t.Errorf("expected adjacency[nodeA][nodeC] = 8.0, got %v", decoded.Adjacency["nodeA"]["nodeC"])
	}
	if decoded.Adjacency["nodeB"]["nodeC"] != 3.2 {
		t.Errorf("expected adjacency[nodeB][nodeC] = 3.2, got %v", decoded.Adjacency["nodeB"]["nodeC"])
	}
}
