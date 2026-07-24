//go:build instrument

package relay

import (
	"sync/atomic"
	"time"
)

// Real stage-latency collector (-tags instrument). Lock-free two-tier linear
// histograms: 1µs resolution up to 10ms (service times are µs-scale), 100µs
// resolution up to 1s (queueing times are ms-scale), plus one overflow bucket.
// All observation paths are a single bucket index computation + two atomic
// adds, so the collector itself does not serialize the fan-out it measures.
//
// CAVEAT: an instrumented build perturbs throughput (extra time.Now per frame
// per subscriber). Use it for latency ATTRIBUTION only; throughput and absolute
// e2e latency claims come from the clean build.

const (
	stageFineStep      = time.Microsecond
	stageFineMax       = 10 * time.Millisecond
	stageFineBuckets   = int(stageFineMax / stageFineStep) // 10_000
	stageCoarseStep    = 100 * time.Microsecond
	stageCoarseMax     = time.Second
	stageCoarseBuckets = int((stageCoarseMax - stageFineMax) / stageCoarseStep) // 9_900
	stageBuckets       = stageFineBuckets + stageCoarseBuckets + 1              // + overflow
)

type stageHist struct {
	buckets [stageBuckets]atomic.Int64
	count   atomic.Int64
	max     atomic.Int64
}

func (h *stageHist) observe(d time.Duration) {
	if d < 0 {
		return
	}
	var idx int
	switch {
	case d < stageFineMax:
		idx = int(d / stageFineStep)
	case d < stageCoarseMax:
		idx = stageFineBuckets + int((d-stageFineMax)/stageCoarseStep)
	default:
		idx = stageBuckets - 1
	}
	h.buckets[idx].Add(1)
	h.count.Add(1)
	for {
		cur := h.max.Load()
		if int64(d) <= cur || h.max.CompareAndSwap(cur, int64(d)) {
			return
		}
	}
}

// stageBucketUpper is the inclusive upper bound of bucket idx — the value a
// nearest-rank percentile reports for samples landing in that bucket.
func stageBucketUpper(idx int) time.Duration {
	if idx < stageFineBuckets {
		return time.Duration(idx+1) * stageFineStep
	}
	if idx < stageBuckets-1 {
		return stageFineMax + time.Duration(idx-stageFineBuckets+1)*stageCoarseStep
	}
	return stageCoarseMax // overflow: reported as the ceiling
}

// percentileFromCounts is nearest-rank over a bucket-count snapshot.
func percentileFromCounts(counts []int64, total int64, p float64) time.Duration {
	if total == 0 {
		return 0
	}
	rank := int64(p / 100 * float64(total))
	if rank < 1 {
		rank = 1
	}
	var cum int64
	for i, c := range counts {
		cum += c
		if cum >= rank {
			return stageBucketUpper(i)
		}
	}
	return stageBucketUpper(stageBuckets - 1)
}

func (h *stageHist) snapshot() StageSnapshot {
	counts := make([]int64, stageBuckets)
	var total int64
	for i := range h.buckets {
		c := h.buckets[i].Load()
		counts[i] = c
		total += c
	}
	return StageSnapshot{
		N:   total,
		P50: percentileFromCounts(counts, total, 50),
		P95: percentileFromCounts(counts, total, 95),
		P99: percentileFromCounts(counts, total, 99),
		Max: time.Duration(h.max.Load()),
	}
}

func (h *stageHist) reset() {
	for i := range h.buckets {
		h.buckets[i].Store(0)
	}
	h.count.Store(0)
	h.max.Store(0)
}

// stageCollector (instrument build) records real per-stage histograms. All
// methods are nil-safe so call sites need no guards.
type stageCollector struct {
	ingress stageHist // A: per-frame fill/append service
	ring    stageHist // R: group arrival → deliverGroup entry, per subscriber
	open    stageHist // O: OpenGroupAt duration
	egress  stageHist // C: per-frame WriteFrame service
}

func (c *stageCollector) now() time.Time {
	if c == nil {
		return time.Time{}
	}
	return time.Now()
}

func (c *stageCollector) ingressFrame(t0 time.Time) {
	if c == nil || t0.IsZero() {
		return
	}
	c.ingress.observe(time.Since(t0))
}

// stampArrival records the group's ring-arrival instant (atomically, since
// egress goroutines read it concurrently with the fill worker's writes).
func (c *stageCollector) stampArrival(gc *groupCache) {
	if c == nil || gc == nil {
		return
	}
	gc.ingressArrivalNano.Store(time.Now().UnixNano())
}

func (c *stageCollector) ringResidence(gc *groupCache, t time.Time) {
	if c == nil || gc == nil || t.IsZero() {
		return
	}
	arrival := gc.ingressArrivalNano.Load()
	if arrival == 0 {
		return // egress reached the cache before the arrival stamp
	}
	c.ring.observe(t.Sub(time.Unix(0, arrival)))
}

func (c *stageCollector) groupOpen(t0 time.Time) {
	if c == nil || t0.IsZero() {
		return
	}
	c.open.observe(time.Since(t0))
}

func (c *stageCollector) egressFrame(t0 time.Time) {
	if c == nil || t0.IsZero() {
		return
	}
	c.egress.observe(time.Since(t0))
}

func (c *stageCollector) report() *StageReport {
	if c == nil {
		return nil
	}
	return &StageReport{
		IngressService: c.ingress.snapshot(),
		RingResidence:  c.ring.snapshot(),
		GroupOpen:      c.open.snapshot(),
		EgressService:  c.egress.snapshot(),
	}
}

func (c *stageCollector) reset() {
	if c == nil {
		return
	}
	c.ingress.reset()
	c.ring.reset()
	c.open.reset()
	c.egress.reset()
}
