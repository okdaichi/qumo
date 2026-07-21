// Package gctune resolves and applies the qumo relay's garbage-collector target.
//
// A fan-out relay holds a large, STABLE live set — one QUIC connection per
// subscriber, whose ~9 goroutines' stacks dominate RSS (measured: Go heap
// in-use ~200MB while RSS ~1.4GB at ~14K sessions; the gap is off-heap
// goroutine stacks). Every GC cycle re-scans all of those stacks, so at the
// default GOGC=100 the GC-scan CPU grows with connection count and becomes the
// scaling ceiling. Because the live set is legitimate and stable (not garbage),
// collecting it less often costs only some peak RSS headroom while cutting GC
// CPU — raising the GC target has reached ~18–20K concurrent subscriber
// sessions on an 8-core host, roughly doubling the default ceiling.
//
// The policy is OPT-IN: the relay changes nothing unless the operator sets
// RELAY_GOGC. GOGC — the Go runtime's own knob — always takes precedence, and
// the relay never overrides it. The same Resolve feeds both the relay's startup
// Apply and the read-only `qumo doctor` explanation, so guidance and runtime
// can never drift.
//
// GOMEMLIMIT is deliberately not part of the policy: capping memory for this
// large-stable-live-set forces constant GC into a death-spiral (measured:
// GOMEMLIMIT configs collapsed to ~15–18K sessions while GOGC=high held 20K).
package gctune

import (
	"log/slog"
	"os"
	"runtime/debug"
	"strconv"
)

// Source identifies which input determined the effective GC target.
type Source string

const (
	// SourceGOGC means GOGC was set; the runtime honors it and the relay defers.
	SourceGOGC Source = "GOGC"
	// SourceRelayGOGC means RELAY_GOGC selected the GC target.
	SourceRelayGOGC Source = "RELAY_GOGC"
	// SourceRuntimeDefault means nothing was set; the GC stays at the runtime
	// default of 100.
	SourceRuntimeDefault Source = "runtime default"
)

// runtimeDefaultPercent is the Go runtime's built-in GOGC value, reported by
// the policy when neither GOGC nor RELAY_GOGC is set.
const runtimeDefaultPercent = 100

// Env holds the GC-relevant process environment. Passing it explicitly (rather
// than reading os.Getenv inside Resolve) keeps resolution pure and testable.
type Env struct {
	// GOGC is the Go runtime's own GC knob; when set the relay defers to it.
	GOGC string
	// RelayGOGC is qumo's opt-in, relay-specific GC override.
	RelayGOGC string
	// GOMEMLIMIT is reported for operator context; the relay never sets it and
	// Resolve does not act on it (see the package doc).
	GOMEMLIMIT string
}

// EnvFromOS reads the GC-relevant variables from the process environment.
func EnvFromOS() Env {
	return Env{
		GOGC:       os.Getenv("GOGC"),
		RelayGOGC:  os.Getenv("RELAY_GOGC"),
		GOMEMLIMIT: os.Getenv("GOMEMLIMIT"),
	}
}

// Plan is the outcome of applying the relay's GC policy to an Env. It is used
// two ways: Apply enacts it at relay startup, and `qumo doctor` renders it
// read-only.
type Plan struct {
	// Apply reports whether the relay should call debug.SetGCPercent. It is true
	// only when RELAY_GOGC selected a percent; when GOGC owns the setting or
	// nothing is set, the relay leaves the runtime alone.
	Apply bool
	// Percent is the GC target to set when Apply is true.
	Percent int
	// Source is the input that won.
	Source Source
	// Reason is a one-line, operator-facing explanation of why Source won.
	Reason string
	// Warnings lists non-fatal problems with the inputs (e.g. an unparsable
	// RELAY_GOGC), suitable for display or structured logging.
	Warnings []string
}

// EffectivePercent reports the GC target in force once the policy has run. When
// the relay applies RELAY_GOGC it is that value; when nothing is set it is the
// runtime default (100). It reports -1 when GOGC owns the setting, whose value
// may be non-numeric (e.g. "off"): callers should show the raw GOGC string in
// that case.
func (p Plan) EffectivePercent() int {
	switch p.Source {
	case SourceRelayGOGC:
		return p.Percent
	case SourceRuntimeDefault:
		return runtimeDefaultPercent
	default: // SourceGOGC
		return -1
	}
}

// Resolve applies the relay's opt-in GC policy to env. It never mutates
// runtime state.
//
// Precedence:
//   - GOGC set → the runtime already honors it; the relay does nothing.
//   - Else RELAY_GOGC a valid positive int → the relay raises the GC target.
//   - Else (nothing, or an invalid RELAY_GOGC) → no-op; the runtime default
//     (100) stays in place. An invalid RELAY_GOGC additionally warns.
func Resolve(env Env) Plan {
	if env.GOGC != "" {
		plan := Plan{
			Source: SourceGOGC,
			Reason: "GOGC is set; the Go runtime already honors it and the relay never overrides an explicit operator choice.",
		}
		if env.RelayGOGC != "" {
			// Surface the shadowed override so an operator who set RELAY_GOGC
			// isn't left wondering why tuning had no effect — this warning shows
			// up both at startup (Apply) and in `qumo doctor`.
			plan.Warnings = append(plan.Warnings,
				"RELAY_GOGC "+strconv.Quote(env.RelayGOGC)+" ignored because GOGC "+strconv.Quote(env.GOGC)+" is set (GOGC takes precedence)")
		}
		return plan
	}
	if env.RelayGOGC != "" {
		n, err := strconv.Atoi(env.RelayGOGC)
		if err != nil || n <= 0 {
			return Plan{
				Source:   SourceRuntimeDefault,
				Reason:   "RELAY_GOGC is set but not a positive integer; it was ignored and the runtime default (100) stays in place.",
				Warnings: []string{"invalid RELAY_GOGC " + strconv.Quote(env.RelayGOGC) + " (want a positive integer); ignored"},
			}
		}
		return Plan{
			Apply:   true,
			Percent: n,
			Source:  SourceRelayGOGC,
			Reason:  "RELAY_GOGC selected; the relay raises the GC target to cut GC-scan CPU for its large stable live set.",
		}
	}
	return Plan{
		Source: SourceRuntimeDefault,
		Reason: "neither GOGC nor RELAY_GOGC is set; the relay leaves the runtime default (100) in place. Set RELAY_GOGC on high-fan-out hosts to lift the session ceiling.",
	}
}

// Apply resolves the GC policy from the process environment, enacts it, and
// returns the Plan for inspection. It is the relay's startup hook; it changes
// the GC target only when the Plan says to (opt-in via RELAY_GOGC).
func Apply() Plan {
	plan := Resolve(EnvFromOS())
	for _, w := range plan.Warnings {
		slog.Warn("relay GC policy", "warning", w)
	}
	if plan.Apply {
		debug.SetGCPercent(plan.Percent)
		slog.Info("relay GC target tuned",
			"gogc_percent", plan.Percent,
			"source", string(plan.Source),
		)
	}
	return plan
}
