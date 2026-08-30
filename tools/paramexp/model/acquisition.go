// Acquisition functions for Bayesian optimization. An Acquisition scores a
// candidate point x given a fitted GP; the BO scheduler and SuggestedNext pick
// points that maximize it, balancing exploitation (high predicted mean) and
// exploration (high predictive uncertainty).

package model

import "math"

// Acquisition scores a candidate point; higher = more worth measuring next.
type Acquisition func(gp *GaussianProcess, x []float64) float64

// NewExpectedImprovement returns the Expected-Improvement acquisition for
// maximization against the best objective seen so far (best). xi > 0 encourages
// exploration beyond the current best. EI = (μ−best−xi)·Φ(z) + σ·φ(z) with
// z = (μ−best−xi)/σ; at a deterministic point (σ≈0) it degenerates to the
// positive improvement max(0, μ−best−xi).
func NewExpectedImprovement(best, xi float64) Acquisition {
	return func(gp *GaussianProcess, x []float64) float64 {
		mean, std, err := gp.Predict(x)
		if err != nil {
			return math.Inf(-1)
		}
		improvement := mean - best - xi
		if std < 1e-12 {
			if improvement > 0 {
				return improvement
			}
			return 0
		}
		z := improvement / std
		phi := math.Exp(-0.5*z*z) / math.Sqrt(2*math.Pi) // normal PDF
		Phi := 0.5 * (1 + math.Erf(z/math.Sqrt2))        // normal CDF
		ei := improvement*Phi + std*phi
		if ei < 0 {
			return 0
		}
		return ei
	}
}

// NewUpperConfidenceBound returns the UCB acquisition μ + κ·σ. Larger κ ⇒ more
// exploration (favor uncertain points); κ=0 ⇒ pure exploitation (the predicted
// mean).
func NewUpperConfidenceBound(kappa float64) Acquisition {
	return func(gp *GaussianProcess, x []float64) float64 {
		mean, std, err := gp.Predict(x)
		if err != nil {
			return math.Inf(-1)
		}
		return mean + kappa*std
	}
}

// NewPredictiveVariance returns the pure-exploration acquisition σ — sample
// wherever the model is most uncertain. Useful for mapping the response surface
// rather than locating an optimum.
func NewPredictiveVariance() Acquisition {
	return func(gp *GaussianProcess, x []float64) float64 {
		_, std, err := gp.Predict(x)
		if err != nil {
			return math.Inf(-1)
		}
		return std
	}
}

// MaximizeAcquisition finds the point maximizing acq over [0,1]^dim by random
// search (n candidates drawn from rng). Points within 1e-9 of any in exclude
// (already-measured or already-believed-this-batch points) are skipped, so a
// batch call converges to distinct points. Returns the argmax and its value;
// returns (nil, -Inf) if every candidate is excluded.
func MaximizeAcquisition(gp *GaussianProcess, acq Acquisition, dim, n int, rng *LCG, exclude [][]float64) ([]float64, float64) {
	var bestX []float64
	bestVal := math.Inf(-1)
	for range n {
		x := make([]float64, dim)
		for d := range x {
			x[d] = rng.Float64()
		}
		if excluded(x, exclude) {
			continue
		}
		v := acq(gp, x)
		if v > bestVal {
			bestVal = v
			bestX = x
		}
	}
	return bestX, bestVal
}

// AcquisitionFor resolves a named acquisition ("ei"/"ucb"/"variance") with the
// given knobs (best/xi for EI, kappa for UCB). Empty/unknown ⇒ EI.
func AcquisitionFor(name string, best, xi, kappa float64) Acquisition {
	switch name {
	case "ucb":
		return NewUpperConfidenceBound(kappa)
	case "variance":
		return NewPredictiveVariance()
	default:
		return NewExpectedImprovement(best, xi)
	}
}

func excluded(x []float64, exclude [][]float64) bool {
	for _, e := range exclude {
		if nearEqual(x, e) {
			return true
		}
	}
	return false
}
