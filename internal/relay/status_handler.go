package relay

import (
	"encoding/json"
	"net/http"
	"sync/atomic"
	"time"
)

// Status represents the health status of the relay server
type Status struct {
	Timestamp         time.Time `json:"timestamp"`
	Uptime            string    `json:"uptime"`
	ActiveConnections int32     `json:"active_connections"`
}

// statusHandler manages health check state
type statusHandler struct {
	startTime         time.Time
	activeConnections atomic.Int32
}

// newStatusHandler creates a new health checker
func newStatusHandler() *statusHandler {
	return &statusHandler{
		startTime: time.Now(),
	}
}

// IncrementConnections increments the active connection count
func (h *statusHandler) incrementConnections() {
	if h == nil {
		return
	}
	h.activeConnections.Add(1)
}

// DecrementConnections decrements the active connection count
func (h *statusHandler) decrementConnections() {
	if h == nil {
		return
	}
	h.activeConnections.Add(-1)
}

// GetStatus returns the current health status
func (h *statusHandler) getStatus() Status {
	if h == nil {
		return Status{}
	}

	uptime := time.Since(h.startTime)
	activeConns := h.activeConnections.Load()

	return Status{
		Timestamp:         time.Now(),
		Uptime:            uptime.String(),
		ActiveConnections: activeConns,
	}
}

// ServeHTTP implements http.Handler for health check endpoint
func (h *statusHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	status := h.getStatus()
	statusCode := http.StatusOK

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	if r.Method == http.MethodHead {
		return
	}

	response := map[string]any{
		"timestamp":          status.Timestamp,
		"uptime":             status.Uptime,
		"active_connections": status.ActiveConnections,
		"live":               true,
		"ready":              true,
	}

	_ = json.NewEncoder(w).Encode(response)
}
