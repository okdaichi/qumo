// Package main — report: SVG plots + text report generation.
package main

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	rptW = 800
	rptH = 400
	rptM = 50
)

var rptColors = []string{"#1f77b4", "#ff7f0e", "#2ca02c", "#d62728", "#9467bd"}

// GenerateReport creates the output directory with SVG plots + a text report.
func GenerateReport(dir string, results []storedResult, space ParamSpace,
	knees []KneePoint, importance []ImportanceRank, interactions []Interaction, objective string) error {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	// 1. Per-parameter sweep plots
	for _, p := range space.Params {
		svg := sweepSVG(results, p, objective)
		if svg != "" {
			writeFile(filepath.Join(dir, fmt.Sprintf("sweep_%s.svg", p.Name)), svg)
		}
	}

	// 2. Parameter importance bar chart
	if len(importance) > 0 {
		writeFile(filepath.Join(dir, "importance.svg"), importanceSVG(importance))
	}

	// 3. Interaction heatmap
	if len(interactions) > 0 {
		writeFile(filepath.Join(dir, "interactions.svg"), interactionSVG(interactions, space))
	}

	// 4. Text report (JSON + human-readable)
	reportJSON, _ := json.MarshalIndent(struct {
		Objective    string             `json:"objective"`
		N            int                `json:"n_experiments"`
		Best         []configSummary    `json:"best"`
		Worst        []configSummary    `json:"worst"`
		Knees        []KneePoint        `json:"knees"`
		Importance   []ImportanceRank   `json:"importance"`
		Interactions []Interaction      `json:"interactions"`
	}{
		Objective:    objective,
		N:            len(results),
		Best:         topConfigs(results, objective, 5, true),
		Worst:        topConfigs(results, objective, 5, false),
		Knees:        knees,
		Importance:   importance,
		Interactions: interactions,
	}, "", "  ")
	writeFile(filepath.Join(dir, "report.json"), string(reportJSON))

	// Human-readable text report
	text := buildTextReport(results, space, knees, importance, interactions, objective)
	writeFile(filepath.Join(dir, "report.txt"), text)

	return nil
}

type configSummary struct {
	Vector  ParamVector `json:"vector"`
	Metrics MetricSet    `json:"metrics"`
}

func topConfigs(results []storedResult, objective string, n int, best bool) []configSummary {
	sort.Slice(results, func(i, j int) bool {
		vi, vj := results[i].Metrics[objective], results[j].Metrics[objective]
		if best {
			return vi > vj
		}
		return vi < vj
	})
	if n > len(results) {
		n = len(results)
	}
	out := make([]configSummary, n)
	for i := range n {
		out[i] = configSummary{Vector: results[i].Vector, Metrics: results[i].Metrics}
	}
	return out
}

func buildTextReport(results []storedResult, space ParamSpace, knees []KneePoint,
	importance []ImportanceRank, interactions []Interaction, objective string) string {
	var sb strings.Builder
	sb.WriteString("=== Parameter Exploration Report ===\n\n")
	sb.WriteString(fmt.Sprintf("Experiments: %d\n", len(results)))
	sb.WriteString(fmt.Sprintf("Objective: %s (maximize)\n\n", objective))

	// Best/worst
	best := topConfigs(results, objective, 3, true)
	worst := topConfigs(results, objective, 3, false)
	sb.WriteString("--- Top 3 configurations ---\n")
	for i, c := range best {
		sb.WriteString(fmt.Sprintf("  #%d: %s → %s=%s\n", i+1, fmtVector(c.Vector), objective, fmtMetric(c.Metrics[objective])))
	}
	sb.WriteString("\n--- Bottom 3 ---\n")
	for i, c := range worst {
		sb.WriteString(fmt.Sprintf("  #%d: %s → %s=%s\n", i+1, fmtVector(c.Vector), objective, fmtMetric(c.Metrics[objective])))
	}

	// Knees
	if len(knees) > 0 {
		sb.WriteString("\n--- Knee points ---\n")
		for _, k := range knees {
			sb.WriteString(fmt.Sprintf("  %s=%s (score=%.3f)\n", k.Param, k.Value, k.Score))
		}
	}

	// Importance
	if len(importance) > 0 {
		sb.WriteString("\n--- Parameter importance (η²) ---\n")
		for _, r := range importance {
			bar := strings.Repeat("█", int(r.Importance*30))
			sb.WriteString(fmt.Sprintf("  %-12s %.3f %s\n", r.Param, r.Importance, bar))
		}
	}

	// Interactions
	if len(interactions) > 0 {
		sb.WriteString("\n--- Parameter interactions ---\n")
		for _, it := range interactions {
			sb.WriteString(fmt.Sprintf("  %s × %s: %.3f\n", it.ParamA, it.ParamB, it.Score))
		}
	}

	return sb.String()
}

// ---- SVG generators ----

func sweepSVG(results []storedResult, p ParamDef, objective string) string {
	groups := make(map[string][]float64)
	for _, sr := range results {
		val := sr.Vector[p.Name]
		m, ok := sr.Metrics[objective]
		if !ok {
			continue
		}
		groups[val] = append(groups[val], m)
	}
	if len(groups) < 2 {
		return ""
	}
	xs, ys := []float64{}, []float64{}
	yMin, yMax := math.MaxFloat64, -math.MaxFloat64
	for _, val := range p.Values {
		vals, ok := groups[val]
		if !ok || len(vals) == 0 {
			continue
		}
		mean, std := meanStd(vals)
		xs = append(xs, float64(len(xs)))
		ys = append(ys, mean)
		lo, hi := mean-std, mean+std
		if lo < yMin { yMin = lo }
		if hi > yMax { yMax = hi }
	}
	if len(xs) < 2 {
		return ""
	}
	plotW := rptW - 2*rptM
	plotH := rptH - 2*rptM
	xScale := func(i float64) float64 {
		if len(xs) <= 1 { return float64(rptM) }
		return float64(rptM) + i/float64(len(xs)-1)*float64(plotW)
	}
	yScale := func(v float64) float64 {
		if yMax == yMin { return float64(rptH - rptM) }
		return float64(rptH - rptM) - (v-yMin)/(yMax-yMin)*float64(plotH)
	}
	var sb strings.Builder
	sb.WriteString(`<?xml version="1.0"?>` + "\n")
	sb.WriteString(fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" font-family="sans-serif" font-size="12">`, rptW, rptH))
	sb.WriteString(`<rect width="100%" height="100%" fill="white"/>`)
	sb.WriteString(fmt.Sprintf(`<text x="%d" y="20" text-anchor="middle" font-size="14" font-weight="bold">%s vs %s</text>`, rptW/2, objective, p.Name))
	// axes
	sb.WriteString(fmt.Sprintf(`<line x1="%d" y1="%d" x2="%d" y2="%d" stroke="#333"/>`, rptM, rptM, rptM, rptH-rptM))
	sb.WriteString(fmt.Sprintf(`<line x1="%d" y1="%d" x2="%d" y2="%d" stroke="#333"/>`, rptM, rptH-rptM, rptW-rptM, rptH-rptM))
	// line + points
	d := ""
	for i, x := range xs {
		px, py := xScale(x), yScale(ys[i])
		if i == 0 { d = fmt.Sprintf("M%.1f %.1f", px, py) } else { d += fmt.Sprintf(" L%.1f %.1f", px, py) }
		sb.WriteString(fmt.Sprintf(`<circle cx="%.1f" cy="%.1f" r="3" fill="#1f77b4"/>`, px, py))
		// x labels
		sb.WriteString(fmt.Sprintf(`<text x="%.1f" y="%d" text-anchor="middle">%s</text>`, px, rptH-rptM+15, p.Values[i]))
	}
	sb.WriteString(fmt.Sprintf(`<path d="%s" fill="none" stroke="#1f77b4" stroke-width="2"/>`, d))
	sb.WriteString(`</svg>`)
	return sb.String()
}

func importanceSVG(ranks []ImportanceRank) string {
	plotW := rptW - 2*rptM
	plotH := rptH - 2*rptM
	maxImp := 0.0
	for _, r := range ranks {
		if r.Importance > maxImp { maxImp = r.Importance }
	}
	if maxImp == 0 { maxImp = 1 }
	bw := float64(plotW) / float64(len(ranks)) * 0.6
	var sb strings.Builder
	sb.WriteString(`<?xml version="1.0"?>` + "\n")
	sb.WriteString(fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" font-family="sans-serif" font-size="12">`, rptW, rptH))
	sb.WriteString(`<rect width="100%" height="100%" fill="white"/>`)
	sb.WriteString(fmt.Sprintf(`<text x="%d" y="20" text-anchor="middle" font-size="14" font-weight="bold">Parameter Importance (η²)</text>`, rptW/2))
	for i, r := range ranks {
		x := float64(rptM) + float64(i)*(float64(plotW)/float64(len(ranks))) + (float64(plotW)/float64(len(ranks))-bw)/2
		h := r.Importance / maxImp * float64(plotH)
		y := float64(rptH-rptM) - h
		sb.WriteString(fmt.Sprintf(`<rect x="%.1f" y="%.1f" width="%.1f" height="%.1f" fill="%s"/>`, x, y, bw, h, rptColors[i%len(rptColors)]))
		sb.WriteString(fmt.Sprintf(`<text x="%.1f" y="%.1f" text-anchor="middle" font-size="10">%s</text>`, x+bw/2, float64(rptH-rptM)+15, r.Param))
		sb.WriteString(fmt.Sprintf(`<text x="%.1f" y="%.1f" text-anchor="middle" font-size="9">%.2f</text>`, x+bw/2, y-4, r.Importance))
	}
	sb.WriteString(`</svg>`)
	return sb.String()
}

func interactionSVG(interactions []Interaction, space ParamSpace) string {
	// Simple grid heatmap: params × params, cell color = interaction score.
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
	for _, s := range scoreMap { if s > maxScore { maxScore = s } }
	if maxScore == 0 { maxScore = 1 }
	var sb strings.Builder
	sb.WriteString(`<?xml version="1.0"?>` + "\n")
	sb.WriteString(fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" font-family="sans-serif" font-size="10">`, totalW, totalH))
	sb.WriteString(`<rect width="100%" height="100%" fill="white"/>`)
	for i, pi := range params {
		for j, pj := range params {
			x := 80 + j*cell
			y := 30 + i*cell
			score := scoreMap[pi.Name+"|"+pj.Name]
			intensity := int(score / maxScore * 255)
			if i == j { intensity = 0 }
			sb.WriteString(fmt.Sprintf(`<rect x="%d" y="%d" width="%d" height="%d" fill="rgb(%d,%d,%d)" stroke="#ccc"/>`,
				x, y, cell, cell, 255-intensity, 255-intensity, 255))
			if score > 0 {
				sb.WriteString(fmt.Sprintf(`<text x="%d" y="%d" text-anchor="middle">%.2f</text>`, x+cell/2, y+cell/2+3, score))
			}
		}
		sb.WriteString(fmt.Sprintf(`<text x="75" y="%d" text-anchor="end">%s</text>`, 30+i*cell+cell/2, pi.Name))
		sb.WriteString(fmt.Sprintf(`<text x="%d" y="25" text-anchor="middle">%s</text>`, 80+i*cell+cell/2, pi.Name))
	}
	sb.WriteString(`</svg>`)
	return sb.String()
}

// ---- helpers ----

func writeFile(path, content string) {
	os.WriteFile(path, []byte(content), 0644)
}

func fmtVector(v ParamVector) string {
	var parts []string
	for k, val := range v {
		parts = append(parts, fmt.Sprintf("%s=%s", k, val))
	}
	sort.Strings(parts)
	return strings.Join(parts, " ")
}

func fmtMetric(v float64) string {
	if v == math.Trunc(v) {
		return fmt.Sprintf("%.0f", v)
	}
	return fmt.Sprintf("%.2f", v)
}
