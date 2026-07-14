// Package visualization renders SVG plots from observed data and precomputed
// model surfaces. It is model-agnostic: callers that need surrogate-derived
// surfaces (response bands, contours) compute the mean/std grid first and pass
// it in, so this package depends only on the experiment and analysis types.
package visualization

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/qumo-dev/qumo/tools/paramexp/analysis"
	"github.com/qumo-dev/qumo/tools/paramexp/experiment"
)

const (
	W = 800
	H = 400
	M = 50
)

// Palette is the default series color cycle.
var Palette = []string{"#1f77b4", "#ff7f0e", "#2ca02c", "#d62728", "#9467bd"}

// svgWrap returns the standard SVG document header sized W×H.
func svgWrap(title, body string) string {
	return `<?xml version="1.0"?>` + "\n" +
		fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" font-family="sans-serif" font-size="12">`, W, H) +
		`<rect width="100%" height="100%" fill="white"/>` +
		fmt.Sprintf(`<text x="%d" y="20" text-anchor="middle" font-size="14" font-weight="bold">%s</text>`, W/2, escape(title)) +
		body + `</svg>`
}

func escape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	return r.Replace(s)
}

// SweepSVG renders a one-at-a-time sweep of the objective versus a parameter's
// levels: mean line, points, and a ±1σ shaded band. Returns "" if <2 groups.
func SweepSVG(obs []experiment.Observation, p experiment.ParamDef, objective string) string {
	groups := make(map[string][]float64)
	for _, o := range obs {
		m, ok := o.Metrics[objective]
		if !ok {
			continue
		}
		groups[o.Vector[p.Name]] = append(groups[o.Vector[p.Name]], m)
	}
	if len(groups) < 2 {
		return ""
	}
	var xs, means, stds []float64
	var labels []string
	yMin, yMax := math.MaxFloat64, -math.MaxFloat64
	for _, val := range p.Values {
		vals, ok := groups[val]
		if !ok || len(vals) == 0 {
			continue
		}
		mean, std := meanStd(vals)
		xs = append(xs, float64(len(xs)))
		means = append(means, mean)
		stds = append(stds, std)
		labels = append(labels, val)
		if mean-std < yMin {
			yMin = mean - std
		}
		if mean+std > yMax {
			yMax = mean + std
		}
	}
	if len(xs) < 2 {
		return ""
	}

	plotW := W - 2*M
	plotH := H - 2*M
	xScale := func(i float64) float64 {
		if len(xs) <= 1 {
			return float64(M)
		}
		return float64(M) + i/float64(len(xs)-1)*float64(plotW)
	}
	yScale := func(v float64) float64 {
		if yMax == yMin {
			return float64(H - M)
		}
		return float64(H-M) - (v-yMin)/(yMax-yMin)*float64(plotH)
	}

	var sb strings.Builder
	// ±1σ band polygon: lower edge forward, upper edge backward.
	var poly []string
	for i, x := range xs {
		poly = append(poly, fmt.Sprintf("%.1f,%.1f", xScale(x), yScale(means[i]-stds[i])))
	}
	for i := len(xs) - 1; i >= 0; i-- {
		poly = append(poly, fmt.Sprintf("%.1f,%.1f", xScale(xs[i]), yScale(means[i]+stds[i])))
	}
	sb.WriteString(fmt.Sprintf(`<polygon points="%s" fill="%s" fill-opacity="0.15" stroke="none"/>`, strings.Join(poly, " "), Palette[0]))
	// mean line + points.
	d := ""
	for i, x := range xs {
		px, py := xScale(x), yScale(means[i])
		if i == 0 {
			d = fmt.Sprintf("M%.1f %.1f", px, py)
		} else {
			d += fmt.Sprintf(" L%.1f %.1f", px, py)
		}
		sb.WriteString(fmt.Sprintf(`<circle cx="%.1f" cy="%.1f" r="3" fill="%s"/>`, px, py, Palette[0]))
		sb.WriteString(fmt.Sprintf(`<text x="%.1f" y="%d" text-anchor="middle">%s</text>`, px, H-M+15, escape(labels[i])))
	}
	sb.WriteString(fmt.Sprintf(`<path d="%s" fill="none" stroke="%s" stroke-width="2"/>`, d, Palette[0]))
	// axes
	sb.WriteString(fmt.Sprintf(`<line x1="%d" y1="%d" x2="%d" y2="%d" stroke="#333"/>`, M, M, M, H-M))
	sb.WriteString(fmt.Sprintf(`<line x1="%d" y1="%d" x2="%d" y2="%d" stroke="#333"/>`, M, H-M, W-M, H-M))
	return svgWrap(fmt.Sprintf("%s vs %s", objective, p.Name), sb.String())
}

// ImportanceSVG renders a horizontal-ish bar chart of η² per parameter.
func ImportanceSVG(ranks []analysis.ImportanceRank) string {
	plotW := W - 2*M
	plotH := H - 2*M
	maxImp := 0.0
	for _, r := range ranks {
		if r.Importance > maxImp {
			maxImp = r.Importance
		}
	}
	if maxImp == 0 {
		maxImp = 1
	}
	bw := float64(plotW) / float64(len(ranks)) * 0.6
	var sb strings.Builder
	for i, r := range ranks {
		slot := float64(plotW) / float64(len(ranks))
		x := float64(M) + float64(i)*slot + (slot-bw)/2
		h := r.Importance / maxImp * float64(plotH)
		y := float64(H-M) - h
		sb.WriteString(fmt.Sprintf(`<rect x="%.1f" y="%.1f" width="%.1f" height="%.1f" fill="%s"/>`, x, y, bw, h, Palette[i%len(Palette)]))
		sb.WriteString(fmt.Sprintf(`<text x="%.1f" y="%.1f" text-anchor="middle" font-size="10">%s</text>`, x+bw/2, float64(H-M)+15, escape(r.Param)))
		sb.WriteString(fmt.Sprintf(`<text x="%.1f" y="%.1f" text-anchor="middle" font-size="9">%.2f</text>`, x+bw/2, y-4, r.Importance))
	}
	return svgWrap("Parameter Importance (η²)", sb.String())
}

// InteractionSVG renders an N×N interaction-score heatmap.
func InteractionSVG(interactions []analysis.Interaction, space experiment.ParamSpace) string {
	params := space.Params
	n := len(params)
	cell := 50
	totalW := n*cell + 100
	totalH := n*cell + 100
	scoreMap := make(map[string]float64)
	for _, it := range interactions {
		scoreMap[it.ParamA+"|"+it.ParamB] = it.Score
		scoreMap[it.ParamB+"|"+it.ParamA] = it.Score
	}
	maxScore := 0.0
	for _, s := range scoreMap {
		if s > maxScore {
			maxScore = s
		}
	}
	if maxScore == 0 {
		maxScore = 1
	}
	var sb strings.Builder
	sb.WriteString(`<?xml version="1.0"?>` + "\n")
	sb.WriteString(fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" font-family="sans-serif" font-size="10">`, totalW, totalH))
	sb.WriteString(`<rect width="100%" height="100%" fill="white"/>`)
	sb.WriteString(fmt.Sprintf(`<text x="%d" y="20" text-anchor="middle" font-size="14" font-weight="bold">Parameter Interactions</text>`, totalW/2))
	for i, pi := range params {
		for j, pj := range params {
			x := 80 + j*cell
			y := 40 + i*cell
			score := scoreMap[pi.Name+"|"+pj.Name]
			intensity := int(score / maxScore * 255)
			if i == j {
				intensity = 0
			}
			sb.WriteString(fmt.Sprintf(`<rect x="%d" y="%d" width="%d" height="%d" fill="rgb(%d,%d,%d)" stroke="#ccc"/>`,
				x, y, cell, cell, 255-intensity, 255-intensity, 255))
			if score > 0 {
				sb.WriteString(fmt.Sprintf(`<text x="%d" y="%d" text-anchor="middle">%.2f</text>`, x+cell/2, y+cell/2+3, score))
			}
		}
		sb.WriteString(fmt.Sprintf(`<text x="75" y="%d" text-anchor="end">%s</text>`, 40+i*cell+cell/2, escape(pi.Name)))
		sb.WriteString(fmt.Sprintf(`<text x="%d" y="35" text-anchor="middle">%s</text>`, 80+i*cell+cell/2, escape(pi.Name)))
	}
	sb.WriteString(`</svg>`)
	return sb.String()
}

// ResponseSurfaceSVG renders a 1-D response surface (mean ± 2σ band) from a
// precomputed model grid: xs are level indices in [0,1], means/stds the model
// predictions. Used for GP-derived surfaces.
func ResponseSurfaceSVG(xs, means, stds []float64, paramName, objective string) string {
	if len(xs) < 2 {
		return ""
	}
	yMin, yMax := math.MaxFloat64, -math.MaxFloat64
	for i := range xs {
		lo, hi := means[i]-2*stds[i], means[i]+2*stds[i]
		if lo < yMin {
			yMin = lo
		}
		if hi > yMax {
			yMax = hi
		}
	}
	plotW := W - 2*M
	plotH := H - 2*M
	xScale := func(x float64) float64 { return float64(M) + x*float64(plotW) }
	yScale := func(v float64) float64 {
		if yMax == yMin {
			return float64(H - M)
		}
		return float64(H-M) - (v-yMin)/(yMax-yMin)*float64(plotH)
	}
	var sb strings.Builder
	// ±2σ band polygon.
	var poly []string
	for i := range xs {
		poly = append(poly, fmt.Sprintf("%.1f,%.1f", xScale(xs[i]), yScale(means[i]-2*stds[i])))
	}
	for i := len(xs) - 1; i >= 0; i-- {
		poly = append(poly, fmt.Sprintf("%.1f,%.1f", xScale(xs[i]), yScale(means[i]+2*stds[i])))
	}
	sb.WriteString(fmt.Sprintf(`<polygon points="%s" fill="%s" fill-opacity="0.15"/>`, strings.Join(poly, " "), Palette[0]))
	d := ""
	for i := range xs {
		px, py := xScale(xs[i]), yScale(means[i])
		if i == 0 {
			d = fmt.Sprintf("M%.1f %.1f", px, py)
		} else {
			d += fmt.Sprintf(" L%.1f %.1f", px, py)
		}
	}
	sb.WriteString(fmt.Sprintf(`<path d="%s" fill="none" stroke="%s" stroke-width="2"/>`, d, Palette[0]))
	sb.WriteString(fmt.Sprintf(`<line x1="%d" y1="%d" x2="%d" y2="%d" stroke="#333"/>`, M, M, M, H-M))
	sb.WriteString(fmt.Sprintf(`<line x1="%d" y1="%d" x2="%d" y2="%d" stroke="#333"/>`, M, H-M, W-M, H-M))
	sb.WriteString(fmt.Sprintf(`<text x="%d" y="%d" text-anchor="middle">%s (low → high)</text>`, W/2, H-12, escape(paramName)))
	return svgWrap(fmt.Sprintf("Response surface: %s vs %s (±2σ)", objective, paramName), sb.String())
}

// ContourSVG renders a 2-D heatmap of model means over a grid spanning two
// parameters. grid[i][j] is the predicted mean at xLevels[i], yLevels[j].
func ContourSVG(grid [][]float64, xName, yName string, xLabels, yLabels []string) string {
	rows := len(grid)
	if rows == 0 {
		return ""
	}
	cols := len(grid[0])
	cell := 40
	totalW := cols*cell + 120
	totalH := rows*cell + 120
	min, max := math.MaxFloat64, -math.MaxFloat64
	for _, r := range grid {
		for _, v := range r {
			if v < min {
				min = v
			}
			if v > max {
				max = v
			}
		}
	}
	if max == min {
		max = min + 1
	}
	var sb strings.Builder
	sb.WriteString(`<?xml version="1.0"?>` + "\n")
	sb.WriteString(fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" font-family="sans-serif" font-size="10">`, totalW, totalH))
	sb.WriteString(`<rect width="100%" height="100%" fill="white"/>`)
	sb.WriteString(fmt.Sprintf(`<text x="%d" y="18" text-anchor="middle" font-size="14" font-weight="bold">Contour: %s × %s</text>`, totalW/2, escape(xName), escape(yName)))
	for i := 0; i < rows; i++ {
		for j := 0; j < cols; j++ {
			v := grid[i][j]
			t := (v - min) / (max - min)
			// blue (low) → red (high).
			r := int(t * 255)
			b := int((1 - t) * 255)
			x := 80 + j*cell
			y := 40 + i*cell
			sb.WriteString(fmt.Sprintf(`<rect x="%d" y="%d" width="%d" height="%d" fill="rgb(%d,128,%d)" stroke="#eee"/>`, x, y, cell, cell, r, b))
		}
	}
	for j, lab := range xLabels {
		if j >= cols {
			break
		}
		sb.WriteString(fmt.Sprintf(`<text x="%d" y="%d" text-anchor="middle">%s</text>`, 80+j*cell+cell/2, 40+rows*cell+15, escape(lab)))
	}
	for i, lab := range yLabels {
		if i >= rows {
			break
		}
		sb.WriteString(fmt.Sprintf(`<text x="75" y="%d" text-anchor="end">%s</text>`, 40+i*cell+cell/2, escape(lab)))
	}
	sb.WriteString(fmt.Sprintf(`<text x="%d" y="%d" text-anchor="middle" font-size="10">min=%.2g  max=%.2g</text>`, totalW/2, totalH-8, min, max))
	sb.WriteString(`</svg>`)
	return sb.String()
}

// meanStd is a local population-stat helper.
func meanStd(xs []float64) (float64, float64) {
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

// SortedParams returns the param names sorted, for stable output ordering.
func SortedParams(space experiment.ParamSpace) []string {
	names := make([]string, 0, len(space.Params))
	for _, p := range space.Params {
		names = append(names, p.Name)
	}
	sort.Strings(names)
	return names
}
