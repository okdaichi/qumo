package topology

import (
	"log/slog"
	"sync"
)

// Router abstracts the path-computation algorithm.
// Implementations are swappable (Dijkstra, k-shortest, constraint-based, etc.).
type Router interface {
	// Route computes a path from src to dst on the given graph snapshot.
	Route(g *Graph, from, to string) (RouteResult, error)
}

func NewDijkstraRouter() Router {
	return &dijkstraRouter{}
}

// dijkstraRouter implements Router using Dijkstra's shortest path algorithm.
type dijkstraRouter struct{}

// Route computes the shortest path from src to dst using Dijkstra's algorithm.
func (d *dijkstraRouter) Route(g *Graph, from, to string) (RouteResult, error) {
	path, cost, err := shortestPath(g, from, to)
	if err != nil {
		return RouteResult{}, err
	}

	nextHop := from
	if len(path) >= 2 {
		nextHop = path[1]
	}

	return RouteResult{
		From:     from,
		To:       to,
		NextHop:  nextHop,
		FullPath: path,
		Cost:     float64(cost),
	}, nil
}

// RelayInfo is the payload a relay sends when registering.
// Neighbors maps neighbor relay name → edge cost.
// If cost is 0 or omitted, default weight 1 is applied.
type RelayInfo struct {
	Name      string             `json:"name"`
	Region    string             `json:"region,omitempty"`
	Address   string             `json:"address,omitempty"` // MoQT endpoint URL (e.g. "https://host:4433")
	Neighbors map[string]float64 `json:"neighbors"`
}

// RouteResult is the response for a route query.
type RouteResult struct {
	From           string   `json:"from"`
	To             string   `json:"to"`
	NextHop        string   `json:"next_hop"`
	NextHopAddress string   `json:"next_hop_address,omitempty"` // MoQT endpoint URL of next_hop
	FullPath       []string `json:"full_path"`
	Cost           float64  `json:"cost"`
}

// Topology maintains an in-memory directed graph of relays.
// Relays push their state via PUT /relay/<name>; the controller
// never polls (SDN model).
//
// Thread-safe: all mutations go through the write lock.
// Zero-value is safe to use (Router defaults to Dijkstra, Store is optional).
//
// Example:
//
//	topo := &Topology{
//	  Router: NewDijkstraRouter(),
//	  Store:  NewFileStore("topology.json"),
//	}
type Topology struct {
	// Router computes paths. If nil, uses default Dijkstra implementation.
	Router Router

	// Store persists topology snapshots. Optional (nil = no persistence).
	Store Store

	mu       sync.RWMutex
	graph    *Graph
	initOnce sync.Once
}

// Register adds or updates a relay and its edges.
// Each call replaces the previous neighbor set for this relay.
// Edges use the cost from the registration payload; 0/omitted defaults to 1.
func (t *Topology) Register(reg RelayInfo) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.init()

	// Ensure the node exists.
	node, ok := t.graph.Nodes[reg.Name]
	if !ok {
		node = &Node{
			ID:    reg.Name,
			Edges: make([]Edge, 0, len(reg.Neighbors)),
		}
		t.graph.addNode(node)
	}

	// Update region if provided.
	if reg.Region != "" {
		node.Region = reg.Region
	}

	// Update address if provided.
	if reg.Address != "" {
		node.Address = reg.Address
	}

	// Replace edge list with new neighbors and their costs.
	node.Edges = make([]Edge, 0, len(reg.Neighbors))
	for nb, cost := range reg.Neighbors {
		if cost <= 0 {
			cost = 1 // default weight
		}
		// Auto-create neighbor node if not yet registered.
		if _, exists := t.graph.Nodes[nb]; !exists {
			t.graph.addNode(&Node{
				ID:    nb,
				Edges: []Edge{},
			})
		}
		node.Edges = append(node.Edges, Edge{To: nb, Cost: Cost(cost)})
	}

	t.save()
}

// Deregister removes a relay and all edges pointing to it.
func (t *Topology) Deregister(name string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.init()

	if _, ok := t.graph.Nodes[name]; !ok {
		return false
	}

	// Remove node.
	delete(t.graph.Nodes, name)

	// Remove dangling edges from other nodes.
	for _, node := range t.graph.Nodes {
		filtered := node.Edges[:0]
		for _, e := range node.Edges {
			if e.To != name {
				filtered = append(filtered, e)
			}
		}
		node.Edges = filtered
	}

	t.save()
	return true
}

// Route computes the shortest path from src to dst using the configured Router.
// The returned RouteResult includes NextHopAddress if the next-hop node has a
// registered address.
func (t *Topology) Route(from, to string) (RouteResult, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	t.init()

	router := t.Router
	if router == nil {
		router = NewDijkstraRouter()
	}
	result, err := router.Route(t.graph, from, to)
	if err != nil {
		return result, err
	}

	// Populate NextHopAddress from the graph.
	if nh, ok := t.graph.Nodes[result.NextHop]; ok {
		result.NextHopAddress = nh.Address
	}

	return result, nil
}

// Snapshot returns a deep copy of the current graph for safe read access.
func (t *Topology) Snapshot() *Graph {
	t.mu.RLock()
	defer t.mu.RUnlock()

	t.init()

	return t.deepCopy()
}

// deepCopy creates a deep copy of the graph. Caller must hold at least a read lock.
func (t *Topology) deepCopy() *Graph {
	cp := newGraph()
	for id, node := range t.graph.Nodes {
		cpNode := &Node{
			ID:      node.ID,
			Region:  node.Region,
			Address: node.Address,
			Edges:   make([]Edge, len(node.Edges)),
		}
		copy(cpNode.Edges, node.Edges)
		cp.Nodes[id] = cpNode
	}
	return cp
}

// Restore replaces the current graph with the given snapshot.
// Used for HA sync from a peer controller.
func (t *Topology) Restore(g *Graph) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.graph = g
	t.save()
}

// init initializes the graph and restores from store if needed.
// Must be called with at least a read lock held.
func (t *Topology) init() {
	if t.graph == nil {
		t.graph = newGraph()
	}

	// Restore from store once on first access
	t.initOnce.Do(func() {
		if t.Store != nil {
			if g, err := t.Store.Load(); err == nil && g != nil {
				t.graph = g
				slog.Info("topology restored from store", "nodes", len(g.Nodes))
			}
		}
	})
}

// save persists the current graph to the store (if configured).
// Caller must hold at least a write lock.
func (t *Topology) save() {
	if t.Store == nil {
		return
	}
	if err := t.Store.Save(t.graph); err != nil {
		slog.Error("failed to save topology", "error", err)
	}
}
