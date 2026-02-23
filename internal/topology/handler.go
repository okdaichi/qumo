package topology

import (
	"encoding/json"
	"net/http"
	"strings"
)

// RelayRegistrationHandler serves the relay registration API (write operations):
//
//	PUT    /relay/<name>   — register/update a relay and its neighbors
//	DELETE /relay/<name>   — remove a relay from the topology
//
// Payloads use the RelayRegistration type.
type RelayRegistrationHandler struct {
	Topology *Topology
}

// registerRequest is the JSON body for PUT /relay/<name>.
// Neighbors maps neighbor name → edge cost (0 or omitted defaults to 1).
type registerRequest struct {
	Region    string             `json:"region,omitempty"`
	Address   string             `json:"address,omitempty"` // MoQT endpoint URL
	Neighbors map[string]float64 `json:"neighbors"`
}

// NewNodeHandlerFunc returns an http.HandlerFunc for relay registration
// (PUT /relay/<name>, DELETE /relay/<name>) and delegates to the existing
// helper methods on RelayRegistrationHandler to keep logic in one place.
func NewNodeHandlerFunc(topo *Topology) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Extract relay name from path: /relay/<name>
		name := strings.TrimPrefix(r.URL.Path, "/relay/")
		if name == "" || name == r.URL.Path {
			jsonError(w, http.StatusBadRequest, "relay name is required in path: /relay/<name>")
			return
		}

		h := &RelayRegistrationHandler{Topology: topo}
		switch r.Method {
		case http.MethodPut:
			h.handlePut(w, r, name)
		case http.MethodDelete:
			h.handleDelete(w, r, name)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

func (h *RelayRegistrationHandler) handlePut(w http.ResponseWriter, r *http.Request, name string) {
	var req registerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	h.Topology.Register(RelayInfo{
		Name:      name,
		Region:    req.Region,
		Address:   req.Address,
		Neighbors: req.Neighbors,
	})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]any{
		"status": "registered",
		"relay":  name,
	})
}

func (h *RelayRegistrationHandler) handleDelete(w http.ResponseWriter, _ *http.Request, name string) {
	removed := h.Topology.Deregister(name)
	if !removed {
		jsonError(w, http.StatusNotFound, "relay not found: "+name)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{
		"status": "deregistered",
		"relay":  name,
	})
}

// RouteHandlerFunc returns an http.HandlerFunc that computes a route
// between `from` and `to` using the provided Topology.
func RouteHandlerFunc(topo *Topology) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		from := r.URL.Query().Get("from")
		to := r.URL.Query().Get("to")

		if from == "" || to == "" {
			jsonError(w, http.StatusBadRequest, "'from' and 'to' query parameters are required")
			return
		}

		result, err := topo.Route(from, to)
		if err != nil {
			jsonError(w, http.StatusNotFound, err.Error())
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(result)
	}
}

// GraphHandlerFunc returns an http.HandlerFunc that serves /graph (topology).
func GraphHandlerFunc(topo *Topology) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		g := topo.Snapshot()
		resp := g.ToResponse()

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(resp)
	}
}

// jsonError writes a JSON error response.
func jsonError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": message})
}

// RegisterHandlers registers HTTP handlers for all topology-related routes
// onto the provided ServeMux. This centralizes routing for callers like CLI.
func RegisterHandlers(mux *http.ServeMux, topo *Topology) {

}
