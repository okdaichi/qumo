//go:build !darwin

package provenance

// readSysctlMachine is a no-op on non-darwin platforms; machine details are
// read from /proc on Linux and left "unknown"/0 elsewhere.
func readSysctlMachine() (cpu string, memMB int, ok bool) {
	return "", 0, false
}
