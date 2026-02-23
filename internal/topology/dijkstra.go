package topology

import (
	"container/heap"
	"errors"
	"math"
)

// errNodeNotFound is returned when the requested node does not exist in the graph.
var errNodeNotFound = errors.New("node not found")

// errNoPath is returned when there is no path between two nodes.
var errNoPath = errors.New("no path between nodes")

// shortestPath computes the shortest path from src to dst using Dijkstra's algorithm.
// Returns the ordered list of node IDs along the path and the total cost.
func shortestPath(g *Graph, src, dst string) ([]string, Cost, error) {
	if _, ok := g.Nodes[src]; !ok {
		return nil, 0, errNodeNotFound
	}
	if _, ok := g.Nodes[dst]; !ok {
		return nil, 0, errNodeNotFound
	}

	dist := make(map[string]Cost, len(g.Nodes))
	prev := make(map[string]string, len(g.Nodes))

	for id := range g.Nodes {
		dist[id] = Cost(math.Inf(1))
	}
	dist[src] = Cost(0)

	pq := &priorityQueue{}
	heap.Init(pq)
	heap.Push(pq, &pqItem{nodeID: src, cost: Cost(0)})

	for pq.Len() > 0 {
		item := heap.Pop(pq).(*pqItem)
		u := item.nodeID

		if u == dst {
			break
		}

		if item.cost > dist[u] {
			continue // stale entry
		}

		node := g.Nodes[u]
		for _, edge := range node.Edges {
			alt := dist[u] + edge.Cost
			if alt < dist[edge.To] {
				dist[edge.To] = alt
				prev[edge.To] = u
				heap.Push(pq, &pqItem{nodeID: edge.To, cost: alt})
			}
		}
	}

	if math.IsInf(float64(dist[dst]), 1) {
		return nil, 0, errNoPath
	}

	// Reconstruct path
	path := []string{}
	for at := dst; at != ""; at = prev[at] {
		path = append(path, at)
		if at == src {
			break
		}
	}
	// Reverse
	for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
		path[i], path[j] = path[j], path[i]
	}

	return path, dist[dst], nil
}

// --- priority queue for Dijkstra ---

type pqItem struct {
	nodeID string
	cost   Cost
	index  int
}

type priorityQueue []*pqItem

func (pq priorityQueue) Len() int           { return len(pq) }
func (pq priorityQueue) Less(i, j int) bool { return pq[i].cost < pq[j].cost }
func (pq priorityQueue) Swap(i, j int) {
	pq[i], pq[j] = pq[j], pq[i]
	pq[i].index = i
	pq[j].index = j
}
func (pq *priorityQueue) Push(x any) {
	item := x.(*pqItem)
	item.index = len(*pq)
	*pq = append(*pq, item)
}
func (pq *priorityQueue) Pop() any {
	old := *pq
	n := len(old)
	item := old[n-1]
	old[n-1] = nil
	item.index = -1
	*pq = old[:n-1]
	return item
}
