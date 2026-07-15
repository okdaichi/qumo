package model

import (
	"fmt"

	"github.com/qumo-dev/qumo/tools/paramexp/experiment"
)

// FitGP fits a GaussianProcess on the given objective across observations. When
// any observation carries replicate variance (N>1) it fits heteroscedastically
// (per-point observation noise = Variances/N); otherwise homoscedastically.
// Observations lacking an encoded vector or the objective metric are skipped.
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
