// Package runner executes black-box benchmarks: it takes a parameter vector,
// invokes an external command with the parameters as environment variables,
// and parses the emitted metrics (and optional resource telemetry) from stdout.
package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/qumo-dev/qumo/tools/paramexp/experiment"
)

// Runner executes one experiment and returns its result.
type Runner interface {
	Run(ctx context.Context, v experiment.ParamVector) (*experiment.Result, error)
}

// ExecRunner runs an external command. The command receives parameters as
// PARAM_<NAME> env vars (name uppercased, dashes→underscores) and must print a
// JSON object of metrics on stdout (the last JSON-looking line wins). It may
// optionally print a JSON object with a "telemetry" key, or a top-level object
// containing telemetry fields (cpu_pct/gc_pause_ms/syscalls/retransmits/
// rss_mb/goroutines).
type ExecRunner struct {
	Cmd         string
	Timeout     time.Duration // per attempt; 0 → no timeout
	MaxAttempts int           // ≥1; failed/timed-out attempts are retried
	Backoff     time.Duration // sleep between attempts
}

// NewExecRunner constructs an ExecRunner with sensible defaults.
func NewExecRunner(cmd string, timeout time.Duration, maxAttempts int) *ExecRunner {
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	return &ExecRunner{Cmd: cmd, Timeout: timeout, MaxAttempts: maxAttempts, Backoff: 500 * time.Millisecond}
}

// Run executes the command with retry. The returned Result.Attempts counts
// tries (≥1). A non-nil error is returned only when the command cannot be
// constructed at all; otherwise failures surface as Result.Error / ExitCode.
func (r *ExecRunner) Run(ctx context.Context, v experiment.ParamVector) (*experiment.Result, error) {
	parts := strings.Fields(r.Cmd)
	if len(parts) == 0 {
		return nil, fmt.Errorf("empty runner command")
	}

	var lastResult *experiment.Result
	now := time.Now()
	for attempt := 1; attempt <= r.MaxAttempts; attempt++ {
		res, retryable := r.runOnce(ctx, v, parts, attempt)
		lastResult = res
		if !retryable {
			break
		}
		if attempt < r.MaxAttempts && r.Backoff > 0 {
			select {
			case <-time.After(r.Backoff):
			case <-ctx.Done():
				return lastResult, nil
			}
		}
	}
	if lastResult != nil {
		lastResult.Timestamp = now
	}
	return lastResult, nil
}

// runOnce runs one attempt. The bool is true if the attempt is retryable
// (non-zero exit or timeout) and attempts remain.
func (r *ExecRunner) runOnce(ctx context.Context, v experiment.ParamVector, parts []string, attempt int) (*experiment.Result, bool) {
	attemptCtx := ctx
	var cancel context.CancelFunc
	if r.Timeout > 0 {
		attemptCtx, cancel = context.WithTimeout(ctx, r.Timeout)
	} else {
		cancel = func() {}
	}
	defer cancel()

	cmd := exec.CommandContext(attemptCtx, parts[0], parts[1:]...)
	cmd.Env = buildEnv(v)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	start := time.Now()
	err := cmd.Run()
	duration := time.Since(start).Seconds()

	exitCode := 0
	timedOut := false
	if err != nil {
		if attemptCtx.Err() == context.DeadlineExceeded {
			timedOut = true
			exitCode = -1
		} else if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			// Process failed to start / I/O error — not retryable as an exit.
			exitCode = -1
		}
	}

	metrics, tel := parseOutput(stdout.String())

	errStr := ""
	if exitCode != 0 {
		errStr = strings.TrimSpace(stderr.String())
		if errStr == "" && err != nil {
			errStr = err.Error()
		}
		if timedOut {
			errStr = fmt.Sprintf("timeout after %s: %s", r.Timeout, errStr)
		}
	}

	return &experiment.Result{
		Metrics:   metrics,
		Telemetry: tel,
		Duration:  duration,
		ExitCode:  exitCode,
		Attempts:  attempt,
		Error:     errStr,
		Stdout:    stdout.String(),
		Stderr:    strings.TrimSpace(stderr.String()),
		Timestamp: start,
	}, exitCode != 0
}

// buildEnv returns the inherited environment plus PARAM_<NAME> entries.
func buildEnv(v experiment.ParamVector) []string {
	env := os.Environ()
	for name, val := range v {
		envName := "PARAM_" + strings.ToUpper(strings.ReplaceAll(name, "-", "_"))
		env = append(env, fmt.Sprintf("%s=%s", envName, val))
	}
	return env
}

// parseOutput extracts the metrics MetricSet and optional Telemetry from stdout.
//
// The benchmark may emit any of:
//   - a JSON line of metrics (the last JSON line wins for metrics);
//   - a JSON object carrying a nested "telemetry" object alongside metrics;
//   - a separate JSON object whose top-level keys are telemetry fields.
//
// Telemetry is taken from a nested "telemetry" object if present, else from any
// telemetry-shaped line. Returns nil Telemetry when none is present.
func parseOutput(stdout string) (experiment.MetricSet, *experiment.Telemetry) {
	var objs []map[string]any
	for line := range strings.SplitSeq(strings.TrimSpace(stdout), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var obj map[string]any
		if err := json.Unmarshal([]byte(line), &obj); err == nil && len(obj) > 0 {
			objs = append(objs, obj)
		}
	}
	if len(objs) == 0 {
		return experiment.MetricSet{}, nil
	}

	// Metrics: from the last decoded object (drop nested objects like "telemetry").
	metrics := toMetricSet(objs[len(objs)-1])

	// Telemetry: prefer a nested "telemetry" object in any line, else a
	// telemetry-shaped top-level line.
	var tel *experiment.Telemetry
	for _, obj := range objs {
		if nested, ok := obj["telemetry"].(map[string]any); ok {
			tel = telFromObj(nested)
			break
		}
	}
	if tel == nil {
		for _, obj := range objs {
			if t := telFromObj(obj); t != nil {
				tel = t
				break
			}
		}
	}
	return metrics, tel
}

// toMetricSet converts a decoded object to scalar numeric metrics (dropping
// non-scalar fields like nested "telemetry").
func toMetricSet(obj map[string]any) experiment.MetricSet {
	m := make(experiment.MetricSet, len(obj))
	for k, v := range obj {
		f, ok := toFloat(v)
		if !ok {
			continue
		}
		m[k] = f
	}
	return m
}

// telFromObj returns a Telemetry if the object contains at least one known
// telemetry field.
func telFromObj(obj map[string]any) *experiment.Telemetry {
	get := func(key string) (float64, bool) {
		if v, ok := obj[key]; ok {
			return toFloat(v)
		}
		return 0, false
	}
	t := &experiment.Telemetry{}
	any := false
	if v, ok := get("cpu_pct"); ok {
		t.CPUpct = v
		any = true
	}
	if v, ok := get("gc_pause_ms"); ok {
		t.GCPauseMs = v
		any = true
	}
	if v, ok := get("syscalls"); ok {
		t.Syscalls = v
		any = true
	}
	if v, ok := get("retransmits"); ok {
		t.Retransmits = v
		any = true
	}
	if v, ok := get("rss_mb"); ok {
		t.RSSmb = v
		any = true
	}
	if v, ok := get("goroutines"); ok {
		t.Goroutines = v
		any = true
	}
	if !any {
		return nil
	}
	t.Raw = obj
	return t
}

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case json.Number:
		if f, err := n.Float64(); err == nil {
			return f, true
		}
	}
	return 0, false
}
