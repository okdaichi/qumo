package hls

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func Test_liveness_stale(t *testing.T) {
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	const after = 10 * time.Second

	tests := map[string]struct {
		marked time.Time // zero means nothing has ever arrived
		want   bool
	}{
		"never marked":     {want: true},
		"just arrived":     {marked: now, want: false},
		"within tolerance": {marked: now.Add(-9 * time.Second), want: false},
		"on the boundary":  {marked: now.Add(-10 * time.Second), want: false},
		"past tolerance":   {marked: now.Add(-11 * time.Second), want: true},
		"long gone":        {marked: now.Add(-5 * time.Minute), want: true},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			var live liveness
			if !tt.marked.IsZero() {
				live.mark(tt.marked)
			}
			assert.Equal(t, tt.want, live.stale(now, after))
		})
	}
}

// A manifest describing media that stopped arriving is refused, because it is
// the manifest that claims a stream exists. Segments are not: they are immutable
// and addressable, so a client holding a URL is served the bytes whatever the
// feed is doing.
func Test_withLiveness_gatesManifestsOnly(t *testing.T) {
	tests := map[string]struct {
		path string
		want int
	}{
		"hls playlist":  {path: "/live/cam1/playlist.m3u8", want: http.StatusServiceUnavailable},
		"dash manifest": {path: "/live/cam1/manifest.mpd", want: http.StatusServiceUnavailable},
		"segment":       {path: "/live/cam1/e000001-g00000007.m4s", want: http.StatusOK},
		"init segment":  {path: "/live/cam1/init.m4s", want: http.StatusOK},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			var live liveness // never marked: nothing has arrived
			handler := withLiveness(
				http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusOK)
				}),
				&live, time.Second,
			)

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tt.path, nil))

			assert.Equal(t, tt.want, rec.Code)
			if tt.want == http.StatusServiceUnavailable {
				assert.Equal(t, "1", rec.Header().Get("Retry-After"),
					"a player should read this as a wait, not a failure")
			}
		})
	}
}

// Once media is arriving the manifest is served normally.
func Test_withLiveness_servesALiveFeed(t *testing.T) {
	var live liveness
	live.mark(time.Now())

	handler := withLiveness(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}),
		&live, time.Minute,
	)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/live/cam1/playlist.m3u8", nil))

	assert.Equal(t, http.StatusOK, rec.Code)
}
