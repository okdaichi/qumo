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

func (*stageCollector) clearArrival(*groupCache) {}

func (*stageCollector) groupOpen(time.Time) {}

func (*stageCollector) egressFrame(time.Time) {}

func (*stageCollector) report() *StageReport { return nil }

func (*stageCollector) reset() {}

// Mechanism-investigation no-ops (see stage_latency.go / _instrument.go).

func (*stageCollector) groupBroadcast(*groupCache) {}

func (*stageCollector) ringResidenceSplit(*groupCache, time.Time, bool) {}

func (*stageCollector) enterDeliver(*groupCache) {}

func (*stageCollector) exitDeliver(*groupCache, time.Time) {}

func (*stageCollector) groupReleased(*groupCache) {}

func (*stageCollector) broadcastTimed(time.Time) {}

func (*stageCollector) fillSemWaited(time.Time) {}
