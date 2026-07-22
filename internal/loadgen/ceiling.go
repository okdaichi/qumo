package loadgen

import "fmt"

// ceilingSearch parameterizes the auto-ceiling search: climb session counts
// until the relay stops holding, then optionally bisect the boundary.
type ceilingSearch struct {
	start  int     // first session count to probe
	max    int     // upper bound / safety cap
	step   int     // fixed climb increment; 0 = geometric
	growth float64 // geometric factor when step == 0
	bisect bool    // refine the boundary after the first failure
	tol    int     // bisection stops when firstFail-lastHold <= tol
}

func (s ceilingSearch) validate() error {
	if s.start < 1 {
		return fmt.Errorf("--start must be >= 1 (got %d)", s.start)
	}
	if s.max < s.start {
		return fmt.Errorf("--max (%d) must be >= --start (%d)", s.max, s.start)
	}
	if s.step < 0 {
		return fmt.Errorf("--step must be >= 0 (got %d)", s.step)
	}
	if s.step == 0 && s.growth <= 1 {
		return fmt.Errorf("--growth must be > 1 when --step is 0 (got %g)", s.growth)
	}
	if s.bisect && s.tol < 1 {
		return fmt.Errorf("--bisect-tol must be >= 1 (got %d)", s.tol)
	}
	return nil
}

// nextClimb returns the next session count above cur, clamped to max. Geometric
// (cur*growth) unless a fixed step is set. Always advances by at least 1 so the
// climb can't stall.
func (s ceilingSearch) nextClimb(cur int) int {
	var next int
	if s.step > 0 {
		next = cur + s.step
	} else {
		next = int(float64(cur) * s.growth)
		if next <= cur {
			next = cur + 1
		}
	}
	if next > s.max {
		next = s.max
	}
	return next
}

// ceilingResult is the outcome of an auto-ceiling search.
type ceilingResult struct {
	ceiling   int // highest N that held (0 if none held)
	firstFail int // lowest N that failed (0 if nothing failed within max)
	probes    int // number of probes run
}

// findCeiling climbs session counts (geometric or fixed step) until probe
// reports a non-hold or the cap is reached, then optionally bisects the
// [lastHold, firstFail] interval to within tol. probe(n) reports whether N
// sessions held; all I/O lives in probe, so this control logic is pure and
// unit-testable. It stops early, propagating the error, if probe errors.
func findCeiling(s ceilingSearch, probe func(n int) (bool, error)) (ceilingResult, error) {
	var res ceilingResult
	lastHold, firstFail := 0, 0

	// Climb from start until a probe fails or we hold at the cap.
	n := s.start
	for {
		held, err := probe(n)
		res.probes++
		if err != nil {
			return res, err
		}
		if !held {
			firstFail = n
			break
		}
		lastHold = n
		if n >= s.max {
			break // held at/above the cap
		}
		n = s.nextClimb(n)
	}

	// Bisect the boundary between the last hold and the first failure.
	if s.bisect && lastHold > 0 && firstFail > 0 {
		lo, hi := lastHold, firstFail
		for hi-lo > s.tol {
			mid := lo + (hi-lo)/2
			held, err := probe(mid)
			res.probes++
			if err != nil {
				return res, err
			}
			if held {
				lo = mid
			} else {
				hi = mid
			}
		}
		lastHold, firstFail = lo, hi
	}

	res.ceiling = lastHold
	res.firstFail = firstFail
	return res, nil
}

// printCeiling writes the auto-ceiling summary to stdout.
func printCeiling(s ceilingSearch, r ceilingResult) {
	fmt.Printf("\n=== auto-ceiling ===\n")
	if r.ceiling == 0 {
		fmt.Printf("  no HOLD: even --start=%d could not hold (%d probe(s))\n", s.start, r.probes)
		return
	}
	fmt.Printf("  ceiling (highest HOLDS): %d sessions (%d probe(s))\n", r.ceiling, r.probes)
	if r.firstFail > 0 {
		fmt.Printf("  first CANNOT-HOLD at:    %d sessions\n", r.firstFail)
		if !s.bisect {
			fmt.Printf("  (add --bisect to pin the boundary within [%d, %d])\n", r.ceiling, r.firstFail)
		}
	} else {
		fmt.Printf("  held through --max=%d — raise --max to find the true ceiling\n", s.max)
	}
}
