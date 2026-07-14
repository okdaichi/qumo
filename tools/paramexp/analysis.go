// Package main — analysis: knee detection, regression, parameter importance, interactions.
package main

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// --- Knee detection (Kneedle algorithm) ---

// KneePoint is a detected knee in a 1-D sweep.
type KneePoint struct {
	Param    string  `json:"param"`
	Value    string  `json:"value"` // the param value at the knee
	ValueIdx int     `json:"value_idx"`
	Metric   string  `json:"metric"`
	Score    float64 `json:"score"` // normalized distance, higher = sharper knee
}

// DetectKnees finds knee points for each parameter (one-at-a-time sweeps).
// For each param, fix all others at their median and sweep the param's values.
func DetectKnees(results []storedResult, space ParamSpace, objective string) []KneePoint {
	var knees []KneePoint
	for _, p := range space.Params {
		// Group results by this param's value, averaging the objective.
		groups := groupByParam(results, p.Name)
		if len(groups) < 3 {
			continue // need ≥3 points for knee
		}
		xs, ys := sortedXY(groups, p.Values, objective)
		if len(xs) < 3 {
			continue
		}
		// Kneedle: normalize x and y to [0,1], compute the distance from the
		// diagonal, find the max-distance point.
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

func kneedle(xs, ys []float64) int {
	if len(xs) < 3 {
		return 0
	}
	// Normalize to [0,1]
	xNorm := normalize(xs)
	yNorm := normalize(ys)
	// Y is the objective (e.g., throughput). For a concave curve (diminishing
	// returns), the knee is where yNorm is farthest BELOW the diagonal.
	maxDist := 0.0
	maxIdx := 0
	for i := range xNorm {
		// diagonal value at xNorm[i]
		diag := xNorm[i] // line from (0,0) to (1,1)
		dist := diag - yNorm[i] // positive when y is below the diagonal
		if dist > maxDist {
			maxDist = dist
			maxIdx = i
		}
	}
	return maxIdx
}

func kneeScore(xs, ys []float64, idx int) float64 {
	xNorm := normalize(xs)
	yNorm := normalize(ys)
	diag := xNorm[idx]
	return diag - yNorm[idx]
}

// --- Regression detection ---

// RegressionCheck compares a set of results against a baseline metric value.
// A regression is a statistically significant degradation (Welch's t-test
// approximation: if the mean drops by more than 2 standard deviations).
type Regression struct {
	Param    string  `json:"param"`
	Value    string  `json:"value"`
	Metric   string  `json:"metric"`
	Baseline float64 `json:"baseline"`
	Observed float64 `json:"observed"`
	ZScore   float64 `json:"z_score"`
}

func DetectRegressions(results []storedResult, baseline MetricSet, threshold float64) []Regression {
	// threshold = z-score cutoff (default 2.0)
	if threshold == 0 {
		threshold = 2.0
	}
	var regressions []Regression
	for metric, baseVal := range baseline {
		if baseVal == 0 {
			continue
		}
		values := extractMetric(results, metric)
		if len(values) == 0 {
			continue
		}
		mean, std := meanStd(values)
		if std == 0 {
			continue
		}
		for _, sr := range results {
			val, ok := sr.Metrics[metric]
			if !ok {
				continue
			}
			z := (val - mean) / std
			// Regression: significantly worse than baseline (lower is worse for
			// throughput; higher is worse for latency).
			if isWorse(metric, val, baseVal) && abs(z) > threshold {
				regressions = append(regressions, Regression{
					Metric: metric, Baseline: baseVal, Observed: val, ZScore: z,
				})
			}
		}
	}
	return regressions
}

// --- Parameter importance (correlation-based) ---

// ImportanceRank is the ranking of one parameter's effect on an objective.
type ImportanceRank struct {
	Param     string  `json:"param"`
	Importance float64 `json:"importance"` // 0-1 (fraction of variance explained)
}

// RankImportance computes the correlation ratio (η²) for each parameter
// against the objective. η² measures how much of the metric's variance is
// explained by the parameter's group means (ANOVA-style, no linear assumption).
func RankImportance(results []storedResult, space ParamSpace, objective string) []ImportanceRank {
	allVals := extractMetric(results, objective)
	if len(allVals) < 3 {
		return nil
	}
	totalMean, _ := meanStd(allVals)
	totalVar := variance(allVals, totalMean)
	if totalVar == 0 {
		return nil
	}

	var ranks []ImportanceRank
	for _, p := range space.Params {
		groups := groupByParam(results, p.Name)
		if len(groups) < 2 {
			continue
		}
		// Between-group variance (explained)
		var betweenVar float64
		n := 0
		for _, vals := range groups {
			if len(vals) == 0 {
				continue
			}
			groupMean, _ := meanStd(vals)
			betweenVar += float64(len(vals)) * (groupMean - totalMean) * (groupMean - totalMean)
			n += len(vals)
		}
		etaSquared := betweenVar / (totalVar * float64(n))
		if etaSquared > 1 {
			etaSquared = 1
		}
		ranks = append(ranks, ImportanceRank{Param: p.Name, Importance: etaSquared})
	}
	sort.Slice(ranks, func(i, j int) bool { return ranks[i].Importance > ranks[j].Importance })
	return ranks
}

// --- Parameter interactions ---

// Interaction is a pairwise interaction score (how much the joint effect
// differs from the sum of individual effects).
type Interaction struct {
	ParamA   string  `json:"param_a"`
	ParamB   string  `json:"param_b"`
	Score    float64 `json:"score"` // higher = stronger interaction
}

func DetectInteractions(results []storedResult, space ParamSpace, objective string) []Interaction {
	var interactions []Interaction
	params := space.Params
	for i := 0; i < len(params); i++ {
		for j := i + 1; j < len(params); j++ {
			score := interactionScore(results, params[i], params[j], objective)
			if score > 0.01 { // filter weak interactions
				interactions = append(interactions, Interaction{
					ParamA: params[i].Name, ParamB: params[j].Name, Score: score,
				})
			}
		}
	}
	sort.Slice(interactions, func(i, j int) bool { return interactions[i].Score > interactions[j].Score })
	return interactions
}

// interactionScore: compute the variance of the objective conditioned on the
// joint (paramA, paramB) grouping, minus the sum of marginal variances.
// A large positive value indicates interaction.
func interactionScore(results []storedResult, a, b ParamDef, objective string) float64 {
	// Marginal eta² for a and b individually
	etaA := etaSquared(results, a.Name, objective)
	etaB := etaSquared(results, b.Name, objective)
	// Joint eta² for (a, b)
	etaAB := etaSquared2D(results, a.Name, b.Name, objective)
	return max(0, etaAB-etaA-etaB)
}

// ---- helpers ----

func groupByParam(results []storedResult, param string) map[string][]float64 {
	groups := make(map[string][]float64)
	for _, sr := range results {
		val, ok := sr.Vector[param]
		if !ok {
			continue
		}
		m, ok := sr.Metrics["throughput_fps"]
		if !ok {
			// fallback: use the first metric
			for _, v := range sr.Metrics {
				m = v
				break
			}
		}
		groups[val] = append(groups[val], m)
	}
	return groups
}

func sortedXY(groups map[string][]float64, order []string, objective string) ([]float64, []float64) {
	var xs, ys []float64
	for _, val := range order {
		vals, ok := groups[val]
		if !ok || len(vals) == 0 {
			continue
		}
		mean, _ := meanStd(vals)
		xs = append(xs, float64(len(xs)))
		ys = append(ys, mean)
	}
	return xs, ys
}

func normalize(xs []float64) []float64 {
	if len(xs) == 0 {
		return xs
	}
	min, max := xs[0], xs[0]
	for _, x := range xs {
		if x < min {
			min = x
		}
		if x > max {
			max = x
		}
	}
	rng := max - min
	if rng == 0 {
		return xs
	}
	out := make([]float64, len(xs))
	for i, x := range xs {
		out[i] = (x - min) / rng
	}
	return out
}

func extractMetric(results []storedResult, metric string) []float64 {
	var vals []float64
	for _, sr := range results {
		if v, ok := sr.Metrics[metric]; ok {
			vals = append(vals, v)
		}
	}
	return vals
}

func meanStd(xs []float64) (float64, float64) {
	if len(xs) == 0 {
		return 0, 0
	}
	sum := 0.0
	for _, x := range xs {
		sum += x
	}
	mean := sum / float64(len(xs))
	var sqSum float64
	for _, x := range xs {
		sqSum += (x - mean) * (x - mean)
	}
	std := math.Sqrt(sqSum / float64(len(xs)))
	return mean, std
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

func etaSquared(results []storedResult, param, objective string) float64 {
	allVals := extractMetricFiltered(results, objective)
	if len(allVals) < 3 {
		return 0
	}
	totalMean, _ := meanStd(allVals)
	totalVar := variance(allVals, totalMean)
	if totalVar == 0 {
		return 0
	}
	groups := groupByParamFiltered(results, param, objective)
	var betweenVar float64
	n := 0
	for _, vals := range groups {
		if len(vals) == 0 {
			continue
		}
		gm, _ := meanStd(vals)
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

func etaSquared2D(results []storedResult, paramA, paramB, objective string) float64 {
	allVals := extractMetricFiltered(results, objective)
	if len(allVals) < 3 {
		return 0
	}
	totalMean, _ := meanStd(allVals)
	totalVar := variance(allVals, totalMean)
	if totalVar == 0 {
		return 0
	}
	groups := make(map[string][]float64)
	for _, sr := range results {
		v, ok := sr.Metrics[objective]
		if !ok {
			continue
		}
		key := sr.Vector[paramA] + "|" + sr.Vector[paramB]
		groups[key] = append(groups[key], v)
	}
	var betweenVar float64
	n := 0
	for _, vals := range groups {
		if len(vals) == 0 {
			continue
		}
		gm, _ := meanStd(vals)
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

func groupByParamFiltered(results []storedResult, param, objective string) map[string][]float64 {
	groups := make(map[string][]float64)
	for _, sr := range results {
		val, ok := sr.Vector[param]
		if !ok {
			continue
		}
		m, ok := sr.Metrics[objective]
		if !ok {
			continue
		}
		groups[val] = append(groups[val], m)
	}
	return groups
}

func extractMetricFiltered(results []storedResult, metric string) []float64 {
	var vals []float64
	for _, sr := range results {
		if v, ok := sr.Metrics[metric]; ok {
			vals = append(vals, v)
		}
	}
	return vals
}

func isWorse(metric string, val, baseline float64) bool {
	if strings.Contains(metric, "latency") || strings.Contains(metric, "loss") ||
		strings.Contains(metric, "p99") || strings.Contains(metric, "p95") ||
		strings.Contains(metric, "error") || strings.Contains(metric, "dropped") {
		return val > baseline // higher is worse
	}
	return val < baseline // lower is worse (throughput etc.)
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

func max(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

var _ = fmt.Sprintf
