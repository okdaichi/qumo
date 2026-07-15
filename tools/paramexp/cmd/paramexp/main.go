// Command paramexp explores a black-box system's parameter space: it samples,
// runs the benchmark per sample, fits a Gaussian-process surrogate of the
// response surface, and reports knees, sensitivity, interactions, and a
// response-surface/contour visualization.
//
// Usage:
//
//	paramexp explore --config params.yaml --objective throughput_fps
//	paramexp report  --db paramexp.db --config params.yaml --objective throughput_fps
//	paramexp list    --db paramexp.db
//
// The runner command receives parameters as PARAM_<NAME> env vars and must
// print a JSON object of metrics on stdout (the last JSON line wins).
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/qumo-dev/qumo/tools/paramexp/analysis"
	"github.com/qumo-dev/qumo/tools/paramexp/experiment"
	"github.com/qumo-dev/qumo/tools/paramexp/model"
	"github.com/qumo-dev/qumo/tools/paramexp/report"
	"github.com/qumo-dev/qumo/tools/paramexp/runner"
	"github.com/qumo-dev/qumo/tools/paramexp/sampler"
	"github.com/qumo-dev/qumo/tools/paramexp/storage"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}
	switch os.Args[1] {
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
	runnerCmd := fs.String("runner", "", "benchmark command (overrides config)")
	objective := fs.String("objective", "", "metric to maximize (overrides config)")
	dbPath := fs.String("db", "", "SQLite path (overrides config)")
	samples := fs.Int("samples", 0, "initial LHS sample count (overrides config)")
	adaptive := fs.Int("adaptive", 0, "adaptive rounds (overrides config)")
	output := fs.String("output", "", "report output dir (overrides config)")
	timeout := fs.Duration("timeout", 0, "per-run timeout (overrides config)")
	maxAttempts := fs.Int("max-attempts", 0, "retry attempts per run (overrides config)")
	replicates := fs.Int("replicates", 0, "runs per parameter vector (overrides config)")
	scheduler := fs.String("scheduler", "", "static (default) | bo (Bayesian optimization)")
	acquisition := fs.String("acquisition", "", "BO acquisition: ei (default) | ucb | variance")
	fs.Parse(args)

	if *configPath == "" {
		fmt.Fprintln(os.Stderr, "error: --config is required")
		os.Exit(1)
	}
	cfg, err := experiment.ParseConfig(*configPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	if *runnerCmd != "" {
		cfg.Runner = *runnerCmd
	}
	if *dbPath != "" {
		cfg.DBPath = *dbPath
	}
	if *objective != "" {
		cfg.Objective = *objective
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
	if *timeout > 0 {
		cfg.Timeout = *timeout
	}
	if *maxAttempts > 0 {
		cfg.MaxAttempts = *maxAttempts
	}
	if *replicates > 0 {
		cfg.Replicates = *replicates
	}
	if *scheduler != "" {
		cfg.Scheduler = *scheduler
	}
	if *acquisition != "" {
		cfg.BOAcquisition = *acquisition
	}

	enc, err := experiment.NewEncoder(cfg.Space)
	if err != nil {
		log.Fatalf("encoding: %v", err)
	}
	store, err := storage.Open(cfg.DBPath)
	if err != nil {
		log.Fatalf("storage: %v", err)
	}
	defer store.Close()

	run := storage.Capture(cfg, storage.Abs("."))
	if err := store.SaveRun(&run); err != nil {
		log.Fatalf("save run: %v", err)
	}
	defer store.FinishRun(run.ID)

	log.Printf("parameter space: %d params (%d discrete points), objective=%s",
		cfg.Space.Dim(), cfg.Space.Size(), cfg.Objective)
	log.Printf("sampling: %d LHS + %d adaptive rounds (%d neighbors each)",
		cfg.Samples, cfg.Adaptive, 5)
	log.Printf("runner: %s (timeout=%s, attempts=%d)", cfg.Runner, cfg.Timeout, cfg.MaxAttempts)

	execRunner := runner.NewExecRunner(cfg.Runner, cfg.Timeout, cfg.MaxAttempts)
	var sched sampler.Scheduler
	switch cfg.Scheduler {
	case "bo":
		kappa := cfg.BOKappa
		if kappa <= 0 {
			kappa = 2.0
		}
		rounds := cfg.BORounds
		if rounds <= 0 {
			rounds = 5
		}
		sched = &sampler.BayesianScheduler{
			LHSn: cfg.Samples, Rounds: rounds, Batch: cfg.BOBatch,
			Acquisition: cfg.BOAcquisition, Kappa: kappa, Xi: cfg.BOXi,
			Seed: 0x1234567, // deterministic; could be config-driven
		}
		log.Printf("scheduler: bayesian optimization (acquisition=%s, rounds=%d, batch=%d)",
			acqName(cfg.BOAcquisition), rounds, max1(cfg.BOBatch))
	default:
		sched = &sampler.StaticScheduler{LHSn: cfg.Samples, AdaptiveRounds: cfg.Adaptive, AdaptiveN: 5}
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	for {
		obs, err := store.Observations(false)
		if err != nil {
			log.Printf("load observations: %v", err)
		}
		vectors, phase, err := sched.Next(ctx, sampler.SchedulerState{Space: cfg.Space, Enc: enc, Observations: obs, Objective: cfg.Objective})
		if err == io.EOF {
			break
		}
		if err != nil {
			if err == context.Canceled {
				break
			}
			log.Fatalf("scheduler: %v", err)
		}
		log.Printf("=== phase %s: %d vectors ===", phase, len(vectors))
		for i, v := range vectors {
			if ctx.Err() != nil {
				break
			}
			x, _ := enc.Encode(v)
			exp := &experiment.Experiment{
				RunID: run.ID, Vector: v, EncodedX: x,
				Phase: phase, CreatedAt: time.Now(),
				Manifest: manifest(cfg, v),
			}
			if err := store.SaveExperiment(exp); err != nil {
				log.Printf("  [%d/%d] save: %v", i+1, len(vectors), err)
				continue
			}
			log.Printf("  [%d/%d] %s", i+1, len(vectors), v.String())
			for r := 1; r <= cfg.Replicates; r++ {
				if ctx.Err() != nil {
					break
				}
				res, err := execRunner.Run(ctx, v)
				if err != nil || res == nil {
					log.Printf("    replicate %d/%d runner error: %v", r, cfg.Replicates, err)
					continue
				}
				res.ExperimentID = exp.ID
				res.Replicate = r
				_ = store.AppendAttempt(storage.Attempt{
					ExperimentID: exp.ID, Attempt: max1(res.Attempts),
					StartedAt: res.Timestamp, DurationSec: res.Duration,
					ExitCode: res.ExitCode, TimedOut: strings.Contains(res.Error, "timeout"),
					Error: res.Error, Stdout: res.Stdout, Stderr: res.Stderr,
					Telemetry: res.Telemetry,
				})
				_ = store.SaveResult(res)
				_ = store.SaveTelemetry(exp.ID, res.Telemetry)
				if res.ExitCode != 0 {
					log.Printf("    replicate %d/%d EXIT %d: %s", r, cfg.Replicates, res.ExitCode, truncate(res.Error, 80))
				} else {
					log.Printf("    replicate %d/%d → %s", r, cfg.Replicates, fmtMetrics(res.Metrics, cfg.Objective))
				}
			}
		}
	}

	generateReport(store, cfg, enc)
}

func reportOnly(args []string) {
	fs := flag.NewFlagSet("report", flag.ExitOnError)
	dbPath := fs.String("db", "paramexp.db", "SQLite path")
	configPath := fs.String("config", "", "parameter space config (YAML)")
	objective := fs.String("objective", "throughput_fps", "metric to maximize")
	output := fs.String("output", "report_out", "output dir")
	fs.Parse(args)

	store, err := storage.Open(*dbPath)
	if err != nil {
		log.Fatalf("storage: %v", err)
	}
	defer store.Close()

	space, enc := loadSpace(*configPath, store)
	cfg := &experiment.Config{Space: space, Objective: *objective, Output: *output}
	generateReport(store, cfg, enc)
}

func listCmd(args []string) {
	fs := flag.NewFlagSet("list", flag.ExitOnError)
	dbPath := fs.String("db", "paramexp.db", "SQLite path")
	fs.Parse(args)

	store, err := storage.Open(*dbPath)
	if err != nil {
		log.Fatalf("storage: %v", err)
	}
	defer store.Close()

	obs, _ := store.Observations(true)
	for _, o := range obs {
		metrics, _ := json.Marshal(o.Metrics)
		fmt.Printf("[%d] %s → %s\n", o.ExperimentID, o.Vector.String(), string(metrics))
	}
}

// loadSpace returns the param space from config, or infers it from stored
// observations when no config is given.
func loadSpace(configPath string, store *storage.Storage) (experiment.ParamSpace, *experiment.Encoder) {
	if configPath != "" {
		if cfg, err := experiment.ParseConfig(configPath); err == nil {
			if enc, err := experiment.NewEncoder(cfg.Space); err == nil {
				return cfg.Space, enc
			}
			return cfg.Space, nil
		}
	}
	obs, _ := store.Observations(true)
	space := inferSpace(obs)
	enc, _ := experiment.NewEncoder(space)
	return space, enc
}

func inferSpace(obs []experiment.Observation) experiment.ParamSpace {
	paramSet := make(map[string]map[string]bool)
	for _, o := range obs {
		for k, v := range o.Vector {
			if paramSet[k] == nil {
				paramSet[k] = make(map[string]bool)
			}
			paramSet[k][v] = true
		}
	}
	var space experiment.ParamSpace
	for name, vals := range paramSet {
		var sorted []string
		for v := range vals {
			sorted = append(sorted, v)
		}
		sort.Strings(sorted)
		space.Params = append(space.Params, experiment.ParamDef{Name: name, Values: sorted})
	}
	_ = space.Normalize()
	return space
}

// generateReport runs analysis + GP fit and writes the report.
func generateReport(store *storage.Storage, cfg *experiment.Config, enc *experiment.Encoder) {
	log.Println("=== Analysis ===")
	obs, err := store.Observations(false)
	if err != nil {
		log.Printf("load observations: %v", err)
		return
	}
	if len(obs) == 0 {
		log.Println("no successful results to analyze")
		return
	}
	log.Printf("analyzing %d results", len(obs))

	knees := analysis.DetectKnees(obs, cfg.Space, cfg.Objective)
	for _, k := range knees {
		log.Printf("  knee: %s=%s (score=%.3f)", k.Param, k.Value, k.Score)
	}
	importance := analysis.RankImportance(obs, cfg.Space, cfg.Objective)
	for _, r := range importance {
		log.Printf("  importance: %-12s η²=%.3f", r.Param, r.Importance)
	}
	interactions := analysis.DetectInteractions(obs, cfg.Space, cfg.Objective)
	for _, it := range interactions {
		log.Printf("  interaction: %s × %s = %.3f", it.ParamA, it.ParamB, it.Score)
	}
	stability := analysis.StabilityReport(obs, cfg.Objective)
	if n := countUnstable(stability); n > 0 {
		log.Printf("  stability: %d/%d configs unstable (cv>%.2f)", n, len(stability), analysis.UnstableCV)
	}
	best, peers := analysis.IndistinguishableFromBest(obs, cfg.Objective)
	if best.ExperimentID != 0 {
		log.Printf("  best: %s = %.3g ± %.2g (n=%d); %d config(s) statistically indistinguishable",
			best.Vector, best.Mean, best.SE, best.N, len(peers))
	}

	in := report.Inputs{
		Dir: cfg.Output, Objective: cfg.Objective, Space: cfg.Space,
		Observations: obs, Knees: knees, Importance: importance, Interactions: interactions,
		Stability: stability, Best: best, Peers: peers,
	}

	// GP surrogate fit (best-effort; surfaces/sensitivity skipped on failure).
	gp := fitGP(obs, cfg.Objective)
	if gp != nil {
		hp := gp.Hyperparameters()
		sens := analysis.GPSensitivity(gp, enc.Names())
		log.Printf("  GP fit: %s", fmtLengthScales(enc.Names(), hp.LengthScales))
		in.Sensitivity = sens
		in.Surfaces = report.BuildSurfaces(gp, cfg.Space, 21)
		in.Contour = report.BuildContour(gp, cfg.Space, sens, 13)
		in.Suggestions = suggestedNext(gp, obs, enc, cfg)
		if len(in.Suggestions) > 0 {
			log.Printf("  suggested next: %d point(s) (acquisition=%s)", len(in.Suggestions), acqName(cfg.BOAcquisition))
		}
	}

	log.Println("=== Generating report ===")
	if err := report.Generate(in); err != nil {
		log.Printf("report: %v", err)
		return
	}
	log.Printf("report written to %s/", cfg.Output)
}

// fitGP fits the GP on the objective via the shared model.FitGP (heteroscedastic
// when replicates carry variance). Returns nil on too-few data or failure.
func fitGP(obs []experiment.Observation, objective string) *model.GaussianProcess {
	gp, err := model.FitGP(obs, objective, model.Options{})
	if err != nil {
		log.Printf("  GP fit skipped: %v", err)
		return nil
	}
	return gp
}

// suggestedNext returns the model's recommended next measurements under the
// configured acquisition (the brief's "what should we measure next").
func suggestedNext(gp *model.GaussianProcess, obs []experiment.Observation, enc *experiment.Encoder, cfg *experiment.Config) []analysis.Suggestion {
	best := 0.0
	first := true
	for _, o := range obs {
		if v, ok := o.Metrics[cfg.Objective]; ok && (first || v > best) {
			best, first = v, false
		}
	}
	acq := model.AcquisitionFor(cfg.BOAcquisition, best, cfg.BOXi, defaultKappa(cfg.BOKappa))
	return analysis.SuggestedNext(gp, enc, acq, 5, 0xC0FFEE)
}

func defaultKappa(k float64) float64 {
	if k <= 0 {
		return 2.0
	}
	return k
}

func acqName(s string) string {
	if s == "" {
		return "ei"
	}
	return s
}

func countUnstable(stab []analysis.Stability) int {
	n := 0
	for _, s := range stab {
		if s.Unstable {
			n++
		}
	}
	return n
}

func fmtLengthScales(names []string, lens []float64) string {
	parts := make([]string, 0, len(names))
	for i, n := range names {
		ls := 0.0
		if i < len(lens) {
			ls = lens[i]
		}
		parts = append(parts, fmt.Sprintf("%s=%.3g", n, ls))
	}
	return strings.Join(parts, " ")
}

func manifest(cfg *experiment.Config, v experiment.ParamVector) string {
	m := map[string]any{"runner": cfg.Runner, "vector": v}
	b, _ := json.Marshal(m)
	return string(b)
}

func fmtMetrics(m experiment.MetricSet, highlight string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		s := fmt.Sprintf("%s=%.1f", k, m[k])
		if k == highlight {
			s = "*" + s + "*"
		}
		parts[i] = s
	}
	return strings.Join(parts, " ")
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func max1(n int) int {
	if n < 1 {
		return 1
	}
	return n
}

func usage() {
	fmt.Fprintln(os.Stderr, `paramexp — automated parameter exploration

Usage:
  paramexp explore  --config <params.yaml> [--objective <metric>]
  paramexp report   --db <paramexp.db> [--config <params.yaml>] [--objective <metric>]
  paramexp list     --db <paramexp.db>

Commands:
  explore    Run the full pipeline: sample → run → store → fit GP → analyze → report
  report     Re-generate the report (and re-fit the GP) from an existing database
  list       List all stored observations

The runner receives parameters as PARAM_<NAME> env vars and prints a JSON
object of metrics on stdout (last JSON line wins). Example:
  {"throughput_fps": 420, "latency_p99_ms": 1.5, "loss_pct": 0.0}`)
}
