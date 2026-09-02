package update

import (
	"fmt"
	"strconv"
	"strings"
)

// semver is a minimal semver/SemCalVer representation sufficient for comparing
// release versions produced by GoReleaser (e.g. "v1.2.3", "v0.5.0-rc.1",
// or SemCalVer "v1.0.260903").
type semver struct {
	major, minor, patch int
	pre                 string // pre-release suffix (empty for stable)
}

// parseSemver parses a version string like "v1.2.3" or "v0.5.0-rc.1".
func parseSemver(s string) (semver, error) {
	s = strings.TrimPrefix(s, "v")

	// Split off pre-release.
	var pre string
	if idx := strings.IndexByte(s, '-'); idx != -1 {
		pre = s[idx+1:]
		s = s[:idx]
	}

	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return semver{}, fmt.Errorf("invalid semver: %q", s)
	}

	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return semver{}, fmt.Errorf("invalid major: %w", err)
	}
	minor, err := strconv.Atoi(parts[1])
	if err != nil {
		return semver{}, fmt.Errorf("invalid minor: %w", err)
	}
	patch, err := strconv.Atoi(parts[2])
	if err != nil {
		return semver{}, fmt.Errorf("invalid patch: %w", err)
	}

	return semver{major: major, minor: minor, patch: patch, pre: pre}, nil
}

func (v semver) isPrerelease() bool { return v.pre != "" }

// greaterThan returns true if v is strictly greater than other.
// Pre-release versions are considered less than their corresponding
// release (e.g. 1.0.0-rc.1 < 1.0.0).
func (v semver) greaterThan(other semver) bool {
	if v.major != other.major {
		return v.major > other.major
	}
	if v.minor != other.minor {
		return v.minor > other.minor
	}
	if v.patch != other.patch {
		return v.patch > other.patch
	}
	// Same major.minor.patch — compare pre-release.
	// A release (empty pre) beats any pre-release.
	if v.pre == "" && other.pre != "" {
		return true
	}
	if v.pre != "" && other.pre == "" {
		return false
	}
	// Both have pre-release: lexicographic comparison.
	return v.pre > other.pre
}

func (v semver) String() string {
	s := fmt.Sprintf("%d.%d.%d", v.major, v.minor, v.patch)
	if v.pre != "" {
		s += "-" + v.pre
	}
	return s
}
