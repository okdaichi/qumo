package model

// lcg is a small deterministic linear-congruential generator used for the
// multistart hyperparameter search. Determinism matters for reproducible fits.
type lcg struct {
	state uint64
}

func newLCG(seed uint64) *lcg {
	s := seed
	if s == 0 {
		s = 0x9e3779b97f4a7c15
	}
	return &lcg{state: s}
}

// next advances the generator (PCG-style constants) and returns the next state.
func (l *lcg) next() uint64 {
	l.state = l.state*6364136223846793005 + 1442695040888963407
	return l.state
}

// uniform returns a float in [lo, hi).
func (l *lcg) uniform(lo, hi float64) float64 {
	x := float64(l.next()>>11) / float64(1<<53)
	return lo + x*(hi-lo)
}
