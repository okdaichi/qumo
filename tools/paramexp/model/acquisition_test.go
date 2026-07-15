package model

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fit1D fits a GP on a smooth function and returns it plus a held-out far point.
func fit1D(t *testing.T) (*GaussianProcess, []float64) {
	t.Helper()
	n := 16
	X := make([][]float64, n)
	y := make([]float64, n)
	for i := 0; i < n; i++ {
		x := float64(i) / float64(n-1) * 0.6 // cluster in [0,0.6]; 0.9 is far
		X[i] = []float64{x}
		y[i] = math.Sin(2 * math.Pi * x)
	}
	gp := NewGP(Options{Starts: 30})
	require.NoError(t, gp.Fit(X, y))
	return gp, []float64{0.9}
}

func TestExpectedImprovement_NonnegativeAndFavorsUncertain(t *testing.T) {
	gp, far := fit1D(t)
	best := 1.0
	ei := NewExpectedImprovement(best, 0.0)

	// A training point (low uncertainty, near best) has ~0 EI.
	trainingEI := ei(gp, []float64{0.3})
	// A far-from-data point is highly uncertain; EI there exceeds the known point.
	farEI := ei(gp, far)
	assert.GreaterOrEqual(t, trainingEI, 0.0)
	assert.GreaterOrEqual(t, farEI, 0.0)
	assert.Greater(t, farEI, trainingEI, "EI should favor the uncertain far point")
}

func TestUpperConfidenceBound_GrowsWithKappa(t *testing.T) {
	gp, x := fit1D(t)
	lo := NewUpperConfidenceBound(0.5)(gp, x)
	hi := NewUpperConfidenceBound(5.0)(gp, x)
	assert.Greater(t, hi, lo, "UCB must increase with kappa")
}

func TestPredictiveVariance_FarExceedsTraining(t *testing.T) {
	gp, far := fit1D(t)
	vari := NewPredictiveVariance()
	assert.Greater(t, vari(gp, far), vari(gp, []float64{0.3}),
		"predictive variance is higher far from training data")
}

func TestMaximizeAcquisition_AvoidsExcludedAndArgmax(t *testing.T) {
	gp, _ := fit1D(t)
	ei := NewExpectedImprovement(0.5, 0.0)
	rng := NewLCG(42)
	x1, v1 := MaximizeAcquisition(gp, ei, 1, 1000, rng, nil)
	require.NotNil(t, x1)
	assert.Greater(t, v1, 0.0)

	// Excluding x1 must yield a different maximizer.
	rng2 := NewLCG(42) // same candidate stream so the only difference is the exclusion
	x2, _ := MaximizeAcquisition(gp, ei, 1, 1000, rng2, [][]float64{x1})
	require.NotNil(t, x2)
	assert.False(t, nearEqual(x1, x2), "excluded point must not be re-picked")
}
