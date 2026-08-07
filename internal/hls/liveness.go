package hls

import (
	"net/http"
	"path"
	"strings"
	"sync"
	"time"
)

// liveness records when the feed last committed a group, so the server can tell
// a stream in progress from one that has ended.
//
// The ledger keeps every group and the manifest lists the most recent window of
// them, which stays true after a publisher stops: the same playlist is served,
// just as promptly, describing media that is now minutes old. Nothing in the
// response says so, so a client that arrives late plays a dead session as
// though it were live. What separates the two is not in the stored data at all
// — it is whether anything is still arriving, which only the feed knows.
type liveness struct {
	mu   sync.Mutex
	last time.Time
}

// mark records that a group arrived.
func (l *liveness) mark(at time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.last = at
}

// stale reports whether nothing has arrived for longer than after. A feed that
// has never received a group is stale: the stream has not started.
func (l *liveness) stale(now time.Time, after time.Duration) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.last.IsZero() || now.Sub(l.last) > after
}

// withLiveness refuses to serve a manifest describing a feed that has gone
// quiet, answering 503 until media is arriving again.
//
// Only manifests are gated. A segment is immutable and addressable, so a client
// that already holds its URL is served the bytes whatever the feed is doing —
// the same reason a segment outside the manifest window stays fetchable. It is
// the manifest that claims a stream exists, and that claim is what goes stale.
func withLiveness(h http.Handler, live *liveness, after time.Duration) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isManifest(r.URL.Path) && live.stale(time.Now(), after) {
			// Retry-After tells a client this is a wait rather than a failure,
			// which is the difference between a player backing off and giving up.
			w.Header().Set("Retry-After", "1")
			http.Error(w, "hls: no media is currently arriving for this track",
				http.StatusServiceUnavailable)
			return
		}
		h.ServeHTTP(w, r)
	})
}

// isManifest reports whether a request path names a playlist or an MPD, matching
// how the stream handler routes.
func isManifest(urlPath string) bool {
	base := path.Base(urlPath)
	return strings.HasSuffix(base, ".m3u8") || strings.HasSuffix(base, ".mpd")
}
