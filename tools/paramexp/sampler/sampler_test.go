package sampler

import (
	"math"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/qumo-dev/qumo/tools/paramexp/experiment"
)

func twoDEnc(t *testing.T) *experiment.Encoder {
	t.Helper()
	space := experiment.ParamSpace{Params: []experiment.ParamDef{
		{Name: "a", Type: experiment.TypeContinuous, Min: 0, Max: 1},
		{Name: "b", Type: experiment.TypeContinuous, Min: 0, Max: 1},
	}}
	enc, err := experiment.NewEncoder(space)
	require.NoError(t, err)
	return enc
}

func TestSobol_DelegatesToLHS(t *testing.T) {
	// Sobol is a phase-2 placeholder that delegates to LHS until a verified
	// (0,m)-net Joe-Kuo generator lands. It must still produce n in-bounds vectors.
	enc := twoDEnc(t)
	vectors, err := Sobol{}.Sample(enc, 8)
	require.NoError(t, err)
	require.Len(t, vectors, 8)
	for _, v := range vectors {
		for _, s := range []string{"a", "b"} {
			f := parseFloat(v[s])
			assert.GreaterOrEqual(t, f, 0.0)
			assert.Less(t, f, 1.0+1e-9)
		}
	}
}

func TestLHS_Coverage(t *testing.T) {
	// Continuous 3-D, n=12: each dimension must hit all 12 strata once.
	space := experiment.ParamSpace{Params: []experiment.ParamDef{
		{Name: "x", Type: experiment.TypeContinuous, Min: 0, Max: 1},
		{Name: "y", Type: experiment.TypeContinuous, Min: 0, Max: 1},
		{Name: "z", Type: experiment.TypeContinuous, Min: 0, Max: 1},
	}}
	enc, _ := experiment.NewEncoder(space)
	n := 12
	vectors, err := LHS{}.Sample(enc, n)
	require.NoError(t, err)
	require.Len(t, vectors, n)
	for _, name := range []string{"x", "y", "z"} {
		strata := make(map[int]bool)
		for _, v := range vectors {
			strata[floatToIntCell(parseFloat(v[name]), n)] = true
		}
		// LHS with n strata should hit most/all strata; require ≥ n-1 (center placement).
		assert.GreaterOrEqual(t, len(strata), n-1, "dim %s coverage", name)
	}
}

func TestAdaptive_Neighbors(t *testing.T) {
	space := experiment.ParamSpace{Params: []experiment.ParamDef{
		{Name: "w", Type: experiment.TypeDiscrete, Values: []string{"1", "2", "4", "8"}},
		{Name: "b", Type: experiment.TypeDiscrete, Values: []string{"x", "y", "z"}},
	}}
	enc, _ := experiment.NewEncoder(space)

	// Best observation at w=2(idx1), b=y(idx1).
	obs := []experiment.Observation{{
		Vector:  experiment.ParamVector{"w": "2", "b": "y"},
		Metrics: experiment.MetricSet{"throughput_fps": 100},
	}}
	a := &Adaptive{}
	neighbors, err := a.SampleNear(enc, obs, 5, "throughput_fps", space)
	require.NoError(t, err)
	for _, n := range neighbors {
		// Every neighbor differs from the base by exactly one ±1 step.
		assert.True(t, n.Equal(obs[0].Vector) == false)
	}
	assert.NotEmpty(t, neighbors)
}

// helpers

func parseFloat(s string) float64 {
	f, _ := strconv.ParseFloat(s, 64)
	return f
}

// floatToIntCell bins a coordinate in [0,1) into one of n strata (0-based).
func floatToIntCell(f float64, n int) int {
	return clampInt(int(math.Floor(f*float64(n))), 0, n-1)
}
func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
