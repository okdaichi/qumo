package relay

import (
	"encoding/json"
	"net/http"
	"time"
)

// Status represents the health status of the relay server
type Status struct {
	Timestamp time.Time `json:"timestamp"`
	Uptime    string    `json:"uptime"`
}

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

// getStatus returns the current health status
func (h *statusHandler) getStatus() Status {
	if h == nil {
		return Status{}
	}
	return Status{
		Timestamp: time.Now(),
		Uptime:    time.Since(h.startTime).String(),
	}
}

// ServeHTTP implements http.Handler for health check endpoint
func (h *statusHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	status := h.getStatus()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}

	_ = json.NewEncoder(w).Encode(map[string]any{
		"timestamp": status.Timestamp,
		"uptime":    status.Uptime,
		"live":      true,
		"ready":     true,
	})
}

