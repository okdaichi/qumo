// Package doctor implements the read-only `qumo doctor` command: it explains
// the relay's effective runtime configuration and offers operator guidance
// without mutating anything. GC is its first (currently only) section; the
// report is structured so further checks (sockets, QUIC, kernel) can slot in.
package doctor

import (
	"fmt"
	"io"
	"os"

	"github.com/qumo-dev/qumo/internal/gctune"
)

// Run executes `qumo doctor`, writing the report to stdout. It is read-only and
// changes no configuration. It currently takes no arguments and rejects any, so
// a typo or stray flag gets feedback rather than a silently-ignored full report
// (a section filter like `doctor gc` is a future addition).
func Run(args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("doctor takes no arguments (got %q); it reports all sections", args)
	}
	return report(os.Stdout, gctune.EnvFromOS())
}

// report renders the doctor report for env to w. It is separated from Run so
// tests can capture output and vary the environment without touching os.Getenv.
func report(w io.Writer, env gctune.Env) error {
	ew := &errWriter{w: w}
	ew.printf("qumo doctor — effective runtime configuration\n\n")
	gcSection(ew, env)
	return ew.err
}

// gcSection explains the garbage-collector target: every input, the effective
// value, which input won and why, any warnings, and workload guidance.
func gcSection(ew *errWriter, env gctune.Env) {
	plan := gctune.Resolve(env)

	ew.printf("GC target (garbage collector)\n")
	ew.printf("  Inputs:\n")
	ew.printf("    GOGC        = %s\n", orUnset(env.GOGC))
	ew.printf("    RELAY_GOGC  = %s\n", orUnset(env.RelayGOGC))
	ew.printf("    GOMEMLIMIT  = %s\n", orUnset(env.GOMEMLIMIT))
	ew.printf("  Effective:    %s  (source: %s)\n", effectiveLabel(plan, env), plan.Source)
	ew.printf("  Why:          %s\n", plan.Reason)

	for _, warn := range plan.Warnings {
		ew.printf("  Warning:      %s\n", warn)
	}
	if env.GOMEMLIMIT != "" {
		ew.printf("  Warning:      GOMEMLIMIT is set (%s); not recommended for the relay's large stable live set — capping memory forces constant GC and collapses throughput.\n", env.GOMEMLIMIT)
	}

	ew.printf("  Guidance:     A fan-out relay's goroutine stacks dominate RSS, so GC-scan CPU\n")
	ew.printf("                grows with session count and becomes the ceiling. On big-memory\n")
	ew.printf("                hosts pushing >15K sessions, set RELAY_GOGC (600–1600 reached\n")
	ew.printf("                ~18–20K on an 8-core host). GOGC always overrides. Do not set\n")
	ew.printf("                GOMEMLIMIT for this workload.\n")
}

// errWriter wraps an io.Writer and remembers the first write error, so a run of
// best-effort Fprintf calls can be checked once at the end rather than after
// each line (the "errors are values" pattern). Once a write fails, later
// printf calls are no-ops.
type errWriter struct {
	w   io.Writer
	err error
}

// printf formats and writes one line unless a prior write already failed.
func (e *errWriter) printf(format string, a ...any) {
	if e.err != nil {
		return
	}
	_, e.err = fmt.Fprintf(e.w, format, a...)
}

// effectiveLabel renders the GC target in force for display. RELAY_GOGC and the
// runtime default resolve to a percentage; a GOGC that the runtime owns is
// shown as its raw value, which may be non-numeric (e.g. "off").
func effectiveLabel(plan gctune.Plan, env gctune.Env) string {
	if plan.Source == gctune.SourceGOGC {
		return fmt.Sprintf("GOGC=%s (runtime-controlled)", env.GOGC)
	}
	return fmt.Sprintf("%d%%", plan.EffectivePercent())
}

// orUnset renders an empty env value as an explicit "(unset)" marker.
func orUnset(v string) string {
	if v == "" {
		return "(unset)"
	}
	return v
}
