package playground

import (
	"errors"
	"flag"
	"io"
)

// ParseFlags parses `qumo playground` CLI args, layering them onto the base
// Options (Assets, StartRelay, CertDir) supplied by main. It returns the merged
// Options ready for Run.
//
// Flags:
//
//	-ui-addr     Address the UI HTTP server binds (default 127.0.0.1:8080).
//	-relay-addr  Address the relay WebTransport server binds (default 127.0.0.1:4433).
//
// The browser-facing relay URL is not a flag: it is derived at request time
// from whatever host the UI is opened at (see Server.handleConfig).
func ParseFlags(args []string, base Options) (Options, error) {
	if base.Assets == nil {
		return Options{}, errors.New("playground: Assets is required")
	}
	if base.StartRelay == nil {
		return Options{}, errors.New("playground: StartRelay is required")
	}

	fs := flag.NewFlagSet("qumo playground", flag.ContinueOnError)
	// Discard flag's own usage output; main.printUsage owns the help text.
	fs.SetOutput(io.Discard)
	uiAddr := fs.String("ui-addr", defaultUIAddr, "address the UI HTTP server binds")
	relayAddr := fs.String("relay-addr", defaultRelayAddr, "address the relay WebTransport server binds")

	if err := fs.Parse(args); err != nil {
		return Options{}, err
	}

	if fs.NArg() > 0 {
		return Options{}, errUnknownArg(fs.Arg(0))
	}

	base.UIAddr = *uiAddr
	base.RelayAddr = *relayAddr
	return base, nil
}

// errUnknownArg wraps an unexpected positional argument so main can render it
// consistently with its other "unknown command" messages.
type unknownArgError struct{ arg string }

func (e *unknownArgError) Error() string { return "unexpected argument: " + e.arg }

func errUnknownArg(arg string) error { return &unknownArgError{arg: arg} }

// IsErrHelp reports whether err is flag's help signal (from -h/--help).
func IsErrHelp(err error) bool { return errors.Is(err, flag.ErrHelp) }

// UsageHelp writes a short playground flags summary to w.
func UsageHelp(w io.Writer) {
	const help = `Usage: qumo playground [flags]

Start a self-contained local demo: an in-process relay plus the embedded web UI.

Flags:
  --ui-addr <addr>    UI HTTP bind address (default: 127.0.0.1:8080)
  --relay-addr <addr> relay WebTransport bind address (default: 127.0.0.1:4433)

The browser learns the relay URL automatically from whatever host it opened the
UI at, so there is no --host flag.

Public hosting (behind your own TLS-terminating reverse proxy):
  qumo playground --relay-addr 0.0.0.0:4433
  # proxy https://example.com -> 127.0.0.1:8080; relay UDP/4433 reachable directly.
  # The UI must be HTTPS: WebTransport requires a secure context (localhost excepted).
  # /config returns relayUrl=https://example.com:4433 (derived from the proxy's Host).
`
	_, _ = io.WriteString(w, help)
}
