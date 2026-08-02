//go:build !instrument

package relay

import "net/http"

// registerStagesDebug (default build) registers a stub /debug/stages that
// reports no counters. The gomoqt ServerCounters API is present only in the
// instrumented gomoqt linked under -tags instrument; the default build links
// the published gomoqt, whose Server struct has no Counters field. The real
// handler therefore lives in debug_stages_instrument.go and this stub keeps
// the endpoint available (returning "{}") so callers see a stable shape.
func registerStagesDebug(mux *http.ServeMux, _ *Server) {
	mux.HandleFunc("/debug/stages", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("{}"))
	})
}
