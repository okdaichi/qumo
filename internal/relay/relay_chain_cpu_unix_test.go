//go:build integration && (linux || darwin)

package relay

import (
	"syscall"
	"time"
)

// processCPUTime returns user+system CPU time consumed by this process. All
// in-process relays share the process, so the delta across a measurement is the
// whole chain's CPU cost (per-relay-added CPU ≈ delta-vs-shallower-chain).
func processCPUTime() time.Duration {
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
		return 0
	}
	return time.Duration(ru.Utime.Nano()+ru.Stime.Nano()) * time.Nanosecond
}
