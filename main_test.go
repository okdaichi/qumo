package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPrintUsage_WritesHelpToStderr(t *testing.T) {
	// Capture stderr
	saved := os.Stderr
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stderr = w

	printUsage()

	w.Close()
	var buf bytes.Buffer
	_, err = buf.ReadFrom(r)
	require.NoError(t, err)
	os.Stderr = saved

	out := buf.String()
	assert.Contains(t, out, "Usage: qumo <command> [flags]")
	assert.Contains(t, out, "Commands:")
	assert.Contains(t, out, "relay")
	assert.Contains(t, out, "Flags:")
}

// Test cases that exercise main() by re-executing the test binary in a child
// process. The child path is selected with -test.run and an env var toggles
// child behavior. This avoids calling os.Exit() in the test process.

func TestRun_Unit(t *testing.T) {
	origRelay := runRelay
	origSDN := runSDN
	defer func() {
		runRelay = origRelay
		runSDN = origSDN
	}()

	tests := map[string]struct {
		args               []string
		stubRelay          func([]string) error
		stubSDN            func([]string) error
		wantCode           int
		wantStderrContains []string
	}{
		"no args": {
			args:               []string{},
			wantCode:           1,
			wantStderrContains: []string{"Usage: qumo"},
		},
		"unknown command": {
			args:               []string{"badcmd"},
			wantCode:           1,
			wantStderrContains: []string{"unknown command"},
		},
		"relay success": {
			args:      []string{"relay"},
			stubRelay: func(_ []string) error { return nil },
			wantCode:  0,
		},
		"relay error": {
			args:               []string{"relay"},
			stubRelay:          func(_ []string) error { return fmt.Errorf("boom") },
			wantCode:           1,
			wantStderrContains: []string{"error: boom"},
		},
		"relay passes args": {
			args: []string{"relay", "-config", "x"},
			stubRelay: func(a []string) error {
				assert.Equal(t, []string{"-config", "x"}, a)
				return nil
			},
			wantCode: 0,
		},
		"sdn success": {
			args:     []string{"sdn"},
			stubSDN:  func(_ []string) error { return nil },
			wantCode: 0,
		},
		"sdn error": {
			args:               []string{"sdn"},
			stubSDN:            func(_ []string) error { return fmt.Errorf("sdn-fail") },
			wantCode:           1,
			wantStderrContains: []string{"error: sdn-fail"},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if tt.stubRelay != nil {
				runRelay = tt.stubRelay
			} else {
				runRelay = func([]string) error { return nil }
			}
			if tt.stubSDN != nil {
				runSDN = tt.stubSDN
			} else {
				runSDN = func([]string) error { return nil }
			}

			// capture stderr
			saved := os.Stderr
			r, w, err := os.Pipe()
			require.NoError(t, err)
			os.Stderr = w

			code := run(tt.args)

			w.Close()
			var buf bytes.Buffer
			_, err = buf.ReadFrom(r)
			require.NoError(t, err)
			os.Stderr = saved

			out := buf.String()

			assert.Equal(t, tt.wantCode, code)
			for _, want := range tt.wantStderrContains {
				assert.Contains(t, out, want)
			}
			if tt.wantCode == 0 {
				assert.NotContains(t, out, "error:")
			}
		})
	}
}

func TestMain_Subprocess(t *testing.T) {
	tests := map[string]struct {
		args               []string // args passed to the child main (after program name)
		wantExitNonZero    bool
		wantOutputContains []string
	}{
		"no args": {
			args:               []string{},
			wantExitNonZero:    true,
			wantOutputContains: []string{"Usage: qumo"},
		},
		"unknown command": {
			args:               []string{"badcmd"},
			wantExitNonZero:    true,
			wantOutputContains: []string{"unknown command", "Usage: qumo"},
		},
		"relay missing config file": {
			// cli.RunRelay will attempt to load the provided config file and fail
			args:               []string{"relay", "-config", "does-not-exist.yaml"},
			wantExitNonZero:    true,
			wantOutputContains: []string{"failed to load config", "error:"},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			out, exitErr := runChildMain(t, tt.args...)

			if tt.wantExitNonZero {
				// Expect non-zero exit
				if exitErr == nil {
					t.Fatalf("expected child to exit non-zero, got success; output=%q", out)
				}
			} else {
				require.NoError(t, exitErr)
			}

			for _, want := range tt.wantOutputContains {
				assert.Contains(t, out, want)
			}
		})
	}
}

// runChildMain re-executes the test binary in a special child mode that calls
// main(). It returns combined stdout+stderr and any exec error.
func runChildMain(t *testing.T, args ...string) (string, error) {
	// Use the current test binary and ask it to run only the helper test.
	cmdArgs := append([]string{"-test.run=TestMain_ChildProcess", "--"}, args...)
	cmd := exec.Command(os.Args[0], cmdArgs...)
	// Signal to the child that it should execute main().
	cmd.Env = append(os.Environ(), "QUOMO_TEST_MAIN=1")
	b, err := cmd.CombinedOutput()
	return string(b), err
}

// TestMain_ChildProcess runs inside the spawned child test binary. When the
// QUOMO_TEST_MAIN env var is set the child will call main() with the
// arguments provided after "--" on the command line and then exit.
func TestMain_ChildProcess(t *testing.T) {
	if os.Getenv("QUOMO_TEST_MAIN") != "1" {
		return // not the helper child; let the test runner handle normal tests
	}

	// Find the separator `--` and use arguments after it as program args.
	sep := "--"
	var progArgs []string
	for i, a := range os.Args {
		if a == sep && i+1 < len(os.Args) {
			progArgs = os.Args[i+1:]
			break
		}
	}

	// If there was no `--`, default to no extra args (simulate os.Args length 1)
	if progArgs == nil {
		progArgs = []string{}
	}

	// Build os.Args for main() (program name + progArgs)
	os.Args = append([]string{"qumo"}, progArgs...)
	main()
	// main() should call os.Exit; if it returns, fail the child test.
	t.Fatalf("main() returned unexpectedly")
}
