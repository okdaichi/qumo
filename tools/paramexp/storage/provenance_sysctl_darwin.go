//go:build darwin

package storage

import "syscall"

func readSysctlMachine() (cpu string, memMB int, ok bool) {
	cpu, err := syscall.Sysctl("machdep.cpu.brand_string")
	if err == nil && cpu != "" {
		return cpu, 0, true // memory size is best-effort; read from /proc on Linux only.
	}
	return "", 0, false
}
