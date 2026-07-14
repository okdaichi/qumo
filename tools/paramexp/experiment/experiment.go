// Package experiment holds the shared domain types of paramexp: the parameter
// space, parameter vectors, experiments, results, and observations.
//
// It is the sole leaf package of the framework — every other package imports
// it, and it imports nothing internal. Keeping the domain here is what makes
// the import graph acyclic.
package experiment

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ParamType classifies how a parameter is encoded into the numeric [0,1] space
// used by samplers and surrogate models.
type ParamType int

const (
	// TypeUnspecified means the type is inferred from the definition: explicit
	// min/max → continuous; else all-numeric Values → discrete; else categorical.
	TypeUnspecified ParamType = iota
	TypeContinuous            // real-valued in [Min, Max]
	TypeDiscrete              // ordered levels (numeric or ordinal strings)
	TypeCategorical           // unordered levels
)

// String returns the lowercase type name used in YAML/JSON.
func (t ParamType) String() string {
	switch t {
	case TypeContinuous:
		return "continuous"
	case TypeDiscrete:
		return "discrete"
	case TypeCategorical:
		return "categorical"
	default:
		return "unspecified"
	}
}

// ParseParamType parses a type name; "" → TypeUnspecified.
func ParseParamType(s string) (ParamType, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "unspecified":
		return TypeUnspecified, nil
	case "continuous", "float", "real":
		return TypeContinuous, nil
	case "discrete", "ordinal":
		return TypeDiscrete, nil
	case "categorical", "enum":
		return TypeCategorical, nil
	default:
		return TypeUnspecified, fmt.Errorf("unknown param type %q", s)
	}
}

// ParamDef defines one parameter dimension.
type ParamDef struct {
	Name   string   `yaml:"name" json:"name"`
	Type   ParamType `yaml:"type" json:"type"`
	Values []string `yaml:"values" json:"values"` // discrete/categorical levels
	Min    float64  `yaml:"min" json:"min,omitempty"` // continuous lower bound
	Max    float64  `yaml:"max" json:"max,omitempty"` // continuous upper bound
}

// ParamSpace is the full parameter space.
type ParamSpace struct {
	Params []ParamDef `yaml:"params" json:"params"`
}

// Size returns the number of points in the full Cartesian space (the product
// of per-dimension level counts). Continuous dimensions contribute 0 (unbounded).
func (ps ParamSpace) Size() int {
	n := 1
	for _, p := range ps.Params {
		if p.Type == TypeContinuous {
			return 0 // unbounded
		}
		n *= len(p.Values)
	}
	return n
}

// Dim returns the number of parameter dimensions.
func (ps ParamSpace) Dim() int { return len(ps.Params) }

// Normalize infers TypeUnspecified parameters and validates the space.
//   - explicit Min/Max (and no Values) → Continuous
//   - all Values parse as float → Discrete (sorted ascending by numeric value)
//   - otherwise → Categorical
//
// Continuous params require Min < Max. Discrete/categorical params require ≥2 Values.
func (ps *ParamSpace) Normalize() error {
	for i := range ps.Params {
		p := &ps.Params[i]
		if p.Type == TypeUnspecified {
			switch {
			case len(p.Values) == 0 && p.Min != p.Max:
				p.Type = TypeContinuous
			case allFloat(p.Values):
				p.Type = TypeDiscrete
			default:
				p.Type = TypeCategorical
			}
		}
		switch p.Type {
		case TypeContinuous:
			if p.Min >= p.Max {
				return fmt.Errorf("parameter %s: continuous requires min < max (got %g..%g)", p.Name, p.Min, p.Max)
			}
		case TypeDiscrete:
			if len(p.Values) < 2 {
				return fmt.Errorf("parameter %s: discrete requires ≥2 values", p.Name)
			}
			sort.SliceStable(p.Values, func(a, b int) bool {
				fa, _ := strconv.ParseFloat(p.Values[a], 64)
				fb, _ := strconv.ParseFloat(p.Values[b], 64)
				return fa < fb
			})
		case TypeCategorical:
			if len(p.Values) < 2 {
				return fmt.Errorf("parameter %s: categorical requires ≥2 values", p.Name)
			}
		}
	}
	return nil
}

// ParamVector is one point in the parameter space: param name → original
// string value (what the black-box runner receives).
type ParamVector map[string]string

// Copy returns a shallow copy of the vector.
func (v ParamVector) Copy() ParamVector {
	out := make(ParamVector, len(v))
	for k, val := range v {
		out[k] = val
	}
	return out
}

// Equal reports whether two vectors hold the same key/value pairs.
func (v ParamVector) Equal(other ParamVector) bool {
	if len(v) != len(other) {
		return false
	}
	for k, va := range v {
		if vb, ok := other[k]; !ok || va != vb {
			return false
		}
	}
	return true
}

// String renders the vector as sorted "name=value" pairs, for stable logging
// and report output. Satisfies fmt.Stringer.
func (v ParamVector) String() string {
	keys := make([]string, 0, len(v))
	for k := range v {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = k + "=" + v[k]
	}
	return strings.Join(parts, " ")
}

// MetricSet is the multidimensional measurement vector produced by one run.
type MetricSet map[string]float64

// Telemetry is an optional per-run resource snapshot the benchmark may emit,
// used downstream for statistical bottleneck attribution. All fields are
// best-effort; missing ones stay zero.
type Telemetry struct {
	CPUpct      float64 `json:"cpu_pct,omitempty"`
	GCPauseMs   float64 `json:"gc_pause_ms,omitempty"`
	Syscalls    float64 `json:"syscalls,omitempty"`
	Retransmits float64 `json:"retransmits,omitempty"`
	RSSmb       float64 `json:"rss_mb,omitempty"`
	Goroutines  float64 `json:"goroutines,omitempty"`
	Raw         map[string]any `json:"raw,omitempty"`
}

// Experiment is one planned run: a parameter vector plus metadata.
type Experiment struct {
	ID        int64       `json:"id"`
	RunID     int64       `json:"run_id"`
	Vector    ParamVector `json:"vector"`
	EncodedX  []float64   `json:"encoded_x,omitempty"`
	Phase     string      `json:"phase"` // lhs|sobol|adaptive-N|bo-N|baseline
	CreatedAt time.Time   `json:"created_at"`
	Manifest  string      `json:"manifest"`
}

// Result is the outcome of running one experiment (possibly after retries).
type Result struct {
	ExperimentID int64       `json:"experiment_id"`
	Metrics      MetricSet   `json:"metrics"`
	Telemetry    *Telemetry  `json:"telemetry,omitempty"`
	Duration     float64     `json:"duration_sec"`
	ExitCode     int         `json:"exit_code"`
	Attempts     int         `json:"attempts"`
	Replicate    int         `json:"replicate,omitempty"` // 1-based index when a vector is run N times
	Error        string      `json:"error,omitempty"`
	Stdout       string      `json:"stdout,omitempty"`
	Stderr       string      `json:"stderr,omitempty"`
	Timestamp    time.Time   `json:"timestamp"`
}

// Observation is the analysis-oriented join of an Experiment with its Results.
// When the experiment was replicated (run N times), Metrics holds the per-metric
// MEANS across replicates, Variances the per-metric population variance, and N
// the replicate count; the GP fits on the means with per-point noise = Var/N.
type Observation struct {
	ExperimentID int64        `json:"experiment_id"`
	Vector       ParamVector  `json:"vector"`
	EncodedX     []float64    `json:"encoded_x"`
	Metrics      MetricSet    `json:"metrics"`     // per-metric means across replicates
	Variances    MetricSet    `json:"variances,omitempty"`
	N            int          `json:"n,omitempty"`
	Telemetry    *Telemetry   `json:"telemetry,omitempty"`
	Duration     float64      `json:"duration_sec"`
	ExitCode     int          `json:"exit_code"`
	Phase        string       `json:"phase"`
	Timestamp    time.Time    `json:"timestamp"`
}

// Config is the paramexp configuration parsed from YAML.
type Config struct {
	Space       ParamSpace   `yaml:"space"`
	Runner      string       `yaml:"runner"`       // benchmark command template
	DBPath      string       `yaml:"db"`           // SQLite path (default paramexp.db)
	Samples     int          `yaml:"samples"`      // initial sample count (default 20)
	Adaptive    int          `yaml:"adaptive"`     // adaptive rounds (default 3)
	Output      string       `yaml:"output"`       // report dir (default report)
	Objective   string       `yaml:"objective"`    // metric to maximize (default throughput_fps)
	Timeout     time.Duration `yaml:"timeout"`     // per-run timeout (default 10m)
	MaxAttempts int          `yaml:"max_attempts"` // retry count (default 1)
	Replicates  int          `yaml:"replicates"`   // runs per parameter vector (default 1)
}

// ParseConfig reads a minimal YAML config (see example/params.yaml). No YAML
// dependency: the format is simple enough for a line-oriented parser.
func ParseConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	cfg := &Config{
		DBPath:    "paramexp.db",
		Samples:   20,
		Adaptive:  3,
		Output:    "report_out",
		Objective: "throughput_fps",
		Timeout:   10 * time.Minute,
	}
	if err := parseYAML(string(data), cfg); err != nil {
		return nil, err
	}
	if cfg.Runner == "" {
		return nil, fmt.Errorf("runner is required")
	}
	if len(cfg.Space.Params) == 0 {
		return nil, fmt.Errorf("at least one parameter is required")
	}
	if cfg.MaxAttempts < 1 {
		cfg.MaxAttempts = 1
	}
	if cfg.Replicates < 1 {
		cfg.Replicates = 1
	}
	if err := cfg.Space.Normalize(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// parseYAML is a minimal line-oriented YAML parser supporting a top-level set
// of keys plus a "space.params" list whose entries have name/type/values/min/max.
// Indentation is not significant; list items are introduced by "- ".
func parseYAML(src string, cfg *Config) error {
	var inParams bool
	lastType := TypeUnspecified
	for _, raw := range strings.Split(src, "\n") {
		line := strings.TrimSpace(stripComment(raw))
		if line == "" {
			continue
		}
		switch {
		case line == "space:":
			inParams = false
		case line == "params:":
			inParams = true
		case strings.HasPrefix(line, "- name:") || strings.HasPrefix(line, "name:"):
			if !inParams {
				continue
			}
			name := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(line, "- name:"), "name:"))
			name = trimQuote(name)
			cfg.Space.Params = append(cfg.Space.Params, ParamDef{Name: name})
			lastType = TypeUnspecified
		case strings.HasPrefix(line, "- type:") || strings.HasPrefix(line, "type:"):
			if !inParams || len(cfg.Space.Params) == 0 {
				continue
			}
			t := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(line, "- type:"), "type:"))
			pt, err := ParseParamType(trimQuote(t))
			if err != nil {
				return err
			}
			cfg.Space.Params[len(cfg.Space.Params)-1].Type = pt
			lastType = pt
		case strings.HasPrefix(line, "- values:") || strings.HasPrefix(line, "values:"):
			if !inParams || len(cfg.Space.Params) == 0 {
				continue
			}
			v := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(line, "- values:"), "values:"))
			cfg.Space.Params[len(cfg.Space.Params)-1].Values = parseList(v)
		case strings.HasPrefix(line, "- min:") || strings.HasPrefix(line, "min:"):
			if !inParams || len(cfg.Space.Params) == 0 {
				continue
			}
			v := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(line, "- min:"), "min:"))
			f, _ := strconv.ParseFloat(trimQuote(v), 64)
			cfg.Space.Params[len(cfg.Space.Params)-1].Min = f
			if lastType == TypeUnspecified {
				cfg.Space.Params[len(cfg.Space.Params)-1].Type = TypeContinuous
			}
		case strings.HasPrefix(line, "- max:") || strings.HasPrefix(line, "max:"):
			if !inParams || len(cfg.Space.Params) == 0 {
				continue
			}
			v := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(line, "- max:"), "max:"))
			f, _ := strconv.ParseFloat(trimQuote(v), 64)
			cfg.Space.Params[len(cfg.Space.Params)-1].Max = f
			if lastType == TypeUnspecified {
				cfg.Space.Params[len(cfg.Space.Params)-1].Type = TypeContinuous
			}
		case strings.HasPrefix(line, "runner:"):
			cfg.Runner = trimQuote(strings.TrimSpace(strings.TrimPrefix(line, "runner:")))
		case strings.HasPrefix(line, "db:"):
			cfg.DBPath = trimQuote(strings.TrimSpace(strings.TrimPrefix(line, "db:")))
		case strings.HasPrefix(line, "samples:"):
			cfg.Samples = parseInt(trimQuote(strings.TrimSpace(strings.TrimPrefix(line, "samples:"))))
		case strings.HasPrefix(line, "adaptive:"):
			cfg.Adaptive = parseInt(trimQuote(strings.TrimSpace(strings.TrimPrefix(line, "adaptive:"))))
		case strings.HasPrefix(line, "output:"):
			cfg.Output = trimQuote(strings.TrimSpace(strings.TrimPrefix(line, "output:")))
		case strings.HasPrefix(line, "objective:"):
			cfg.Objective = trimQuote(strings.TrimSpace(strings.TrimPrefix(line, "objective:")))
		case strings.HasPrefix(line, "timeout:"):
			d, err := time.ParseDuration(trimQuote(strings.TrimSpace(strings.TrimPrefix(line, "timeout:"))))
			if err == nil && d > 0 {
				cfg.Timeout = d
			}
		case strings.HasPrefix(line, "max_attempts:"):
			n := parseInt(trimQuote(strings.TrimSpace(strings.TrimPrefix(line, "max_attempts:"))))
			if n > 0 {
				cfg.MaxAttempts = n
			}
		case strings.HasPrefix(line, "replicates:"):
			n := parseInt(trimQuote(strings.TrimSpace(strings.TrimPrefix(line, "replicates:"))))
			if n > 0 {
				cfg.Replicates = n
			}
		}
	}
	return nil
}

func allFloat(values []string) bool {
	if len(values) == 0 {
		return false
	}
	for _, v := range values {
		if _, err := strconv.ParseFloat(v, 64); err != nil {
			return false
		}
	}
	return true
}

func stripComment(line string) string {
	// Strip a trailing " #..." comment only when the # is preceded by whitespace
	// and not inside a value; keep it simple: a standalone " #" marks a comment.
	for i := 0; i < len(line); i++ {
		if line[i] == '#' && (i == 0 || line[i-1] == ' ' || line[i-1] == '\t') {
			return line[:i]
		}
	}
	return line
}

func trimQuote(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'') && s[len(s)-1] == s[0] {
		return s[1 : len(s)-1]
	}
	return s
}

func parseList(s string) []string {
	s = strings.TrimSpace(s)
	s = strings.TrimSuffix(strings.TrimPrefix(s, "["), "]")
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = trimQuote(strings.TrimSpace(p))
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func parseInt(s string) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n <= 0 {
		return 0
	}
	return n
}
