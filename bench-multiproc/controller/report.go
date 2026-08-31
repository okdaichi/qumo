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

	Hub   *RelayMetrics   `json:"hub"`
	Edges []*RelayMetrics `json:"edges"`

	// E2E latency percentiles (publisher→subscriber, milliseconds), populated
	// when --latency-probe is enabled during the run. Measured from the
	// publisher's embedded UnixNano timestamp in frame bytes [8:16].
	LatencySamples int     `json:"latency_samples,omitempty"`
	LatencyP50Ms   float64 `json:"latency_p50_ms,omitempty"`
	LatencyP95Ms   float64 `json:"latency_p95_ms,omitempty"`
	LatencyP99Ms   float64 `json:"latency_p99_ms,omitempty"`
	LatencyMinMs   float64 `json:"latency_min_ms,omitempty"`
	LatencyMaxMs   float64 `json:"latency_max_ms,omitempty"`
	LatencyMeanMs  float64 `json:"latency_mean_ms,omitempty"`
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

	// Subscriber-pipeline metrics.
	SubscribersActive float64 `json:"subs_active,omitempty"` // qumo_relay_subscribers_active (relay gauge)
	SubscriberSkips   int64   `json:"subs_skips,omitempty"`  // qumo_relay_subscriber_skips_total (relay count)
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

// PrintEdgeDistribution prints a per-edge metrics breakdown to verify that
// traffic is evenly distributed across all edge processes in each cell.
func PrintEdgeDistribution(results []*CellResult) {
	fmt.Println()
	fmt.Println("============================================================")
	fmt.Println("  Per-Edge Traffic Distribution")
	fmt.Println("============================================================")
	fmt.Println("  For each cell, the per-edge breakdown below verifies that every")
	fmt.Println("  edge process participates in forwarding. If any edge shows zero")
	fmt.Println("  connected subscribers or zero egress bytes, the experiment is")
	fmt.Println("  invalid — that edge is idle.")
	fmt.Println()

	for _, r := range results {
		if len(r.Edges) == 0 {
			continue
		}
		fmt.Printf("  P=%d X=%d total=%d conn=%d — Edge Breakdown:\n",
			r.P, r.X, r.TotalSubs, r.Connected)

		// Column header.
		fmt.Printf("    %-6s %-7s %-7s %-8s %-8s %-7s %-8s %-6s\n",
			"edge", "subs-act", "conn", "recv", "egrMB", "cpu_s", "rssMB", "skips")
		fmt.Printf("    %-6s %-7s %-7s %-8s %-8s %-7s %-8s %-6s\n",
			"------", "-------", "-------", "--------", "--------", "-------", "--------", "------")

		subCounts := make([]int, len(r.Edges))
		for i, e := range r.Edges {
			if e == nil {
				fmt.Printf("    %-6d %-7s %-7s %-8s %-8s %-7s %-8s %-6s\n",
					i, "—", "—", "—", "—", "—", "—", "—")
				continue
			}
			egrMB := float64(e.EgressBytes) / 1_000_000
			connStr := fmt.Sprintf("%d", e.Connected)
			recvStr := fmt.Sprintf("%d", e.Receiving)
			subAct := fmt.Sprintf("%.0f", e.SubscribersActive)
			cpuStr := fmt.Sprintf("%.2f", e.CPUDeltaS)
			rssStr := fmt.Sprintf("%.0f", e.RSSMB)
			skipStr := fmt.Sprintf("%d", e.SubscriberSkips)

			// Mark zero egress or zero subscribers as MISSING.
			if e.EgressBytes == 0 || e.Receiving == 0 {
				connStr = "IDLE"
				recvStr = "IDLE"
				egrMB = 0
			}

			fmt.Printf("    %-6d %-7s %-7s %-8s %-8.2f %-7s %-8s %-6s\n",
				i, subAct, connStr, recvStr, egrMB, cpuStr, rssStr, skipStr)
			subCounts[i] = e.Connected
		}

		// Balance analysis: check if one edge is overloaded vs another.
		mean, minSubs, maxSubs := summarize(subCounts)
		if mean > 0 {
			imbalance := float64(maxSubs-minSubs) / float64(mean) * 100
			fmt.Printf("    → mean-edge: %.0f subs, range: %d–%d, imbalance: %.1f%%\n",
				mean, minSubs, maxSubs, imbalance)
			if imbalance > 20 {
				fmt.Printf("    ⚠  Imbalance >20%% indicates an edge is a bottleneck.\n")
			} else {
				fmt.Printf("    ✅ Distribution is balanced (imbalance <20%%).\n")
			}
		}
		fmt.Println()
	}
}

// summarize returns the mean, min, and max of a non-empty int slice.
func summarize(vals []int) (mean float64, min, max int) {
	if len(vals) == 0 {
		return 0, 0, 0
	}
	min = vals[0]
	max = vals[0]
	sum := 0
	for _, v := range vals {
		if v < min {
			min = v
		}
		if v > max {
			max = v
		}
		sum += v
	}
	return float64(sum) / float64(len(vals)), min, max
}

// PrintScalingSummary prints the scaling efficiency analysis.
// Uses the spec definition: ScalingEfficiency = Connected / (P × Max(P=1)).
// If refMaxP1 > 0, it uses that as the baseline instead of auto-detecting from
// the P=1 cells in results (useful when results don't contain P=1 cells).
func PrintScalingSummary(results []*CellResult, refMaxP1 int) {
	// Group by P to find max passing X per P.
	type pEntry struct {
		P       int
		MaxTot  int
		MaxX    int
		MaxConn int
	}
	byP := make(map[int]*pEntry)
	for _, r := range results {
		if r.Sustained {
			e, ok := byP[r.P]
			if !ok || r.Connected > e.MaxConn {
				byP[r.P] = &pEntry{P: r.P, MaxTot: r.TotalSubs, MaxX: r.X, MaxConn: r.Connected}
			}
		}
	}

	// Determine Max(P=1): prefer the caller-supplied value, fall back to
	// auto-detecting from P=1 cells in the sweep results.
	maxP1 := refMaxP1
	if maxP1 <= 0 {
		if e, ok := byP[1]; ok {
			maxP1 = e.MaxConn
		}
	}

	fmt.Println()
	fmt.Println("============================================================")
	fmt.Println("  Scaling Efficiency Analysis")
	fmt.Println("============================================================")
	fmt.Println()
	fmt.Printf("  Baseline: Max(P=1) = %d subscribers per edge\n", maxP1)
	fmt.Printf("  Expected aggregate = P × %d\n", maxP1)
	fmt.Printf("  Efficiency = Connected / (P × Max(P=1))\n")
	fmt.Println()
	fmt.Printf("%-6s %-10s %-12s %-12s %-10s\n", "P", "X/edge", "total_subs", "connected", "efficiency")
	fmt.Println("------ ---------- ------------ ------------ ----------")

	for _, p := range []int{1, 2, 3, 4} {
		e, ok := byP[p]
		if !ok {
			continue
		}
		eff := "?"
		if maxP1 > 0 && p > 0 {
			expected := p * maxP1
			if expected > 0 {
				eff = fmt.Sprintf("%.1f%%", float64(e.MaxConn)/float64(expected)*100)
			}
		}
		fmt.Printf("%-6d %-10d %-12d %-12d %-10s\n", p, e.MaxX, e.MaxTot, e.MaxConn, eff)
	}

	fmt.Println()
	fmt.Println("  Ideal: efficiency ≈ 100% for all P (linear scaling)")
	fmt.Println("  If efficiency drops as P grows → shared infrastructure bottleneck")
	fmt.Println("  If efficiency < 50% at P=2 → relay code has a fundamental scaling issue")
	fmt.Println("  If hub_cpu_s is high for low P → hub is the bottleneck")
	fmt.Println()
}

// ShowTiming prints the wall duration of the experiment.
func ShowTiming(start time.Time) {
	fmt.Printf("  wall: %s\n", time.Since(start).Round(time.Second))
}
