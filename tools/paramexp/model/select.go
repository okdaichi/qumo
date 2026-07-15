// Selection of acquisition-maximizing points that are novel (not in `exclude`).
// For a fully-discrete space the candidate set is enumerated exactly, so a novel
// point is always found if one exists (no premature exhaustion). For continuous
// spaces, random search + decode-level dedup is used.

package model

import (
	"sort"

	"github.com/qumo-dev/qumo/tools/paramexp/experiment"
)

// SelectByAcquisition returns up to n ParamVectors that maximize acq, excluding
// any in `exclude`. For a discrete space it enumerates all candidates (guaranteed
// to find novel points if any remain); for a continuous space it random-searches
// and dedups at the decoded-vector level. Results are ranked by acquisition desc.
func SelectByAcquisition(gp *GaussianProcess, acq Acquisition, enc *experiment.Encoder,
	space experiment.ParamSpace, exclude []experiment.ParamVector, n int, rng *LCG) []experiment.ParamVector {
	if gp == nil || enc == nil || n <= 0 {
		return nil
	}
	excluded := func(v experiment.ParamVector) bool {
		for _, e := range exclude {
			if e.Equal(v) {
				return true
			}
		}
		return false
	}

	type scored struct {
		vec experiment.ParamVector
		x   []float64
		val float64
	}

	var scoredCands []scored
	if space.IsDiscrete() {
		for _, v := range space.AllVectors() {
			if excluded(v) {
				continue
			}
			x, err := enc.Encode(v)
			if err != nil {
				continue
			}
			scoredCands = append(scoredCands, scored{vec: v, x: x, val: acq(gp, x)})
		}
	} else {
		dim := enc.Dim()
		candidates := dim * 1000
		seen := make(map[string]bool, len(exclude))
		for _, e := range exclude {
			seen[e.String()] = true
		}
		for i := 0; i < candidates && len(scoredCands) < n*5; i++ {
			x := make([]float64, dim)
			for d := range x {
				x[d] = rng.Float64()
			}
			vec, err := enc.Decode(x)
			if err != nil || seen[vec.String()] {
				continue
			}
			seen[vec.String()] = true
			scoredCands = append(scoredCands, scored{vec: vec, x: x, val: acq(gp, x)})
		}
	}

	sort.Slice(scoredCands, func(i, j int) bool { return scoredCands[i].val > scoredCands[j].val })
	out := make([]experiment.ParamVector, 0, n)
	for k := 0; k < n && k < len(scoredCands); k++ {
		out = append(out, scoredCands[k].vec)
	}
	return out
}
