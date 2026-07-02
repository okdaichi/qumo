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
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
)

// EnvVar names the environment variable holding the comma-separated list of
// allowed WebTransport origins. The entry "*" allows any origin.
const EnvVar = "CORS_ALLOWED_ORIGINS"

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
//   - its Origin is listed in allowed, or
//   - its Origin host matches the request Host (same-origin browser request).
//
// An empty allowed slice mirrors webtransport-go's default checkSameOrigin:
// only headerless and same-origin requests pass. This is the secure default;
// callers add explicit entries only for legitimate cross-origin clients
// (e.g. a separate Vite dev server or a multi-origin deployment).
func NewChecker(allowed []string) func(*http.Request) bool {
	wildcard := slices.Contains(allowed, "*")
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
		return strings.EqualFold(u.Host, r.Host)
	}
}
