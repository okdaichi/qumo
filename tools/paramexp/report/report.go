// Package report assembles the analysis results and visualization SVGs into a
// report directory: per-parameter sweeps, importance, interactions, response
// surfaces and a 2-D contour, plus a JSON summary, a text report, and an HTML
// index that renders every SVG inline.
package report

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/qumo-dev/qumo/tools/paramexp/analysis"
	"github.com/qumo-dev/qumo/tools/paramexp/experiment"
	"github.com/qumo-dev/qumo/tools/paramexp/model"
)

// Surface is a precomputed 1-D model surface (level index → mean, std).
type Surface struct {
	Param string
	Xs    []float64 // level indices in [0,1]
	Means []float64
	Stds  []float64
}

// Contour is a precomputed 2-D model surface.
type Contour struct {
	XParam, YParam string
	XLabels        []string
	YLabels        []string
	Grid           [][]float64 // [i][j] mean at XLabels[i] × YLabels[j]
}

// Inputs bundles everything needed to render a report.
type Inputs struct {
	Dir          string
	Objective    string
	Space        experiment.ParamSpace
	Observations []experiment.Observation
	Knees        []analysis.KneePoint
	Importance   []analysis.ImportanceRank
	Interactions []analysis.Interaction
	Sensitivity  []analysis.Sensitivity // GP-derived; may be nil
	Surfaces     []Surface              // GP-derived 1-D surfaces; may be nil
	Contour      *Contour               // GP-derived 2-D contour; may be nil
	Stability    []analysis.Stability   // per-config CV / unstable flag; may be nil
	Best         analysis.ConfigCI      // best config (max objective mean) + its CI
	Peers        []analysis.ConfigCI    // configs statistically indistinguishable from Best
}

// Generate writes the report directory.
func Generate(in Inputs) error {
	if err := os.MkdirAll(in.Dir, 0o755); err != nil {
		return err
	}

	// 1. Per-parameter empirical sweeps.
	for _, p := range in.Space.Params {
		if svg := sweepSVG(in.Observations, p, in.Objective); svg != "" {
			writeFile(filepath.Join(in.Dir, fmt.Sprintf("sweep_%s.svg", p.Name)), svg)
		}
	}

	// 2. Importance (η²) and, if present, GP sensitivity.
	if len(in.Importance) > 0 {
		writeFile(filepath.Join(in.Dir, "importance.svg"), importanceSVG(in.Importance))
	}
	if len(in.Sensitivity) > 0 {
		writeFile(filepath.Join(in.Dir, "sensitivity.svg"), importanceSVG(sensitivityToRanks(in.Sensitivity)))
	}

	// 3. Interaction heatmap.
	if len(in.Interactions) > 0 {
		writeFile(filepath.Join(in.Dir, "interactions.svg"), interactionSVG(in.Interactions, in.Space))
	}

	// 4. GP-derived 1-D response surfaces.
	for _, s := range in.Surfaces {
		svg := responseSurfaceSVG(s.Xs, s.Means, s.Stds, s.Param, in.Objective)
		if svg != "" {
			writeFile(filepath.Join(in.Dir, fmt.Sprintf("surface_%s.svg", s.Param)), svg)
		}
	}

	// 5. 2-D contour over the two most-sensitive params.
	if in.Contour != nil {
		writeFile(filepath.Join(in.Dir, "contour.svg"),
			contourSVG(in.Contour.Grid, in.Contour.XParam, in.Contour.YParam, in.Contour.XLabels, in.Contour.YLabels))
	}

	// 6. JSON summary.
	summary := struct {
		Objective    string                   `json:"objective"`
		N            int                      `json:"n_experiments"`
		Best         []configSummary          `json:"best"`
		Worst        []configSummary          `json:"worst"`
		Knees        []analysis.KneePoint     `json:"knees"`
		Importance   []analysis.ImportanceRank `json:"importance"`
		Interactions []analysis.Interaction   `json:"interactions"`
		Sensitivity  []analysis.Sensitivity   `json:"sensitivity,omitempty"`
		Stability    []analysis.Stability     `json:"stability,omitempty"`
		BestConfig   analysis.ConfigCI        `json:"best_config,omitempty"`
		Peers        []analysis.ConfigCI      `json:"indistinguishable_peers,omitempty"`
	}{
		Objective:    in.Objective,
		N:            len(in.Observations),
		Best:         topConfigs(in.Observations, in.Objective, 5, true),
		Worst:        topConfigs(in.Observations, in.Objective, 5, false),
		Knees:        in.Knees,
		Importance:   in.Importance,
		Interactions: in.Interactions,
		Sensitivity:  in.Sensitivity,
		Stability:    in.Stability,
		BestConfig:   in.Best,
		Peers:        in.Peers,
	}
	b, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		// A marshal error (e.g. a NaN sneaking into a metric) would otherwise
		// produce a silently empty report.json — surface it.
		return fmt.Errorf("marshal report.json: %w", err)
	}
	writeFile(filepath.Join(in.Dir, "report.json"), string(b))

	// 7. Text report.
	writeFile(filepath.Join(in.Dir, "report.txt"), buildText(in))

	// 8. HTML index.
	writeFile(filepath.Join(in.Dir, "index.html"), buildHTML(in))

	return nil
}

// BuildSurfaces computes a 1-D GP response surface per parameter by sweeping
// that parameter across [0,1] while holding the others at 0.5 (median). Used by
// the CLI when a GP is available.
func BuildSurfaces(gp model.Surrogate, space experiment.ParamSpace, n int) []Surface {
	dim := space.Dim()
	if gp == nil || dim == 0 {
		return nil
	}
	if n < 2 {
		n = 21
	}
	var out []Surface
	for d, p := range space.Params {
		xs := make([]float64, n)
		grid := make([][]float64, n)
		for i := 0; i < n; i++ {
			x := float64(i) / float64(n-1)
			xs[i] = x
			pt := medianPoint(dim, d, x)
			grid[i] = pt
		}
		means, stds, err := gp.PredictBatch(grid)
		if err != nil {
			continue
		}
		out = append(out, Surface{Param: p.Name, Xs: xs, Means: means, Stds: stds})
	}
	return out
}

// BuildContour computes a 2-D GP surface over the two most-sensitive params
// (others held at median). Returns nil if there are fewer than 2 params.
func BuildContour(gp model.Surrogate, space experiment.ParamSpace, sens []analysis.Sensitivity, n int) *Contour {
	if gp == nil || space.Dim() < 2 || len(sens) < 2 {
		return nil
	}
	if n < 2 {
		n = 13
	}
	// Two most-sensitive params.
	dx := indexOfParam(space, sens[0].Param)
	dy := indexOfParam(space, sens[1].Param)
	if dx < 0 || dy < 0 {
		return nil
	}
	grid := make([][]float64, n*n)
	idx := 0
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			pt := medianPoint(space.Dim(), dx, float64(i)/float64(n-1))
			pt[dy] = float64(j) / float64(n-1)
			grid[idx] = pt
			idx++
		}
	}
	means, _, err := gp.PredictBatch(grid)
	if err != nil {
		return nil
	}
	// Reshape into [i][j].
	out := make([][]float64, n)
	for i := 0; i < n; i++ {
		out[i] = make([]float64, n)
		for j := 0; j < n; j++ {
			out[i][j] = means[i*n+j]
		}
	}
	return &Contour{
		XParam: space.Params[dx].Name, YParam: space.Params[dy].Name,
		XLabels: levelLabels(space.Params[dx], n),
		YLabels: levelLabels(space.Params[dy], n),
		Grid:    out,
	}
}

func levelLabels(p experiment.ParamDef, n int) []string {
	if n > 6 {
		// sparse labels: first, middle, last
		return []string{labelAt(p, 0), labelAt(p, 0.5), labelAt(p, 1)}
	}
	out := make([]string, n)
	for i := 0; i < n; i++ {
		out[i] = labelAt(p, float64(i)/float64(n-1))
	}
	return out
}

func labelAt(p experiment.ParamDef, u float64) string {
	switch p.Type {
	case experiment.TypeContinuous:
		v := p.Min + u*(p.Max-p.Min)
		return fmt.Sprintf("%.3g", v)
	default:
		if len(p.Values) == 0 {
			return ""
		}
		idx := int(math.Round(u * float64(len(p.Values)-1)))
		if idx < 0 {
			idx = 0
		}
		if idx >= len(p.Values) {
			idx = len(p.Values) - 1
		}
		return p.Values[idx]
	}
}

func medianPoint(dim, except int, val float64) []float64 {
	pt := make([]float64, dim)
	for i := range pt {
		pt[i] = 0.5
	}
	pt[except] = val
	return pt
}

func indexOfParam(space experiment.ParamSpace, name string) int {
	for i, p := range space.Params {
		if p.Name == name {
			return i
		}
	}
	return -1
}

func sensitivityToRanks(s []analysis.Sensitivity) []analysis.ImportanceRank {
	out := make([]analysis.ImportanceRank, len(s))
	for i, x := range s {
		out[i] = analysis.ImportanceRank{Param: x.Param, Importance: x.Importance}
	}
	return out
}

type configSummary struct {
	Vector  experiment.ParamVector `json:"vector"`
	Metrics experiment.MetricSet    `json:"metrics"`
	N       int                    `json:"n,omitempty"`
	CI      float64                `json:"ci,omitempty"` // half-width z·sqrt(var/N) of the objective mean
}

func topConfigs(obs []experiment.Observation, objective string, n int, best bool) []configSummary {
	cp := append([]experiment.Observation(nil), obs...)
	sort.Slice(cp, func(i, j int) bool {
		vi, vj := cp[i].Metrics[objective], cp[j].Metrics[objective]
		if best {
			return vi > vj
		}
		return vi < vj
	})
	if n > len(cp) {
		n = len(cp)
	}
	out := make([]configSummary, n)
	for i := range n {
		o := cp[i]
		nr := o.N
		if nr < 1 {
			nr = 1
		}
		out[i] = configSummary{
			Vector: o.Vector, Metrics: o.Metrics, N: o.N,
			CI: 1.96 * math.Sqrt(o.Variances[objective]/float64(nr)),
		}
	}
	return out
}

func buildText(in Inputs) string {
	var sb strings.Builder
	sb.WriteString("=== Parameter Exploration Report ===\n\n")
	sb.WriteString(fmt.Sprintf("Experiments: %d (aggregated; replicates collapsed to mean±CI)\n", len(in.Observations)))
	sb.WriteString(fmt.Sprintf("Objective: %s (maximize)\n\n", in.Objective))
	best := topConfigs(in.Observations, in.Objective, 3, true)
	worst := topConfigs(in.Observations, in.Objective, 3, false)
	sb.WriteString("--- Top 3 configurations (mean ± 95% CI) ---\n")
	for i, c := range best {
		sb.WriteString(fmt.Sprintf("  #%d: %s → %s=%s\n", i+1, c.Vector.String(), in.Objective, fmtMeanCI(c.Metrics[in.Objective], c.CI, c.N)))
	}
	sb.WriteString("\n--- Bottom 3 ---\n")
	for i, c := range worst {
		sb.WriteString(fmt.Sprintf("  #%d: %s → %s=%s\n", i+1, c.Vector.String(), in.Objective, fmtMeanCI(c.Metrics[in.Objective], c.CI, c.N)))
	}

	// Best config + statistically indistinguishable peers (the "can't tell apart
	// from best" set — the brief asks to surface this rather than rank by noise).
	if in.Best.ExperimentID != 0 {
		sb.WriteString("\n--- Best vs. indistinguishable peers ---\n")
		sb.WriteString(fmt.Sprintf("  best: %s → %s=%s\n", in.Best.Vector.String(), in.Objective,
			fmtMeanCI(in.Best.Mean, 1.96*in.Best.SE, in.Best.N)))
		if len(in.Peers) == 0 {
			sb.WriteString("  (no other config is statistically indistinguishable from the best)\n")
		}
		for _, p := range in.Peers {
			sb.WriteString(fmt.Sprintf("  ~peer: %s → %s=%s\n", p.Vector.String(), in.Objective,
				fmtMeanCI(p.Mean, 1.96*p.SE, p.N)))
		}
	}

	// Stability: which configs are noisy across replicates.
	if unstable := unstableStability(in.Stability); len(unstable) > 0 {
		sb.WriteString("\n--- Unstable configurations (cv>0.15) ---\n")
		for _, s := range unstable {
			sb.WriteString(fmt.Sprintf("  %s → %s (cv=%.2f, n=%d)\n", s.Vector.String(), in.Objective, s.CV, s.N))
		}
	}

	if len(in.Knees) > 0 {
		sb.WriteString("\n--- Knee points ---\n")
		for _, k := range in.Knees {
			sb.WriteString(fmt.Sprintf("  %s=%s (score=%.3f)\n", k.Param, k.Value, k.Score))
		}
	}
	if len(in.Importance) > 0 {
		sb.WriteString("\n--- Parameter importance (η² = variance explained) ---\n")
		for _, r := range in.Importance {
			bar := strings.Repeat("█", int(r.Importance*30))
			sb.WriteString(fmt.Sprintf("  %-12s %.3f %s\n", r.Param, r.Importance, bar))
		}
	}
	if len(in.Sensitivity) > 0 {
		sb.WriteString("\n--- GP relevance (1/ℓ² ARD, normalized) ---\n")
		for _, r := range in.Sensitivity {
			bar := strings.Repeat("█", int(r.Importance*30))
			sb.WriteString(fmt.Sprintf("  %-12s %.3f %s\n", r.Param, r.Importance, bar))
		}
		sb.WriteString("\nNote: η² and GP 1/ℓ² measure different things — η² is contribution to\n" +
			"overall variance; 1/ℓ² is local relevance/smoothness. A dimension with a\n" +
			"large-but-smooth effect ranks high in η² but low in 1/ℓ². Contradictory\n" +
			"rankings are informative, not a bug.\n")
	}
	if len(in.Interactions) > 0 {
		sb.WriteString("\n--- Parameter interactions ---\n")
		for _, it := range in.Interactions {
			sb.WriteString(fmt.Sprintf("  %s × %s: %.3f\n", it.ParamA, it.ParamB, it.Score))
		}
	}
	return sb.String()
}

// unstableStability returns only the configs flagged unstable (sorted by CV desc).
func unstableStability(stab []analysis.Stability) []analysis.Stability {
	var out []analysis.Stability
	for _, s := range stab {
		if s.Unstable {
			out = append(out, s)
		}
	}
	return out
}

// fmtMeanCI renders "value ± ci (n=N)" when replicated, else the bare value.
func fmtMeanCI(mean, ci float64, n int) string {
	if n > 1 {
		return fmt.Sprintf("%.4g ± %.3g (n=%d)", mean, ci, n)
	}
	return fmtMetric(mean)
}

func buildHTML(in Inputs) string {
	var sb strings.Builder
	sb.WriteString("<!DOCTYPE html><html><head><meta charset='utf-8'><style>")
	sb.WriteString("body{font-family:sans-serif;margin:20px} figure{display:inline-block;margin:10px}")
	sb.WriteString("img{max-width:600px;border:1px solid #ddd} h2{margin-top:1.5em}")
	sb.WriteString("</style></head><body>\n")
	sb.WriteString("<h1>paramexp report</h1>\n")
	sb.WriteString(fmt.Sprintf("<p>Objective: <code>%s</code> · Experiments: %d</p>\n", in.Objective, len(in.Observations)))
	section := func(title string, files []string) {
		if len(files) == 0 {
			return
		}
		sb.WriteString(fmt.Sprintf("<h2>%s</h2>\n", title))
		for _, f := range files {
			sb.WriteString(fmt.Sprintf("<figure><img src='%s'/><figcaption>%s</figcaption></figure>\n", f, f))
		}
	}
	var sweeps, surfaces []string
	entries, _ := os.ReadDir(in.Dir)
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".svg") {
			continue
		}
		switch {
		case strings.HasPrefix(e.Name(), "sweep_"):
			sweeps = append(sweeps, e.Name())
		case strings.HasPrefix(e.Name(), "surface_"):
			surfaces = append(surfaces, e.Name())
		}
	}
	sort.Strings(sweeps)
	sort.Strings(surfaces)
	section("Sweeps", sweeps)
	section("GP response surfaces (±2σ)", surfaces)
	other := []string{}
	if hasFile(in.Dir, "importance.svg") {
		other = append(other, "importance.svg")
	}
	if hasFile(in.Dir, "sensitivity.svg") {
		other = append(other, "sensitivity.svg")
	}
	if hasFile(in.Dir, "interactions.svg") {
		other = append(other, "interactions.svg")
	}
	if hasFile(in.Dir, "contour.svg") {
		other = append(other, "contour.svg")
	}
	section("Sensitivity, interactions, contour", other)
	sb.WriteString("</body></html>\n")
	return sb.String()
}

func hasFile(dir, name string) bool {
	_, err := os.Stat(filepath.Join(dir, name))
	return err == nil
}

func writeFile(path, content string) {
	_ = os.WriteFile(path, []byte(content), 0o644)
}

func fmtMetric(v float64) string {
	if v == math.Trunc(v) {
		return fmt.Sprintf("%.0f", v)
	}
	return fmt.Sprintf("%.2f", v)
}
