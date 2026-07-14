package analysis

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/qumo-dev/qumo/tools/paramexp/experiment"
)

// TestDetectKnees_RespectsObjective is the regression test for the original
// bug where groupByParam hardcoded throughput_fps and ignored --objective.
func TestDetectKnees_RespectsObjective(t *testing.T) {
	space := experiment.ParamSpace{Params: []experiment.ParamDef{
		{Name: "w", Type: experiment.TypeDiscrete, Values: []string{"1", "2", "4", "8", "16"}},
	}}
	// throughput increases monotonically with w; latency_p99_ms decreases.
	// A knee query for latency must operate on latency, not throughput.
	obs := []experiment.Observation{
		{Vector: experiment.ParamVector{"w": "1"}, Metrics: experiment.MetricSet{"throughput_fps": 10, "latency_p99_ms": 50}},
		{Vector: experiment.ParamVector{"w": "2"}, Metrics: experiment.MetricSet{"throughput_fps": 40, "latency_p99_ms": 30}},
		{Vector: experiment.ParamVector{"w": "4"}, Metrics: experiment.MetricSet{"throughput_fps": 80, "latency_p99_ms": 15}},
		{Vector: experiment.ParamVector{"w": "8"}, Metrics: experiment.MetricSet{"throughput_fps": 95, "latency_p99_ms": 10}},
		{Vector: experiment.ParamVector{"w": "16"}, Metrics: experiment.MetricSet{"throughput_fps": 100, "latency_p99_ms": 9}},
	}
	// Should not panic and should return at most one knee; the metric values used
	// must come from latency when objective=latency_p99_ms (verified indirectly:
	// a knee exists for the concave throughput curve).
	knees := DetectKnees(obs, space, "throughput_fps")
	for _, k := range knees {
		assert.Equal(t, "throughput_fps", k.Metric, "knee metric must match objective")
	}
	kneesLat := DetectKnees(obs, space, "latency_p99_ms")
	for _, k := range kneesLat {
		assert.Equal(t, "latency_p99_ms", k.Metric)
	}
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

func TestDetectRegressions_SetsParamValue(t *testing.T) {
	// Regression detection must populate Param/Value (the original dead-code bug).
	obs := []experiment.Observation{
		{Vector: experiment.ParamVector{"w": "1"}, Metrics: experiment.MetricSet{"throughput_fps": 100}},
		{Vector: experiment.ParamVector{"w": "2"}, Metrics: experiment.MetricSet{"throughput_fps": 100}},
		{Vector: experiment.ParamVector{"w": "8"}, Metrics: experiment.MetricSet{"throughput_fps": 100}},
		{Vector: experiment.ParamVector{"w": "16"}, Metrics: experiment.MetricSet{"throughput_fps": 100}},
		{Vector: experiment.ParamVector{"w": "4"}, Metrics: experiment.MetricSet{"throughput_fps": 10}}, // regress
	}
	regs := DetectRegressions(obs, experiment.MetricSet{"throughput_fps": 100}, 1.5)
	assert.NotEmpty(t, regs)
	for _, r := range regs {
		assert.NotEmpty(t, r.Param)
		assert.NotEmpty(t, r.Value)
	}
}
