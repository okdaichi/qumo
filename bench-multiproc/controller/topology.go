package controller

import (
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
)

// RelayNode describes one relay process in the topology.
type RelayNode struct {
	// Index: -1 for hub, 0..P-1 for edges.
	Index int
	// Name is the RELAY_NAME env var value (e.g. "hub-P3", "edge0-P3").
	Name string
	// Port is the listen port for this relay.
	Port int
	// IsHub is true for the hub relay.
	IsHub bool
	// PeerAddr is the address this relay should peer-connect to (hub addr; empty for hub).
	PeerAddr string
}

// Topology holds all relay nodes and derived information.
type Topology struct {
	Hub   *RelayNode
	Edges []*RelayNode
	Dir   string // working directory (cert files live here)
	Cfg   *Config
}

// BuildTopology constructs a Topology from a Config.
func BuildTopology(cfg *Config) *Topology {
	hub := &RelayNode{
		Index:    -1,
		Name:     fmt.Sprintf("hub-P%d", cfg.P),
		Port:     cfg.HubPort,
		IsHub:    true,
		PeerAddr: "",
	}
	edges := make([]*RelayNode, cfg.P)
	for i := range edges {
		edges[i] = &RelayNode{
			Index:    i,
			Name:     fmt.Sprintf("edge%d-P%d", i, cfg.P),
			Port:     cfg.EdgePort(i),
			IsHub:    false,
			PeerAddr: fmt.Sprintf("127.0.0.1:%d", cfg.HubPort),
		}
	}
	return &Topology{
		Hub:   hub,
		Edges: edges,
		Cfg:   cfg,
	}
}

// AllRelays returns all relay nodes (hub first, then edges).
func (top *Topology) AllRelays() []*RelayNode {
	out := make([]*RelayNode, 0, 1+len(top.Edges))
	out = append(out, top.Hub)
	for i := range top.Edges {
		out = append(out, top.Edges[i])
	}
	return out
}

// findCoreCount returns the number of CPUs available by counting "processor"
// lines in /proc/cpuinfo, falling back to GOMAXPROCS. Returns at least 1.
func findCoreCount() int {
	data, err := os.ReadFile("/proc/cpuinfo")
	if err == nil {
		count := 0
		for _, line := range strings.Split(string(data), "\n") {
			// Linux /proc/cpuinfo uses "processor\t: N"
			if strings.HasPrefix(line, "processor\t") || strings.HasPrefix(line, "processor ") {
				count++
			}
		}
		if count > 0 {
			return count
		}
	}
	if s := os.Getenv("GOMAXPROCS"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			return n
		}
	}
	return 1
}

// CoreRange returns the taskset-compatible CPU range for this relay.
// Core allocation: 60% of cores for relays, 40% for load generators.
func (top *Topology) CoreRange(n *RelayNode) string {
	totalCores := findCoreCount()
	relayCores := int(math.Max(float64(totalCores*6/10), float64(top.Cfg.TotalRelays())))
	total := top.Cfg.TotalRelays()
	cpr := relayCores / total
	if cpr < 1 {
		cpr = 1
	}
	idx := n.Index + 1 // hub is 0, edges are 1..P
	if n.IsHub {
		idx = 0
	}
	start := idx * cpr
	end := start + cpr - 1
	if cpr == 1 {
		return strconv.Itoa(start)
	}
	return fmt.Sprintf("%d-%d", start, end)
}

// LoadGenMask returns the taskset mask for load generators (everything after
// relay cores).
func (top *Topology) LoadGenMask() string {
	totalCores := findCoreCount()
	relayCores := int(math.Max(float64(totalCores*6/10), float64(top.Cfg.TotalRelays())))
	if relayCores >= totalCores {
		return ""
	}
	return fmt.Sprintf("%d-%d", relayCores, totalCores-1)
}
