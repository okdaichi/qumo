// Package envconfig resolves configuration values from the process environment.
//
// Every qumo command (relay, ingest, the HLS egress, seed-moq) reads its
// settings from environment variables with a default, and that "return the env
// value, or a default when it is unset or empty" read is the same in each — so it
// lives here once rather than copy-pasted per command.
package envconfig

import "os"

// String returns the value of the environment variable key, or def when key is
// unset or empty. os.Getenv cannot distinguish a missing variable from one set
// to the empty string, so an explicitly empty value is treated as unset and the
// default is returned.
func String(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
