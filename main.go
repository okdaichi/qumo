package main

import (
	"fmt"
	"os"

	"github.com/okdaichi/qumo/internal/cli"
)

var (
	// overridable command handlers for easier unit-testing
	runRelay = cli.RunRelay
	runSDN   = cli.RunSDN
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

	var err error
	switch cmd {
	case "relay":
		err = runRelay(cmdArgs)
	case "sdn":
		err = runSDN(cmdArgs)
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
	fmt.Fprintln(os.Stderr, "Usage: qumo <command> [flags]")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Commands:")
	fmt.Fprintln(os.Stderr, "  relay    Start the MoQ relay server")
	fmt.Fprintln(os.Stderr, "  sdn      Start the SDN controller")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Flags:")
	fmt.Fprintln(os.Stderr, "  -config string   path to config file")
	fmt.Fprintln(os.Stderr, "                   defaults: config.relay.yaml (relay), config.sdn.yaml (sdn)")
}
