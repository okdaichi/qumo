// Package model provides surrogate models of the response surface f: X→y.
// The primary implementation is a Gaussian Process with automatic relevance
// determination (ARD), which predicts both a mean and a predictive variance
// (uncertainty). Random Forest / Gradient-Boosted Trees (later phases) will
// implement the same Surrogate interface.
package model

import "math"

// Kernel evaluates a covariance between two points in [0,1]^D given per-dimension
// length-scales. Implementations must be symmetric.
type Kernel interface {
	// Eval computes k(a,b). Lens is the per-dimension length-scale vector.
	Eval(a, b, lens []float64) float64
	// Name returns a short identifier.
	Name() string
}

// RBF is the anisotropic squared-exponential (Gaussian) kernel:
//
//	k(a,b) = exp(-0.5 * Σ_d ((a_d-b_d)/ℓ_d)²)
//
// It is multiplied by the signal variance σ_f² at use sites. It is smooth,
// infinitely differentiable, and the ARD length-scales directly express each
// dimension's relevance.
type RBF struct{}

func (RBF) Name() string { return "rbf" }

func (RBF) Eval(a, b, lens []float64) float64 {
	var sq float64
	for d := range a {
		diff := (a[d] - b[d]) / lens[d]
		sq += diff * diff
	}
	return math.Exp(-0.5 * sq)
}

// Matern is the Matérn kernel with fixed ν. ν=1.5 (default) gives a once-
// differentiable surface, useful when the response is rougher than RBF assumes.
type Matern struct {
	Nu float64 // 1.5 or 2.5; default 1.5
}

func (m Matern) Name() string { return "matern" }

func (m Matern) Eval(a, b, lens []float64) float64 {
	nu := m.Nu
	if nu == 0 {
		nu = 1.5
	}
	var sq float64
	for d := range a {
		diff := (a[d] - b[d]) / lens[d]
		sq += diff * diff
	}
	r := math.Sqrt(sq)
	switch nu {
	case 0.5:
		return math.Exp(-r)
	case 1.5:
		return (1 + math.Sqrt(3)*r) * math.Exp(-math.Sqrt(3)*r)
	case 2.5:
		s := math.Sqrt(5) * r
		return (1 + s + s*s/3) * math.Exp(-s)
	default:
		// generic via Bessel functions is omitted; fall back to 1.5 form scaled.
		return (1 + math.Sqrt(3)*r) * math.Exp(-math.Sqrt(3)*r)
	}
}
