package model

import (
	"fmt"
	"sort"

	"github.com/qumo-dev/qumo/tools/paramexp/experiment"
)

// FitGP fits a GaussianProcess on the given objective across observations. When
// any observation carries replicate variance (N>1) it fits heteroscedastically
// (per-point observation noise = Variances/N); otherwise homoscedastically.
// Observations lacking an encoded vector or the objective metric are skipped.
// A flaky vector with only one successful replicate (N=1, zero variance) is
// assigned the median noise of well-replicated points so the GP downweights it
// rather than treating it as noise-free.
func FitGP(obs []experiment.Observation, objective string, opt Options) (*GaussianProcess, error) {
	var X [][]float64
	var yMean, yVar []float64
	replicated := false
	for _, o := range obs {
		if len(o.EncodedX) == 0 {
			continue
		}
		m, ok := o.Metrics[objective]
		if !ok {
			continue
		}
		v := o.Variances[objective]
		if o.N > 1 {
			v /= float64(o.N) // variance of the mean
			replicated = true
		}
		X = append(X, o.EncodedX)
		yMean = append(yMean, m)
		yVar = append(yVar, v)
	}
	if len(X) < 2 {
		return nil, fmt.Errorf("fitgp: need ≥2 observations with objective %q and an encoded vector", objective)
	}
	// Borrow variance for N=1 points (flaky vectors that yielded only one
	// replicate): they should be downweighted (high noise), not trusted (zero
	// noise). Use the median var/N of well-replicated points.
	if replicated {
		var positives []float64
		for _, v := range yVar {
			if v > 0 {
				positives = append(positives, v)
			}
		}
		if len(positives) > 0 {
			sort.Float64s(positives)
			median := positives[len(positives)/2]
			for i := range yVar {
				if yVar[i] == 0 {
					yVar[i] = median
				}
			}
		}
	}
	gp := NewGP(opt)
	var err error
	if replicated {
		err = gp.FitReplicated(X, yMean, yVar)
	} else {
		err = gp.Fit(X, yMean)
	}
	if err != nil {
		return nil, fmt.Errorf("fitgp: %w", err)
	}
	return gp, nil
}
