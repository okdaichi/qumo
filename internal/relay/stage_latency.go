//go:build !instrument

// Default-build (production) implementation of per-stage latency instrumentation.
// Every symbol here is a no-op so the hot path pays nothing: now() returns the
// zero time.Time (time.Now is never called), and the record methods are empty.
// The real implementation lives in stage_latency_instrument.go behind the
// `instrument` build tag. Signatures here MUST match that file exactly so the
// call sites in handler.go / group_cache.go are identical across builds.

package relay

import (
	"time"

	"github.com/quic-go/quic-go"
)

// stageCollector records per-stage frame latencies. No-op in the default build.
type stageCollector struct{}

// newStageCollector returns a no-op collector (default build). It is the default
// value for groupRing.stages / trackDistributor.stages so those are never nil.
func newStageCollector() *stageCollector { return &stageCollector{} }

// now returns the zero time (no syscall). Callers pair it with ingress/egress:
//
//	t0 := c.now()
//	...work...
//	c.ingress(t0)
//
// In the default build this constructs a zero time.Time and ingress/egress are
// empty, so the compiler elides the whole thing and no time.Now fires.
func (c *stageCollector) now() time.Time { return time.Time{} }

func (c *stageCollector) ingress(time.Time)  {}
func (c *stageCollector) egress(time.Time)   {}
func (c *stageCollector) residence(time.Time, int64) {}

// transit records the publisher WriteFrame → relay-arrival latency by reading the
// payload-embedded publish timestamp (body[8:16] = UnixNano, the bench contract).
// No-op here; the instrument build decodes and records it.
func (c *stageCollector) transit([]byte) {}

// stampArrival records a group's ingress-arrival time on its cache. No-op here.
func (gc *groupCache) stampArrival(*stageCollector) {}

// applyStageTracer wires a quic-go connection tracer for stage D. No-op in the
// default build (and in Phase 1 of the instrument build); Phase 2 sets cfg.Tracer
// under //go:build instrument.
func applyStageTracer(*quic.Config, *stageCollector) {}

// StageLatency returns the per-stage latency report, or nil when instrumentation
// is disabled (default build). Callers must nil-check before reading fields.
func (s *Server) StageLatency() *StageReport { return nil }
