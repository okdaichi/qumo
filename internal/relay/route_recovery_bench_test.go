package relay

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/qumo-dev/gomoqt/moqt"
)

// newBenchHandler builds a relayHandler with a live Announcement for path, for
// benchmarking the route-election critical section. (No testing.TB; panics are
// not expected in a bench.)
func newBenchHandler(path moqt.BroadcastPath) *relayHandler {
	ann, _ := moqt.NewAnnouncement(context.Background(), path)
	hctx, hcancel := context.WithCancel(context.Background())
	return &relayHandler{
		announcement: ann,
		session:      &moqt.Session{},
		tracks:       newTrackManager(0, nil),
		nodeID:       "bench",
		ctx:          hctx,
		cancel:       hcancel,
	}
}

// electAndInstall mirrors serveSession's routeMu-held election+install critical
// section for h. Kept in lock-step with server.go; if serveSession's critical
// section changes, update this. Used only to drive a representative workload
// for contention benchmarking — it is not production code.
func electAndInstall(s *Server, h *relayHandler) {
	s.routeMu.Lock()
	defer s.routeMu.Unlock()
	if _, existing := s.TrackMux.TrackHandler(h.announcement.BroadcastPath()); existing != nil {
		if rr, ok := existing.(RouteReporter); ok {
			better, _ := isBetterRoute(h.RouteStats(), rr.RouteStats())
			if !better {
				s.retainRouteLocked(h)
				return
			}
			if dr, ok := existing.(Drainable); ok {
				dr.Drain(DrainTimeout)
			}
		}
	}
	s.installRoute(h)
}

// BenchmarkRouteElection_Parallel drives the routeMu-held election+install
// critical section concurrently across DISTINCT paths (the cross-path
// contention case a global lock would serialize). Its purpose is to expose
// routeMu contention in a mutex profile, not to be a throughput trophy: each
// iteration creates a fresh Announcement (allocation that dominates wall time
// but does NOT touch routeMu), so ns/op is alloc-bound — read the
// -mutexprofile, not the absolute numbers, to judge routeMu.
//
// Finding (2026-07, 16-core win/amd64): under max churn, routeMu IS the top
// mutex-contention source (~100% of blocked time under electAndInstall; mux.mu
// inside TrackMux.Announce does not register). Combined with
// BenchmarkRouteInstall_Sequential (hold time H ≈ 1.5µs), routeMu saturates at
// ~1/H ≈ 670k installs/sec. Realistic relay install rates (publisher
// join/move, transitive announce) are ~10³–10⁴× lower, so routeMu is NOT a
// bottleneck under representative load — keep the simple global lock; revisit
// only if a production mutex profile shows routeMu.
//
// Run: go test -run=^$ -bench=BenchmarkRouteElection_Parallel \
//          -mutexprofile=mu.out -mutexprofilefraction=1 -cpu=1,8,16 -count=5
func BenchmarkRouteElection_Parallel(b *testing.B) {
	s := &Server{Config: &Config{}, TrackMux: moqt.NewTrackMux(0)}
	s.alternates = make(map[moqt.BroadcastPath]*alternate)
	var counter atomic.Uint64

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			i := counter.Add(1)
			h := newBenchHandler(moqt.BroadcastPath(fmt.Sprintf("/bench/%d", i)))
			electAndInstall(s, h)
		}
	})
}

// BenchmarkRouteInstall_Sequential measures the routeMu critical-section cost
// per install (TrackHandler + installRoute→Announce) with the per-op
// Announcement allocation moved to untimed setup, so ns/op ≈ the hold time H.
// Combined with the parallel benchmark's saturation point, this bounds the
// install rate at which routeMu would become a bottleneck (≈ 1/H serialized).
func BenchmarkRouteInstall_Sequential(b *testing.B) {
	s := &Server{Config: &Config{}, TrackMux: moqt.NewTrackMux(0)}
	s.alternates = make(map[moqt.BroadcastPath]*alternate)
	hs := make([]*relayHandler, b.N)
	for i := range hs {
		hs[i] = newBenchHandler(moqt.BroadcastPath(fmt.Sprintf("/seq/%d", i)))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		electAndInstall(s, hs[i])
	}
}
