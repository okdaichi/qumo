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

	assert.Equal(t, status.ActiveConnections, 0, "expected activeConnections to be 0, got %d", status.ActiveConnections)
	assert.NotEqual(t, "", status.Uptime, "expected uptime to be non-empty")
}

func TestStatusHandler_ServeHTTP(t *testing.T) {
	h := newStatusHandler()

	// Test GET request
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status code 200, got %d", w.Code)
	}

	var resp map[string]any
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, true, resp["live"])
	assert.Equal(t, true, resp["ready"])
	assert.Equal(t, float64(0), resp["active_connections"])

	// Test HEAD request
	req = httptest.NewRequest(http.MethodHead, "/health", nil)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status code 200, got %d", w.Code)
	}
	if w.Body.Len() != 0 {
		t.Error("expected empty body for HEAD request")
	}

	// Test invalid method
	req = httptest.NewRequest(http.MethodPost, "/health", nil)
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

func TestStatusHandler_InvalidMethod(t *testing.T) {
	h := newStatusHandler()
	req := httptest.NewRequest(http.MethodPost, "/health", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}
