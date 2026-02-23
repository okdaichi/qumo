package sdn

import (
	"encoding/json"
	"net/http"
	"strings"
)

// HandlerFunc returns an http.HandlerFunc for announce resource
// operations (PUT/DELETE on /announce/<relay>/<broadcast_path>).
//
// URL format:
//
//	PUT    /announce/<relay>/<broadcast_path>  — register
//	DELETE /announce/<relay>/<broadcast_path>  — deregister
//
// The broadcast_path may contain slashes (e.g. /live/stream1),
// so the relay name is the first path segment after /announce/.
func HandlerFunc(table *announceTable) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Path: /announce/<relay>/<broadcast_path>
		rest := strings.TrimPrefix(r.URL.Path, "/announce/")
		if rest == "" || rest == r.URL.Path {
			jsonError(w, http.StatusBadRequest, "path must be /announce/<relay>/<broadcast_path>")
			return
		}

		// Split into relay name and broadcast path
		parts := strings.SplitN(rest, "/", 2)
		if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
			jsonError(w, http.StatusBadRequest, "path must be /announce/<relay>/<broadcast_path>")
			return
		}

		relayName := parts[0]
		broadcastPath := "/" + parts[1] // restore leading slash

		switch r.Method {
		case http.MethodPut:
			table.Register(relayName, broadcastPath)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]string{
				"status":         "registered",
				"relay":          relayName,
				"broadcast_path": broadcastPath,
			})

		case http.MethodDelete:
			removed := table.Deregister(relayName, broadcastPath)
			if !removed {
				jsonError(w, http.StatusNotFound, "announce entry not found")
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]string{
				"status": "deregistered",
			})

		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

// LookupHandlerFunc returns an http.HandlerFunc for announce lookups.
//
//	GET /announce/lookup?broadcast_path=X
//
// Returns all relays that have announced the specified broadcast path.
func LookupHandlerFunc(table *announceTable) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		bp := r.URL.Query().Get("broadcast_path")
		if bp == "" {
			jsonError(w, http.StatusBadRequest, "'broadcast_path' query parameter is required")
			return
		}

		entries := table.Lookup(bp)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(LookupResponse{
			BroadcastPath: bp,
			Relays:        entries,
		})
	}
}

// ListHandlerFunc returns an http.HandlerFunc that lists all announce entries.
//
//	GET /announce
func ListHandlerFunc(table *announceTable) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		all := table.AllEntries()
		if all == nil {
			all = []announceEntry{}
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]any{
			"entries": all,
			"count":   len(all),
		})
	}
}

// jsonError writes a JSON error response.
func jsonError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": message})
}
