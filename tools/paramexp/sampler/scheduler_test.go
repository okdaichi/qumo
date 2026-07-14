package sampler

import (
	"context"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/qumo-dev/qumo/tools/paramexp/experiment"
)

func schedEnc3(t *testing.T) (*experiment.Encoder, experiment.ParamSpace) {
	t.Helper()
	space := experiment.ParamSpace{Params: []experiment.ParamDef{
		{Name: "a", Type: experiment.TypeDiscrete, Values: []string{"1", "2", "4", "8"}},
		{Name: "b", Type: experiment.TypeDiscrete, Values: []string{"x", "y", "z"}},
		{Name: "c", Type: experiment.TypeDiscrete, Values: []string{"p", "q"}},
	}}
	require.NoError(t, space.Normalize())
	enc, err := experiment.NewEncoder(space)
	require.NoError(t, err)
	return enc, space
}

func TestStatic_LHSThenAdaptiveThenEOF(t *testing.T) {
	enc, space := schedEnc3(t)
	s := &StaticScheduler{LHSn: 6, AdaptiveRounds: 2, AdaptiveN: 4}

	v0, phase0, err := s.Next(context.Background(), SchedulerState{Space: space, Enc: enc, Objective: "m"})
	require.NoError(t, err)
	assert.Equal(t, "lhs", phase0)
	assert.Len(t, v0, 6)

	_, _, err = s.Next(context.Background(), SchedulerState{Space: space, Enc: enc, Objective: "m"})
	assert.ErrorIs(t, err, io.EOF)
}

func TestStatic_AdaptiveUsesObservations(t *testing.T) {
	enc, space := schedEnc3(t)
	s := &StaticScheduler{LHSn: 2, AdaptiveRounds: 1, AdaptiveN: 4}

	_, _, _ = s.Next(context.Background(), SchedulerState{Space: space, Enc: enc, Objective: "m"})

	obs := []experiment.Observation{
		{Vector: experiment.ParamVector{"a": "4", "b": "y", "c": "q"}, Metrics: experiment.MetricSet{"m": 100}},
		{Vector: experiment.ParamVector{"a": "2", "b": "x", "c": "p"}, Metrics: experiment.MetricSet{"m": 10}},
		{Vector: experiment.ParamVector{"a": "8", "b": "z", "c": "q"}, Metrics: experiment.MetricSet{"m": 50}},
	}
	v, phase, err := s.Next(context.Background(), SchedulerState{Space: space, Enc: enc, Observations: obs, Objective: "m"})
	require.NoError(t, err)
	assert.Contains(t, phase, "adaptive")
	assert.NotEmpty(t, v)
	_, _, err = s.Next(context.Background(), SchedulerState{Space: space, Enc: enc, Observations: obs, Objective: "m"})
	assert.ErrorIs(t, err, io.EOF)
}

func TestStatic_RespectsContextCancel(t *testing.T) {
	enc, space := schedEnc3(t)
	s := &StaticScheduler{LHSn: 4}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := s.Next(ctx, SchedulerState{Space: space, Enc: enc})
	assert.ErrorIs(t, err, context.Canceled)
}
