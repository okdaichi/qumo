package topology

// Graph represents a topology graph using adjacency lists.
// Nodes are indexed by their ID for O(1) lookup.
type Graph struct {
	Nodes map[string]*Node
}

// newGraph creates an empty graph.
func newGraph() *Graph {
	return &Graph{
		Nodes: make(map[string]*Node),
	}
}

// addNode inserts or updates a node in the graph.
func (g *Graph) addNode(n *Node) {
	g.Nodes[n.ID] = n
}

// addEdge adds a directed edge from src to dst with the given cost.
func (g *Graph) addEdge(src, dst string, cost Cost) {
	node, ok := g.Nodes[src]
	if !ok {
		return
	}
	// Avoid duplicate edges
	for i, e := range node.Edges {
		if e.To == dst {
			node.Edges[i].Cost = cost
			return
		}
	}
	node.Edges = append(node.Edges, Edge{To: dst, Cost: cost})
}

// Node represents a relay node in the topology.
type Node struct {
	ID      string `json:"id"`
	Region  string `json:"region"`
	Address string `json:"address,omitempty"` // MoQT endpoint URL
	Edges   []Edge `json:"edges"`
}

// Edge represents a directed connection to another node.
type Edge struct {
	To   string `json:"to"`
	Cost Cost   `json:"cost"`
}

// GraphResponse is the JSON response for the gateway GET /graph endpoint.
type GraphResponse struct {
	Nodes     []NodeResponse                `json:"nodes"`
	Adjacency map[string]map[string]float64 `json:"adjacency"`
}

// NodeResponse is a node in the graph response.
type NodeResponse struct {
	ID      string `json:"id"`
	Region  string `json:"region"`
	Address string `json:"address,omitempty"`
}

// ToResponse converts the graph into a flat response structure.
func (g *Graph) ToResponse() GraphResponse {
	resp := GraphResponse{
		Nodes:     make([]NodeResponse, 0, len(g.Nodes)),
		Adjacency: make(map[string]map[string]float64),
	}

	for _, n := range g.Nodes {
		resp.Nodes = append(resp.Nodes, NodeResponse{
			ID:      n.ID,
			Region:  n.Region,
			Address: n.Address,
		})

		// Build adjacency map (efficient for Dijkstra/routing)
		if len(n.Edges) > 0 {
			resp.Adjacency[n.ID] = make(map[string]float64)
			for _, e := range n.Edges {
				resp.Adjacency[n.ID][e.To] = float64(e.Cost)
			}
		}
	}

	return resp
}

// FromResponse reconstructs a Graph from a GraphResponse.
// Used for HA sync: import a snapshot exported by a peer controller.
func FromResponse(resp GraphResponse) *Graph {
	g := newGraph()

	// Create nodes.
	for _, nr := range resp.Nodes {
		g.addNode(&Node{
			ID:      nr.ID,
			Region:  nr.Region,
			Address: nr.Address,
			Edges:   []Edge{},
		})
	}

	// Populate edges from adjacency map.
	for src, neighbors := range resp.Adjacency {
		node, ok := g.Nodes[src]
		if !ok {
			// Node in adjacency but not in nodes list — create it.
			node = &Node{ID: src, Edges: []Edge{}}
			g.addNode(node)
		}
		for dst, cost := range neighbors {
			node.Edges = append(node.Edges, Edge{To: dst, Cost: Cost(cost)})
		}
	}

	return g
}
