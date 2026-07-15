package analysis

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/qumo-dev/qumo/tools/paramexp/experiment"
	"github.com/qumo-dev/qumo/tools/paramexp/model"
)

func TestSuggestedNext_DistinctAndRanked(t *testing.T) {
	// Fit a GP on a 1-D surface, then ask for 4 suggested next points.
	space := experiment.ParamSpace{Params: []experiment.ParamDef{
		{Name: "x", Type: experiment.TypeContinuous, Min: 0, Max: 1},
	}}
	require.NoError(t, space.Normalize())
	enc, err := experiment.NewEncoder(space)
	require.NoError(t, err)

	obs := make([]experiment.Observation, 12)
	for i := range obs {
		x := float64(i) / 11 * 0.6
		obs[i] = experiment.Observation{
			EncodedX: []float64{x},
			Metrics:  experiment.MetricSet{"m": math.Sin(2 * math.Pi * x)},
			N:        1,
		}
	}
	gp, err := model.FitGP(obs, "m", model.Options{Starts: 30})
	require.NoError(t, err)

	acq := model.NewPredictiveVariance() // pure exploration → picks uncertain points
	sug := SuggestedNext(gp, enc, acq, 4, 12345)
	require.Len(t, sug, 4)

	// All distinct vectors.
	seen := map[string]bool{}
	for _, s := range sug {
		key := s.Vector.String()
		assert.False(t, seen[key], "duplicate suggested point %s", key)
		seen[key] = true
	}
	// Ranked by acquisition value, descending.
	for i := 1; i < len(sug); i++ {
		assert.GreaterOrEqual(t, sug[i-1].AcqValue, sug[i].AcqValue, "not ranked descending")
	}
}
