package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/okdaichi/qumo/internal/relay"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSetupTLS tests TLS configuration setup (basic validation only)
func TestSetupTLSInvalidFiles(t *testing.T) {
	_, err := setupTLS("/nonexistent/cert.pem", "/nonexistent/key.pem")
	if err == nil {
		t.Error("Expected error for nonexistent certificate files, got nil")
	}
}

// TestSetupTLSEmptyPaths tests error handling for empty paths
func TestSetupTLSEmptyPaths(t *testing.T) {
	_, err := setupTLS("", "")
	if err == nil {
		t.Error("Expected error for empty certificate paths, got nil")
	}
}

func TestHealthHandler_ProbeLive_GETAndHEAD(t *testing.T) {
	h := &healthHandler{
		statusFunc: func() relay.Status {
			return relay.Status{Status: "healthy", ActiveConnections: 1, Timestamp: time.Now(), Uptime: "1s"}
		},
	}

	// GET
	req := httptest.NewRequest(http.MethodGet, "/health?probe=live", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)

	var resp map[string]string
	err := json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Equal(t, "alive", resp["status"])

	// HEAD should return no body
	req = httptest.NewRequest(http.MethodHead, "/health?probe=live", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, 0, rec.Body.Len())
}

func TestHealthHandler_ProbeReady_Cases(t *testing.T) {
	tests := map[string]struct {
		status     relay.Status
		wantCode   int
		wantReady  bool
		wantReason string
	}{
		"ready with healthy status": {
			status:    relay.Status{ActiveConnections: 0, Status: "healthy"},
			wantCode:  http.StatusOK,
			wantReady: true,
		},
		"invalid connection state": {
			status:     relay.Status{ActiveConnections: -1, Status: "healthy"},
			wantCode:   http.StatusServiceUnavailable,
			wantReady:  false,
			wantReason: "invalid_connection_state",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			h := &healthHandler{statusFunc: func() relay.Status { return tt.status }}
			req := httptest.NewRequest(http.MethodGet, "/health?probe=ready", nil)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			assert.Equal(t, tt.wantCode, rec.Code)

			var resp map[string]any
			err := json.NewDecoder(rec.Body).Decode(&resp)
			require.NoError(t, err)
			assert.Equal(t, tt.wantReady, resp["ready"])
			if !tt.wantReady && tt.wantReason != "" {
				assert.Equal(t, tt.wantReason, resp["reason"])
			}
		})
	}
}

func TestHealthHandler_DefaultStatusResponses(t *testing.T) {
	tests := map[string]struct {
		status   relay.Status
		wantCode int
	}{
		"unhealthy status code": {status: relay.Status{Status: "unhealthy", ActiveConnections: 0}, wantCode: http.StatusServiceUnavailable},
		"healthy status code":   {status: relay.Status{Status: "healthy", ActiveConnections: 0}, wantCode: http.StatusOK},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			h := &healthHandler{statusFunc: func() relay.Status { return tt.status }}
			req := httptest.NewRequest(http.MethodGet, "/health", nil)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			assert.Equal(t, tt.wantCode, rec.Code)

			var resp map[string]any
			err := json.NewDecoder(rec.Body).Decode(&resp)
			require.NoError(t, err)
			assert.Equal(t, tt.status.Status, resp["status"])
			assert.Contains(t, resp, "live")
			assert.Contains(t, resp, "ready")
		})
	}
}

func TestHealthHandler_InvalidMethod(t *testing.T) {
	h := &healthHandler{statusFunc: func() relay.Status {
		return relay.Status{Status: "healthy", ActiveConnections: 0}
	}}
	req := httptest.NewRequest(http.MethodPost, "/health", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}

// --- serveComponents tests ---

type mockServer struct {
	listenCalled   chan struct{}
	shutdownCalled chan struct{}
	listenErr      error
}

func newMockServer(listenErr error) *mockServer {
	return &mockServer{listenCalled: make(chan struct{}), shutdownCalled: make(chan struct{}), listenErr: listenErr}
}

func (m *mockServer) ListenAndServe() error {
	close(m.listenCalled)
	if m.listenErr != nil {
		return m.listenErr
	}
	// Block until Shutdown signals us to exit
	<-m.shutdownCalled
	return nil
}

func (m *mockServer) Shutdown(_ context.Context) error {
	// signal the listen goroutine to exit
	select {
	case <-m.shutdownCalled:
		// already closed
	default:
		close(m.shutdownCalled)
	}
	return nil
}

func TestServeComponents_ShutdownOnContextCancel(t *testing.T) {
	relayMock := newMockServer(nil)
	httpMock := newMockServer(nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Run serveComponents in background
	go func() { _ = serveComponents(ctx, relayMock, httpMock, 1*time.Second) }()

	// wait for both ListenAndServe to have been invoked
	<-relayMock.listenCalled
	<-httpMock.listenCalled

	// cancel context to trigger shutdown
	cancel()

	// verify Shutdown was called on both mocks
	select {
	case <-relayMock.shutdownCalled:
		// ok
	case <-time.After(500 * time.Millisecond):
		t.Fatal("relay shutdown was not called")
	}

	select {
	case <-httpMock.shutdownCalled:
		// ok
	case <-time.After(500 * time.Millisecond):
		t.Fatal("http shutdown was not called")
	}
}

func TestServeComponents_IgnoresImmediateListenError(t *testing.T) {
	// Relay.ListenAndServe returns an error immediately. serveComponents should
	// still wait for ctx cancellation and call Shutdown on the other server.
	relayMock := newMockServer(fmt.Errorf("listen failed"))
	httpMock := newMockServer(nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() { _ = serveComponents(ctx, relayMock, httpMock, 1*time.Second) }()

	// relayMock.listenCalled will be closed quickly even though it returned
	<-relayMock.listenCalled
	<-httpMock.listenCalled

	cancel()

	select {
	case <-httpMock.shutdownCalled:
		// ok
	case <-time.After(500 * time.Millisecond):
		t.Fatal("http shutdown was not called after context cancel")
	}
}

// panicServer simulates a server whose ListenAndServe panics.
// serveComponents should recover the panic and return an error.
type panicServer struct{}

func (p *panicServer) ListenAndServe() error          { panic("boom") }
func (p *panicServer) Shutdown(context.Context) error { return nil }

func TestServeComponents_ReturnsErrorOnPanic(t *testing.T) {
	relayPanic := &panicServer{}
	httpMock := newMockServer(nil)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- serveComponents(ctx, relayPanic, httpMock, 1*time.Second) }()

	select {
	case err := <-errCh:
		require.Error(t, err)
		assert.Contains(t, err.Error(), "panic")
	case <-time.After(500 * time.Millisecond):
		t.Fatal("serveComponents did not return after panic")
	}
}

func TestRunRelay_WildcardRequiresAdvertiseAddr(t *testing.T) {
	t.Setenv("RELAY_ADDR", "0.0.0.0:4433")
	t.Setenv("ADVERTISE_ADDR", "")

	err := RunRelay(nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ADVERTISE_ADDR is required")
}

func TestRunRelay_InvalidGroupCacheSize(t *testing.T) {
	t.Setenv("RELAY_ADDR", "localhost:4433")
	t.Setenv("ADVERTISE_ADDR", "localhost:4433")
	t.Setenv("GROUP_CACHE_SIZE", "not-a-number")

	err := RunRelay(nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "GROUP_CACHE_SIZE")
}

func TestRunRelay_InvalidBootstrapInterval(t *testing.T) {
	t.Setenv("RELAY_ADDR", "localhost:4433")
	t.Setenv("ADVERTISE_ADDR", "localhost:4433")
	t.Setenv("GROUP_CACHE_SIZE", "")
	t.Setenv("FRAME_CAPACITY", "")
	t.Setenv("BOOTSTRAP_URLS", "http://bs:8080")
	t.Setenv("BOOTSTRAP_INTERVAL", "bad-duration")

	err := RunRelay(nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "BOOTSTRAP_INTERVAL")
}
