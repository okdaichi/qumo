package analysis

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/qumo-dev/qumo/tools/paramexp/experiment"
)

// TestDetectKnees_RespectsObjective is the regression test for the original
// bug where groupByParam hardcoded throughput_fps and ignored --objective.
func TestDetectKnees_RespectsObjective(t *testing.T) {
	space := experiment.ParamSpace{Params: []experiment.ParamDef{
		{Name: "w", Type: experiment.TypeDiscrete, Values: []string{"1", "2", "4", "8", "16"}},
	}}
	// throughput increases monotonically with w; latency_p99_ms decreases.
	obs := []experiment.Observation{
		{Vector: experiment.ParamVector{"w": "1"}, Metrics: experiment.MetricSet{"throughput_fps": 10, "latency_p99_ms": 50}},
		{Vector: experiment.ParamVector{"w": "2"}, Metrics: experiment.MetricSet{"throughput_fps": 40, "latency_p99_ms": 30}},
		{Vector: experiment.ParamVector{"w": "4"}, Metrics: experiment.MetricSet{"throughput_fps": 80, "latency_p99_ms": 15}},
		{Vector: experiment.ParamVector{"w": "8"}, Metrics: experiment.MetricSet{"throughput_fps": 95, "latency_p99_ms": 10}},
		{Vector: experiment.ParamVector{"w": "16"}, Metrics: experiment.MetricSet{"throughput_fps": 100, "latency_p99_ms": 9}},
	}
	for _, objective := range []string{"throughput_fps", "latency_p99_ms"} {
		knees := DetectKnees(obs, space, objective)
		for _, k := range knees {
			assert.Equal(t, objective, k.Metric, "knee metric must match objective")
		}
	}
}

// TestDetectKnees_ConcaveThroughput is the regression test for the sign bug:
// a concave-increasing (diminishing-returns) throughput sweep — the default
// objective — must actually return a knee. The old single-sign (xNorm - yNorm)
// criterion found nothing because the curve lies above the diagonal.
func TestDetectKnees_ConcaveThroughput(t *testing.T) {
	space := experiment.ParamSpace{Params: []experiment.ParamDef{
		{Name: "w", Type: experiment.TypeDiscrete, Values: []string{"1", "2", "4", "8", "16"}},
	}}
	obs := []experiment.Observation{
		{Vector: experiment.ParamVector{"w": "1"}, Metrics: experiment.MetricSet{"throughput_fps": 10}},
		{Vector: experiment.ParamVector{"w": "2"}, Metrics: experiment.MetricSet{"throughput_fps": 40}},
		{Vector: experiment.ParamVector{"w": "4"}, Metrics: experiment.MetricSet{"throughput_fps": 80}},
		{Vector: experiment.ParamVector{"w": "8"}, Metrics: experiment.MetricSet{"throughput_fps": 95}},
		{Vector: experiment.ParamVector{"w": "16"}, Metrics: experiment.MetricSet{"throughput_fps": 100}},
	}
	knees := DetectKnees(obs, space, "throughput_fps")
	require.NotEmpty(t, knees, "a concave diminishing-returns sweep must yield a knee")
	// The elbow sits around w=2..4 (gains 10→40→80 then flatten to 95→100).
	assert.Contains(t, []string{"2", "4"}, knees[0].Value)
}

func TestRankImportance_KnownEta(t *testing.T) {
	// w drives all the variance; b is uniform noise. η²(w) ≈ 1, η²(b) ≈ 0.
	space := experiment.ParamSpace{Params: []experiment.ParamDef{
		{Name: "w", Type: experiment.TypeDiscrete, Values: []string{"1", "2", "4"}},
		{Name: "b", Type: experiment.TypeDiscrete, Values: []string{"x", "y"}},
	}}
	obs := []experiment.Observation{
		{Vector: experiment.ParamVector{"w": "1", "b": "x"}, Metrics: experiment.MetricSet{"m": 0}},
		{Vector: experiment.ParamVector{"w": "1", "b": "y"}, Metrics: experiment.MetricSet{"m": 0}},
		{Vector: experiment.ParamVector{"w": "2", "b": "x"}, Metrics: experiment.MetricSet{"m": 50}},
		{Vector: experiment.ParamVector{"w": "2", "b": "y"}, Metrics: experiment.MetricSet{"m": 50}},
		{Vector: experiment.ParamVector{"w": "4", "b": "x"}, Metrics: experiment.MetricSet{"m": 100}},
		{Vector: experiment.ParamVector{"w": "4", "b": "y"}, Metrics: experiment.MetricSet{"m": 100}},
	}
	ranks := RankImportance(obs, space, "m")
	top := ranks[0]
	assert.Equal(t, "w", top.Param)
	assert.Greater(t, top.Importance, 0.95)
}

func TestDetectInteractions_Sign(t *testing.T) {
	// Pure interaction: m = 1 only when w=high AND b=high; additive otherwise.
	space := experiment.ParamSpace{Params: []experiment.ParamDef{
		{Name: "w", Type: experiment.TypeDiscrete, Values: []string{"lo", "hi"}},
		{Name: "b", Type: experiment.TypeDiscrete, Values: []string{"lo", "hi"}},
	}}
	interacting := []experiment.Observation{
		{Vector: experiment.ParamVector{"w": "lo", "b": "lo"}, Metrics: experiment.MetricSet{"m": 0}},
		{Vector: experiment.ParamVector{"w": "lo", "b": "hi"}, Metrics: experiment.MetricSet{"m": 0}},
		{Vector: experiment.ParamVector{"w": "hi", "b": "lo"}, Metrics: experiment.MetricSet{"m": 0}},
		{Vector: experiment.ParamVector{"w": "hi", "b": "hi"}, Metrics: experiment.MetricSet{"m": 100}},
	}
	ints := DetectInteractions(interacting, space, "m")
	assert.NotEmpty(t, ints, "an interacting surface should yield a positive interaction score")

	additive := []experiment.Observation{
		{Vector: experiment.ParamVector{"w": "lo", "b": "lo"}, Metrics: experiment.MetricSet{"m": 0}},
		{Vector: experiment.ParamVector{"w": "lo", "b": "hi"}, Metrics: experiment.MetricSet{"m": 50}},
		{Vector: experiment.ParamVector{"w": "hi", "b": "lo"}, Metrics: experiment.MetricSet{"m": 50}},
		{Vector: experiment.ParamVector{"w": "hi", "b": "hi"}, Metrics: experiment.MetricSet{"m": 100}},
	}
	intsAdd := DetectInteractions(additive, space, "m")
	assert.Empty(t, intsAdd, "an additive surface should yield ~0 interaction")
}

func TestDetectRegressions_RecordsVector(t *testing.T) {
	// Regression detection must record the full, deterministic vector of the
	// offending run (the original bug used non-deterministic map iteration).
	obs := []experiment.Observation{
		{Vector: experiment.ParamVector{"w": "1"}, Metrics: experiment.MetricSet{"throughput_fps": 100}},
		{Vector: experiment.ParamVector{"w": "2"}, Metrics: experiment.MetricSet{"throughput_fps": 100}},
		{Vector: experiment.ParamVector{"w": "8"}, Metrics: experiment.MetricSet{"throughput_fps": 100}},
		{Vector: experiment.ParamVector{"w": "16"}, Metrics: experiment.MetricSet{"throughput_fps": 100}},
		{Vector: experiment.ParamVector{"w": "4"}, Metrics: experiment.MetricSet{"throughput_fps": 10}}, // regress
	}
	regs := DetectRegressions(obs, experiment.MetricSet{"throughput_fps": 100}, 1.5)
	require.NotEmpty(t, regs)
	// The single regression must be attributed to w=4 exactly (deterministic).
	require.Len(t, regs, 1)
	assert.Equal(t, "4", regs[0].Vector["w"])
}

func TestStabilityReport_FlagsHighCV(t *testing.T) {
	obs := []experiment.Observation{
		{ExperimentID: 1, Vector: experiment.ParamVector{"w": "1"}, Metrics: experiment.MetricSet{"m": 100}, Variances: experiment.MetricSet{"m": 1}, N: 5},   // cv=0.01 stable
		{ExperimentID: 2, Vector: experiment.ParamVector{"w": "2"}, Metrics: experiment.MetricSet{"m": 100}, Variances: experiment.MetricSet{"m": 900}, N: 5}, // cv=0.3 unstable
		{ExperimentID: 3, Vector: experiment.ParamVector{"w": "4"}, Metrics: experiment.MetricSet{"m": 100}, Variances: experiment.MetricSet{"m": 0}, N: 1},   // single-replicate → stable
	}
	stab := StabilityReport(obs, "m")
	byID := map[int64]bool{}
	for _, s := range stab {
		if s.Unstable {
			byID[s.ExperimentID] = true
		}
	}
	assert.True(t, byID[2], "high-CV config (cv=0.3) is unstable")
	assert.False(t, byID[1], "low-CV config is stable")
	assert.False(t, byID[3], "single-replicate config is stable (no evidence)")
}

func TestIndistinguishableFromBest(t *testing.T) {
	// Best at mean=100 (n=20, var=4 → se≈0.45). A near config at mean=99.5
	// (overlapping CI) is a peer; a clearly-worse config at mean=50 is not.
	obs := []experiment.Observation{
		{ExperimentID: 1, Vector: experiment.ParamVector{"w": "best"}, Metrics: experiment.MetricSet{"m": 100}, Variances: experiment.MetricSet{"m": 4}, N: 20},
		{ExperimentID: 2, Vector: experiment.ParamVector{"w": "near"}, Metrics: experiment.MetricSet{"m": 99.5}, Variances: experiment.MetricSet{"m": 4}, N: 20},
		{ExperimentID: 3, Vector: experiment.ParamVector{"w": "worse"}, Metrics: experiment.MetricSet{"m": 50}, Variances: experiment.MetricSet{"m": 4}, N: 20},
	}
	best, peers := IndistinguishableFromBest(obs, "m")
	assert.Equal(t, int64(1), best.ExperimentID)
	require.Len(t, peers, 1, "only the near config is indistinguishable from best")
	assert.Equal(t, int64(2), peers[0].ExperimentID)
}
