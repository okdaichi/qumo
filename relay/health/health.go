package health

import (
	"encoding/json"
	"net/http"
	"sync/atomic"
	"time"
)

const DefaultAddress = ":8080"

// Status represents the health status of the relay server
type Status struct {
	Status            string    `json:"status"` // "healthy", "degraded", "unhealthy"
	Timestamp         time.Time `json:"timestamp"`
	Uptime            string    `json:"uptime"`
	ActiveConnections int32     `json:"active_connections"`
	UpstreamConnected bool      `json:"upstream_connected"`
	Version           string    `json:"version,omitempty"`
}

// StatusHandler manages health check state
type StatusHandler struct {
	startTime         time.Time
	activeConnections atomic.Int32
	upstreamConnected atomic.Bool
	upstreamRequired  bool // Whether upstream connection is required for readiness
	version           string
}

// NewStatusHandler creates a new health checker
func NewStatusHandler() *StatusHandler {
	return &StatusHandler{
		startTime:        time.Now(),
		version:          "v0.1.0",
		upstreamRequired: false, // Default: upstream not required
	}
}

// SetUpstreamRequired sets whether upstream connection is required for readiness
func (h *StatusHandler) SetUpstreamRequired(required bool) {
	h.upstreamRequired = required
}

// IncrementConnections increments the active connection count
func (h *StatusHandler) IncrementConnections() {
	if h == nil {
		return
	}
	h.activeConnections.Add(1)
}

// DecrementConnections decrements the active connection count
func (h *StatusHandler) DecrementConnections() {
	if h == nil {
		return
	}
	h.activeConnections.Add(-1)
}

// SetUpstreamConnected sets the upstream connection status
func (h *StatusHandler) SetUpstreamConnected(connected bool) {
	if h == nil {
		return
	}
	h.upstreamConnected.Store(connected)
}

// GetStatus returns the current health status
func (h *StatusHandler) GetStatus() Status {
	uptime := time.Since(h.startTime)
	activeConns := h.activeConnections.Load()
	upstreamConn := h.upstreamConnected.Load()

	// Determine overall status
	status := "healthy"

	// Check for unhealthy conditions
	if activeConns < 0 {
		status = "unhealthy" // Should never happen, but defensive
	} else if h.upstreamRequired && !upstreamConn {
		// If upstream is required but not connected, mark as degraded
		status = "degraded"
	}

	return Status{
		Status:            status,
		Timestamp:         time.Now(),
		Uptime:            uptime.String(),
		ActiveConnections: activeConns,
		UpstreamConnected: upstreamConn,
		Version:           h.version,
	}
}

// ServeHTTP implements http.Handler for health check endpoint
func (h *StatusHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	status := h.GetStatus()

	// Set status code based on health
	statusCode := http.StatusOK
	switch status.Status {
	case "unhealthy":
		statusCode = http.StatusServiceUnavailable
	case "degraded":
		statusCode = http.StatusOK // Still operational, just degraded
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)

	if r.Method == http.MethodHead {
		return
	}

	json.NewEncoder(w).Encode(status)
}

// ServeLive implements http.Handler for liveness probe endpoint
// This should always return 200 OK unless the process is completely stuck
func (h *StatusHandler) ServeLive(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	// Liveness: just check if we can respond
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	if r.Method == http.MethodHead {
		return
	}

	json.NewEncoder(w).Encode(map[string]string{
		"status": "alive",
	})
}

// ServeReady implements http.Handler for readiness probe endpoint
// Returns 200 OK only if the service is ready to accept traffic
func (h *StatusHandler) ServeReady(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	activeConns := h.activeConnections.Load()
	upstreamConn := h.upstreamConnected.Load()

	// Readiness checks
	ready := true
	reason := "ready"

	// Check if connections are in valid state
	if activeConns < 0 {
		ready = false
		reason = "invalid_connection_state"
	}

	// Check upstream if required
	if h.upstreamRequired && !upstreamConn {
		ready = false
		reason = "upstream_not_connected"
	}

	statusCode := http.StatusOK
	if !ready {
		statusCode = http.StatusServiceUnavailable
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)

	if r.Method == http.MethodHead {
		return
	}

	response := map[string]interface{}{
		"ready": ready,
	}
	if !ready {
		response["reason"] = reason
	}

	json.NewEncoder(w).Encode(response)
}

func RegisterHandlers(mux *http.ServeMux) {
	statusHandler := NewStatusHandler()
	mux.Handle("/health", statusHandler)
	mux.Handle("/healthz", statusHandler) // Kubernetes convention
	mux.HandleFunc("/ready", readinessHandler)
}

// readinessHandler handles readiness probe (always ready if serving)
func readinessHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ready"))
}
