// Package main — experiment types: parameter space, experiment, result, manifest.
package main

import (
	"fmt"
	"os"
	"sort"
	"time"
)

// ParamDef defines one discrete parameter dimension.
type ParamDef struct {
	Name   string   `yaml:"name" json:"name"`
	Values []string `yaml:"values" json:"values"` // discrete levels (strings for generality)
}

// ParamSpace is the full discrete parameter space.
type ParamSpace struct {
	Params []ParamDef `yaml:"params" json:"params"`
}

// Size returns the total number of points in the full Cartesian space.
func (ps ParamSpace) Size() int {
	n := 1
	for _, p := range ps.Params {
		n *= len(p.Values)
	}
	return n
}

// ParamVector is one point in the parameter space (param name → value).
type ParamVector map[string]string

// Experiment is one planned run: a parameter vector + metadata.
type Experiment struct {
	ID         int64       `json:"id"`
	Vector     ParamVector `json:"vector"`
	Phase      string      `json:"phase"` // "lhs" | "sobol" | "adaptive" | "baseline"
	CreatedAt  time.Time   `json:"created_at"`
	Manifest   string      `json:"manifest"` // reproducible manifest (JSON of the full config)
}

// MetricSet is the output of one benchmark run: named metrics.
type MetricSet map[string]float64

// Result is an experiment + its measured metrics + status.
type Result struct {
	ExperimentID int64     `json:"experiment_id"`
	Metrics      MetricSet `json:"metrics"`
	Duration     float64   `json:"duration_sec"` // wall-clock of the benchmark
	ExitCode     int       `json:"exit_code"`
	Error        string    `json:"error,omitempty"`
	Stdout       string    `json:"stdout,omitempty"` // truncated
	Timestamp    time.Time `json:"timestamp"`
}

// config represents the paramexp configuration (YAML).
type config struct {
	Space    ParamSpace `yaml:"space"`
	Runner   string     `yaml:"runner"`    // command template, e.g. "bash bench.sh"
	DBPath   string     `yaml:"db"`        // SQLite path, default "paramexp.db"
	Samples  int        `yaml:"samples"`   // initial sample count (LHS), default 20
	Adaptive int        `yaml:"adaptive"`  // adaptive rounds, default 3
	Output   string     `yaml:"output"`    // report output dir, default "report"
}

// parseYAMLConfig reads a minimal YAML config (key:value, no nesting beyond
// the space params). This avoids a YAML dependency; the format is simple enough.
func parseYAMLConfig(path string) (*config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	cfg := &config{
		DBPath:   "paramexp.db",
		Samples:  20,
		Adaptive: 3,
		Output:   "report",
	}
	// Minimal YAML parser: line-oriented, supports top-level keys and a "space:"
	// section with "params:" list. Each param has "name:" and "values: [a,b,c]".
	lines := splitYAMLLines(string(data))
	var inParams bool
	for _, line := range lines {
		if line == "" || startsWith(line, "#") {
			continue
		}
		switch {
		case line == "space:":
			inParams = false
		case line == "params:":
			inParams = true
		case startsWith(line, "- name:") || startsWith(line, "name:"):
			if inParams {
				name := ""
				if startsWith(line, "- name:") {
					name = trimValue(line[len("- name:"):])
				} else {
					name = trimValue(line[len("name:"):])
				}
				cfg.Space.Params = append(cfg.Space.Params, ParamDef{Name: name})
			}
		case startsWith(line, "- values:") || startsWith(line, "values:"):
			if inParams && len(cfg.Space.Params) > 0 {
				v := ""
				if startsWith(line, "- values:") {
					v = trimValue(line[len("- values:"):])
				} else {
					v = trimValue(line[len("values:"):])
				}
				cfg.Space.Params[len(cfg.Space.Params)-1].Values = parseList(v)
			}
		case startsWith(line, "runner:"):
			cfg.Runner = trimValue(line[len("runner:"):])
		case startsWith(line, "db:"):
			cfg.DBPath = trimValue(line[len("db:"):])
		case startsWith(line, "samples:"):
			cfg.Samples = parseInt(trimValue(line[len("samples:"):]))
		case startsWith(line, "adaptive:"):
			cfg.Adaptive = parseInt(trimValue(line[len("adaptive:"):]))
		case startsWith(line, "output:"):
			cfg.Output = trimValue(line[len("output:"):])
		}
	}
	// Validate
	if cfg.Runner == "" {
		return nil, fmt.Errorf("runner is required")
	}
	if len(cfg.Space.Params) == 0 {
		return nil, fmt.Errorf("at least one parameter is required")
	}
	for i, p := range cfg.Space.Params {
		if len(p.Values) < 2 {
			return nil, fmt.Errorf("parameter %s must have ≥2 values (has %d)", p.Name, len(p.Values))
		}
		sort.Strings(cfg.Space.Params[i].Values)
	}
	return cfg, nil
}

// helper functions for minimal YAML parsing
func splitYAMLLines(s string) []string {
	var lines []string
	for _, l := range splitNewlines(s) {
		lines = append(lines, trimSpace(l))
	}
	return lines
}

func startsWith(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}

func trimValue(s string) string {
	return trimSpace(trimQuote(trimSpace(s)))
}

func trimQuote(s string) string {
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'') && s[len(s)-1] == s[0] {
		return s[1 : len(s)-1]
	}
	return s
}

func parseList(s string) []string {
	s = trimSpace(s)
	s = trim(s, "[]")
	parts := split(s, ",")
	var out []string
	for _, p := range parts {
		p = trimSpace(trimQuote(p))
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func parseInt(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			break
		}
		n = n*10 + int(c-'0')
	}
	if n == 0 {
		return 20 // default
	}
	return n
}
