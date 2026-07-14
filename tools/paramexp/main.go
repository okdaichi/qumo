// Package main — paramexp: automated parameter exploration framework.
//
// Usage:
//
//	paramexp explore --config params.yaml --runner 'bash bench.sh' --objective throughput_fps
//
// The config defines a discrete parameter space. paramexp samples it (LHS),
// runs the benchmark per sample, stores results in SQLite, then analyzes:
// knee points, parameter importance, interactions, and generates a report.
//
// The runner receives parameters as environment variables (PARAM_<NAME>) and
// must output a JSON line of metrics on stdout.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	cmd := os.Args[1]
	switch cmd {
	case "explore":
		explore(os.Args[2:])
	case "report":
		reportOnly(os.Args[2:])
	case "list":
		listCmd(os.Args[2:])
	default:
		usage()
		os.Exit(1)
	}
}

func explore(args []string) {
	fs := flag.NewFlagSet("explore", flag.ExitOnError)
	configPath := fs.String("config", "", "parameter space config (YAML)")
	runner := fs.String("runner", "", "benchmark command template (overrides config)")
	objective := fs.String("objective", "throughput_fps", "metric to maximize")
	dbPath := fs.String("db", "", "SQLite path (overrides config, default paramexp.db)")
	samples := fs.Int("samples", 0, "initial LHS sample count (overrides config)")
	adaptive := fs.Int("adaptive", 0, "adaptive rounds (overrides config)")
	output := fs.String("output", "", "report output dir (overrides config)")
	fs.Parse(args)

	if *configPath == "" {
		fmt.Fprintln(os.Stderr, "error: --config is required")
		os.Exit(1)
	}

	cfg, err := parseYAMLConfig(*configPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	// Apply CLI overrides
	if *runner != "" {
		cfg.Runner = *runner
	}
	if *dbPath != "" {
		cfg.DBPath = *dbPath
	}
	if *samples > 0 {
		cfg.Samples = *samples
	}
	if *adaptive > 0 {
		cfg.Adaptive = *adaptive
	}
	if *output != "" {
		cfg.Output = *output
	}

	log.Printf("parameter space: %d params, %d total points",
		len(cfg.Space.Params), cfg.Space.Size())
	log.Printf("sampling: %d LHS + %d adaptive rounds", cfg.Samples, cfg.Adaptive)
	log.Printf("runner: %s", cfg.Runner)
	log.Printf("objective: %s (maximize)", *objective)

	// Open storage
	store, err := OpenStorage(cfg.DBPath)
	if err != nil {
		log.Fatalf("storage: %v", err)
	}
	defer store.Close()

	// Phase 1: LHS broad exploration
	log.Println("\n=== Phase 1: LHS broad exploration ===")
	lhs := LHSSampler{}
	vectors := lhs.Sample(cfg.Space, cfg.Samples)
	allExisting := make([]ParamVector, len(vectors))
	copy(allExisting, vectors)

	for i, v := range vectors {
		exp := Experiment{
			Vector:    v,
			Phase:     "lhs",
			CreatedAt: time.Now(),
			Manifest:  makeManifest(cfg, v),
		}
		if err := store.SaveExperiment(&exp); err != nil {
			log.Printf("  [%d/%d] save experiment: %v", i+1, len(vectors), err)
			continue
		}
		log.Printf("  [%d/%d] %s", i+1, len(vectors), fmtVector(v))
		result := runExperiment(cfg.Runner, exp.ID, v)
		store.SaveResult(result)
		if result.ExitCode != 0 {
			log.Printf("    EXIT %d: %s", result.ExitCode, truncate(result.Error, 80))
		} else {
			log.Printf("    → %s", fmtMetrics(result.Metrics, *objective))
		}
	}

	// Phase 2: Adaptive sampling around best/interesting regions
	for round := range cfg.Adaptive {
		results, _ := store.AllResults()
		if len(results) < 3 {
			break
		}
		log.Printf("\n=== Phase 2.%d: Adaptive sampling ===", round+1)
		as := &AdaptiveSampler{existing: allExisting}
		neighbors := as.SampleNear(cfg.Space, results, 5, *objective)
		if len(neighbors) == 0 {
			log.Println("  no new neighbors to explore")
			break
		}
		for i, v := range neighbors {
			exp := Experiment{
				Vector: v, Phase: fmt.Sprintf("adaptive-%d", round+1),
				CreatedAt: time.Now(), Manifest: makeManifest(cfg, v),
			}
			store.SaveExperiment(&exp)
			log.Printf("  [%d/%d] %s", i+1, len(neighbors), fmtVector(v))
			result := runExperiment(cfg.Runner, exp.ID, v)
			store.SaveResult(result)
			if result.ExitCode == 0 {
				log.Printf("    → %s", fmtMetrics(result.Metrics, *objective))
			}
		}
		allExisting = append(allExisting, neighbors...)
	}

	// Phase 3: Analysis + report
	log.Println("\n=== Phase 3: Analysis ===")
	results, _ := store.AllResults()
	if len(results) == 0 {
		log.Println("no successful results to analyze")
		return
	}
	log.Printf("analyzing %d results", len(results))

	knees := DetectKnees(results, cfg.Space, *objective)
	for _, k := range knees {
		log.Printf("  knee: %s=%s (score=%.3f)", k.Param, k.Value, k.Score)
	}

	importance := RankImportance(results, cfg.Space, *objective)
	for _, r := range importance {
		log.Printf("  importance: %-12s η²=%.3f", r.Param, r.Importance)
	}

	interactions := DetectInteractions(results, cfg.Space, *objective)
	for _, it := range interactions {
		log.Printf("  interaction: %s × %s = %.3f", it.ParamA, it.ParamB, it.Score)
	}

	log.Println("\n=== Generating report ===")
	if err := GenerateReport(cfg.Output, results, cfg.Space, knees, importance, interactions, *objective); err != nil {
		log.Printf("report: %v", err)
	} else {
		log.Printf("report written to %s/", cfg.Output)
	}
}

func runExperiment(runnerCmd string, expID int64, v ParamVector) *Result {
	r := NewRunner(runnerCmd, 0)
	result, err := r.Run(v)
	if err != nil {
		return &Result{
			ExperimentID: expID,
			Metrics:      MetricSet{},
			ExitCode:     -1,
			Error:        err.Error(),
			Timestamp:    time.Now(),
		}
	}
	result.ExperimentID = expID
	return result
}

func reportOnly(args []string) {
	fs := flag.NewFlagSet("report", flag.ExitOnError)
	dbPath := fs.String("db", "paramexp.db", "SQLite path")
	objective := fs.String("objective", "throughput_fps", "metric to maximize")
	configPath := fs.String("config", "", "parameter space config (for param names)")
	output := fs.String("output", "report", "output dir")
	fs.Parse(args)

	store, err := OpenStorage(*dbPath)
	if err != nil {
		log.Fatalf("storage: %v", err)
	}
	defer store.Close()

	results, _ := store.AllResults()
	if len(results) == 0 {
		log.Println("no results")
		return
	}

	var space ParamSpace
	if *configPath != "" {
		cfg, err := parseYAMLConfig(*configPath)
		if err == nil {
			space = cfg.Space
		}
	}
	if len(space.Params) == 0 {
		// Infer space from results
		paramSet := make(map[string]map[string]bool)
		for _, sr := range results {
			for k, v := range sr.Vector {
				if paramSet[k] == nil {
					paramSet[k] = make(map[string]bool)
				}
				paramSet[k][v] = true
			}
		}
		for name, vals := range paramSet {
			var sorted []string
			for v := range vals {
				sorted = append(sorted, v)
			}
			space.Params = append(space.Params, ParamDef{Name: name, Values: sorted})
		}
	}

	knees := DetectKnees(results, space, *objective)
	importance := RankImportance(results, space, *objective)
	interactions := DetectInteractions(results, space, *objective)

	if err := GenerateReport(*output, results, space, knees, importance, interactions, *objective); err != nil {
		log.Fatalf("report: %v", err)
	}
	log.Printf("report written to %s/", *output)
}

func listCmd(args []string) {
	fs := flag.NewFlagSet("list", flag.ExitOnError)
	dbPath := fs.String("db", "paramexp.db", "SQLite path")
	fs.Parse(args)

	store, err := OpenStorage(*dbPath)
	if err != nil {
		log.Fatalf("storage: %v", err)
	}
	defer store.Close()

	results, _ := store.AllResults()
	for _, sr := range results {
		metrics, _ := json.Marshal(sr.Metrics)
		fmt.Printf("[%d] %s → %s\n", sr.ExperimentID, fmtVector(sr.Vector), string(metrics))
	}
}

func makeManifest(cfg *config, v ParamVector) string {
	m := map[string]any{
		"runner":  cfg.Runner,
		"vector":  v,
	}
	b, _ := json.Marshal(m)
	return string(b)
}

func fmtMetrics(m MetricSet, highlight string) string {
	var parts []string
	for k, v := range m {
		s := fmt.Sprintf("%s=%.1f", k, v)
		if k == highlight {
			s = "*" + s + "*"
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, " ")
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func usage() {
	fmt.Fprintln(os.Stderr, `paramexp — automated parameter exploration

Usage:
  paramexp explore  --config <params.yaml> --objective <metric>
  paramexp report   --db <paramexp.db> --objective <metric>
  paramexp list     --db <paramexp.db>

Commands:
  explore    Run the full pipeline: sample → run → store → analyze → report
  report     Re-generate the report from an existing database
  list       List all stored results

The runner command receives parameters as PARAM_<NAME> env vars and must
output a JSON line of metrics on stdout. Example:
  {"throughput_fps": 420, "latency_p99_ms": 1.5, "loss_pct": 0.0}`)
}
