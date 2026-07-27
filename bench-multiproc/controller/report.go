package controller

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// CellResult is the complete result of one (P, X) experiment cell.
type CellResult struct {
	P              int     `json:"P"`
	X              int     `json:"X"`
	TotalSubs      int     `json:"total_subs"`
	Connected      int     `json:"connected"`
	Receiving      int     `json:"receiving"`
	AggCPUS        float64 `json:"agg_cpu_s"`
	AggEgressBytes int64   `json:"agg_egress_bytes"`
	PeakRSSMB      float64 `json:"peak_rss_mb"`
	HubCPUS        float64 `json:"hub_cpu_s"`
	HubSessions    float64 `json:"hub_sessions"`
	AllEdgesActive bool    `json:"all_edges_active"`
	Sustained      bool    `json:"sustained"`
	StopReasons    string  `json:"stop_reasons"`
	WallDuration   string  `json:"wall_s"`

	Hub    *RelayMetrics   `json:"hub"`
	Edges  []*RelayMetrics `json:"edges"`
}

// RelayMetrics holds the parsed metrics for one relay process.
type RelayMetrics struct {
	CPUDeltaS   float64 `json:"cpu_delta_s"`
	RSSMB       float64 `json:"rss_mb"`
	HeapMB      float64 `json:"heap_mb"`
	Goros       float64 `json:"goros"`
	Sessions    float64 `json:"sessions"`
	EgressBytes int64   `json:"egress_bytes"`
	GCMaxMS     float64 `json:"gc_max_ms"`
	GCCount     int64   `json:"gc_count"`
	GCCPUS      float64 `json:"gc_cpu_s"`
	Connected   int     `json:"connected,omitempty"`
	Receiving   int     `json:"receiving,omitempty"`
}

// EdgeMap returns a map of edge index to RelayMetrics for easy access.
func (r *CellResult) EdgeMap() map[int]*RelayMetrics {
	m := make(map[int]*RelayMetrics, len(r.Edges))
	for i, e := range r.Edges {
		m[i] = e
	}
	return m
}

// ToJSON returns the JSON representation of the result.
func (r *CellResult) ToJSON() (string, error) {
	data, err := json.Marshal(r)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// AppendJSONL appends one JSON line to results.jsonl in the given directory.
func (r *CellResult) AppendJSONL(dir string) error {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("mkdir %q: %w", dir, err)
	}
	f, err := os.OpenFile(filepath.Join(dir, "results.jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open results.jsonl: %w", err)
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	enc.SetEscapeHTML(false)
	return enc.Encode(r)
}

// PrintTable prints a human-readable summary table of a sweep's results.
func PrintTable(results []*CellResult) {
	fmt.Println()
	fmt.Println("============================================================")
	fmt.Println("  Hub+Edge relay experiment — Results Summary")
	fmt.Println("============================================================")
	fmt.Println()
	fmt.Printf("%-4s %-5s %-6s %-6s %-6s %-8s %-7s %-7s %-6s %-6s %s\n",
		"P", "X", "total", "conn", "recv%", "cpu_s", "rssMB", "egrMB", "hubCPU", "edges?", "status")
	fmt.Println("---- ----- ------ ------ ------ -------- ------- ------- ------ ------ ----------")

	for _, r := range results {
		recvPct := "?"
		if r.Connected > 0 {
			recvPct = fmt.Sprintf("%d%%", r.Receiving*100/r.Connected)
		}
		egrMB := float64(r.AggEgressBytes) / 1_000_000
		edgeStatus := "all"
		if !r.AllEdgesActive {
			edgeStatus = "MISSING"
		}
		status := "PASS"
		if !r.Sustained {
			status = "NO(" + r.StopReasons + ")"
		}

		fmt.Printf("%-4d %-5d %-6d %-6d %-6s %-8.2f %-7.1f %-7.0f %-6.2f %-6s %s\n",
			r.P, r.X, r.TotalSubs, r.Connected, recvPct,
			r.AggCPUS, r.PeakRSSMB, egrMB,
			r.HubCPUS, edgeStatus, status)
	}
	fmt.Println()
}

// PrintScalingSummary prints the scaling efficiency analysis.
func PrintScalingSummary(results []*CellResult) {
	// Group by P to find max passing X per P.
	type pEntry struct {
		P       int
		MaxTot  int
		MaxX    int
	}
	byP := make(map[int]*pEntry)
	for _, r := range results {
		if r.Sustained {
			e, ok := byP[r.P]
			if !ok || r.TotalSubs > e.MaxTot {
				byP[r.P] = &pEntry{P: r.P, MaxTot: r.TotalSubs, MaxX: r.X}
			}
		}
	}

	fmt.Println()
	fmt.Println("============================================================")
	fmt.Println("  Per-edge ceiling (max X that holds per P)")
	fmt.Println("============================================================")
	fmt.Printf("%-6s %-10s %-12s %-10s\n", "P", "X/edge", "total_subs", "scaling")
	fmt.Println("------ ---------- ------------ ----------")

	p1Tot := 0
	if e, ok := byP[1]; ok {
		p1Tot = e.MaxTot
	}
	for _, p := range []int{1, 2, 3, 4} {
		e, ok := byP[p]
		if !ok {
			continue
		}
		ratio := "?"
		if p1Tot > 0 {
			ratio = fmt.Sprintf("%.2fx", float64(e.MaxTot)/float64(p1Tot))
		}
		fmt.Printf("%-6d %-10d %-12d %-10s\n", p, e.MaxX, e.MaxTot, ratio)
	}
	fmt.Println()
	fmt.Println("  If total_subs scales linearly with P → process scaling works")
	fmt.Println("  If total_subs plateaus → shared infrastructure bottleneck")
	fmt.Println("  If hub_cpu_s is high for low P → hub is the bottleneck")
	fmt.Println("  If edges_active=false → topology broken (not fanning out)")
	fmt.Println()
}

// ShowTiming prints the wall duration of the experiment.
func ShowTiming(start time.Time) {
	fmt.Printf("  wall: %s\n", time.Since(start).Round(time.Second))
}
