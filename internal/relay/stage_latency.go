package relay

import "time"

// Stage-latency instrumentation (benchmark-time diagnostic).
//
// The relay's frame pipeline is decomposed into stages so a benchmark can
// attribute end-to-end latency to a specific segment instead of inferring it
// from throughput:
//
//	A IngressService — per-frame clone+publish time in groupRing.fill
//	R RingResidence  — group arrival (processGroup reserve) → deliverGroup entry,
//	                   per subscriber: the relay-internal queueing indicator
//	O GroupOpen      — OpenGroupAt duration (QUIC uni-stream open; the
//	                   MAX_STREAMS backpressure point)
//	C EgressService  — per-frame WriteFrame time
//
// The residual of end-to-end minus these stages is the transport legs
// (upstream receive + quic-go send-queue drain + subscriber read).
//
// The collector is a build-tagged dual implementation: the default build's
// stageCollector is a zero-size no-op whose methods (including the time.Now
// source, now()) compile to nothing, so production carries no overhead. Build
// with -tags instrument to record real histograms. This file holds the shared,
// build-independent surface.
//
// The report types and Server methods are exported even though their only
// in-repo callers are the integration-tagged benchmarks: the default-build
// unused linter cannot see build-tag-gated consumers, and the export marks the
// surface as consumed outside the default compilation unit.

// StageSnapshot is one stage's recorded latency distribution.
type StageSnapshot struct {
	N                  int64
	P50, P95, P99, Max time.Duration
}

// StageReport is a snapshot of all pipeline stages. Returned by
// Server.StageLatency; nil unless built with -tags instrument.
type StageReport struct {
	IngressService StageSnapshot
	RingResidence  StageSnapshot
	GroupOpen      StageSnapshot
	EgressService  StageSnapshot

	// Mechanism investigation (A: serialization vs B: shared-resource
	// contention). RingResidence (reserve→egress pickup) is split at the instant
	// fill first broadcast the group's data:
	//   RingFill = reserve → first broadcast   (fill-worker/ingest latency)
	//   RingWake = first broadcast → pickup     (egress wake + schedule latency)
	// and, independently, by how the egress goroutine reached the group:
	//   RingBehind = picked up directly in a delivery loop (subscriber was behind)
	//   RingWoken  = picked up after a notify wait (subscriber was caught up)
	// DeliverSpan is deliverGroup entry→end per subscriber (how long a subscriber
	// is busy per group). BroadcastDur is broadcast() wall time; FillSemWait is
	// the time processGroup blocks acquiring a fill-worker slot (ingest
	// backpressure). MaxConcurrentGroups/Deliveries are peak overlap gauges.
	RingFill                StageSnapshot
	RingWake                StageSnapshot
	RingBehind              StageSnapshot
	RingWoken               StageSnapshot
	DeliverSpan             StageSnapshot
	BroadcastDur            StageSnapshot
	FillSemWait             StageSnapshot
	BroadcastN              int64
	MaxConcurrentGroups     int64
	MaxConcurrentDeliveries int64
	// GroupInterArrival is the spacing between consecutive group reserves
	// (publisher group-open cadence). p50 ≈ gap and a tight spread mean groups
	// arrive paced in real time; a mass of near-zero deltas means burst creation.
	GroupInterArrival StageSnapshot
}

// StageLatency returns the per-stage latency distributions recorded since the
// last reset. It returns nil in the default build (no instrumentation).
func (s *Server) StageLatency() *StageReport {
	if s == nil || s.sampler == nil {
		return nil
	}
	return s.sampler.stageAgg.report()
}

// StageLatencyReset discards recorded stage samples so a benchmark can exclude
// its ramp-up phase. No-op in the default build.
func (s *Server) StageLatencyReset() {
	if s == nil || s.sampler == nil {
		return
	}
	s.sampler.stageAgg.reset()
}

// stagesRef returns the server-wide stage collector, or nil when sampling is
// disabled (nil sampler). All stageCollector methods are nil-safe.
func (s *statsSampler) stagesRef() *stageCollector {
	if s == nil {
		return nil
	}
	return &s.stageAgg
}
