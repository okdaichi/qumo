// Package main — runner: executes a black-box benchmark per experiment.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Runner executes a benchmark command with the given parameter vector.
// The command receives params as environment variables (PARAM_<NAME>=value).
// It must output JSON metrics on stdout (last JSON line wins).
type Runner struct {
	commandTemplate string // e.g. "bash bench.sh"
	timeout         time.Duration
}

func NewRunner(cmd string, timeout time.Duration) *Runner {
	if timeout == 0 {
		timeout = 10 * time.Minute
	}
	return &Runner{commandTemplate: cmd, timeout: timeout}
}

// Run executes one experiment and returns the result.
func (r *Runner) Run(vector ParamVector) (*Result, error) {
	// Build the command with params as env vars.
	parts := splitCmd(r.commandTemplate)
	if len(parts) == 0 {
		return nil, fmt.Errorf("empty runner command")
	}
	cmd := exec.Command(parts[0], parts[1:]...)

	// Set PARAM_<NAME> env vars for each parameter.
	cmd.Env = os.Environ()
	for name, val := range vector {
		envName := "PARAM_" + strings.ToUpper(strings.ReplaceAll(name, "-", "_"))
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", envName, val))
	}

	// Capture stdout/stderr.
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	start := time.Now()
	err := cmd.Run()
	duration := time.Since(start).Seconds()

	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			return nil, fmt.Errorf("runner failed: %w (stderr: %s)", err, stderr.String())
		}
	}

	// Parse metrics from stdout (last JSON line).
	metrics := parseMetrics(stdout.String())

	stdoutStr := stdout.String()
	if len(stdoutStr) > 4096 {
		stdoutStr = stdoutStr[:4096] + "...[truncated]"
	}

	return &Result{
		Metrics:   metrics,
		Duration:  duration,
		ExitCode:  exitCode,
		Error:     cond(exitCode != 0, stderr.String(), ""),
		Stdout:    stdoutStr,
		Timestamp: start,
	}, nil
}

// parseMetrics extracts a JSON object from the last JSON-looking line of stdout.
func parseMetrics(stdout string) MetricSet {
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var m MetricSet
		if err := json.Unmarshal([]byte(line), &m); err == nil && len(m) > 0 {
			return m
		}
	}
	return MetricSet{}
}

func splitCmd(s string) []string {
	// Simple split on spaces (no quote handling for MVP).
	return strings.Fields(s)
}

func cond(b bool, a, b2 string) string {
	if b {
		return a
	}
	return b2
}
