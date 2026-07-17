package relay

import "time"

// Per-stage relay latency instrumentation types. Always compiled (both the default
// and //go:build instrument builds) so the benchmark harness can reference them
// unconditionally; the recording machinery behind them is build-tagged — see
// stage_latency.go (no-op) and stage_latency_instrument.go (real).
//
// The goal is to localize WHERE frame latency accumulates under load, replacing
// black-box throughput inference with direct measurement. The frame lifecycle is
// decomposed into stages; their P50s sum (roughly) to the end-to-end latency, and
// the gap between that sum and the measured end-to-end is the Residual — the
// quic-go sendQueue→syscall drain, i.e. the serial socket bottleneck suspected of
// owning the latency growth.

// stageSnapshot is a latency distribution for one pipeline stage, reported as
// nearest-rank percentiles. Populated only under //go:build instrument.
type stageSnapshot struct {
	N   int // sample count
	P50 time.Duration
	P95 time.Duration
	P99 time.Duration
	Max time.Duration
}

// stageReport is the per-stage latency breakdown surfaced by Server.stageLatency.
//
// Ingress/Residence/Egress/Enqueue are recorded by the collector. EndToEnd and
// Residual are NOT collected here: EndToEnd is measured externally (the payload-
// embedded publish timestamp, read by subscribers), and the caller fills it then
// derives Residual = EndToEnd.P50 − (Ingress.P50 + Residence.P50 + Egress.P50 +
// Enqueue.P50), which isolates the quic-go sendQueue→syscall drain (plus wire +
// subscriber read, both ~0 on loopback).
type stageReport struct {
	Transit   stageSnapshot // publisher WriteFrame → relay ingest arrival (ingress QUIC transport)
	Ingress   stageSnapshot // A: per-frame clone + RCU publish (groupRing.fill)
	Residence stageSnapshot // R: group arrival → egress deliverGroup start
	Egress    stageSnapshot // C: per-frame gw.WriteFrame
	Enqueue   stageSnapshot // D: WriteFrame return → quic-go PacketSent (Phase 2)
	EndToEnd  stageSnapshot // E2E: payload publish → subscriber read (caller-filled)
	Residual  time.Duration // E2E.P50 − sum of stage P50s = egress QUIC transport (caller-computed)
}
