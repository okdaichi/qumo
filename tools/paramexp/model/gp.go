package model

import (
	"errors"
	"fmt"
	"math"

	"gonum.org/v1/gonum/mat"
	"gonum.org/v1/gonum/optimize"
)

// Surrogate models a scalar response surface. Predict returns a mean and a
// standard deviation (predictive uncertainty). Random Forest / GBT (later
// phases) implement this interface so analysis and the BO driver are agnostic
// to the model family.
type Surrogate interface {
	// Fit trains the model on inputs X (n×D) and targets y (n).
	Fit(X [][]float64, y []float64) error
	// Predict returns the predicted mean and predictive std at one point.
	Predict(x []float64) (mean, std float64, err error)
	// PredictBatch evaluates many points.
	PredictBatch(X [][]float64) (mean, std []float64, err error)
	// Hyperparameters returns learned model parameters (for sensitivity/inspection).
	Hyperparameters() Hyperparameters
}

// Hyperparameters holds the GP's learned kernel parameters.
type Hyperparameters struct {
	LengthScales []float64 `json:"length_scales"` // per-dimension (ARD)
	SignalVar    float64   `json:"signal_var"`    // σ_f²
	NoiseVar     float64   `json:"noise_var"`     // σ_n²
}

// Options configure GP fitting.
type Options struct {
	Kernel       Kernel // nil → RBF{}
	NoiseFloor   float64
	Starts       int    // multistart random-search points; 0 → 40+8*D
	OptBudget    int    // reserved for polish iterations
	Seed         uint64
	LengthBounds [2]float64
}

// GaussianProcess is an ARD-kernel GP with Gaussian observation noise,
// fit by maximizing the log-marginal-likelihood.
type GaussianProcess struct {
	opt    Options
	kernel Kernel

	X      [][]float64
	yMean  float64
	yStd   float64
	lens   []float64
	sigF2  float64
	noise  float64

	// measuredNoise[i] is the per-point observation variance (in standardized-y
	// units) when the GP is fit on replicate means; nil/empty ⇒ homoscedastic.
	// Added to the training-covariance diagonal alongside the noise floor.
	measuredNoise []float64

	// Cached factorization K = L Lᵀ over the training set (fit on standardized y).
	chol *mat.Cholesky
	alpha *mat.VecDense // K⁻¹ y'
	dim   int

	constant bool // true if y had ~0 variance → predict the mean
}

// NewGP constructs a GP. opt may be zero-valued; sensible defaults apply.
func NewGP(opt Options) *GaussianProcess {
	gp := &GaussianProcess{opt: opt}
	if gp.opt.Kernel == nil {
		gp.opt.Kernel = RBF{}
	}
	if gp.opt.NoiseFloor == 0 {
		gp.opt.NoiseFloor = 1e-8
	}
	if gp.opt.LengthBounds[0] == 0 && gp.opt.LengthBounds[1] == 0 {
		gp.opt.LengthBounds = [2]float64{1e-3, 1e2}
	}
	return gp
}

// Hyperparameters implements Surrogate.
func (g *GaussianProcess) Hyperparameters() Hyperparameters {
	hp := Hyperparameters{
		LengthScales: append([]float64(nil), g.lens...),
		SignalVar:    g.sigF2,
		NoiseVar:     g.noise,
	}
	if hp.LengthScales == nil {
		hp.LengthScales = []float64{}
	}
	return hp
}

// Fit trains the GP on (X, y). It standardizes y, optimizes ARD length-scales,
// signal variance, and noise by maximizing the log-marginal-likelihood via
// multistart random search + a Nelder-Mead polish, and caches the Cholesky.
func (g *GaussianProcess) Fit(X [][]float64, y []float64) error {
	g.measuredNoise = nil // reset (a previous FitReplicated may have set it)
	if len(X) == 0 || len(X) != len(y) {
		return fmt.Errorf("gp: X and y length mismatch (X=%d y=%d)", len(X), len(y))
	}
	dim := len(X[0])
	for _, row := range X {
		if len(row) != dim {
			return errors.New("gp: ragged X")
		}
	}
	g.dim = dim
	g.X = X
	g.kernel = g.opt.Kernel

	// Standardize y. If it is (near) constant, short-circuit to a mean predictor.
	mean, std := MeanStd(y)
	g.yMean = mean
	if std < 1e-12 {
		g.yStd = 0
		g.constant = true
		g.lens = ones(dim, 1.0)
		g.sigF2 = 0
		g.noise = g.opt.NoiseFloor
		return nil
	}
	g.yStd = std
	g.constant = false
	ys := make([]float64, len(y))
	for i := range y {
		ys[i] = (y[i] - mean) / std
	}

	// Collapse near-duplicate X rows (averaging y) to avoid a singular K.
	// The deduplicated set is the GP's effective training set; g.X is reassigned
	// so Predict (which sizes kstar by len(g.X)) stays consistent with alpha.
	Xd, yd := dedup(X, ys)
	g.X = Xd
	return g.optimizeAndFactorize(Xd, yd)
}

// FitReplicated trains the GP on per-vector replicate means with known per-point
// variance (heteroscedastic). yMean[i]/yVar[i] are the mean/variance of the
// objective across the N replicates at X[i]. The per-point observation noise
// becomes yVar[i] (in standardized-y units), so high-variance points are
// downweighted automatically; the global noise hyperparameter stays as a floor.
// Equivalent to Fit when all yVar are ~0 (single replicate).
func (g *GaussianProcess) FitReplicated(X [][]float64, yMean, yVar []float64) error {
	if len(X) == 0 || len(X) != len(yMean) || len(X) != len(yVar) {
		return fmt.Errorf("gp: X/yMean/yVar length mismatch (X=%d yMean=%d yVar=%d)", len(X), len(yMean), len(yVar))
	}
	dim := len(X[0])
	for _, row := range X {
		if len(row) != dim {
			return errors.New("gp: ragged X")
		}
	}
	g.dim = dim
	g.X = X
	g.kernel = g.opt.Kernel
	g.measuredNoise = nil // reset (Fit may have been called before)

	mean, std := MeanStd(yMean)
	g.yMean = mean
	if std < 1e-12 {
		g.yStd = 0
		g.constant = true
		g.lens = ones(dim, 1.0)
		g.sigF2 = 0
		g.noise = g.opt.NoiseFloor
		return nil
	}
	g.yStd = std
	g.constant = false
	ys := make([]float64, len(yMean))
	for i := range yMean {
		ys[i] = (yMean[i] - mean) / std
	}

	// Dedup carries the variance (inverse-variance pooled) so a merged point's
	// measured noise reflects the combined uncertainty.
	Xd, yd, vd := dedupWithVar(X, ys, yVar)
	g.X = Xd
	g.measuredNoise = make([]float64, len(Xd))
	for i := range Xd {
		g.measuredNoise[i] = vd[i] / (std * std) // measured variance in standardized-y units
	}
	return g.optimizeAndFactorize(Xd, yd)
}

// optimizeAndFactorize runs the multistart + Nelder-Mead hyperparameter search
// (maximizing the LML) and caches the Cholesky/α. Shared by Fit and FitReplicated.
func (g *GaussianProcess) optimizeAndFactorize(Xd [][]float64, yd []float64) error {
	D := g.dim
	starts := g.opt.Starts
	if starts == 0 {
		starts = 40 + 8*D
		if starts > 200 {
			starts = 200
		}
		if len(Xd) > 500 {
			starts /= 2
		}
	}

	objective := func(theta []float64) float64 {
		return g.negLogMarginalLikelihood(Xd, yd, theta)
	}

	rng := NewLCG(g.opt.Seed)
	bestTheta := medianHeuristicTheta(D, yd)
	bestVal := objective(bestTheta)
	for s := 0; s < starts; s++ {
		theta := randomTheta(D, rng)
		v := objective(theta)
		if v < bestVal {
			bestVal, bestTheta = v, theta
		}
	}

	polished := polishNM(objective, bestTheta)
	if pv := objective(polished); pv < bestVal {
		bestVal, bestTheta = pv, polished
	}

	if medianVal := objective(medianHeuristicTheta(D, yd)); medianVal < bestVal {
		bestTheta = medianHeuristicTheta(D, yd)
	}

	g.unpackTheta(bestTheta)
	return g.factorize(Xd, yd)
}

// negLogMarginalLikelihood computes -LML at θ (log-space). Adds adaptive jitter
// and returns +Inf if Cholesky fails at this θ.
func (g *GaussianProcess) negLogMarginalLikelihood(X [][]float64, y []float64, theta []float64) float64 {
	D := g.dim
	lens := make([]float64, D)
	sigF2 := math.Exp(theta[D])
	noise := math.Exp(theta[D+1])
	for d := range D {
		lens[d] = clamp(math.Exp(theta[d]), g.opt.LengthBounds[0], g.opt.LengthBounds[1])
	}

	// K = σ_f² · R + σ_n² · I, where R is the unit-variance correlation from the
	// kernel. The signal variance σ_f² scales the correlation.
	K := mat.NewSymDense(len(X), nil)
	for i := range X {
		for j := i; j < len(X); j++ {
			K.SetSym(i, j, sigF2*g.kernel.Eval(X[i], X[j], lens))
		}
	}
	trace := 0.0
	for i := range X {
		n := noise
		if i < len(g.measuredNoise) {
			n += g.measuredNoise[i]
		}
		trace += K.At(i, i)
		trace += n
		K.SetSym(i, i, K.At(i, i)+n)
	}
	jitter := 1e-6 * (trace/float64(len(X)) + 1e-12)

	var chol mat.Cholesky
	for attempt := 0; attempt < 4; attempt++ {
		Kj := mat.NewSymDense(len(X), nil)
		for i := range X {
			for j := i; j < len(X); j++ {
				Kj.SetSym(i, j, K.At(i, j))
			}
		}
		for i := range X {
			Kj.SetSym(i, i, Kj.At(i, i)+jitter)
		}
		if chol.Factorize(Kj) {
			yv := mat.NewVecDense(len(y), y)
			alpha := &mat.VecDense{}
			chol.SolveVecTo(alpha, yv)
			// LML = -0.5·yᵀα - 0.5·log|K| - (n/2)·log(2π). LogDet() returns
			// log|K| (= 2·Σ log L_ii), so the complexity term is -0.5·logdet.
			ytAlpha := mat.Dot(yv, alpha)
			logdet := chol.LogDet()
			n := float64(len(y))
			lml := -0.5*ytAlpha - 0.5*logdet - 0.5*n*math.Log(2*math.Pi)
			return -lml
		}
		jitter *= 100
	}
	return math.Inf(1)
}

// unpackTheta stores the chosen hyperparameters (without factorizing).
func (g *GaussianProcess) unpackTheta(theta []float64) {
	D := g.dim
	g.lens = make([]float64, D)
	for d := range D {
		g.lens[d] = clamp(math.Exp(theta[d]), g.opt.LengthBounds[0], g.opt.LengthBounds[1])
	}
	g.sigF2 = math.Exp(theta[D])
	g.noise = math.Exp(theta[D+1])
}

// cov is the GP covariance: the kernel correlation scaled by the signal
// variance σ_f². Used uniformly in fit/factorize/predict so σ_f² is applied
// consistently everywhere K or k* is built.
func (g *GaussianProcess) cov(a, b []float64) float64 {
	return g.sigF2 * g.kernel.Eval(a, b, g.lens)
}

// factorize caches K = L Lᵀ and α = K⁻¹ y' for prediction.
func (g *GaussianProcess) factorize(X [][]float64, y []float64) error {
	n := len(X)
	K := mat.NewSymDense(n, nil)
	for i := range X {
		for j := i; j < n; j++ {
			K.SetSym(i, j, g.cov(X[i], X[j]))
		}
		nv := g.noise
		if i < len(g.measuredNoise) {
			nv += g.measuredNoise[i]
		}
		K.SetSym(i, i, K.At(i, i)+nv)
	}
	var chol mat.Cholesky
	jitter := 1e-6
	for attempt := 0; attempt < 5; attempt++ {
		Kj := mat.NewSymDense(n, nil)
		for i := range X {
			for j := i; j < n; j++ {
				Kj.SetSym(i, j, K.At(i, j))
			}
			Kj.SetSym(i, i, Kj.At(i, i)+jitter)
		}
		if chol.Factorize(Kj) {
			g.chol = &chol
			yv := mat.NewVecDense(n, y)
			alpha := &mat.VecDense{}
			chol.SolveVecTo(alpha, yv)
			g.alpha = alpha
			return nil
		}
		jitter *= 100
	}
	return ErrCholeskyFailed
}

// ErrCholeskyFailed signals the kernel matrix could not be factorized even
// after adaptive jitter escalation. Callers may fall back to the prior mean.
var ErrCholeskyFailed = errors.New("gp: Cholesky factorization failed (kernel matrix not PD)")

// Predict returns the posterior mean and predictive std at x.
func (g *GaussianProcess) Predict(x []float64) (float64, float64, error) {
	if g.constant {
		return g.yMean, 0, nil
	}
	if g.chol == nil {
		return g.yMean, g.yStd, nil
	}
	kstar := mat.NewVecDense(len(g.X), nil)
	for i := range g.X {
		kstar.SetVec(i, g.cov(g.X[i], x))
	}
	meanStd := mat.Dot(kstar, g.alpha)

	// predictive variance: k(x,x) - k*ᵀ K⁻¹ k*  via K⁻¹ k* (SolveVecTo).
	v := mat.NewVecDense(len(g.X), nil)
	g.chol.SolveVecTo(v, kstar)
	varSig2 := g.cov(x, x) - mat.Dot(kstar, v)
	if varSig2 < 0 {
		varSig2 = 0
	}

	mean := g.yMean + g.yStd*meanStd
	std := g.yStd * math.Sqrt(varSig2)
	return mean, std, nil
}

// PredictBatch evaluates many points.
func (g *GaussianProcess) PredictBatch(X [][]float64) ([]float64, []float64, error) {
	means := make([]float64, len(X))
	stds := make([]float64, len(X))
	for i, x := range X {
		m, s, err := g.Predict(x)
		if err != nil {
			return nil, nil, err
		}
		means[i], stds[i] = m, s
	}
	return means, stds, nil
}

// --- MultiOutput ---

// MultiOutput holds one independent GP per metric. Each metric is fit in
// isolation; predictions are made per metric.
type MultiOutput struct {
	GPs   map[string]*GaussianProcess
	order []string
}

// FitGPs trains one independent GP per requested metric. X is the shared
// encoded input matrix; metrics maps each metric name to its target vector.
func FitGPs(X [][]float64, metrics map[string][]float64, opt Options) (*MultiOutput, error) {
	mo := &MultiOutput{GPs: make(map[string]*GaussianProcess)}
	for name, y := range metrics {
		gp := NewGP(opt)
		if err := gp.Fit(X, y); err != nil {
			return nil, fmt.Errorf("fit GP for %s: %w", name, err)
		}
		mo.GPs[name] = gp
		mo.order = append(mo.order, name)
	}
	return mo, nil
}

// Get returns the GP for a metric (nil if absent).
func (m *MultiOutput) Get(metric string) *GaussianProcess { return m.GPs[metric] }

// --- optimization helpers ---

// medianHeuristicTheta: ℓ_d=1.0, σ_f²=Var(y')=1 (standardized), σ_n²=1e-3.
func medianHeuristicTheta(D int, _ []float64) []float64 {
	theta := make([]float64, D+2)
	for d := range D {
		theta[d] = 0.0 // log(1)
	}
	theta[D] = 0.0       // σ_f²=1
	theta[D+1] = math.Log(1e-3)
	return theta
}

func randomTheta(D int, rng *LCG) []float64 {
	theta := make([]float64, D+2)
	for d := range D {
		theta[d] = rng.Uniform(math.Log(1e-2), math.Log(1e1))
	}
	theta[D] = rng.Uniform(math.Log(1e-3), math.Log(1e1))
	theta[D+1] = rng.Uniform(math.Log(1e-6), math.Log(1e-1))
	return theta
}

// polishNM runs a short Nelder-Mead on f seeded at x0.
func polishNM(f func([]float64) float64, x0 []float64) []float64 {
	problem := optimize.Problem{
		Func: f,
	}
	res, err := optimize.Minimize(problem, x0, &optimize.Settings{
		MajorIterations: 50,
		FuncEvaluations: 200,
		GradEvaluations: 0,
	}, &optimize.NelderMead{})
	if err != nil || res == nil || len(res.X) != len(x0) {
		return x0
	}
	return res.X
}

// --- small numeric helpers ---

// MeanStd returns the population mean and standard deviation of xs.
func MeanStd(xs []float64) (float64, float64) {
	if len(xs) == 0 {
		return 0, 0
	}
	sum := 0.0
	for _, x := range xs {
		sum += x
	}
	mean := sum / float64(len(xs))
	var sq float64
	for _, x := range xs {
		sq += (x - mean) * (x - mean)
	}
	return mean, math.Sqrt(sq / float64(len(xs)))
}

func ones(n int, v float64) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = v
	}
	return out
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	if math.IsNaN(v) {
		return lo
	}
	return v
}

// dedup collapses near-identical X rows (averaging their y) to keep K non-singular.
func dedup(X [][]float64, y []float64) ([][]float64, []float64) {
	keep := [][]float64{}
	sums := []float64{}
	counts := []int{}
	for i, xi := range X {
		found := -1
		for j, xj := range keep {
			if nearEqual(xi, xj) {
				found = j
				break
			}
		}
		if found >= 0 {
			sums[found] += y[i]
			counts[found]++
		} else {
			keep = append(keep, xi)
			sums = append(sums, y[i])
			counts = append(counts, 1)
		}
	}
	ykeep := make([]float64, len(sums))
	for i := range sums {
		ykeep[i] = sums[i] / float64(counts[i])
	}
	return keep, ykeep
}

// dedupWithVar is dedup that also pools per-point variance: a merged point keeps
// the inverse-variance-weighted mean of y and the combined observation variance
// (pooled as 1/Σ(1/vᵢ); a zero-variance point dominates). Used by FitReplicated.
func dedupWithVar(X [][]float64, y, v []float64) ([][]float64, []float64, []float64) {
	keep := [][]float64{}
	ykeep := []float64{}
	vkeep := []float64{}
	for i, xi := range X {
		found := -1
		for j, xj := range keep {
			if nearEqual(xi, xj) {
				found = j
				break
			}
		}
		if found < 0 {
			keep = append(keep, xi)
			ykeep = append(ykeep, y[i])
			vkeep = append(vkeep, v[i])
			continue
		}
		// Inverse-variance pooling: wᵢ = 1/vᵢ (clamp tiny v to avoid div-by-zero).
		va, vb := vkeep[found], v[i]
		if va < 1e-12 {
			va = 1e-12
		}
		if vb < 1e-12 {
			vb = 1e-12
		}
		wa, wb := 1/va, 1/vb
		ykeep[found] = (wa*ykeep[found] + wb*y[i]) / (wa + wb)
		vkeep[found] = 1 / (wa + wb) // variance of the weighted mean (known-variance case)
	}
	return keep, ykeep, vkeep
}

func nearEqual(a, b []float64) bool {
	if len(a) != len(b) {
		return false
	}
	for d := range a {
		if math.Abs(a[d]-b[d]) > 1e-9 {
			return false
		}
	}
	return true
}
