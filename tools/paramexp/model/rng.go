package model

// LCG is a small deterministic linear-congruential generator. Determinism
// matters for reproducible GP multistart fits and reproducible acquisition
// search; a fixed seed reproduces a run exactly.
type LCG struct {
	state uint64
}

// NewLCG returns a generator seeded by seed (0 → a fixed non-zero seed).
func NewLCG(seed uint64) *LCG {
	s := seed
	if s == 0 {
		s = 0x9e3779b97f4a7c15
	}
	return &LCG{state: s}
}

// next advances the generator (PCG-style constants) and returns the next state.
func (l *LCG) next() uint64 {
	l.state = l.state*6364136223846793005 + 1442695040888963407
	return l.state
}

// Float64 returns a value in [0, 1).
func (l *LCG) Float64() float64 {
	return float64(l.next()>>11) / float64(1<<53)
}

// Uniform returns a value in [lo, hi).
func (l *LCG) Uniform(lo, hi float64) float64 {
	return lo + l.Float64()*(hi-lo)
}

// Uint64 returns the raw next state (full-width).
func (l *LCG) Uint64() uint64 { return l.next() }
