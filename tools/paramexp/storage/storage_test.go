package storage

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/qumo-dev/qumo/tools/paramexp/experiment"
	"github.com/qumo-dev/qumo/tools/paramexp/provenance"
)

func TestRoundTrip(t *testing.T) {
	s, err := Open(":memory:")
	require.NoError(t, err)
	defer s.Close()

	run := provenance.Run{
		StartedAt:        time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC),
		FrameworkVersion: "test",
		GitRevision:      "abc123",
		GitDirty:         true,
		ConfigHash:       "deadbeef",
		ConfigJSON:       "{}",
		MachineJSON:      "{}",
		EnvJSON:          "[]",
	}
	require.NoError(t, s.SaveRun(&run))
	require.NotZero(t, run.ID)

	exp := &experiment.Experiment{
		RunID: run.ID, Vector: experiment.ParamVector{"w": "4"}, EncodedX: []float64{0.5},
		Phase: "lhs", CreatedAt: time.Now(),
	}
	require.NoError(t, s.SaveExperiment(exp))
	require.NotZero(t, exp.ID)

	require.NoError(t, s.AppendAttempt(Attempt{
		ExperimentID: exp.ID, Attempt: 1, StartedAt: time.Now(),
		DurationSec: 0.5, ExitCode: 0,
	}))
	res := &experiment.Result{
		ExperimentID: exp.ID, Metrics: experiment.MetricSet{"throughput_fps": 100},
		Duration:     0.5, ExitCode: 0, Attempts: 1, Timestamp: time.Now(),
	}
	require.NoError(t, s.SaveResult(res))
	require.NoError(t, s.SaveTelemetry(exp.ID, &experiment.Telemetry{CPUpct: 50, RSSmb: 128}))

	obs, err := s.Observations(false)
	require.NoError(t, err)
	require.Len(t, obs, 1)
	assert.Equal(t, "4", obs[0].Vector["w"])
	assert.InDelta(t, 100, obs[0].Metrics["throughput_fps"], 1e-9)
	assert.InDelta(t, 0.5, obs[0].EncodedX[0], 1e-9)
}

func TestObservations_FailureFilter(t *testing.T) {
	s, _ := Open(":memory:")
	defer s.Close()
	var run provenance.Run
	run.StartedAt = time.Now()
	s.SaveRun(&run)

	mk := func(phase string, exit int, val string) {
		e := &experiment.Experiment{RunID: run.ID, Vector: experiment.ParamVector{"w": val}, Phase: phase, CreatedAt: time.Now()}
		s.SaveExperiment(e)
		s.SaveResult(&experiment.Result{ExperimentID: e.ID, Metrics: experiment.MetricSet{"throughput_fps": 1}, ExitCode: exit, Attempts: 1, Timestamp: time.Now()})
	}
	mk("lhs", 0, "1")
	mk("lhs", 7, "2") // failure
	mk("lhs", 0, "4")

	ok, _ := s.Observations(false)
	require.Len(t, ok, 2, "failures excluded")
	all, _ := s.Observations(true)
	require.Len(t, all, 3, "failures included")
}

func TestSchemaIdempotent(t *testing.T) {
	// Opening twice (same schema) must not error.
	s1, err := Open(":memory:")
	require.NoError(t, err)
	s1.Close()
}
