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

// stageSnapshot is one stage's recorded latency distribution.
type stageSnapshot struct {
	N                  int64
	P50, P95, P99, Max time.Duration
}

// stageReport is a snapshot of all pipeline stages. Returned by
// Server.stageLatency; nil unless built with -tags instrument.
type stageReport struct {
	IngressService stageSnapshot
	RingResidence  stageSnapshot
	GroupOpen      stageSnapshot
	EgressService  stageSnapshot
}

// stageLatency returns the per-stage latency distributions recorded since the
// last reset. It returns nil in the default build (no instrumentation).
func (s *Server) stageLatency() *stageReport {
	if s == nil || s.sampler == nil {
		return nil
	}
	return s.sampler.stageAgg.report()
}

// stageLatencyReset discards recorded stage samples so a benchmark can exclude
// its ramp-up phase. No-op in the default build.
func (s *Server) stageLatencyReset() {
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
