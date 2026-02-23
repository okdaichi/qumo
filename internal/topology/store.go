package topology

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Store abstracts topology persistence.
// Implementations can target files, databases, etcd, etc.
type Store interface {
	// Save persists the entire graph.
	Save(g *Graph) error

	// Load restores the graph. Returns (nil, nil) if no data exists yet.
	Load() (*Graph, error)
}

// --- FileStore: JSON file-based implementation ---

// FileStore persists the topology as a JSON file on disk.
// Suitable for single-node deployments and development.
type FileStore struct {
	Path string
}

// NewFileStore creates a FileStore that writes to the given path.
func NewFileStore(path string) *FileStore {
	return &FileStore{Path: path}
}

// persistNode is the JSON-serializable representation of a node.
type persistNode struct {
	ID     string        `json:"id"`
	Region string        `json:"region,omitempty"`
	Edges  []persistEdge `json:"edges,omitempty"`
}

// persistEdge is the JSON-serializable representation of an edge.
type persistEdge struct {
	To   string  `json:"to"`
	Cost float64 `json:"cost"`
}

// persistGraph is the top-level JSON structure written to disk.
type persistGraph struct {
	Nodes []persistNode `json:"nodes"`
}

// Save writes the graph to the JSON file atomically (write-then-rename).
func (s *FileStore) Save(g *Graph) error {
	pg := persistGraph{
		Nodes: make([]persistNode, 0, len(g.Nodes)),
	}
	for _, n := range g.Nodes {
		pn := persistNode{
			ID:     n.ID,
			Region: n.Region,

			Edges: make([]persistEdge, len(n.Edges)),
		}
		for i, e := range n.Edges {
			pn.Edges[i] = persistEdge{To: e.To, Cost: float64(e.Cost)}
		}
		pg.Nodes = append(pg.Nodes, pn)
	}

	data, err := json.MarshalIndent(pg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal topology: %w", err)
	}

	// Atomic write: write temp file, then rename.
	dir := filepath.Dir(s.Path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create dir %s: %w", dir, err)
	}

	tmp := s.Path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := os.Rename(tmp, s.Path); err != nil {
		return fmt.Errorf("rename temp file: %w", err)
	}
	return nil
}

// Load reads the graph from the JSON file.
// Returns (nil, nil) if the file does not exist.
func (s *FileStore) Load() (*Graph, error) {
	data, err := os.ReadFile(s.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // no previous state
		}
		return nil, fmt.Errorf("read topology file: %w", err)
	}

	var pg persistGraph
	if err := json.Unmarshal(data, &pg); err != nil {
		return nil, fmt.Errorf("unmarshal topology: %w", err)
	}

	g := newGraph()
	for _, pn := range pg.Nodes {
		node := &Node{
			ID:     pn.ID,
			Region: pn.Region,

			Edges: make([]Edge, len(pn.Edges)),
		}
		for i, pe := range pn.Edges {
			node.Edges[i] = Edge{To: pe.To, Cost: Cost(pe.Cost)}
		}
		g.addNode(node)
	}

	return g, nil
}
