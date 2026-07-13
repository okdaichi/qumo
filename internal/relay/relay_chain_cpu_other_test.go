//go:build integration && !(linux || darwin)

package relay

import "time"

// processCPUTime is unavailable on this platform (no portable getrusage). The
// harness still reports latency and memory; CPU is reported on Linux/macOS.
func processCPUTime() time.Duration { return 0 }
