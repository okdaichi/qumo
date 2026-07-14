package runner

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/qumo-dev/qumo/tools/paramexp/experiment"
)

// TestHelperProcess is a swappable subprocess driven by PARAM_MODE, so the real
// tests exercise metrics/telemetry/timeout/retry without a real benchmark.
func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_TEST_HELPER") != "1" {
		t.Skip()
	}
	switch os.Getenv("PARAM_MODE") {
	case "metrics":
		os.Stdout.WriteString("booting...\n")
		os.Stdout.WriteString(`{"throughput_fps": 420, "latency_p99_ms": 1.5}` + "\n")
	case "telemetry":
		os.Stdout.WriteString(`{"throughput_fps": 100, "telemetry": {"cpu_pct": 77.5, "rss_mb": 256, "goroutines": 40}}` + "\n")
	case "empty":
		os.Stdout.WriteString("no json here\n")
	case "always_fail":
		os.Exit(3)
	case "slow":
		time.Sleep(2 * time.Second)
		os.Stdout.WriteString(`{"throughput_fps": 1}` + "\n")
	}
	os.Exit(0)
}

// helperCmd re-invokes this test binary as the runner command.
func helperCmd() string { return os.Args[0] + " -test.run=TestHelperProcess" }

// runWithMode sets PARAM_MODE via the vector (→ PARAM_MODE env) and runs once.
func runWithMode(t *testing.T, r *ExecRunner, mode string) *experiment.Result {
	t.Helper()
	t.Setenv("GO_TEST_HELPER", "1")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	res, err := r.Run(ctx, experiment.ParamVector{"MODE": mode})
	require.NoError(t, err)
	require.NotNil(t, res)
	return res
}

func TestParseMetrics_LastJSONWins(t *testing.T) {
	m, tel := parseOutput("booting\n" + `{"throughput_fps": 1}` + "\n" + `{"throughput_fps": 420, "latency_p99_ms": 1.5}` + "\n")
	assert.InDelta(t, 420, m["throughput_fps"], 1e-9)
	assert.Nil(t, tel)
}

func TestParseMetrics_Empty(t *testing.T) {
	m, tel := parseOutput("no json\n")
	assert.Empty(t, m)
	assert.Nil(t, tel)
}

func TestParseMetrics_TelemetryNested(t *testing.T) {
	m, tel := parseOutput(`{"throughput_fps": 100, "telemetry": {"cpu_pct": 77.5, "rss_mb": 256, "goroutines": 40}}`)
	assert.InDelta(t, 100, m["throughput_fps"], 1e-9)
	require.NotNil(t, tel)
	assert.InDelta(t, 77.5, tel.CPUpct, 1e-9)
	assert.InDelta(t, 256, tel.RSSmb, 1e-9)
	assert.InDelta(t, 40, tel.Goroutines, 1e-9)
}

func TestParseMetrics_TelemetryFlat(t *testing.T) {
	_, tel := parseOutput(`{"cpu_pct": 50, "retransmits": 3}`)
	require.NotNil(t, tel)
	assert.InDelta(t, 50, tel.CPUpct, 1e-9)
	assert.InDelta(t, 3, tel.Retransmits, 1e-9)
}

func TestExecRunner_MetricsParsed(t *testing.T) {
	r := NewExecRunner(helperCmd(), 5*time.Second, 1)
	res := runWithMode(t, r, "metrics")
	assert.Equal(t, 0, res.ExitCode)
	assert.InDelta(t, 420, res.Metrics["throughput_fps"], 1e-9)
}

func TestExecRunner_TelemetryParsed(t *testing.T) {
	r := NewExecRunner(helperCmd(), 5*time.Second, 1)
	res := runWithMode(t, r, "telemetry")
	require.NotNil(t, res.Telemetry)
	assert.InDelta(t, 77.5, res.Telemetry.CPUpct, 1e-9)
}

func TestExecRunner_Timeout(t *testing.T) {
	r := NewExecRunner(helperCmd(), 200*time.Millisecond, 1)
	res := runWithMode(t, r, "slow")
	assert.Equal(t, -1, res.ExitCode, "timeout surfaces as exit -1")
	assert.Contains(t, res.Error, "timeout")
}

func TestExecRunner_RetryExhausts(t *testing.T) {
	r := NewExecRunner(helperCmd(), 5*time.Second, 3)
	r.Backoff = time.Millisecond
	res := runWithMode(t, r, "always_fail")
	assert.NotEqual(t, 0, res.ExitCode)
	assert.Equal(t, 3, res.Attempts, "should retry up to MaxAttempts")
}

func TestExecRunner_EmptyMetricsNoCrash(t *testing.T) {
	r := NewExecRunner(helperCmd(), 5*time.Second, 1)
	res := runWithMode(t, r, "empty")
	assert.Equal(t, 0, res.ExitCode)
	assert.Empty(t, res.Metrics)
}

// guard against accidental import churn
var _ = strings.TrimSpace
