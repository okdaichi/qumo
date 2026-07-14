package scheduler

import (
	"context"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/qumo-dev/qumo/tools/paramexp/encoding"
	"github.com/qumo-dev/qumo/tools/paramexp/experiment"
)

func enc3(t *testing.T) (*encoding.Encoder, experiment.ParamSpace) {
	t.Helper()
	space := experiment.ParamSpace{Params: []experiment.ParamDef{
		{Name: "a", Type: experiment.TypeDiscrete, Values: []string{"1", "2", "4", "8"}},
		{Name: "b", Type: experiment.TypeDiscrete, Values: []string{"x", "y", "z"}},
		{Name: "c", Type: experiment.TypeDiscrete, Values: []string{"p", "q"}},
	}}
	require.NoError(t, space.Normalize())
	enc, err := encoding.New(space)
	require.NoError(t, err)
	return enc, space
}

func TestStatic_LHSThenAdaptiveThenEOF(t *testing.T) {
	enc, space := enc3(t)
	s := &Static{LHSn: 6, AdaptiveRounds: 2, AdaptiveN: 4}

	// Round 0: LHS batch of 6.
	v0, phase0, err := s.Next(context.Background(), State{Space: space, Enc: enc, Objective: "m"})
	require.NoError(t, err)
	assert.Equal(t, "lhs", phase0)
	assert.Len(t, v0, 6)

	// Rounds 1..k need ≥3 observations; with none, Next returns EOF.
	_, _, err = s.Next(context.Background(), State{Space: space, Enc: enc, Objective: "m"})
	assert.ErrorIs(t, err, io.EOF)
}

func TestStatic_AdaptiveUsesObservations(t *testing.T) {
	enc, space := enc3(t)
	s := &Static{LHSn: 2, AdaptiveRounds: 1, AdaptiveN: 4}

	// Skip LHS round.
	_, _, _ = s.Next(context.Background(), State{Space: space, Enc: enc, Objective: "m"})

	obs := []experiment.Observation{
		{Vector: experiment.ParamVector{"a": "4", "b": "y", "c": "q"}, Metrics: experiment.MetricSet{"m": 100}},
		{Vector: experiment.ParamVector{"a": "2", "b": "x", "c": "p"}, Metrics: experiment.MetricSet{"m": 10}},
		{Vector: experiment.ParamVector{"a": "8", "b": "z", "c": "q"}, Metrics: experiment.MetricSet{"m": 50}},
	}
	v, phase, err := s.Next(context.Background(), State{Space: space, Enc: enc, Observations: obs, Objective: "m"})
	require.NoError(t, err)
	assert.Contains(t, phase, "adaptive")
	assert.NotEmpty(t, v)
	// After AdaptiveRounds exhausted, EOF.
	_, _, err = s.Next(context.Background(), State{Space: space, Enc: enc, Observations: obs, Objective: "m"})
	assert.ErrorIs(t, err, io.EOF)
}

func TestStatic_RespectsContextCancel(t *testing.T) {
	enc, space := enc3(t)
	s := &Static{LHSn: 4}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := s.Next(ctx, State{Space: space, Enc: enc})
	assert.ErrorIs(t, err, context.Canceled)
}
