package controller

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// SubResult is the measured outcome of a subscriber group.
type SubResult struct {
	Connected      int
	Receiving      int
	TotalFrames    int64
	LatencySamples int     `json:"latency_samples,omitempty"`
	LatencyP50Ms   float64 `json:"latency_p50_ms,omitempty"`
	LatencyP95Ms   float64 `json:"latency_p95_ms,omitempty"`
	LatencyP99Ms   float64 `json:"latency_p99_ms,omitempty"`
	LatencyMinMs   float64 `json:"latency_min_ms,omitempty"`
	LatencyMaxMs   float64 `json:"latency_max_ms,omitempty"`
	LatencyMeanMs  float64 `json:"latency_mean_ms,omitempty"`
}

// SubscribeGroupSubprocess launches subscribers as out-of-process subprocesses
// using "qumo loadgen subscribe". This ensures client and server never share a
// Go runtime — the load generator runs in its own OS process.
// When collectLatency is true, it passes --latency to enable e2e frame timestamp
// latency measurement (adds one extra subscriber connection for sampling).
func SubscribeGroupSubprocess(ctx context.Context, qumoBin, relayAddr, caFile, path, track string, n int, hold time.Duration, collectLatency ...bool) (*SubResult, error) {
	args := []string{
		"loadgen", "subscribe",
		"--relay", relayAddr,
		"--ca", caFile,
		"--path", path,
		"--track", track,
		"--hold", hold.String(),
	}

	// Optional latency collection; must appear BEFORE the positional N arg
	// because Go's flag.Parse stops at the first non-flag argument.
	if len(collectLatency) > 0 && collectLatency[0] {
		args = append(args, "--latency")
	}

	args = append(args, strconv.Itoa(n))

	logFile := filepath.Join(os.TempDir(), fmt.Sprintf("subprocess_%d.log", time.Now().UnixNano()))
	f, err := os.Create(logFile)
	if err != nil {
		return nil, fmt.Errorf("create subprocess log: %w", err)
	}

	cmd := exec.CommandContext(ctx, qumoBin, args...)
	cmd.Stdout = f
	cmd.Stderr = f

	// not actionable: the process might have been killed by ctx cancellation;
	// parse whatever output was captured before termination.
	_ = cmd.Run()

	// This log file is the primary parse source (it holds the structured RESULT
	// JSON line emitted by loadgen), not merely diagnostics. Close it before the
	// read so the writer is flushed; remove it once parsed.
	_ = f.Close()
	defer os.Remove(logFile)

	data, err := os.ReadFile(logFile)
	if err != nil {
		return nil, fmt.Errorf("read subprocess log: %w", err)
	}
	output := string(data)

	// First, try to find the structured RESULT JSON line (added by loadgen in
	// this version). This is the preferred parsing path because it does not
	// depend on human-readable output format stability.
	res := parseResultLine(output)
	if res.Connected > 0 || res.Receiving > 0 || res.LatencySamples > 0 {
		return res, nil
	}

	// Fallback: parse the human-readable report lines (backward compatibility
	// with older loadgen versions).
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		line := scanner.Text()
		if after, ok := strings.CutPrefix(line, "  connected        : "); ok {
			val := after
			res.Connected, _ = strconv.Atoi(strings.TrimSpace(val)) // not actionable: default 0 on parse failure
		} else if after, ok := strings.CutPrefix(line, "  receiving        : "); ok {
			val := after
			res.Receiving, _ = strconv.Atoi(strings.TrimSpace(val)) // not actionable: default 0 on parse failure
		}
	}

	if res.Connected == 0 && !strings.Contains(output, "connected") {
		slog.Warn("subprocess subscriber: no connected count found in output",
			"log", logFile, "output_len", len(output))
	}

	return res, nil
}

// parseResultLine looks for a line matching "RESULT {...}" in the subprocess
// output and returns a SubResult with connected, receiving, and (optionally)
// latency percentiles.
func parseResultLine(output string) *SubResult {
	for line := range strings.SplitSeq(output, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "RESULT ") {
			continue
		}
		jsonPart := strings.TrimPrefix(line, "RESULT ")
		var parsed struct {
			Connected      int     `json:"connected"`
			Receiving      int     `json:"receiving"`
			LatencySamples int     `json:"latency_samples,omitempty"`
			LatencyP50Ms   float64 `json:"latency_p50_ms,omitempty"`
			LatencyP95Ms   float64 `json:"latency_p95_ms,omitempty"`
			LatencyP99Ms   float64 `json:"latency_p99_ms,omitempty"`
			LatencyMinMs   float64 `json:"latency_min_ms,omitempty"`
			LatencyMaxMs   float64 `json:"latency_max_ms,omitempty"`
			LatencyMeanMs  float64 `json:"latency_mean_ms,omitempty"`
		}
		if err := json.Unmarshal([]byte(jsonPart), &parsed); err != nil {
			slog.Debug("failed to parse RESULT JSON line", "line", line, "err", err)
			continue
		}
		return &SubResult{
			Connected:      parsed.Connected,
			Receiving:      parsed.Receiving,
			LatencySamples: parsed.LatencySamples,
			LatencyP50Ms:   parsed.LatencyP50Ms,
			LatencyP95Ms:   parsed.LatencyP95Ms,
			LatencyP99Ms:   parsed.LatencyP99Ms,
			LatencyMinMs:   parsed.LatencyMinMs,
			LatencyMaxMs:   parsed.LatencyMaxMs,
			LatencyMeanMs:  parsed.LatencyMeanMs,
		}
	}
	return &SubResult{}
}

// PublishSubprocess starts a publisher subprocess via "qumo loadgen publish".
// It returns a cancel function that kills the subprocess. The publisher runs in
// its own OS process — it never shares a Go runtime with the relays it tests.
func PublishSubprocess(ctx context.Context, qumoBin, relayAddr, caFile, path, track string, gps float64, size int) (context.CancelFunc, error) {
	args := []string{
		"loadgen", "publish",
		"--relay", relayAddr,
		"--ca", caFile,
		"--path", path,
		"--track", track,
		"--gps", fmt.Sprintf("%.0f", gps),
		"--size", strconv.Itoa(size),
	}

	pubCtx, pubCancel := context.WithCancel(ctx)

	logFile := filepath.Join(os.TempDir(), fmt.Sprintf("publisher_%d.log", time.Now().UnixNano()))
	f, err := os.Create(logFile)
	if err != nil {
		pubCancel()
		return nil, fmt.Errorf("create publisher log: %w", err)
	}

	cmd := exec.CommandContext(pubCtx, qumoBin, args...)
	cmd.Stdout = f
	cmd.Stderr = f

	if err := cmd.Start(); err != nil {
		_ = f.Close()
		pubCancel()
		return nil, fmt.Errorf("start publisher: %w", err)
	}

	slog.Info("publisher subprocess started", "relay", relayAddr, "pid", cmd.Process.Pid, "gps", gps, "size", size)

	// Wait for the process to finish in the background (it runs until cancelled).
	go func() {
		_ = cmd.Wait()
		_ = f.Close() // not actionable: log cleanup after publish finishes
	}()

	return pubCancel, nil
}
