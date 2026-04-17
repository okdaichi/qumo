package bootstrap

import (
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
)

// registerRequest is the JSON body for POST /register.
type registerRequest struct {
	ID     string `json:"id"`
	Addr   string `json:"addr"`
	Region string `json:"region"`
}

// RegisterHandler handles POST /register.
// It extracts the remote IP from the connection and combines it with the
// port supplied by the client, so that NAT/proxy scenarios don't break.
type RegisterHandler struct {
	Store *Store
}

func (h *RegisterHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	const maxBodySize = 1 << 10 // 1 KB
	var req registerRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, maxBodySize)).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if req.ID == "" {
		http.Error(w, "id is required", http.StatusBadRequest)
		return
	}

	// Server-side IP correction: trust the server-observed IP, use
	// the client-supplied port. This prevents NAT/LB/proxy issues.
	addr := correctAddr(r, req.Addr)

	h.Store.Register(Node{
		ID:     req.ID,
		Addr:   addr,
		Region: req.Region,
	})

	w.WriteHeader(http.StatusOK)
}

// correctAddr extracts the remote IP from the request and combines it with
// the port from clientAddr. If extraction fails, clientAddr is returned as-is.
func correctAddr(r *http.Request, clientAddr string) string {
	// Prefer X-Forwarded-For if set (reverse proxy scenario).
	remoteIP := r.Header.Get("X-Forwarded-For")
	if remoteIP == "" {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			return clientAddr
		}
		remoteIP = host
	}

	_, port, err := net.SplitHostPort(clientAddr)
	if err != nil {
		return clientAddr
	}

	return net.JoinHostPort(remoteIP, port)
}

// PeersHandler handles GET /peers.
type PeersHandler struct {
	Store    *Store
	MaxPeers int
}

func (h *PeersHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	selfID := r.URL.Query().Get("self_id")
	region := r.URL.Query().Get("region")

	peers := h.Store.Peers(selfID, region, h.MaxPeers)

	slog.Debug("peers requested", "self_id", selfID, "region", region, "count", len(peers))

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(peers); err != nil {
		slog.Error("failed to encode peers response", "error", err)
	}
}
