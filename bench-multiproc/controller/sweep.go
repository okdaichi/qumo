package controller

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

// SweepConfig holds the sweep-level parameters.
type SweepConfig struct {
	PList []int // edge counts to sweep
	XList []int // subscribers-per-edge to sweep

	// Base config applied to every cell.
	Base Config

	// QumoBin path to the qumo binary.
	QumoBin string
	// CertDir for TLS certs.
	CertDir string
}

// RunSweep executes every (P, X) cell in the sweep, collecting results.
func RunSweep(ctx context.Context, sc *SweepConfig) ([]*CellResult, error) {
	start := time.Now()

	// Resolve paths.
	qumoBin := sc.QumoBin
	if qumoBin == "" {
		var err error
		qumoBin, err = findQumoBin()
		if err != nil {
			return nil, fmt.Errorf("find qumo binary: %w", err)
		}
	}
	slog.Info("using qumo binary", "path", qumoBin)

	certDir := sc.CertDir
	if certDir == "" {
		certDir = filepath.Dir(qumoBin) // bench-multiproc/ directory
	}
	slog.Info("cert directory", "dir", certDir)

	resultsDir := sc.Base.Results
	if resultsDir == "" {
		resultsDir = filepath.Join(certDir, "results")
	}
	sc.Base.Results = resultsDir

	// Create results directory.
	if err := os.MkdirAll(resultsDir, 0o750); err != nil {
		return nil, fmt.Errorf("mkdir results %q: %w", resultsDir, err)
	}

	// Backup previous results.
	resultsFile := filepath.Join(resultsDir, "results.jsonl")
	if _, err := os.Stat(resultsFile); err == nil {
		backup := filepath.Join(resultsDir, fmt.Sprintf("results.jsonl.bak.%s", time.Now().Format("20060102_150405")))
		if err := os.Rename(resultsFile, backup); err != nil {
			slog.Warn("failed to backup previous results", "err", err)
		}
		slog.Info("backed up previous results", "backup", backup)
	}

	fmt.Println()
	fmt.Println("========================================================")
	fmt.Println("  Hub+Edge relay experiment — Go benchmark controller")
	fmt.Println("========================================================")
	fmt.Printf("  Edges:   %v\n", sc.PList)
	fmt.Printf("  Subs/edge: %v\n", sc.XList)
	fmt.Printf("  Workload: %.0f fps, %d B, hold %s\n", sc.Base.GPS, sc.Base.FrameSize, sc.Base.Hold)
	fmt.Println("========================================================")
	fmt.Println()

	var allResults []*CellResult

	for _, P := range sc.PList {
		fmt.Printf("\n======== P=%d edges (hub + %d edge(s), %d total) ========\n", P, P, P+1)
		for _, X := range sc.XList {
			fmt.Printf("\n--- P=%d X=%d (total: %d subs) ---\n", P, X, P*X)

			cfg := sc.Base
			cfg.P = P
			cfg.X = X

			// Randomize ports to avoid TIME_WAIT conflicts with the previous cell.
			cfg.RandomizePorts()

			if err := cfg.Validate(); err != nil {
				slog.Error("invalid config", "err", err)
				continue
			}

			cellCtx, cellCancel := context.WithTimeout(ctx, 5*time.Minute)
			result, err := RunCell(cellCtx, &cfg, qumoBin, certDir)
			cellCancel()

			if err != nil {
				slog.Error("cell failed", "P", P, "X", X, "err", err)
				continue
			}

			// Append to results.jsonl.
			if err := result.AppendJSONL(resultsDir); err != nil {
				slog.Warn("failed to write JSONL", "err", err)
			}

			allResults = append(allResults, result)

			// Print the result line.
			recvPct := "?"
			if result.Connected > 0 {
				recvPct = fmt.Sprintf("%d%%", result.Receiving*100/result.Connected)
			}
			status := "PASS"
			if !result.Sustained {
				status = "NO(" + result.StopReasons + ")"
			}
			fmt.Printf("  P=%d X=%d total=%d conn=%d recv=%s cpu=%.2fs rss=%.0fMB sustained=%s\n",
				P, X, result.TotalSubs, result.Connected, recvPct,
				result.AggCPUS, result.PeakRSSMB, status)

			time.Sleep(3 * time.Second) // inter-cell cooldown
		}
	}

	fmt.Println()
	fmt.Println("========================================================")
	fmt.Println("  Sweep complete.")
	fmt.Printf("  Results: %s/results.jsonl\n", resultsDir)
	fmt.Printf("  Duration: %s\n", time.Since(start).Round(time.Second))
	fmt.Println("========================================================")

	return allResults, nil
}

// findQumoBin searches for the qumo binary in common locations.
func findQumoBin() (string, error) {
	// Check common locations relative to this source file or CWD.
	candidates := []string{
		"qumo",
		"qumo.exe",
		"bin/qumo",
		"bin/qumo.exe",
		"bin/qumo-linux",
		"../qumo",
		"../qumo.exe",
		"../../qumo",
		"../../qumo.exe",
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			abs, err := filepath.Abs(c)
			if err == nil {
				return abs, nil
			}
			return c, nil
		}
	}
	// Check PATH.
	if _, err := os.Stat("qumo"); err == nil {
		return "qumo", nil
	}
	return "", fmt.Errorf("qumo binary not found in search path; use --qumo flag")
}
