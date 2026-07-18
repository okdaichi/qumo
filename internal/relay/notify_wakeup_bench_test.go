package relay

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// BenchmarkEgressWait_IdleWakeups measures the scheduler cost of N subscriber
// egress goroutines parked in the egress wait-select while NO new data arrives
// (steady state between groups). This is the exact structure of the select at
// handler.go egress()/deliverGroup, and the cost it isolates is the poll-timer
// fallback: with NotifyTimeout small, each parked goroutine fires its timer and
// re-selects at 1/NotifyTimeout Hz regardless of media rate — pure scheduler
// overhead. The benchmark reports timer-driven wakeups per parked goroutine per
// second (wakeups_per_gps) so a NotifyTimeout change shows up directly.
//
// It uses a standalone select with the same 4 arms (notify / timer / done /
// ctx) rather than trackDistributor.egress, because egress needs a concrete
// *moqt.TrackWriter (a real QUIC stream). The wait structure — the thing
// NotifyTimeout governs — is reproduced faithfully here.
func BenchmarkEgressWait_IdleWakeups(b *testing.B) {
	for _, timeout := range []time.Duration{1 * time.Millisecond, 10 * time.Millisecond, 100 * time.Millisecond} {
		for _, nSubs := range []int{100, 1000} {
			b.Run(timeout.String()+"/subs="+itoa(nSubs), func(b *testing.B) {
				benchIdleWakeups(b, timeout, nSubs)
			})
		}
	}
}

func benchIdleWakeups(b *testing.B, timeout time.Duration, nSubs int) {
	b.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})

	// Run the parked goroutines for a fixed wall-clock window and count how many
	// times the timer fires across all of them. This measures the steady-state
	// idle wakeup RATE, which b.N (iteration count) cannot express directly.
	const window = 200 * time.Millisecond
	var wakeups atomic.Int64

	b.ResetTimer()
	b.ReportAllocs()

	for iter := 0; iter < b.N; iter++ {
		wakeups.Store(0)
		stop := make(chan struct{})
		var wg sync.WaitGroup
		for range nSubs {
			wg.Go(func() {
				notify := make(chan struct{}, 1) // idle: never signalled
				timer := time.NewTimer(timeout)
				defer timer.Stop()
				for {
					select {
					case <-notify:
					case <-timer.C:
						wakeups.Add(1)
						timer.Reset(timeout)
					case <-done:
						return
					case <-ctx.Done():
						return
					case <-stop:
						return
					}
				}
			})
		}
		time.Sleep(window)
		close(stop)
		wg.Wait()
	}

	b.StopTimer()
	// Wakeups per parked goroutine per second — lower is better; scales ~1/timeout.
	secs := float64(b.N) * window.Seconds()
	if secs > 0 {
		b.ReportMetric(float64(wakeups.Load())/secs/float64(nSubs), "wakeups/gps-sec")
	}
}

// itoa avoids importing strconv into a hot bench label path (trivial helper).
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
