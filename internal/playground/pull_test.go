package playground

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/qumo-dev/qumo/internal/ingest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var errBoom = errors.New("boom")

// fakePullHandle is a test double for *ingest.PullHandle (the pullHandle
// interface), recording Close so the stop path can assert cleanup. A zero value
// is usable.
type fakePullHandle struct {
	sourceURL string
	path      string
	lastErr   string
	closed    bool
}

var _ pullHandle = (*fakePullHandle)(nil)

func (f *fakePullHandle) SourceURL() string { return f.sourceURL }
func (f *fakePullHandle) Path() string      { return f.path }
func (f *fakePullHandle) LastErr() string   { return f.lastErr }
func (f *fakePullHandle) Close()            { f.closed = true }
func (f *fakePullHandle) Wait()             {}

// newPullTestServer builds a Server whose pull routes are exercisable without a
// real QUIC listener or certificate. The handlers are invoked directly, so the
// mux need not wire /api/pull.
func newPullTestServer(t *testing.T) *Server {
	t.Helper()
	return NewServer("127.0.0.1:0", "4433", "deadbeef", newTestAssets())
}

func pullReq(method, body string) *http.Request {
	return httptest.NewRequest(method, "/api/pull", strings.NewReader(body))
}

func post(body string) *http.Request { return pullReq(http.MethodPost, body) }

func decodeStatus(t *testing.T, rr *httptest.ResponseRecorder) pullStatusResponse {
	t.Helper()
	var s pullStatusResponse
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&s))
	return s
}

// TestHandlePullStart_Validation covers the request-validation paths that do
// not depend on a successful pull.
func TestHandlePullStart_Validation(t *testing.T) {
	cases := map[string]struct {
		method string
		body   string
		active bool // pre-set an active pull to trigger the conflict path
		want   int
	}{
		"GET not allowed":     {method: http.MethodGet, body: "", want: http.StatusMethodNotAllowed},
		"invalid JSON":        {method: http.MethodPost, body: "{not json", want: http.StatusBadRequest},
		"missing url":         {method: http.MethodPost, body: `{"path":"/x"}`, want: http.StatusBadRequest},
		"non-rtsp scheme":     {method: http.MethodPost, body: `{"url":"http://127.0.0.1:8080/"}`, want: http.StatusBadRequest},
		"bad path":            {method: http.MethodPost, body: `{"url":"rtsp://x","path":"/a b"}`, want: http.StatusBadRequest},
		"pull already active": {method: http.MethodPost, body: `{"url":"rtsp://x"}`, active: true, want: http.StatusConflict},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			srv := newPullTestServer(t)
			if tc.active {
				srv.pullHandle = &fakePullHandle{}
			}
			rr := httptest.NewRecorder()
			srv.handlePullStart(rr, pullReq(tc.method, tc.body))
			assert.Equal(t, tc.want, rr.Code)
		})
	}
}

// TestHandlePullStart_StarterError covers the 500 path: the injected starter
// fails, the conflict guard is not tripped, and no handle is stored.
func TestHandlePullStart_StarterError(t *testing.T) {
	srv := newPullTestServer(t)
	srv.pullStarter = func(_ context.Context, _ ingest.PullConfig) (pullHandle, error) {
		return nil, errBoom
	}

	rr := httptest.NewRecorder()
	srv.handlePullStart(rr, post(`{"url":"rtsp://127.0.0.1/nonexistent"}`))

	assert.Equal(t, http.StatusInternalServerError, rr.Code)
	assert.Nil(t, srv.pullHandle, "failed start must not store a handle")
}

// TestHandlePullStart_Success covers the happy path: the starter returns a
// handle, the response reports active + the (redacted) URL + path, the handle
// is stored, and a default path is applied when omitted.
func TestHandlePullStart_Success(t *testing.T) {
	srv := newPullTestServer(t)
	var captured ingest.PullConfig
	srv.pullStarter = func(_ context.Context, cfg ingest.PullConfig) (pullHandle, error) {
		captured = cfg
		return &fakePullHandle{sourceURL: "rtsp://cam", path: cfg.BroadcastPath}, nil
	}

	rr := httptest.NewRecorder()
	srv.handlePullStart(rr, post(`{"url":"rtsp://cam"}`)) // no path → default /live/camera

	require.Equal(t, http.StatusOK, rr.Code)
	st := decodeStatus(t, rr)
	assert.True(t, st.Active)
	assert.Equal(t, "/live/camera", captured.BroadcastPath, "default path applied")
	assert.Equal(t, "rtsp://cam", st.URL)
	assert.Equal(t, "/live/camera", st.Path)

	h, ok := srv.pullHandle.(*fakePullHandle)
	require.True(t, ok, "handle stored")
	assert.False(t, h.closed, "start must not close the handle")

	// Clean up the context handlePullStart minted (the fake starter ran no
	// goroutines, but the real one would have).
	t.Cleanup(func() {
		if srv.pullCancel != nil {
			srv.pullCancel()
		}
	})
}

// TestHandlePullStop covers the stop lifecycle.
func TestHandlePullStop(t *testing.T) {
	t.Run("GET not allowed", func(t *testing.T) {
		srv := newPullTestServer(t)
		rr := httptest.NewRecorder()
		srv.handlePullStop(rr, httptest.NewRequest(http.MethodGet, "/api/pull/stop", nil))
		assert.Equal(t, http.StatusMethodNotAllowed, rr.Code)
	})

	t.Run("no active pull → 404", func(t *testing.T) {
		srv := newPullTestServer(t)
		rr := httptest.NewRecorder()
		srv.handlePullStop(rr, post(""))
		assert.Equal(t, http.StatusNotFound, rr.Code)
	})

	t.Run("active pull closes handle and clears state", func(t *testing.T) {
		srv := newPullTestServer(t)
		handle := &fakePullHandle{sourceURL: "rtsp://cam", path: "/live/camera"}
		srv.pullHandle = handle

		rr := httptest.NewRecorder()
		srv.handlePullStop(rr, post(""))

		require.Equal(t, http.StatusOK, rr.Code)
		st := decodeStatus(t, rr)
		assert.False(t, st.Active)
		assert.True(t, handle.closed, "Close called on the handle")
		assert.Nil(t, srv.pullHandle, "state cleared")
	})
}

// TestHandlePullStatus covers the status endpoint for both states.
func TestHandlePullStatus(t *testing.T) {
	t.Run("inactive", func(t *testing.T) {
		srv := newPullTestServer(t)
		rr := httptest.NewRecorder()
		srv.handlePullStatus(rr, httptest.NewRequest(http.MethodGet, "/api/pull/status", nil))
		require.Equal(t, http.StatusOK, rr.Code)
		assert.False(t, decodeStatus(t, rr).Active)
	})

	t.Run("active", func(t *testing.T) {
		srv := newPullTestServer(t)
		srv.pullHandle = &fakePullHandle{sourceURL: "rtsp://cam", path: "/live/camera", lastErr: "dial: refused"}

		rr := httptest.NewRecorder()
		srv.handlePullStatus(rr, httptest.NewRequest(http.MethodGet, "/api/pull/status", nil))
		require.Equal(t, http.StatusOK, rr.Code)

		st := decodeStatus(t, rr)
		assert.True(t, st.Active)
		assert.Equal(t, "rtsp://cam", st.URL)
		assert.Equal(t, "/live/camera", st.Path)
		assert.Equal(t, "dial: refused", st.Error)
	})
}

// TestValidPullURL covers the SSRF scheme/host guard directly. Private/LAN
// hosts are intentionally allowed (IP-camera use case); only the scheme and a
// non-empty host are enforced.
func TestValidPullURL(t *testing.T) {
	cases := map[string]struct {
		url  string
		want bool
	}{
		"rtsp":             {url: "rtsp://camera.example.com/stream", want: true},
		"rtspd (TLS)":      {url: "rtspd://camera.example.com/stream", want: true},
		"LAN host allowed": {url: "rtsp://192.168.1.100/stream", want: true},
		"host with port":   {url: "rtsp://192.168.1.100:8554/stream", want: true},
		"http rejected":    {url: "http://127.0.0.1:8080/", want: false},
		"file rejected":    {url: "file:///etc/passwd", want: false},
		"gopher rejected":  {url: "gopher://x", want: false},
		"missing host":     {url: "rtsp://", want: false},
		"empty":            {url: "", want: false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			err := validPullURL(tc.url)
			if tc.want {
				assert.NoError(t, err)
			} else {
				assert.Error(t, err)
			}
		})
	}
}

// TestValidPullPath covers the broadcast-path charset/length guard. moqt only
// requires a leading '/', so the guard exists to block log-injection (control
// chars) and routing confusion from arbitrary metacharacters.
func TestValidPullPath(t *testing.T) {
	cases := map[string]struct {
		path string
		want bool
	}{
		"default":           {path: "/live/camera", want: true},
		"segments":          {path: "/live/cam-1_2.3~4", want: true},
		"root":              {path: "/", want: true},
		"missing slash":     {path: "live/camera", want: false},
		"empty":             {path: "", want: false},
		"space":             {path: "/a b", want: false},
		"newline (log inj)": {path: "/a\nb", want: false},
		"shell meta":        {path: "/a;rm", want: false},
		"too long":          {path: "/" + strings.Repeat("a", 128), want: false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			err := validPullPath(tc.path)
			if tc.want {
				assert.NoError(t, err)
			} else {
				assert.Error(t, err)
			}
		})
	}
}
