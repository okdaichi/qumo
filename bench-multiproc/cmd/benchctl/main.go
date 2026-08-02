// Command benchctl is the Go benchmark controller for multi-relay scaling experiments.
//
// Usage:
//
//	benchctl run <P> <X> [flags]       — Run one (P, X) cell
//	benchctl sweep [flags]             — Run a sweep over plist and xlist
//	benchctl sweep --plist "1 2 4" --xlist "1000" [flags]
//
// Environment:
//
//	BENCH_QUMO_BIN  — path to qumo binary (overridden by --qumo flag)
//
// DESIGN PRINCIPLE: The system under test (relay/server) always runs in a
// separate OS process from the load generator (client). Subscribers are
// launched as subprocesses via "qumo loadgen subscribe" — never as goroutines
// inside the controller process. This ensures the benchmark measures the relay
// alone, not the combined Go runtime behaviour of client + server.
// See bench-multiproc/DESIGN_PRINCIPLES.md for the full rationale.
//
// Required: qumo binary (build with: go build -o bench-multiproc/bin/qumo .)
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/qumo-dev/qumo/bench-multiproc/controller"
)

func main() {
	os.Exit(run())
}

func run() int {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})))

	if len(os.Args) < 2 {
		usage()
		return 1
	}

	cmd := os.Args[1]
	args := os.Args[2:]

	switch cmd {
	case "run":
		return runCell(args)
	case "sweep":
		return runSweep(args)
	case "calibrate":
		return runCalibrate(args)
	case "-h", "--help", "help":
		usage()
		return 0
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", cmd)
		usage()
		return 1
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `Usage: benchctl <command> [flags]

Commands:
  run <P> <X>    Run one (P, X) cell of the hub+edge experiment
  sweep          Run a sweep over P values and X values
  calibrate      Run P=1 calibration to find per-edge capacity baseline

Flags (shared):
  --gps <f>          groups per second (default 30)
  --size <n>         frame size in bytes (default 1200)
  --hold <d>         subscriber hold duration (default 30s)
  --qumo <path>      path to qumo binary (auto-detect)
  --cert-dir <dir>   directory for TLS certs (auto-detect)
  --results <dir>    output directory (default bench-multiproc/results/)
  --hub-port <n>     hub listen port (default 4433)
  --edge-base <n>    first edge listen port (default 4434)
  --pin              pin relays to dedicated cores (default true; use --pin=false to disable)
  --latency-probe    run e2e latency probe after measurement

Sweep flags:
  --plist "1 2 4"      edge counts to sweep (default "1 2 4")
  --xlist "1000"       subscribers per edge to sweep (default "1000")
  --ref-max-p1 <N>     calibrated Max(P=1) for scaling efficiency (auto if 0)

Examples:
  benchctl run 2 1000 --hold 30s
  benchctl calibrate --xlist "500 750 1000 1500"
  benchctl sweep --plist "1 2 3 4" --xlist "1000" --hold 30s --gps 30
`)
}

// runCell executes one (P, X) cell.
func runCell(args []string) int {
	if len(args) < 2 {
		fmt.Fprintf(os.Stderr, "usage: benchctl run <P> <X> [flags]\n")
		return 1
	}

	P, err := strconv.Atoi(args[0])
	if err != nil || P < 1 {
		fmt.Fprintf(os.Stderr, "invalid P: %q (must be integer >= 1)\n", args[0])
		return 1
	}
	X, err := strconv.Atoi(args[1])
	if err != nil || X < 1 {
		fmt.Fprintf(os.Stderr, "invalid X: %q (must be integer >= 1)\n", args[1])
		return 1
	}

	cfg := controller.DefaultConfig()
	rest, err := cfg.ParseFlags(args[2:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "flag error: %v\n", err)
		return 1
	}
	if len(rest) > 0 {
		fmt.Fprintf(os.Stderr, "unexpected arguments: %v\n", rest)
		return 1
	}
	cfg.P = P
	cfg.X = X

	if err := cfg.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		return 1
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	qumoBin := resolveQumoBin(cfg.QumoBin)
	certDir := resolveCertDir(cfg.CertDir)

	result, err := controller.RunCell(ctx, &cfg, qumoBin, certDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cell failed: %v\n", err)
		return 1
	}

	jsonStr, _ := result.ToJSON()
	fmt.Println(jsonStr)

	if cfg.Results != "" {
		_ = result.AppendJSONL(cfg.Results) // not actionable: non-critical metadata persist
	}

	return 0
}

// runSweep executes a full sweep over plist and xlist.
func runSweep(args []string) int {
	// Extract --plist, --xlist, and --ref-max-p1 BEFORE the common flag parse.
	plist := []int{1, 2, 4}
	xlist := []int{1000}
	refMaxP1 := 0 // 0 = auto-detect from P=1 cells in the sweep
	var filtered []string
	for i := 0; i < len(args); i++ {
		if args[i] == "--plist" && i+1 < len(args) {
			plist = parseIntList(args[i+1])
			i++
		} else if args[i] == "--xlist" && i+1 < len(args) {
			xlist = parseIntList(args[i+1])
			i++
		} else if args[i] == "--ref-max-p1" && i+1 < len(args) {
			refMaxP1, _ = strconv.Atoi(args[i+1])
			i++
		} else if strings.HasPrefix(args[i], "--plist=") {
			plist = parseIntList(strings.TrimPrefix(args[i], "--plist="))
		} else if strings.HasPrefix(args[i], "--xlist=") {
			xlist = parseIntList(strings.TrimPrefix(args[i], "--xlist="))
		} else if strings.HasPrefix(args[i], "--ref-max-p1=") {
			refMaxP1, _ = strconv.Atoi(strings.TrimPrefix(args[i], "--ref-max-p1="))
		} else {
			filtered = append(filtered, args[i])
		}
	}

	cfg := controller.DefaultConfig()
	rest, err := cfg.ParseFlags(filtered)
	if err != nil {
		fmt.Fprintf(os.Stderr, "flag error: %v\n", err)
		return 1
	}
	if len(rest) > 0 {
		fmt.Fprintf(os.Stderr, "unexpected arguments: %v\n", rest)
		return 1
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	qumoBin := resolveQumoBin(cfg.QumoBin)
	certDir := resolveCertDir(cfg.CertDir)

	sc := &controller.SweepConfig{
		PList:   plist,
		XList:   xlist,
		Base:    cfg,
		QumoBin: qumoBin,
		CertDir: certDir,
	}

	results, err := controller.RunSweep(ctx, sc)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sweep failed: %v\n", err)
		return 1
	}

	// Print summary tables.
	controller.PrintTable(results)
	controller.PrintEdgeDistribution(results)
	controller.PrintScalingSummary(results, refMaxP1)

	return 0
}

// resolveQumoBin resolves the qumo binary path.
func resolveQumoBin(hint string) string {
	if hint != "" {
		return hint
	}
	if env := os.Getenv("BENCH_QUMO_BIN"); env != "" {
		return env
	}
	return "qumo"
}

// resolveCertDir resolves the cert directory.
func resolveCertDir(hint string) string {
	if hint != "" {
		return hint
	}
	if env := os.Getenv("BENCH_CERT_DIR"); env != "" {
		return env
	}
	return "."
}

// runCalibrate runs a P=1 calibration sweep to find the per-edge capacity
// baseline (Max(P=1)). It runs P=1 for each X in xlist and prints the best
// sustainable connected subscribers as the calibrated capacity.
func runCalibrate(args []string) int {
	// Extract --xlist before the common flag parse.
	xlist := []int{500, 750, 1000, 1500}
	var filtered []string
	for i := 0; i < len(args); i++ {
		if args[i] == "--xlist" && i+1 < len(args) {
			xlist = parseIntList(args[i+1])
			i++
		} else if strings.HasPrefix(args[i], "--xlist=") {
			xlist = parseIntList(strings.TrimPrefix(args[i], "--xlist="))
		} else {
			filtered = append(filtered, args[i])
		}
	}

	cfg := controller.DefaultConfig()
	rest, err := cfg.ParseFlags(filtered)
	if err != nil {
		fmt.Fprintf(os.Stderr, "flag error: %v\n", err)
		return 1
	}
	if len(rest) > 0 {
		fmt.Fprintf(os.Stderr, "unexpected arguments: %v\n", rest)
		return 1
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	qumoBin := resolveQumoBin(cfg.QumoBin)
	certDir := resolveCertDir(cfg.CertDir)

	sc := &controller.SweepConfig{
		PList:   []int{1},
		XList:   xlist,
		Base:    cfg,
		QumoBin: qumoBin,
		CertDir: certDir,
	}

	results, err := controller.RunSweep(ctx, sc)
	if err != nil {
		fmt.Fprintf(os.Stderr, "calibration sweep failed: %v\n", err)
		return 1
	}

	// Find Max(P=1): the best connected count among sustained P=1 cells.
	maxP1 := 0
	maxP1X := 0
	for _, r := range results {
		if r.Sustained && r.Connected > maxP1 {
			maxP1 = r.Connected
			maxP1X = r.X
		}
	}

	fmt.Println()
	fmt.Println("============================================================")
	fmt.Println("  Calibration Result: Max(P=1)")
	fmt.Println("============================================================")
	fmt.Printf("  Max sustainable subscribers per edge: %d (at X=%d)\n", maxP1, maxP1X)
	fmt.Printf("  Use with: benchctl sweep --ref-max-p1 %d ...\n", maxP1)
	fmt.Printf("  Expected aggregate = P × %d for subsequent runs\n", maxP1)
	fmt.Println()

	return 0
}

// parseIntList parses a space-separated list of positive integers.
func parseIntList(s string) []int {
	parts := strings.Fields(s)
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		n, err := strconv.Atoi(p)
		if err == nil && n > 0 {
			out = append(out, n)
		}
	}
	return out
}
