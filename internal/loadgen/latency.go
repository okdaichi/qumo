package loadgen

import (
	"sync/atomic"
	"time"
)

// latencyHist is a lock-free end-to-end latency histogram. Buckets are 0.1 ms
// wide from 0 to 1 s, plus one overflow bucket, so it stays accurate in the
// sub-millisecond-to-few-millisecond range that dominates single-host loopback
// while still capturing tail outliers. It is safe for concurrent use from the
// many subscriber goroutines: each observation is a single atomic add.
type latencyHist struct {
	buckets [latHistBuckets + 1]atomic.Int64 // last cell is the >=1s overflow
	count   atomic.Int64
}

const (
	latHistBuckets  = 10000                  // 0..1000ms at 0.1ms resolution
	latHistStep     = 100 * time.Microsecond // bucket width
	latHistOverflow = latHistBuckets         // index of the overflow bucket
)

// observe records one latency sample. Negative samples (clock skew / malformed
// payload) are dropped.
func (h *latencyHist) observe(d time.Duration) {
	if d < 0 {
		return
	}
	idx := int(d / latHistStep)
	if idx >= latHistBuckets {
		idx = latHistOverflow
	}
	h.buckets[idx].Add(1)
	h.count.Add(1)
}

// percentile returns the p-th percentile latency (p in [0,100]) by nearest-rank
// over the bucket counts. The returned value is the upper edge of the bucket the
// rank falls into; the overflow bucket reports latHistBuckets*latHistStep (1s)
// as a floor. Returns 0 when no samples were recorded.
func (h *latencyHist) percentile(p float64) time.Duration {
	total := h.count.Load()
	if total == 0 {
		return 0
	}
	if p < 0 {
		p = 0
	}
	if p > 100 {
		p = 100
	}
	// nearest-rank: smallest bucket whose cumulative count >= ceil(p/100 * total)
	rank := max(int64(p/100*float64(total)+0.999999), 1)
	var cum int64
	for i := 0; i <= latHistOverflow; i++ {
		cum += h.buckets[i].Load()
		if cum >= rank {
			if i == latHistOverflow {
				return time.Duration(latHistBuckets) * latHistStep
			}
			return time.Duration(i+1) * latHistStep
		}
	}
	return time.Duration(latHistBuckets) * latHistStep
}

// samples returns how many observations were recorded.
func (h *latencyHist) samples() int64 { return h.count.Load() }

// reset zeroes all buckets. Called once after the establishment/settle phase so
// the reported percentiles and sample count reflect only the steady-state hold
// window, not the high-latency ramp. Concurrent observers may lose a handful of
// samples straddling the reset; that is negligible against the hold-window count.
func (h *latencyHist) reset() {
	for i := range h.buckets {
		h.buckets[i].Store(0)
	}
	h.count.Store(0)
}
