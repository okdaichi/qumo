package topology

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"time"
)

// SyncHandler serves the HA synchronization endpoint:
//
//	GET  /sync — export current topology snapshot for peer consumption
//	PUT  /sync — import topology snapshot from peer controller
//
// This enables Active-Standby HA: the standby periodically pulls
// the active's snapshot, or the active pushes on every mutation.
type SyncHandler struct {
	Topology *Topology
}

// ServeHTTP handles GET (export) and PUT (import) for /sync.
// Kept for backward compatibility and delegates to SyncHandlerFunc.
func (h *SyncHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	SyncHandlerFunc(h.Topology)(w, r)
}

// SyncHandlerFunc returns an http.HandlerFunc that implements the /sync
// export/import behavior (GET/PUT). Keeping a HandlerFunc simplifies
// router registration and unit testing.
func SyncHandlerFunc(topo *Topology) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			g := topo.Snapshot()
			resp := g.ToResponse()

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(resp)

		case http.MethodPut:
			var resp GraphResponse
			if err := json.NewDecoder(r.Body).Decode(&resp); err != nil {
				jsonError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
				return
			}

			g := FromResponse(resp)
			topo.Restore(g)

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]any{
				"status": "synced",
				"nodes":  len(g.Nodes),
			})

		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

// PeerSyncer periodically pulls topology from a peer controller.
// Used by standby nodes to stay in sync with the active controller.
type PeerSyncer struct {
	PeerURL  string // e.g. "http://active-controller:8090"
	Topology *Topology
	Interval time.Duration
	client   *http.Client
}

// NewPeerSyncer creates a syncer that pulls from the given peer URL.
func NewPeerSyncer(peerURL string, topology *Topology, interval time.Duration) *PeerSyncer {
	return &PeerSyncer{
		PeerURL:  peerURL,
		Topology: topology,
		Interval: interval,
		client:   &http.Client{Timeout: 5 * time.Second},
	}
}

// Run starts the periodic sync loop. Blocks until ctx is cancelled.
func (ps *PeerSyncer) Run(ctx context.Context) {
	ticker := time.NewTicker(ps.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := ps.pull(); err != nil {
				slog.Warn("peer sync failed", "peer", ps.PeerURL, "error", err)
			}
		}
	}
}

func (ps *PeerSyncer) pull() error {
	resp, err := ps.client.Get(ps.PeerURL + "/sync")
	if err != nil {
		return fmt.Errorf("GET /sync: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("peer returned status %d", resp.StatusCode)
	}

	var graphResp GraphResponse
	if err := json.NewDecoder(resp.Body).Decode(&graphResp); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	g := FromResponse(graphResp)
	ps.Topology.Restore(g)

	slog.Debug("synced topology from peer", "peer", ps.PeerURL, "nodes", len(g.Nodes))
	return nil
}

// Push sends the current topology to a peer controller.
// Can be called on-demand after mutations for faster convergence.
func (ps *PeerSyncer) Push() error {
	g := ps.Topology.Snapshot()
	resp := g.ToResponse()

	data, err := json.Marshal(resp)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}

	httpResp, err := ps.client.Do(&http.Request{
		Method: http.MethodPut,
		URL:    mustParseURL(ps.PeerURL + "/sync"),
		Body:   io.NopCloser(bytes.NewReader(data)),
		Header: http.Header{"Content-Type": []string{"application/json"}},
	})
	if err != nil {
		return fmt.Errorf("PUT /sync: %w", err)
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode != http.StatusOK {
		return fmt.Errorf("peer returned status %d", httpResp.StatusCode)
	}
	return nil
}

// mustParseURL parses a URL string, panicking on error.
// Only used with hard-coded URLs in PeerSyncer.
func mustParseURL(rawURL string) *url.URL {
	u, err := url.Parse(rawURL)
	if err != nil {
		panic("invalid URL: " + rawURL)
	}
	return u
}
