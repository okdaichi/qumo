package controller

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// RelaySnapshot holds the metrics scraped from a relay's /metrics endpoint.
type RelaySnapshot struct {
	// Process metrics.
	Goroutines    float64
	RSSBytes      float64
	CPUSeconds    float64 // process_cpu_seconds_total

	// Go runtime metrics.
	HeapAllocBytes    float64
	GCDurationSecSum  float64 // go_gc_duration_seconds_sum
	GCDurationCount   float64 // go_gc_duration_seconds_count
	GCDurationMax     float64 // go_gc_duration_seconds{quantile="1"}

	// Relay-specific metrics.
	SessionsActive    float64 // qumo_relay_sessions_active
	SubscribersActive float64 // qumo_relay_subscribers_active
	SubscriberSkips   float64 // qumo_relay_subscriber_skips_total
	EgressBytesTotal  float64 // qumo_relay_egress_bytes_total (sum across all tracks)
	PeersConnected    float64 // qumo_relay_peers_connected
	BroadcastsActive  float64 // qumo_relay_broadcasts_active
}

// ScrapeRelay fetches and parses the /metrics endpoint of a relay process.
func ScrapeRelay(ctx context.Context, port int) (*RelaySnapshot, error) {
	body, err := fetchMetricsBody(ctx, port)
	if err != nil {
		return nil, fmt.Errorf("scrape port %d: %w", port, err)
	}

	return parseRelayMetrics(string(body))
}

// parseRelayMetrics extracts all tracked metrics from a Prometheus text exposition.
func parseRelayMetrics(text string) (*RelaySnapshot, error) {
	var snap RelaySnapshot
	found := 0

	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line[0] == '#' {
			continue
		}

		// Handle histogram/quantile metrics (label suffix after the name).
		// We look for specific metric names with optional label matchers.
		name, val, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}

		f, err := strconv.ParseFloat(strings.TrimSpace(val), 64)
		if err != nil {
			continue
		}

		switch {
		case name == "go_goroutines":
			snap.Goroutines = f
			found++
		case name == "process_resident_memory_bytes":
			snap.RSSBytes = f
			found++
		case name == "process_cpu_seconds_total":
			snap.CPUSeconds = f
			found++
		case name == "go_memstats_heap_alloc_bytes":
			snap.HeapAllocBytes = f
			found++
		case name == "qumo_relay_sessions_active":
			snap.SessionsActive = f
			found++
		case name == "qumo_relay_subscribers_active":
			snap.SubscribersActive = f
			found++
		case name == "qumo_relay_subscriber_skips_total":
			snap.SubscriberSkips = f
			found++
		case name == "qumo_relay_peers_connected":
			snap.PeersConnected = f
			found++
		case name == "qumo_relay_broadcasts_active":
			snap.BroadcastsActive = f
			found++
		case strings.HasPrefix(name, "qumo_relay_egress_bytes_total{"):
			// Per-track egress bytes, e.g. qumo_relay_egress_bytes_total{track="/bench/carry/data"}
			snap.EgressBytesTotal += f
			found++
		case strings.HasPrefix(name, "go_gc_duration_seconds_sum"):
			snap.GCDurationSecSum = f
			found++
		case strings.HasPrefix(name, "go_gc_duration_seconds_count"):
			snap.GCDurationCount = f
			found++
		case strings.HasPrefix(name, "go_gc_duration_seconds{") && strings.Contains(name, `quantile="1"`):
			snap.GCDurationMax = f
			found++
		case strings.HasPrefix(name, "qumo_relay_egress_bytes_total"):
			snap.EgressBytesTotal += f
			found++
		}
	}

	if found == 0 {
		return nil, fmt.Errorf("no expected metrics found in exposition")
	}
	return &snap, nil
}
