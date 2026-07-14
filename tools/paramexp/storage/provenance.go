// Provenance capture for an exploration run: framework + VCS revision, machine
// info, a redacted environment snapshot, and a config hash. All gathering is
// pure Go (no CGO, no gopsutil) and best-effort.

package storage

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"time"
)

// Run is the provenance record for one paramexp invocation.
type Run struct {
	ID               int64     `json:"-"`
	StartedAt        time.Time `json:"started_at"`
	FinishedAt       time.Time `json:"finished_at,omitempty"`
	FrameworkVersion string    `json:"framework_version"`
	GitRevision      string    `json:"git_revision"`
	GitDirty         bool      `json:"git_dirty"`
	ConfigHash       string    `json:"config_hash"`
	ConfigJSON       string    `json:"config_json"`
	MachineJSON      string    `json:"machine_json"`
	EnvJSON          string    `json:"env_json"`
}

// MachineInfo describes the host. Fields are best-effort ("unknown"/0 when
// unavailable on the platform).
type MachineInfo struct {
	OS       string `json:"os"`
	Arch     string `json:"arch"`
	CPUModel string `json:"cpu_model"`
	NumCPU   int    `json:"num_cpu"`
	MemMB    int    `json:"mem_mb"`
}

// CaptureMachine gathers machine info without CGO.
func CaptureMachine() MachineInfo {
	mi := MachineInfo{
		OS:     runtime.GOOS,
		Arch:   runtime.GOARCH,
		NumCPU: runtime.NumCPU(),
	}
	mi.CPUModel, mi.MemMB = readMachineDetails()
	return mi
}

// EnvFilter controls environment-snapshot redaction.
type EnvFilter struct {
	// Deny redacts any var whose name contains one of these substrings.
	Deny []string
}

// DefaultEnvFilter redacts common secret-bearing variables.
func DefaultEnvFilter() EnvFilter {
	return EnvFilter{Deny: []string{
		"KEY", "TOKEN", "SECRET", "PASSWORD", "PASSWD", "CREDENTIAL",
		"AWS_", "GOOGLE_", "GH_", "GITHUB_", "GITLAB_", "PRIVATE",
	}}
}

// CaptureEnv returns a redacted, sorted snapshot of the process environment.
func CaptureEnv(f EnvFilter) []string {
	raw := os.Environ()
	out := make([]string, 0, len(raw))
	for _, kv := range raw {
		name, _, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		if isDenied(name, f.Deny) {
			out = append(out, name+"=<redacted>")
		} else {
			out = append(out, kv)
		}
	}
	return out
}

func isDenied(name string, deny []string) bool {
	up := strings.ToUpper(name)
	for _, d := range deny {
		if strings.Contains(up, strings.ToUpper(d)) {
			return true
		}
	}
	return false
}

// BuildInfo returns the framework (paramexp) VCS revision, dirty flag, and
// version, read from the compiled binary's build info (no `git` binary needed).
func BuildInfo() (version, revision string, dirty bool) {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown", "", false
	}
	version = bi.Main.Version
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			revision = s.Value
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	if version == "" {
		version = "(devel)"
	}
	return
}

// GitRevision returns the HEAD revision and dirty flag of a git working tree
// by shelling out to git. If git is unavailable or dir is not a repo, it
// returns ("", false, nil).
func GitRevision(dir string) (rev string, dirty bool, err error) {
	if dir == "" {
		dir = "."
	}
	rev, err = gitOutput(dir, "rev-parse", "HEAD")
	if err != nil {
		return "", false, nil // best-effort: treat as no-vcs
	}
	status, _ := gitOutput(dir, "status", "--porcelain")
	dirty = strings.TrimSpace(status) != ""
	return rev, dirty, nil
}

func gitOutput(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// Hash canonicalizes v as sorted JSON and returns its sha256 hex digest.
func Hash(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// AsJSON marshals v, returning "" on error (best-effort provenance fields).
func AsJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}

// Capture collects a full Run provenance record given a config object and a
// working-tree directory for VCS info.
func Capture(cfg any, dir string) Run {
	ver, rev, dirty := BuildInfo()
	if rev == "" {
		// Fall back to the working tree (requires -buildvcs=false builds).
		if r, d, _ := GitRevision(dir); r != "" {
			rev, dirty = r, d
		}
	}
	cfgJSON := AsJSON(cfg)
	hash, _ := Hash(cfg)
	return Run{
		StartedAt:        time.Now().UTC(),
		FrameworkVersion: ver,
		GitRevision:      rev,
		GitDirty:         dirty,
		ConfigHash:       hash,
		ConfigJSON:       cfgJSON,
		MachineJSON:      AsJSON(CaptureMachine()),
		EnvJSON:          AsJSON(CaptureEnv(DefaultEnvFilter())),
	}
}

// readMachineDetails returns (cpuModel, memMB) from platform sources. Both are
// best-effort; missing values are ("unknown", 0).
func readMachineDetails() (string, int) {
	cpu, mem := "unknown", 0
	if b, err := os.ReadFile("/proc/cpuinfo"); err == nil {
		cpu = parseProcField(string(b), "model name")
	}
	if b, err := os.ReadFile("/proc/meminfo"); err == nil {
		mem = parseMemTotal(string(b))
	}
	if cpu == "unknown" || mem == 0 {
		if c, m, ok := readSysctlMachine(); ok {
			if cpu == "unknown" {
				cpu = c
			}
			if mem == 0 {
				mem = m
			}
		}
	}
	return cpu, mem
}

func parseProcField(cpuinfo, key string) string {
	for _, line := range strings.Split(cpuinfo, "\n") {
		if strings.HasPrefix(line, key+":") {
			return strings.TrimSpace(strings.TrimPrefix(line, key+":"))
		}
	}
	return "unknown"
}

func parseMemTotal(meminfo string) int {
	for _, line := range strings.Split(meminfo, "\n") {
		if strings.HasPrefix(line, "MemTotal:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				var kb int
				for _, r := range fields[1] {
					if r < '0' || r > '9' {
						break
					}
					kb = kb*10 + int(r-'0')
				}
				if kb > 0 {
					return kb / 1024
				}
			}
			break
		}
	}
	return 0
}

// Abs returns filepath.Abs(dir), falling back to dir on error.
func Abs(dir string) string {
	if a, err := filepath.Abs(dir); err == nil {
		return a
	}
	return dir
}
