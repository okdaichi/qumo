package relay

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewStatusHandler(t *testing.T) {
	h := newStatusHandler()
	if h == nil {
		t.Fatal("newStatusHandler returned nil")
	}
	if h.activeConnections.Load() != 0 {
		t.Errorf("expected activeConnections to be 0, got %d", h.activeConnections.Load())
	}
}

func TestStatusHandler_IncrementDecrementConnections(t *testing.T) {
	h := newStatusHandler()
	h.incrementConnections()
	if h.activeConnections.Load() != 1 {
		t.Errorf("expected activeConnections to be 1, got %d", h.activeConnections.Load())
	}
	h.incrementConnections()
	if h.activeConnections.Load() != 2 {
		t.Errorf("expected activeConnections to be 2, got %d", h.activeConnections.Load())
	}
	h.decrementConnections()
	if h.activeConnections.Load() != 1 {
		t.Errorf("expected activeConnections to be 1, got %d", h.activeConnections.Load())
	}
}

func TestStatusHandler_GetStatus(t *testing.T) {
	h := newStatusHandler()
	status := h.getStatus()
	if status.Status != "healthy" {
		t.Errorf("expected status to be healthy, got %s", status.Status)
	}
	if status.ActiveConnections != 0 {
		t.Errorf("expected activeConnections to be 0, got %d", status.ActiveConnections)
	}
	if status.Uptime == "" {
		t.Error("expected uptime to be set")
	}
}

func TestStatusHandler_ServeHTTP(t *testing.T) {
	h := newStatusHandler()

	// Test GET request
	req := httptest.NewRequest(http.MethodGet, "/status", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status code 200, got %d", w.Code)
	}

	var status Status
	if err := json.NewDecoder(w.Body).Decode(&status); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if status.Status != "healthy" {
		t.Errorf("expected status healthy, got %s", status.Status)
	}

	// Test HEAD request
	req = httptest.NewRequest(http.MethodHead, "/status", nil)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status code 200, got %d", w.Code)
	}
	if w.Body.Len() != 0 {
		t.Error("expected empty body for HEAD request")
	}

	// Test invalid method
	req = httptest.NewRequest(http.MethodPost, "/status", nil)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status code 405, got %d", w.Code)
	}
}

func TestStatusHandler_NilReceiver(t *testing.T) {
	var h *statusHandler

	// These should not panic
	h.incrementConnections()
	h.decrementConnections()

	status := h.getStatus()
	if status != (Status{}) {
		t.Error("expected empty status for nil receiver")
	}
}

func TestStatusHandler_ProbeLive_GETAndHEAD(t *testing.T) {
	h := newStatusHandler()

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

func TestStatusHandler_ProbeReady_Cases(t *testing.T) {
	tests := map[string]struct {
		wantCode   int
		wantReady  bool
		wantReason string
	}{
		"ready with healthy status": {
			wantCode:  http.StatusOK,
			wantReady: true,
		},
		"invalid connection state": {
			wantCode:   http.StatusServiceUnavailable,
			wantReady:  false,
			wantReason: "invalid_connection_state",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			h := newStatusHandler()
			if tt.wantReason != "" {
				h.decrementConnections()
			}
			req := httptest.NewRequest(http.MethodGet, "/health?probe=ready", nil)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			assert.Equal(t, tt.wantCode, rec.Code)

			var resp map[string]any
			err := json.NewDecoder(rec.Body).Decode(&resp)
			require.NoError(t, err)
			assert.Equal(t, tt.wantReady, resp["ready"])
			if tt.wantReason != "" {
				assert.Equal(t, tt.wantReason, resp["reason"])
			}
		})
	}
}

func TestStatusHandler_InvalidMethod(t *testing.T) {
	h := newStatusHandler()
	req := httptest.NewRequest(http.MethodPost, "/health", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}
