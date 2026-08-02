//go:build instrument

package relay

import (
	"encoding/json"
	"net/http"
)

// registerStagesDebug (instrument build) exposes gomoqt's per-stage accept
// pipeline counters (ServerCounters) at /debug/stages. These counters live on
// the instrumented gomoqt Server struct and are nil-safe, returning "{}" when
// unavailable. Only compiled when linking the instrumented gomoqt
// (-tags instrument); the default build uses the stub in debug_stages_noop.go.
func registerStagesDebug(mux *http.ServeMux, srv *Server) {
	mux.HandleFunc("/debug/stages", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if srv == nil || srv.MOQServer == nil || srv.MOQServer.Counters == nil {
			_, _ = w.Write([]byte("{}"))
			return
		}
		c := srv.MOQServer.Counters
		_ = json.NewEncoder(w).Encode(map[string]any{
			"quic_accepts":        c.QUICAccepts.Load(),
			"native_sessions":     c.NativeSessions.Load(),
			"bi_stream_accepts":   c.BiStreamAccepts.Load(),
			"subscribes_received": c.SubscribesReceived.Load(),
			"subscribes_served":   c.SubscribesServed.Load(),
			"accept_errors":       c.AcceptErrors.Load(),
			"subscribe_errors":    c.SubscribeErrors.Load(),
		})
	})
}
