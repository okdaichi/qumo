// Package analysis derives structure from observed data and fitted surrogate
// models: knee points, regressions, parameter importance (η²), pairwise
// interactions, and GP-based sensitivity and uncertainty maps.
package analysis

import (
	"math"
	"sort"
	"strings"

	"github.com/qumo-dev/qumo/tools/paramexp/experiment"
	"github.com/qumo-dev/qumo/tools/paramexp/model"
)

// --- Knee detection (Kneedle) ---

// KneePoint is a detected knee in a one-dimensional sweep.
type KneePoint struct {
	Param    string  `json:"param"`
	Value    string  `json:"value"`
	ValueIdx int     `json:"value_idx"`
	Metric   string  `json:"metric"`
	Score    float64 `json:"score"`
}

// DetectKnees finds knee points for each parameter. For each param it groups
// observations by that param's value, averages the objective, and applies the
// Kneedle distance-from-diagonal criterion.
func DetectKnees(obs []experiment.Observation, space experiment.ParamSpace, objective string) []KneePoint {
	var knees []KneePoint
	for _, p := range space.Params {
		if p.Type == experiment.TypeContinuous {
			continue // Kneedle-on-levels applies to discrete/categorical sweeps
		}
		groups := groupByParam(obs, p.Name, objective)
		if len(groups) < 3 {
			continue
		}
		xs, ys := sortedXY(groups, p.Values)
		if len(xs) < 3 {
			continue
		}
		kneeIdx := kneedle(xs, ys)
		if kneeIdx > 0 && kneeIdx < len(xs)-1 {
			knees = append(knees, KneePoint{
				Param: p.Name, Value: p.Values[kneeIdx], ValueIdx: kneeIdx,
				Metric: objective, Score: kneeScore(xs, ys, kneeIdx),
			})
		}
	}
	return knees
}

// kneedle finds the index of the knee: the point farthest (in normalized
// [0,1]² space) from the diagonal. Using the absolute distance |yNorm - xNorm|
// finds the elbow for both concave (curve above the diagonal, e.g. throughput
// diminishing returns) and convex (curve below) sweeps. Decreasing sweeps are
// mirrored first so the same criterion applies (e.g. latency).
func kneedle(xs, ys []float64) int {
	xNorm := normalize(xs)
	yNorm := normalize(ys)
	if len(ys) >= 2 && ys[len(ys)-1] < ys[0] {
		// Decreasing curve: mirror y so the elbow logic is direction-agnostic.
		for i := range yNorm {
			yNorm[i] = 1 - yNorm[i]
		}
	}
	maxDist := -1.0
	maxIdx := 0
	for i := range xNorm {
		d := yNorm[i] - xNorm[i]
		if d < 0 {
			d = -d
		}
		if d > maxDist {
			maxDist = d
			maxIdx = i
		}
	}
	return maxIdx
}

func kneeScore(xs, ys []float64, idx int) float64 {
	xNorm := normalize(xs)
	yNorm := normalize(ys)
	if len(ys) >= 2 && ys[len(ys)-1] < ys[0] {
		for i := range yNorm {
			yNorm[i] = 1 - yNorm[i]
		}
	}
	d := yNorm[idx] - xNorm[idx]
	if d < 0 {
		d = -d
	}
	return d
}

// --- Regression detection ---

// Regression flags a statistically significant degradation of a metric versus a
// baseline value (|z| > threshold), carrying the full parameter vector of the
// offending run. A regression is a property of a configuration, so the whole
// vector is recorded (deterministic, no information loss) rather than a single
// guessed param/value.
type Regression struct {
	Vector   experiment.ParamVector `json:"vector"`
	Metric   string                 `json:"metric"`
	Baseline float64                `json:"baseline"`
	Observed float64                `json:"observed"`
	ZScore   float64                `json:"z_score"`
}

// DetectRegressions compares observations against a baseline metric set. A
// regression is a run whose metric is worse than baseline by more than `threshold`
// population standard deviations. Each regression records the run's full vector.
func DetectRegressions(obs []experiment.Observation, baseline experiment.MetricSet, threshold float64) []Regression {
	if threshold == 0 {
		threshold = 2.0
	}
	var regressions []Regression
	for metric, baseVal := range baseline {
		if baseVal == 0 {
			continue
		}
		values := extractMetric(obs, metric)
		if len(values) == 0 {
			continue
		}
		mean, std := model.MeanStd(values)
		if std == 0 {
			continue
		}
		for _, o := range obs {
			val, ok := o.Metrics[metric]
			if !ok {
				continue
			}
			z := (val - mean) / std
			if isWorse(metric, val, baseVal) && math.Abs(z) > threshold {
				regressions = append(regressions, Regression{
					Vector: o.Vector, Metric: metric,
					Baseline: baseVal, Observed: val, ZScore: z,
				})
			}
		}
	}
	return regressions
}

// --- Parameter importance (correlation ratio η²) ---

// ImportanceRank ranks one parameter's effect on an objective.
type ImportanceRank struct {
	Param      string  `json:"param"`
	Importance float64 `json:"importance"`
}

// RankImportance computes the correlation ratio (η²) of each parameter against
// the objective: the fraction of metric variance explained by group means.
func RankImportance(obs []experiment.Observation, space experiment.ParamSpace, objective string) []ImportanceRank {
	allVals := extractMetric(obs, objective)
	if len(allVals) < 3 {
		return nil
	}
	totalMean, _ := model.MeanStd(allVals)
	totalVar := variance(allVals, totalMean)
	if totalVar == 0 {
		return nil
	}
	var ranks []ImportanceRank
	for _, p := range space.Params {
		eta := etaSquared(obs, p.Name, objective, totalMean, totalVar)
		ranks = append(ranks, ImportanceRank{Param: p.Name, Importance: eta})
	}
	sort.Slice(ranks, func(i, j int) bool { return ranks[i].Importance > ranks[j].Importance })
	return ranks
}

// --- Interactions ---

// Interaction is a pairwise interaction score: how much the joint effect of two
// parameters exceeds the sum of their individual effects.
type Interaction struct {
	ParamA string  `json:"param_a"`
	ParamB string  `json:"param_b"`
	Score  float64 `json:"score"`
}

// DetectInteractions computes pairwise interaction scores (η²_AB − η²_A − η²_B).
func DetectInteractions(obs []experiment.Observation, space experiment.ParamSpace, objective string) []Interaction {
	var interactions []Interaction
	params := space.Params
	allVals := extractMetric(obs, objective)
	if len(allVals) < 3 {
		return nil
	}
	totalMean, _ := model.MeanStd(allVals)
	totalVar := variance(allVals, totalMean)
	if totalVar == 0 {
		return nil
	}
	for i := 0; i < len(params); i++ {
		for j := i + 1; j < len(params); j++ {
			etaA := etaSquared(obs, params[i].Name, objective, totalMean, totalVar)
			etaB := etaSquared(obs, params[j].Name, objective, totalMean, totalVar)
			etaAB := etaSquared2D(obs, params[i].Name, params[j].Name, objective, totalMean, totalVar)
			score := etaAB - etaA - etaB
			if score > 0.01 {
				interactions = append(interactions, Interaction{
					ParamA: params[i].Name, ParamB: params[j].Name, Score: score,
				})
			}
		}
	}
	sort.Slice(interactions, func(i, j int) bool { return interactions[i].Score > interactions[j].Score })
	return interactions
}

// --- GP-derived sensitivity ---

// Sensitivity is a dimension's importance derived from a fitted surrogate.
type Sensitivity struct {
	Param      string  `json:"param"`
	Importance float64 `json:"importance"` // normalized, sums to 1
}

// GPSensitivity ranks dimensions by the inverse-squared ARD length-scales of a
// fitted GP. Shorter length-scale ⟹ higher sensitivity. Returns normalized
// weights (sum to 1). For an unfit/constant GP returns uniform weights.
func GPSensitivity(gp model.Surrogate, names []string) []Sensitivity {
	hp := gp.Hyperparameters()
	out := make([]Sensitivity, len(names))
	if len(hp.LengthScales) != len(names) {
		// Uniform fallback.
		w := 1.0 / float64(len(names))
		for i, n := range names {
			out[i] = Sensitivity{Param: n, Importance: w}
		}
		return out
	}
	var sum float64
	for i, n := range names {
		w := 1.0 / (hp.LengthScales[i] * hp.LengthScales[i])
		out[i] = Sensitivity{Param: n, Importance: w}
		sum += w
	}
	if sum > 0 {
		for i := range out {
			out[i].Importance /= sum
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Importance > out[j].Importance })
	return out
}

// ConfidenceMap evaluates a surrogate mean/std over a grid of points.
func ConfidenceMap(gp model.Surrogate, grid [][]float64) (mean, std []float64, err error) {
	return gp.PredictBatch(grid)
}

// --- helpers ---

func groupByParam(obs []experiment.Observation, param, objective string) map[string][]float64 {
	groups := make(map[string][]float64)
	for _, o := range obs {
		val, ok := o.Vector[param]
		if !ok {
			continue
		}
		m, ok := o.Metrics[objective]
		if !ok {
			continue
		}
		groups[val] = append(groups[val], m)
	}
	return groups
}

func sortedXY(groups map[string][]float64, order []string) ([]float64, []float64) {
	var xs, ys []float64
	for _, val := range order {
		vals, ok := groups[val]
		if !ok || len(vals) == 0 {
			continue
		}
		mean, _ := model.MeanStd(vals)
		xs = append(xs, float64(len(xs)))
		ys = append(ys, mean)
	}
	return xs, ys
}

func normalize(xs []float64) []float64 {
	if len(xs) == 0 {
		return xs
	}
	lo, hi := xs[0], xs[0]
	for _, x := range xs {
		if x < lo {
			lo = x
		}
		if x > hi {
			hi = x
		}
	}
	rng := hi - lo
	out := make([]float64, len(xs))
	if rng == 0 {
		return out
	}
	for i, x := range xs {
		out[i] = (x - lo) / rng
	}
	return out
}

func extractMetric(obs []experiment.Observation, metric string) []float64 {
	var vals []float64
	for _, o := range obs {
		if v, ok := o.Metrics[metric]; ok {
			vals = append(vals, v)
		}
	}
	return vals
}

func variance(xs []float64, mean float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	var sum float64
	for _, x := range xs {
		sum += (x - mean) * (x - mean)
	}
	return sum / float64(len(xs))
}

func etaSquared(obs []experiment.Observation, param, objective string, totalMean, totalVar float64) float64 {
	if totalVar == 0 {
		return 0
	}
	groups := groupByParam(obs, param, objective)
	var betweenVar float64
	n := 0
	for _, vals := range groups {
		if len(vals) == 0 {
			continue
		}
		gm, _ := model.MeanStd(vals)
		betweenVar += float64(len(vals)) * (gm - totalMean) * (gm - totalMean)
		n += len(vals)
	}
	if n == 0 {
		return 0
	}
	eta := betweenVar / (totalVar * float64(n))
	if eta > 1 {
		eta = 1
	}
	return eta
}

func etaSquared2D(obs []experiment.Observation, paramA, paramB, objective string, totalMean, totalVar float64) float64 {
	if totalVar == 0 {
		return 0
	}
	groups := make(map[string][]float64)
	for _, o := range obs {
		v, ok := o.Metrics[objective]
		if !ok {
			continue
		}
		key := o.Vector[paramA] + "|" + o.Vector[paramB]
		groups[key] = append(groups[key], v)
	}
	var betweenVar float64
	n := 0
	for _, vals := range groups {
		if len(vals) == 0 {
			continue
		}
		gm, _ := model.MeanStd(vals)
		betweenVar += float64(len(vals)) * (gm - totalMean) * (gm - totalMean)
		n += len(vals)
	}
	if n == 0 {
		return 0
	}
	eta := betweenVar / (totalVar * float64(n))
	if eta > 1 {
		eta = 1
	}
	return eta
}

func isWorse(metric string, val, baseline float64) bool {
	if strings.Contains(metric, "latency") || strings.Contains(metric, "loss") ||
		strings.Contains(metric, "p99") || strings.Contains(metric, "p95") ||
		strings.Contains(metric, "error") || strings.Contains(metric, "dropped") {
		return val > baseline // higher is worse
	}
	return val < baseline // lower is worse
}

// --- Stability & statistical distinguishability (replication-aware) ---

// UnstableCV is the coefficient-of-variation threshold above which a config's
// objective is classified as unstable (noisy across replicates). CV = std/|mean|.
var UnstableCV = 0.15

// IndistZ is the z-score for the two-sample "indistinguishable from best" test.
// 1.96 ≈ 95% confidence that two means differ.
var IndistZ = 1.96

// Stability is one config's run-to-run consistency on an objective.
type Stability struct {
	ExperimentID int64                  `json:"experiment_id"`
	Vector       experiment.ParamVector `json:"vector"`
	Mean         float64                `json:"mean"`
	Std          float64                `json:"std"`
	CV           float64                `json:"cv"`
	N            int                    `json:"n"`
	Unstable     bool                   `json:"unstable"`
}

// StabilityReport classifies each observation's objective as stable or unstable
// by its coefficient of variation across replicates (Variances/N must be set by
// the storage aggregation). Single-replicate observations (N<=1, zero variance)
// are conservatively left stable (no evidence of instability).
func StabilityReport(obs []experiment.Observation, objective string) []Stability {
	out := make([]Stability, 0, len(obs))
	for _, o := range obs {
		mean, ok := o.Metrics[objective]
		if !ok {
			continue
		}
		std := math.Sqrt(o.Variances[objective])
		cv := 0.0
		if math.Abs(mean) > 1e-12 {
			cv = std / math.Abs(mean)
		}
		out = append(out, Stability{
			ExperimentID: o.ExperimentID, Vector: o.Vector,
			Mean: mean, Std: std, CV: cv, N: o.N,
			Unstable: o.N > 1 && cv > UnstableCV,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CV > out[j].CV })
	return out
}

// ConfigCI is a config's objective mean with the half-width of its mean's
// confidence interval (z·sqrt(var/N)).
type ConfigCI struct {
	ExperimentID int64                  `json:"experiment_id"`
	Vector       experiment.ParamVector `json:"vector"`
	Mean         float64                `json:"mean"`
	SE           float64                `json:"se"` // standard error sqrt(var/N)
	N            int                    `json:"n"`
}

// IndistinguishableFromBest returns the best config (max objective mean) and the
// other configs whose objective mean is NOT statistically distinguishable from
// it at the IndistZ level (their CIs overlap the best's). A config NOT in peers
// is significantly worse than the best. Peers are the "can't tell apart from
// best" set the brief asks to surface.
func IndistinguishableFromBest(obs []experiment.Observation, objective string) (best ConfigCI, peers []ConfigCI) {
	cis := make([]ConfigCI, 0, len(obs))
	for _, o := range obs {
		mean, ok := o.Metrics[objective]
		if !ok {
			continue
		}
		n := o.N
		if n < 1 {
			n = 1
		}
		cis = append(cis, ConfigCI{
			ExperimentID: o.ExperimentID, Vector: o.Vector,
			Mean: mean, SE: math.Sqrt(o.Variances[objective] / float64(n)), N: n,
		})
	}
	if len(cis) == 0 {
		return ConfigCI{}, nil
	}
	sort.Slice(cis, func(i, j int) bool { return cis[i].Mean > cis[j].Mean })
	best = cis[0]
	for _, c := range cis[1:] {
		// |mean_i - mean_best| <= z·sqrt(se_i² + se_best²)  ⇒ CIs overlap.
		gap := math.Abs(c.Mean - best.Mean)
		threshold := TCritical(min(c.N, best.N)-1) * math.Sqrt(c.SE*c.SE+best.SE*best.SE)
		if gap <= threshold {
			peers = append(peers, c)
		}
	}
	return best, peers
}

// tTable holds two-tailed 95% critical values (t_{0.975,df}) for small degrees
// of freedom; beyond df=30 the normal approximation z≈1.96 is used.
var tTable = map[int]float64{
	1: 12.706, 2: 4.303, 3: 3.182, 4: 2.776, 5: 2.571,
	6: 2.447, 7: 2.365, 8: 2.306, 9: 2.262, 10: 2.228,
	11: 2.201, 12: 2.179, 13: 2.160, 14: 2.145, 15: 2.131,
	20: 2.086, 25: 2.060, 30: 2.042,
}

// TCritical returns the two-tailed 95% t-quantile for df degrees of freedom
// (df ≤ 0 → normal z=1.96; df > 30 → 1.96; interpolated in between).
func TCritical(df int) float64 {
	if df <= 0 {
		return 1.96
	}
	if df > 30 {
		return 1.96
	}
	if v, ok := tTable[df]; ok {
		return v
	}
	// Linear interpolation between nearest table entries.
	var lo, hi int
	for k := range tTable {
		if k < df && k > lo {
			lo = k
		}
		if k > df && (hi == 0 || k < hi) {
			hi = k
		}
	}
	if lo == 0 || hi == 0 {
		return 1.96
	}
	tlo, thi := tTable[lo], tTable[hi]
	return tlo + (thi-tlo)*float64(df-lo)/float64(hi-lo)
}
