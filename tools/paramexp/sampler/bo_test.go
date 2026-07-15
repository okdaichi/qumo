package sampler

import (
	"context"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/qumo-dev/qumo/tools/paramexp/experiment"
)

// boObs builds n observations on the 3-D enc3 space with a high-value region at
// a=high (so the BO acquisition has somewhere promising to point).
func boObs(t *testing.T, enc *experiment.Encoder, n int) []experiment.Observation {
	t.Helper()
	levels := []string{"1", "2", "4", "8"}
	var obs []experiment.Observation
	for i := 0; i < n; i++ {
		a := levels[i%len(levels)]
		b := []string{"x", "y", "z"}[i%3]
		c := []string{"p", "q"}[i%2]
		v := experiment.ParamVector{"a": a, "b": b, "c": c}
		x, err := enc.Encode(v)
		require.NoError(t, err)
		// a="8" (idx 3) is the high-value region; objective "m" maximized there.
		m := 10.0
		if a == "8" {
			m = 100.0
		}
		obs = append(obs, experiment.Observation{
			ExperimentID: int64(i + 1), Vector: v, EncodedX: x,
			Metrics: experiment.MetricSet{"m": m}, N: 1,
		})
	}
	return obs
}

func TestBayesianScheduler_LHSSeed(t *testing.T) {
	enc, space := schedEnc3(t)
	s := &BayesianScheduler{LHSn: 8, Rounds: 2, Acquisition: "ucb", Kappa: 3}
	v, phase, err := s.Next(context.Background(), SchedulerState{Space: space, Enc: enc, Objective: "m"})
	require.NoError(t, err)
	assert.Equal(t, "lhs", phase)
	assert.Len(t, v, 8)
}

func TestBayesianScheduler_AcquisitionRoundsThenEOF(t *testing.T) {
	enc, space := schedEnc3(t)
	s := &BayesianScheduler{LHSn: 4, Rounds: 2, Acquisition: "ucb", Kappa: 3, Candidates: 400}
	// Skip the LHS round.
	_, _, _ = s.Next(context.Background(), SchedulerState{Space: space, Enc: enc, Objective: "m"})

	obs := boObs(t, enc, 8)
	// Rounds 1 and 2 return acquisition-driven points.
	v1, ph1, err := s.Next(context.Background(), SchedulerState{Space: space, Enc: enc, Observations: obs, Objective: "m"})
	require.NoError(t, err)
	assert.Contains(t, ph1, "bo-")
	assert.NotEmpty(t, v1)
	// Picks must not duplicate already-observed vectors.
	for _, p := range v1 {
		assert.False(t, observedHas(obs, p), "BO must not re-pick an observed vector")
	}

	v2, ph2, err := s.Next(context.Background(), SchedulerState{Space: space, Enc: enc, Observations: obs, Objective: "m"})
	require.NoError(t, err)
	assert.Contains(t, ph2, "bo-")
	assert.NotEmpty(t, v2)

	// Rounds exhausted → EOF.
	_, _, err = s.Next(context.Background(), SchedulerState{Space: space, Enc: enc, Observations: obs, Objective: "m"})
	assert.ErrorIs(t, err, io.EOF)
}

func TestBayesianScheduler_TooFewObservationsEOF(t *testing.T) {
	enc, space := schedEnc3(t)
	s := &BayesianScheduler{LHSn: 4, Rounds: 3, Candidates: 200}
	_, _, _ = s.Next(context.Background(), SchedulerState{Space: space, Enc: enc, Objective: "m"}) // LHS
	// Only 2 observations (<4) → can't fit → EOF.
	obs := boObs(t, enc, 2)
	_, _, err := s.Next(context.Background(), SchedulerState{Space: space, Enc: enc, Observations: obs, Objective: "m"})
	assert.ErrorIs(t, err, io.EOF)
}
