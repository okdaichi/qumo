package relay

import (
	"encoding/json"
	"net/http"
	"sync/atomic"
	"time"
)

// Status represents the health status of the relay server
type Status struct {
	Status            string    `json:"status"` // "healthy", "unhealthy"
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

	// Determine overall status
	status := "healthy"
	if activeConns < 0 {
		status = "unhealthy" // Should never happen, but defensive
	}

	return Status{
		Status:            status,
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

	// Set status code based on health
	statusCode := http.StatusOK
	if status.Status == "unhealthy" {
		statusCode = http.StatusServiceUnavailable
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)

	if r.Method == http.MethodHead {
		return
	}

	json.NewEncoder(w).Encode(status)
}
