package ingest

import (
	"fmt"
	"testing"
)

// BenchmarkTrackBufferNotify measures notify()'s per-call fan-out cost across a
// range of subscriber counts. Two scenarios bracket the optimization proposed
// in the trackBuffer.notify fast-path change (a len(ch)==0 check before the
// non-blocking select):
//
//   - empty: every subscriber channel is drained each iteration — the steady
//     state where subscribers keep up. The fast path adds one len() load +
//     branch per subscriber before a send that still succeeds, so this scenario
//     captures any common-path regression.
//   - full: every subscriber channel already holds a pending signal (busy
//     subscribers / a burst the consumer hasn't drained). The fast path lets
//     notify skip the select entirely, so this scenario captures the intended
//     win — the case the PR's benchmark targets.
//
// Each b.N iteration calls notify() once over a pre-built subscriber set; the
// RLock acquisition and map walk are part of notify()'s real cost and stay
// inside the timed region.
//
// Note on the "empty" drain: it runs inside the timed loop, but the drain is
// identical with and without the optimization, so it is an additive constant
// that cancels in a base↔PR benchstat delta — the per-subscriber notify cost
// is what the delta isolates.
func BenchmarkTrackBufferNotify(b *testing.B) {
	for _, n := range []int{1, 10, 100, 1000} {
		b.Run(fmt.Sprintf("empty/subs=%d", n), func(b *testing.B) {
			buf := newTestTrackBuffer()
			chs := make([]chan struct{}, 0, n)
			for range n {
				ch := make(chan struct{}, 1)
				buf.subscribers[ch] = struct{}{}
				chs = append(chs, ch)
			}
			b.ResetTimer()
			for range b.N {
				buf.notify()
				// Reset every channel to empty so the next iteration measures
				// the len()==0 path again (subscribers keep up).
				for _, ch := range chs {
					select {
					case <-ch:
					default:
					}
				}
			}
		})

		b.Run(fmt.Sprintf("full/subs=%d", n), func(b *testing.B) {
			buf := newTestTrackBuffer()
			for range n {
				ch := make(chan struct{}, 1)
				buf.subscribers[ch] = struct{}{}
				ch <- struct{}{} // pre-fill: buffer stays full across iterations
			}
			b.ResetTimer()
			for range b.N {
				buf.notify()
			}
		})
	}
}
