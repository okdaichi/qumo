//go:build !instrument

package relay

import "time"

// stageCollector (default build) is a zero-size no-op. Every method — including
// now(), the sole time source for stage timing — is an empty inlinable body, so
// the compiler elides all stage instrumentation from the production hot path.
// The real implementation lives in stage_latency_instrument.go
// (-tags instrument).
type stageCollector struct{}

func (*stageCollector) now() time.Time { return time.Time{} }

func (*stageCollector) ingressFrame(time.Time) {}

func (*stageCollector) stampArrival(*groupCache) {}

func (*stageCollector) ringResidence(*groupCache, time.Time) {}

func (*stageCollector) groupOpen(time.Time) {}

func (*stageCollector) egressFrame(time.Time) {}

func (*stageCollector) report() *stageReport { return nil }

func (*stageCollector) reset() {}
