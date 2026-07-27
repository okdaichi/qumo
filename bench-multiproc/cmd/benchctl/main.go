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
// The controller replaces bench-multiproc/run-level.sh and run-sweep.sh. It
// manages relay subprocesses natively (no MSYS2/bash overhead), uses goroutine-
// based subscribers (no subprocess per subscriber), and scrapes /metrics via
// Go's net/http instead of shelling out to curl.
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
  --plist "1 2 4"    edge counts to sweep (default "1 2 4")
  --xlist "1000"     subscribers per edge to sweep (default "1000")

Examples:
  benchctl run 2 1000 --hold 30s
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
		_ = result.AppendJSONL(cfg.Results)
	}

	return 0
}

// runSweep executes a full sweep over plist and xlist.
func runSweep(args []string) int {
	// Extract --plist and --xlist BEFORE the common flag parse so they aren't
	// rejected as unknown flags by ParseFlags.
	plist := []int{1, 2, 4}
	xlist := []int{1000}
	var filtered []string
	for i := 0; i < len(args); i++ {
		if args[i] == "--plist" && i+1 < len(args) {
			plist = parseIntList(args[i+1])
			i++
		} else if args[i] == "--xlist" && i+1 < len(args) {
			xlist = parseIntList(args[i+1])
			i++
		} else if strings.HasPrefix(args[i], "--plist=") {
			plist = parseIntList(strings.TrimPrefix(args[i], "--plist="))
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
	controller.PrintScalingSummary(results)

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

// parseIntList parses a space-separated list of integers.
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
