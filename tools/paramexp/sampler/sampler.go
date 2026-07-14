// Package sampler generates parameter vectors (decoded to strings via an
// encoding.Encoder) from a parameter space. Implementations: LHS, Sobol
// (Joe-Kuo direction numbers), and Adaptive (neighbor exploration).
package sampler

import (
	"sort"

	"github.com/qumo-dev/qumo/tools/paramexp/encoding"
	"github.com/qumo-dev/qumo/tools/paramexp/experiment"
)

// Sampler generates n parameter vectors by sampling in [0,1]^D and decoding
// each point to a string ParamVector.
type Sampler interface {
	Sample(enc *encoding.Encoder, n int) ([]experiment.ParamVector, error)
}

// --- LHS ---

// LHS implements Latin Hypercube Sampling: each dimension is partitioned into
// n strata and exactly one sample is drawn per stratum (permuted per dimension),
// giving full one-dimensional coverage.
type LHS struct {
	Seed uint64 // 0 → default deterministic seed
}

// Sample generates n vectors.
func (s LHS) Sample(enc *encoding.Encoder, n int) ([]experiment.ParamVector, error) {
	dim := enc.Dim()
	if n <= 0 {
		return nil, nil
	}
	seed := s.Seed
	if seed == 0 {
		seed = 12345
	}
	perm := make([][]int, dim)
	for d := range dim {
		perm[d] = randPerm(n, seed+uint64(d))
	}
	vectors := make([]experiment.ParamVector, n)
	for i := range n {
		x := make([]float64, dim)
		for d := range dim {
			// Center of the stratum (no jitter): keeps the LHS coverage exact and
			// is deterministic. The encoder rounds discrete/categorical to levels.
			stratum := perm[d][i]
			x[d] = (float64(stratum) + 0.5) / float64(n)
		}
		v, err := enc.Decode(x)
		if err != nil {
			return nil, err
		}
		vectors[i] = v
	}
	return vectors, nil
}

// --- Sobol (Joe-Kuo direction numbers) ---

// Sobol is a placeholder for a Joe-Kuo low-discrepancy sequence.
//
// A first cut of the direction-number recurrence produced a sequence that,
// while it matched reference points for the first few samples, was not a true
// (0,m)-net and degenerated to covering only half the space over long runs —
// repeating the "degenerate Sobol" failure this package was created to avoid.
// Rather than ship a subtly-broken generator, Sample falls back to LHS until a
// direction-number table verified against the (0,m)-net property lands (a
// roadmap phase-2 item, alongside Sobol variance-decomposition sensitivity).
type Sobol struct{}

// Sample delegates to LHS until a verified Sobol generator is implemented.
func (Sobol) Sample(enc *encoding.Encoder, n int) ([]experiment.ParamVector, error) {
	return LHS{}.Sample(enc, n)
}

// --- Adaptive ---

// Adaptive explores neighbors of the best-performing prior observations: for
// each top result it steps ±1 level along each discrete/categorical dimension.
type Adaptive struct {
	Existing []experiment.ParamVector // already-tried vectors (deduplicated against)
}

// SampleNear returns up to n untried neighbor vectors near the best observations.
func (a *Adaptive) SampleNear(enc *encoding.Encoder, obs []experiment.Observation, n int, objective string, space experiment.ParamSpace) ([]experiment.ParamVector, error) {
	if n <= 0 || len(obs) == 0 {
		return nil, nil
	}
	sorted := append([]experiment.Observation(nil), obs...)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Metrics[objective] > sorted[j].Metrics[objective]
	})

	if a.Existing == nil {
		a.Existing = make([]experiment.ParamVector, 0)
	}
	// Seed "existing" with all observations to avoid re-sampling them.
	for _, o := range obs {
		if !contains(a.Existing, o.Vector) {
			a.Existing = append(a.Existing, o.Vector)
		}
	}

	topK := n
	if topK > len(sorted) {
		topK = len(sorted)
	}
	neighbors := make([]experiment.ParamVector, 0, n)
	for i := 0; i < topK && len(neighbors) < n; i++ {
		base := sorted[i].Vector
		for _, p := range space.Params {
			if p.Type == experiment.TypeContinuous {
				continue // continuous has no adjacent level to step to
			}
			curIdx := indexOf(p.Values, base[p.Name])
			if curIdx < 0 {
				continue
			}
			for _, delta := range []int{-1, 1} {
				nIdx := curIdx + delta
				if nIdx < 0 || nIdx >= len(p.Values) {
					continue
				}
				neighbor := base.Copy()
				neighbor[p.Name] = p.Values[nIdx]
				if !contains(a.Existing, neighbor) && !contains(neighbors, neighbor) {
					neighbors = append(neighbors, neighbor)
					a.Existing = append(a.Existing, neighbor)
					if len(neighbors) >= n {
						return neighbors, nil
					}
				}
			}
		}
	}
	return neighbors, nil
}

// --- helpers ---

func contains(slice []experiment.ParamVector, v experiment.ParamVector) bool {
	for _, s := range slice {
		if s.Equal(v) {
			return true
		}
	}
	return false
}

func indexOf(values []string, s string) int {
	for i, v := range values {
		if v == s {
			return i
		}
	}
	return -1
}

// randPerm returns a deterministic Fisher-Yates permutation of [0,n) using an
// LCG (PCG-style constants) seeded by seed.
func randPerm(n int, seed uint64) []int {
	if n <= 0 {
		return nil
	}
	perm := make([]int, n)
	for i := range perm {
		perm[i] = i
	}
	if n == 1 {
		return perm
	}
	s := seed
	if s == 0 {
		s = 12345
	}
	for i := n - 1; i > 0; i-- {
		s = s*6364136223846793005 + 1442695040888963407
		j := int(s>>33) % (i + 1)
		perm[i], perm[j] = perm[j], perm[i]
	}
	return perm
}
