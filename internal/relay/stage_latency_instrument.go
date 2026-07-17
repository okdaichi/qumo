//go:build instrument

// Instrumented implementation of per-stage relay latency collection. Compiled only
// with -tags=instrument. Records per-frame / per-group latencies into sharded,
// capped accumulators and surfaces them via Server.StageLatency. Signatures MUST
// match stage_latency.go (the !instrument no-op build) exactly.
//
// CAVEAT: this build perturbs throughput (per-frame time.Now + sharded accumulator
// writes). Use it for latency ATTRIBUTION only; throughput claims come from the
// default build.

package relay

import (
	"encoding/binary"
	"sync"
	"sync/atomic"
	"time"

	"github.com/HdrHistogram/hdrhistogram-go"
	"github.com/quic-go/quic-go"
)

const stageShards = 16

// stageCollector holds per-stage latency histograms. Sharding keeps K-fanout
// append contention low; shards merge at Snapshot.
type stageCollector struct {
	transitH   stageHistogram
	ingressH   stageHistogram
	residenceH stageHistogram
	egressH    stageHistogram
	enqueueH   stageHistogram
}

func newStageCollector() *stageCollector { return &stageCollector{} }

func (c *stageCollector) now() time.Time { return time.Now() }

func (c *stageCollector) ingress(from time.Time) { c.ingressH.observe(time.Since(from)) }
func (c *stageCollector) egress(from time.Time)  { c.egressH.observe(time.Since(from)) }

// residence takes the egress deliverGroup start and the group's stored arrival
// (UnixNano): both endpoints are already known, so no extra time.Now is needed.
func (c *stageCollector) residence(start time.Time, arrivalNs int64) {
	c.residenceH.observe(start.Sub(time.Unix(0, arrivalNs)))
}

// transit records the publisher→relay ingress transport latency. The publish
// timestamp is payload body[8:16] (UnixNano) per the benchmark contract, so the
// instrumented relay is coupled to that layout — acceptable for a diagnostic.
// Splits the prior residual into ingress-transport (this) vs egress-transport.
func (c *stageCollector) transit(body []byte) {
	if len(body) < 16 {
		return
	}
	pubNs := int64(binary.BigEndian.Uint64(body[8:16]))
	c.transitH.observe(time.Since(time.Unix(0, pubNs)))
}

// stampArrival records the group's ingress-arrival time (UnixNano) for residence.
func (gc *groupCache) stampArrival(c *stageCollector) {
	gc.ingressArrivalNs.Store(c.now().UnixNano())
}

// applyStageTracer wires a quic-go connection tracer for stage D. No-op for Phase
// 1: stage D (Enqueue) stays empty and the quic-go sendQueue→syscall drain is left
// as the report Residual. Phase 2 sets cfg.Tracer to a factory whose Recorder
// captures qlog.PacketSent into the collector's enqueue histogram.
func applyStageTracer(*quic.Config, *stageCollector) {}

// StageLatency returns the per-stage latency report aggregated across all
// distributors that stamped into this Server's collector.
func (s *Server) StageLatency() *StageReport {
	if s.stages == nil {
		return nil
	}
	return &StageReport{
		Transit:   s.stages.transitH.snapshot(),
		Ingress:   s.stages.ingressH.snapshot(),
		Residence: s.stages.residenceH.snapshot(),
		Egress:    s.stages.egressH.snapshot(),
		Enqueue:   s.stages.enqueueH.snapshot(),
	}
}

// stageHistogram is a sharded, mutex-guarded HDR latency histogram. observe
// round-robins across shards via an atomic counter for an even spread (keeps
// K-fanout append contention low); snapshot merges all shards and reads
// percentiles from the merged HDR. HDR gives bounded fixed memory with every
// sample recorded (no cap/early bias) and accurate tail quantiles — the right
// tool for latency distributions.
type stageHistogram struct {
	shards [stageShards]stageShard
	next   atomic.Uint64
}

type stageShard struct {
	mu sync.Mutex
	h  *hdrhistogram.Histogram
}

// newStageHDR creates a latency HDR tracking 1ns..60s at 3 significant figures
// (~0.1% precision); 1ns lowestDiscernibleValue keeps sub-µs stages visible.
func newStageHDR() *hdrhistogram.Histogram {
	return hdrhistogram.New(1, 60_000_000_000, 3)
}

func (h *stageHistogram) observe(d time.Duration) {
	s := &h.shards[h.next.Add(1)%stageShards]
	s.mu.Lock()
	if s.h == nil {
		s.h = newStageHDR()
	}
	_ = s.h.RecordValue(int64(d)) // errors only on out-of-range; 60s ceiling covers it
	s.mu.Unlock()
}

func (h *stageHistogram) snapshot() StageSnapshot {
	merged := newStageHDR()
	for i := range h.shards {
		s := &h.shards[i]
		s.mu.Lock()
		if s.h != nil {
			merged.Merge(s.h)
		}
		s.mu.Unlock()
	}
	if merged.TotalCount() == 0 {
		return StageSnapshot{}
	}
	return StageSnapshot{
		N:   int(merged.TotalCount()),
		P50: time.Duration(merged.ValueAtPercentile(50)),
		P95: time.Duration(merged.ValueAtPercentile(95)),
		P99: time.Duration(merged.ValueAtPercentile(99)),
		Max: time.Duration(merged.Max()),
	}
}
