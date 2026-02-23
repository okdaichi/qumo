package topology

import (
	"testing"
)

func TestShortestPath_Direct(t *testing.T) {
	g := newGraph()
	g.addNode(&Node{ID: "A", Edges: []Edge{{To: "B", Cost: Cost(5)}}})
	g.addNode(&Node{ID: "B", Edges: []Edge{}})

	path, cost, err := shortestPath(g, "A", "B")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cost != Cost(5.0) {
		t.Errorf("expected cost 5.0, got %f", float64(cost))
	}
	if len(path) != 2 || path[0] != "A" || path[1] != "B" {
		t.Errorf("unexpected path: %v", path)
	}
}

func TestShortestPath_MultiHop(t *testing.T) {
	g := newGraph()
	g.addNode(&Node{ID: "A", Edges: []Edge{
		{To: "B", Cost: Cost(10)},
		{To: "C", Cost: Cost(3)},
	}})
	g.addNode(&Node{ID: "B", Edges: []Edge{}})
	g.addNode(&Node{ID: "C", Edges: []Edge{
		{To: "B", Cost: Cost(2)},
	}})

	path, cost, err := shortestPath(g, "A", "B")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// A → C → B = 3 + 2 = 5, which is cheaper than A → B = 10
	if cost != Cost(5.0) {
		t.Errorf("expected cost 5.0, got %f", float64(cost))
	}
	if len(path) != 3 || path[0] != "A" || path[1] != "C" || path[2] != "B" {
		t.Errorf("unexpected path: %v", path)
	}
}

func TestShortestPath_SameNode(t *testing.T) {
	g := newGraph()
	g.addNode(&Node{ID: "A", Edges: []Edge{}})

	path, cost, err := shortestPath(g, "A", "A")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cost != Cost(0) {
		t.Errorf("expected cost 0, got %f", float64(cost))
	}
	if len(path) != 1 || path[0] != "A" {
		t.Errorf("unexpected path: %v", path)
	}
}

func TestShortestPath_Unreachable(t *testing.T) {
	g := newGraph()
	g.addNode(&Node{ID: "A", Edges: []Edge{}})
	g.addNode(&Node{ID: "B", Edges: []Edge{}})

	_, _, err := shortestPath(g, "A", "B")
	if err != errNoPath {
		t.Errorf("expected errNoPath, got %v", err)
	}
}

func TestShortestPath_NodeNotFound(t *testing.T) {
	g := newGraph()
	g.addNode(&Node{ID: "A", Edges: []Edge{}})

	_, _, err := shortestPath(g, "A", "Z")
	if err != errNodeNotFound {
		t.Errorf("expected errNodeNotFound, got %v", err)
	}

	_, _, err = shortestPath(g, "Z", "A")
	if err != errNodeNotFound {
		t.Errorf("expected errNodeNotFound, got %v", err)
	}
}

func TestShortestPath_Complex(t *testing.T) {
	// Diamond graph:
	//     A
	//    / \
	//   B   C
	//    \ /
	//     D
	g := newGraph()
	g.addNode(&Node{ID: "A", Edges: []Edge{
		{To: "B", Cost: Cost(1)},
		{To: "C", Cost: Cost(4)},
	}})
	g.addNode(&Node{ID: "B", Edges: []Edge{
		{To: "D", Cost: Cost(6)},
	}})
	g.addNode(&Node{ID: "C", Edges: []Edge{
		{To: "D", Cost: Cost(1)},
	}})
	g.addNode(&Node{ID: "D", Edges: []Edge{}})

	path, cost, err := shortestPath(g, "A", "D")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// A → C → D = 4 + 1 = 5 is cheaper than A → B → D = 1 + 6 = 7
	if cost != Cost(5.0) {
		t.Errorf("expected cost 5.0, got %f", float64(cost))
	}
	if len(path) != 3 || path[0] != "A" || path[1] != "C" || path[2] != "D" {
		t.Errorf("unexpected path: %v", path)
	}
}
