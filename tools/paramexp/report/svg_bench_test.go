package report

import (
	"testing"

	"github.com/qumo-dev/qumo/tools/paramexp/experiment"
)

// benchObservations builds nObs observations spread across the given param
// levels for one objective metric — the input shape sweepSVG aggregates into
// per-level mean/σ groups.
func benchObservations(nObs int, levels []string, objective string) []experiment.Observation {
	obs := make([]experiment.Observation, 0, nObs)
	for i := 0; i < nObs; i++ {
		obs = append(obs, experiment.Observation{
			Vector:  experiment.ParamVector{"p": levels[i%len(levels)]},
			Metrics: experiment.MetricSet{objective: float64(i%100) + 1},
		})
	}
	return obs
}

// BenchmarkSweepSVG exercises the SVG path-building loop in sweepSVG (the
// strings.Builder optimization): many sweep points → many path appends, where
// the old `d += fmt.Sprintf(...)` was O(N²).
func BenchmarkSweepSVG(b *testing.B) {
	levels := []string{"a", "b", "c", "d", "e", "f", "g", "h"}
	p := experiment.ParamDef{Name: "p", Values: levels}
	const objective = "throughput"
	obs := benchObservations(240, levels, objective)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if s := sweepSVG(obs, p, objective); s == "" {
			b.Fatal("empty svg")
		}
	}
}

// BenchmarkResponseSurfaceSVG exercises the path-building loop in
// responseSurfaceSVG with a dense point set.
func BenchmarkResponseSurfaceSVG(b *testing.B) {
	const n = 32
	xs := make([]float64, n)
	means := make([]float64, n)
	stds := make([]float64, n)
	for i := range xs {
		xs[i] = float64(i)
		means[i] = float64(i)*2 + 10
		stds[i] = float64(i)/2 + 1
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if s := responseSurfaceSVG(xs, means, stds, "p", "throughput"); s == "" {
			b.Fatal("empty svg")
		}
	}
}
