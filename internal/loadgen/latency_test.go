package loadgen

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestLatencyHist_Percentile(t *testing.T) {
	tests := map[string]struct {
		observe []time.Duration
		p       float64
		want    time.Duration
	}{
		"empty is zero": {observe: nil, p: 50, want: 0},
		"single sample": {observe: []time.Duration{500 * time.Microsecond}, p: 99, want: 600 * time.Microsecond},
		"p50 of uniform 0..99 buckets": {
			// 1000 samples, one per 0.1ms bucket edge → p50 ≈ 50ms.
			observe: func() []time.Duration {
				s := make([]time.Duration, 1000)
				for i := range s {
					s[i] = time.Duration(i) * 100 * time.Microsecond
				}
				return s
			}(),
			p:    50,
			want: 50 * time.Millisecond,
		},
		"overflow caps at 1s": {observe: []time.Duration{5 * time.Second}, p: 99, want: 1000 * time.Millisecond},
		"negative dropped":    {observe: []time.Duration{-1 * time.Second}, p: 50, want: 0},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			h := &latencyHist{}
			for _, d := range tt.observe {
				h.observe(d)
			}
			assert.Equal(t, tt.want, h.percentile(tt.p))
		})
	}
}

func TestLatencyHist_Samples(t *testing.T) {
	h := &latencyHist{}
	h.observe(1 * time.Millisecond)
	h.observe(2 * time.Millisecond)
	h.observe(-1) // dropped
	assert.Equal(t, int64(2), h.samples())
}
