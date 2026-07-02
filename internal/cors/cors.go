// Package cors provides WebTransport origin validation (cross-site WebTransport
// forgery mitigation), shared by the relay and ingest servers.
//
// Despite the package name, WebTransport is not protected by HTTP CORS: a web
// page can open a WebTransport session to any origin, so the server must
// validate the request's Origin header itself. NewChecker returns the
// CheckOrigin callback every WebTransportHandler should set; an unset/empty
// allow list is the secure same-origin default. The CORS_ALLOWED_ORIGINS env
// var (kept for consistency with the rest of the codebase) feeds the allow list.
package cors

import (
	"net"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
)

// EnvVar names the environment variable holding the comma-separated list of
// allowed WebTransport origins. The entry "*" allows any origin; the entry
// [SameHost] allows any port on the request's own host.
const EnvVar = "CORS_ALLOWED_ORIGINS"

// SameHost is a special allow-list entry that accepts any request whose Origin
// host matches the request Host, ignoring the port. It suits deployments where
// the UI and the WebTransport server run on the same host but different ports —
// e.g. qumo playground, which derives the relay URL per-request from the
// browser's own Host, so Origin.Host and the relay Host always share a host.
// It is stricter than "*": a different host is still rejected.
const SameHost = "same-host"

// LoadAllowed reads the allowed-origin list from EnvVar. Returns nil when unset
// — the secure same-origin default (see [NewChecker]).
func LoadAllowed() []string {
	var out []string
	for o := range strings.SplitSeq(os.Getenv(EnvVar), ",") {
		if o = strings.TrimSpace(o); o != "" {
			out = append(out, o)
		}
	}
	return out
}

// NewChecker returns a WebTransport CheckOrigin callback that mitigates
// cross-site WebTransport (CSWT) forgery on session upgrades. A request is
// accepted when:
//   - it carries no Origin header (non-browser clients: SDKs, CLIs),
//   - allowed contains the wildcard "*",
//   - its Origin is listed in allowed,
//   - allowed contains [SameHost] and the Origin host equals the request Host
//     (port-insensitive), or
//   - its Origin host matches the request Host including port (same-origin).
//
// An empty allowed slice mirrors webtransport-go's default checkSameOrigin:
// only headerless and same-origin requests pass. This is the secure default;
// callers add explicit entries only for legitimate cross-origin clients
// (e.g. a separate Vite dev server or a multi-origin deployment).
func NewChecker(allowed []string) func(*http.Request) bool {
	wildcard := slices.Contains(allowed, "*")
	sameHost := slices.Contains(allowed, SameHost)
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, o := range allowed {
		allowedSet[o] = struct{}{}
	}
	return func(r *http.Request) bool {
		o := r.Header.Get("Origin")
		if o == "" {
			return true
		}
		if wildcard {
			return true
		}
		if _, ok := allowedSet[o]; ok {
			return true
		}
		u, err := url.Parse(o)
		if err != nil {
			return false
		}
		// Same-host (port-insensitive) mode, e.g. qumo playground where the UI
		// and relay share a host but differ by port.
		if sameHost && equalHost(u.Host, r.Host) {
			return true
		}
		return strings.EqualFold(u.Host, r.Host)
	}
}

// equalHost reports whether two host[:port] strings refer to the same host,
// ignoring any port (e.g. "localhost:5178" and "localhost:4433" match).
func equalHost(a, b string) bool {
	if ah, _, err := net.SplitHostPort(a); err == nil {
		a = ah
	}
	if bh, _, err := net.SplitHostPort(b); err == nil {
		b = bh
	}
	return strings.EqualFold(a, b)
}
