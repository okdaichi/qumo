// Package version holds build-time version metadata injected via ldflags.
// Versions follow SemCalVer: v[major].[minor].[YYMMDD] (e.g. v1.0.260903).
//
//	go build -ldflags "-X github.com/qumo-dev/qumo/internal/version.version=v1.0.260903"
package version

import (
	"fmt"
	"runtime/debug"
)

// These variables are set at build time via -ldflags.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

// Version returns the SemCalVer tag (e.g. "v1.0.260903").
func Version() string { return version }

// Commit returns the short git commit hash.
func Commit() string { return commit }

// Date returns the build date.
func Date() string { return date }

// Full returns a human-readable multi-line version string.
func Full() string {
	s := fmt.Sprintf("qumo %s\n  commit: %s\n  built:  %s", version, commit, date)
	if info, ok := debug.ReadBuildInfo(); ok {
		s += fmt.Sprintf("\n  go:     %s", info.GoVersion)
	}
	return s
}

// Short returns "qumo <version>" for one-line output.
func Short() string {
	return fmt.Sprintf("qumo %s", version)
}
