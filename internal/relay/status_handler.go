package relay

import (
	"encoding/json"
	"net/http"
	"time"
)

// statusHandler manages health check state
type statusHandler struct {
	startTime time.Time
}

// newStatusHandler creates a new health checker
func newStatusHandler() *statusHandler {
	return &statusHandler{
		startTime: time.Now(),
	}
}

// ServeHTTP implements http.Handler for health check endpoint
func (h *statusHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}

	_ = json.NewEncoder(w).Encode(map[string]any{
		"timestamp": time.Now(),
		"uptime":    time.Since(h.startTime).String(),
		"live":      true,
		"ready":     true,
	})
}
