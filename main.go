package main

import (
	"fmt"
	"os"

	"github.com/okdaichi/qumo/internal/cli"
	"github.com/okdaichi/qumo/internal/version"
)

var (
	// overridable command handlers for easier unit-testing
	runRelay     = cli.RunRelay
	runRTMP      = cli.RunRTMP
	runBootstrap = cli.RunBootstrap
)

func main() {
	os.Exit(run(os.Args[1:]))
}

// run executes the command logic and returns an exit code (0 = success).
// Keeping this function small makes unit-testing straightforward.
func run(args []string) int {
	if len(args) < 1 {
		printUsage()
		return 1
	}

	cmd := args[0]
	cmdArgs := args[1:]

	// Handle version flags anywhere in args
	if cmd == "version" || cmd == "--version" || cmd == "-v" {
		fmt.Println(version.Full())
		return 0
	}

	var err error
	switch cmd {
	case "relay":
		err = runRelay(cmdArgs)
	case "rtmp":
		err = runRTMP(cmdArgs)
	case "bootstrap":
		err = runBootstrap(cmdArgs)
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", cmd)
		printUsage()
		return 1
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	return 0
}

func printUsage() {
	fmt.Fprintf(os.Stderr, "Usage: qumo <command> [flags]  (%s)\n", version.Short())
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Commands:")
	fmt.Fprintln(os.Stderr, "  relay      Start the MoQ relay server")
	fmt.Fprintln(os.Stderr, "  rtmp       Start the RTMP ingest server")
	fmt.Fprintln(os.Stderr, "  bootstrap  Start the bootstrap discovery server")
	fmt.Fprintln(os.Stderr, "  version    Print version information")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Flags:")
	fmt.Fprintln(os.Stderr, "  -config string   path to config file (required for relay/rtmp)")
}
