package model

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGP_Recovers1DSurface fits a GP on sin(2πx) and checks held-out accuracy
// and that ~most points fall within ±2σ (the uncertainty is meaningful).
func TestGP_Recovers1DSurface(t *testing.T) {
	gp := NewGP(Options{Starts: 40})
	n := 24
	X := make([][]float64, n)
	y := make([]float64, n)
	for i := 0; i < n; i++ {
		x := float64(i) / float64(n-1)
		X[i] = []float64{x}
		y[i] = math.Sin(2 * math.Pi * x)
	}
	require.NoError(t, gp.Fit(X, y))

	// Held-out grid.
	var sumAbs float64
	var covered int
	gridN := 40
	for i := 0; i < gridN; i++ {
		x := float64(i) / float64(gridN - 1)
		m, s, err := gp.Predict([]float64{x})
		require.NoError(t, err)
		truth := math.Sin(2 * math.Pi * x)
		sumAbs += math.Abs(m - truth)
		if math.Abs(m-truth) <= 2*s+1e-6 {
			covered++
		}
	}
	mae := sumAbs / float64(gridN)
	assert.Less(t, mae, 0.15, "mean abs error should be small; got %f", mae)
	assert.Greater(t, covered, gridN*8/10, "≥80%% of points within ±2σ; got %d/%d", covered, gridN)
}

// TestGP_SensitivityOrdering checks that ARD length-scales correctly identify
// the relevant dimension: f depends only on x1, so x1 ≫ x2,x3 in sensitivity.
func TestGP_SensitivityOrdering(t *testing.T) {
	gp := NewGP(Options{Starts: 80})
	n := 40
	X := make([][]float64, n)
	y := make([]float64, n)
	for i := 0; i < n; i++ {
		x1 := float64(i%10) / 9
		x2 := float64(i%7) / 6 // noise-like, uncorrelated with y
		x3 := float64(i%5) / 4
		X[i] = []float64{x1, x2, x3}
		y[i] = math.Sin(2 * math.Pi * x1)
	}
	require.NoError(t, gp.Fit(X, y))

	hp := gp.Hyperparameters()
	require.Len(t, hp.LengthScales, 3)
	t.Logf("length-scales: x1=%.3f x2=%.3f x3=%.3f", hp.LengthScales[0], hp.LengthScales[1], hp.LengthScales[2])

	w := gpsensitivityWeights(hp.LengthScales)
	assert.Greater(t, w[0], 0.6, "x1 should dominate sensitivity; weights=%v", w)
}

// TestGP_2DAnisotropic: f depends only on x1 → ℓ1 ≪ ℓ2.
func TestGP_2DAnisotropic(t *testing.T) {
	gp := NewGP(Options{Starts: 60})
	n := 36
	X := make([][]float64, n)
	y := make([]float64, n)
	idx := 0
	for i := 0; i < 6; i++ {
		for j := 0; j < 6; j++ {
			x1 := float64(i) / 5
			x2 := float64(j) / 5
			X[idx] = []float64{x1, x2}
			y[idx] = math.Exp(-math.Pow(x1-0.5, 2) / 0.02)
			idx++
		}
	}
	require.NoError(t, gp.Fit(X, y))
	hp := gp.Hyperparameters()
	t.Logf("2D length-scales: x1=%.3f x2=%.3f", hp.LengthScales[0], hp.LengthScales[1])
	assert.Less(t, hp.LengthScales[0], hp.LengthScales[1], "ℓ1 should be ≪ ℓ2")
}

// TestGP_DuplicatePoints does not panic and predicts near their mean.
func TestGP_DuplicatePoints(t *testing.T) {
	gp := NewGP(Options{Starts: 20})
	X := [][]float64{{0.3}, {0.3}, {0.7}, {0.7}}
	y := []float64{0.0, 0.2, 1.0, 1.2}
	require.NoError(t, gp.Fit(X, y))
	m1, _, err := gp.Predict([]float64{0.3})
	require.NoError(t, err)
	assert.InDelta(t, 0.1, m1, 0.15)
}

// TestGP_ConstantY short-circuits to the mean with ~0 variance.
func TestGP_ConstantY(t *testing.T) {
	gp := NewGP(Options{Starts: 10})
	X := [][]float64{{0.1}, {0.4}, {0.6}, {0.9}}
	y := []float64{5, 5, 5, 5}
	require.NoError(t, gp.Fit(X, y))
	m, s, err := gp.Predict([]float64{0.5})
	require.NoError(t, err)
	assert.InDelta(t, 5.0, m, 1e-9)
	assert.LessOrEqual(t, s, 1e-9)
}

// TestGP_PredictBatch returns parallel slices of the right length.
func TestGP_PredictBatch(t *testing.T) {
	gp := NewGP(Options{Starts: 20})
	X := [][]float64{{0.0}, {0.25}, {0.5}, {0.75}, {1.0}}
	y := []float64{0, 1, 0, -1, 0}
	require.NoError(t, gp.Fit(X, y))
	grid := [][]float64{{0.1}, {0.5}, {0.9}}
	mean, std, err := gp.PredictBatch(grid)
	require.NoError(t, err)
	require.Len(t, mean, 3)
	require.Len(t, std, 3)
	for i := range mean {
		assert.False(t, math.IsNaN(mean[i]))
		assert.False(t, math.IsNaN(std[i]))
	}
}

// gpsensitivityWeights mirrors analysis.GPSensitivity's 1/ℓ² normalization.
func gpsensitivityWeights(lens []float64) []float64 {
	w := make([]float64, len(lens))
	var sum float64
	for i, l := range lens {
		w[i] = 1 / (l * l)
		sum += w[i]
	}
	if sum > 0 {
		for i := range w {
			w[i] /= sum
		}
	}
	return w
}
