package relay

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/qumo-dev/gomoqt/moqt"
)

// fanoutSizes is the subscriber-count sweep used to characterize how the
// per-frame egress accounting cost scales with fan-out. 1 → 100 covers the
// single-subscriber case through the relay's realistic operating regime.
var fanoutSizes = []int{1, 2, 4, 16, 64, 100}

// runEgressFanout drives `nSubs` goroutines that each repeat the per-frame
// egress-side accounting the relay does on every delivered frame:
//
//	frame := cache.next(i)
//	n := frame.Len()
//	d.egressCounter.Add(float64(n))   // ONE prometheus.Counter per track, shared
//	d.session.addEgress(int64(n))     // ONE atomic.Int64 per session, shared
//
// withCounters=false drops the two shared-atomics Add calls, yielding the
// contention-free baseline (next + Len only). Comparing the two across the
// fan-out sweep isolates the cache-line ping-pong cost of the shared counters.
//
// This is a maximal-contention upper bound: in production gw.WriteFrame (a
// QUIC stream write) spaces the Add calls apart in time, reducing contention.
// If the counters are NOT a bottleneck under maximal contention, they are
// definitively not one in production. The group is pre-filled and complete, so
// there is no waiting/wakeup on this path — next() always hits.
func runEgressFanout(b *testing.B, nSubs int, withCounters bool) {
	b.Helper()

	trackID := fmt.Sprintf("bench-egress-%d-%d", nSubs, time.Now().UnixNano())
	dist := newTrackDistributor(newTrackManager(), trackID, newBroadcastSession(""))
	defer close(dist.done)

	// Pre-fill one complete group so cache.next(i) is always a hit. Frame size
	// 1200B is representative of a typical video frame on the wire.
	const frameSize = 1200
	const nFrames = 64
	pool := NewFramePool(DefaultNewFrameCapacity)
	gc := newGroupCache(1, nFrames)
	src := moqt.NewFrame(frameSize)
	src.Write(make([]byte, frameSize))
	for range nFrames {
		gc.append(src, pool)
	}

	b.ResetTimer()
	b.ReportAllocs()

	var wg sync.WaitGroup
	opsPerSub := b.N / nSubs
	for range nSubs {
		wg.Go(func() {
			for i := range opsPerSub {
				f := gc.next(i % nFrames)
				if withCounters {
					nn := f.Len()
					dist.egressCounter.Add(float64(nn))
					dist.session.addEgress(int64(nn))
				}
			}
		})
	}
	wg.Wait()
}

// BenchmarkEgressAccounting_Fanout measures the per-frame egress accounting
// (next + egressCounter.Add + addEgress) under increasing fan-out. Hypothesis:
// the shared per-track prometheus.Counter and per-session atomic.Int64 ping-pong
// under contention, so ns/op grows super-linearly with subscriber count.
func BenchmarkEgressAccounting_Fanout(b *testing.B) {
	for _, n := range fanoutSizes {
		b.Run(fmt.Sprintf("subs=%d", n), func(b *testing.B) {
			runEgressFanout(b, n, true)
		})
	}
}

// BenchmarkEgressRead_Fanout is the contention-free baseline: the same loop
// without the two shared-atomics Add calls. ns/op should stay roughly flat
// across the fan-out sweep. The gap between this and the counters variant is
// the contention cost attributable to the shared counters.
func BenchmarkEgressRead_Fanout(b *testing.B) {
	for _, n := range fanoutSizes {
		b.Run(fmt.Sprintf("subs=%d", n), func(b *testing.B) {
			runEgressFanout(b, n, false)
		})
	}
}
