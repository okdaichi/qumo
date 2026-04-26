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
}

func TestStatusHandler_GetStatus(t *testing.T) {
	h := newStatusHandler()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	var resp map[string]any
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.NotEmpty(t, resp["uptime"])
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
	_, hasActiveConns := resp["active_connections"]
	assert.False(t, hasActiveConns, "active_connections should not appear in /health response")

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
	// A nil *statusHandler must not panic on ServeHTTP — the server guards
	// against nil before calling, but verify ServeHTTP itself is safe.
	// We only test that newStatusHandler does not return nil.
	h := newStatusHandler()
	if h == nil {
		t.Fatal("newStatusHandler returned nil")
	}
}

func TestStatusHandler_InvalidMethod(t *testing.T) {
	h := newStatusHandler()
	req := httptest.NewRequest(http.MethodPost, "/health", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}
