package main

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCeilingSearch_Validate(t *testing.T) {
	tests := map[string]struct {
		s       ceilingSearch
		wantErr bool
	}{
		"valid geometric": {s: ceilingSearch{start: 2000, max: 50000, growth: 2}},
		"valid step":      {s: ceilingSearch{start: 1000, max: 50000, step: 1000}},
		"valid bisect":    {s: ceilingSearch{start: 2000, max: 50000, growth: 2, bisect: true, tol: 1000}},
		"start < 1":       {s: ceilingSearch{start: 0, max: 10, growth: 2}, wantErr: true},
		"max < start":     {s: ceilingSearch{start: 100, max: 50, growth: 2}, wantErr: true},
		"negative step":   {s: ceilingSearch{start: 1, max: 10, step: -1}, wantErr: true},
		"growth <= 1":     {s: ceilingSearch{start: 1, max: 10, growth: 1}, wantErr: true},
		"bisect tol < 1":  {s: ceilingSearch{start: 1, max: 10, growth: 2, bisect: true, tol: 0}, wantErr: true},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			err := tt.s.validate()
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
		})
	}
}

func TestCeilingSearch_NextClimb(t *testing.T) {
	tests := map[string]struct {
		s    ceilingSearch
		cur  int
		want int
	}{
		"geometric":         {s: ceilingSearch{growth: 2, max: 100000}, cur: 2000, want: 4000},
		"geometric clamps":  {s: ceilingSearch{growth: 2, max: 5000}, cur: 4000, want: 5000},
		"fixed step":        {s: ceilingSearch{step: 1000, max: 100000}, cur: 2000, want: 3000},
		"step clamps":       {s: ceilingSearch{step: 1000, max: 2500}, cur: 2000, want: 2500},
		"min advance guard": {s: ceilingSearch{growth: 1.0001, max: 100000}, cur: 1, want: 2},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.s.nextClimb(tt.cur))
		})
	}
}

// holdUpTo returns a probe that holds iff n <= k.
func holdUpTo(k int) func(int) (bool, error) {
	return func(n int) (bool, error) { return n <= k, nil }
}

func TestFindCeiling(t *testing.T) {
	tests := map[string]struct {
		s             ceilingSearch
		k             int // probe holds iff n <= k
		wantCeiling   int
		wantFirstFail int
		wantProbes    int
	}{
		"geometric, no bisect": {
			s:           ceilingSearch{start: 2000, max: 50000, growth: 2},
			k:           10000,
			wantCeiling: 8000, wantFirstFail: 16000, wantProbes: 4,
		},
		"geometric, bisect pins boundary": {
			s:           ceilingSearch{start: 2000, max: 50000, growth: 2, bisect: true, tol: 1000},
			k:           10000,
			wantCeiling: 10000, wantFirstFail: 11000, wantProbes: 7,
		},
		"held through max": {
			s:           ceilingSearch{start: 2000, max: 50000, growth: 2},
			k:           100000,
			wantCeiling: 50000, wantFirstFail: 0, wantProbes: 6,
		},
		"none hold": {
			s:           ceilingSearch{start: 2000, max: 50000, growth: 2},
			k:           500,
			wantCeiling: 0, wantFirstFail: 2000, wantProbes: 1,
		},
		"fixed step": {
			s:           ceilingSearch{start: 1000, max: 50000, step: 1000},
			k:           3500,
			wantCeiling: 3000, wantFirstFail: 4000, wantProbes: 4,
		},
		"start equals max, holds": {
			s:           ceilingSearch{start: 5000, max: 5000, growth: 2},
			k:           10000,
			wantCeiling: 5000, wantFirstFail: 0, wantProbes: 1,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := findCeiling(tt.s, holdUpTo(tt.k))
			require.NoError(t, err)
			assert.Equal(t, tt.wantCeiling, got.ceiling, "ceiling")
			assert.Equal(t, tt.wantFirstFail, got.firstFail, "firstFail")
			assert.Equal(t, tt.wantProbes, got.probes, "probes")
			if got.ceiling > 0 {
				assert.LessOrEqual(t, got.ceiling, tt.k, "ceiling should hold")
			}
			if got.firstFail > 0 {
				assert.Greater(t, got.firstFail, tt.k, "firstFail should not hold")
			}
		})
	}
}

func TestFindCeiling_ProbeErrorPropagates(t *testing.T) {
	boom := errors.New("relay unreachable")
	calls := 0
	probe := func(_ int) (bool, error) {
		calls++
		if calls == 2 {
			return false, boom
		}
		return true, nil
	}
	_, err := findCeiling(ceilingSearch{start: 2000, max: 50000, growth: 2}, probe)
	assert.ErrorIs(t, err, boom)
	assert.Equal(t, 2, calls, "search stops at the erroring probe")
}
