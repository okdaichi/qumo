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
// Findings (2026-07, 16-core win/amd64; SINGLE MACHINE, dev box — absolute
// numbers are not production-portable, only the relative contention ranking is):
//
//   - Under this SYNTHETIC max-churn harness (not a production workload), routeMu
//     is the top mutex-contention source (~100% of blocked time under
//     electAndInstall; mux.mu inside TrackMux.Announce does not register). This
//     shows routeMu is the lock that WOULD serialize; it does not characterize
//     production contention levels.
//   - Serialized ceiling ≈ 1/H. In the no-subscriber, no-displacement case
//     (BenchmarkRouteInstall_Sequential) H ≈ 1.5µs → ~670k installs/sec. This is
//     a LOWER BOUND on contention: production H is higher with peer fan-out
//     (BenchmarkRouteInstall_Fanout: H ≈ 1.4µs + ~15ns/subscriber, so ~2.4µs at
//     64 subscribers → ceiling ~420k installs/sec) and with displacement-driven
//     promotes (not measured).
//
// Conclusion (CONDITIONAL): routeMu was NOT DEMONSTRATED to be a bottleneck.
// The measured ceiling (~10⁵–10⁶ installs/sec, depending on fan-out) is far
// above any install rate ASSUMED here, but that rate was not measured and must
// be validated in production (qumo_relay_route_replacements_total, announce
// arrival rate) before relying on the headroom. This is "no evidence of a
// bottleneck under the assumed workload," not "provably safe at scale."
// Tripwire: revisit if the production route-install rate approaches ~10⁵/sec
// or a production mutex profile shows routeMu as a top contender.
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
// This is the NO-SUBSCRIBER, NO-DISPLACEMENT case (empty slot, zero peer
// AnnouncementWriters), so it is a LOWER BOUND on hold time, not a production
// figure: real hold time grows with peer fan-out (see BenchmarkRouteInstall_Fanout)
// and with displacement-driven promotes.
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

// electAndInstallFanout mirrors electAndInstall and additionally performs N
// non-blocking channel sends UNDER routeMu, standing in for the per-subscriber
// fan-out that TrackMux.Announce performs for each attached AnnouncementWriter.
// gomoqt does not expose AnnouncementWriter registration to external packages
// (writers are constructed per-session via unexported newAnnouncementWriter and
// registered via the unexported serveAnnouncements), so the real fan-out is not
// reachable from this package. This proxy therefore characterizes the SCALING
// SHAPE of hold time with subscriber count (each subscriber ≈ one non-blocking
// send under the lock), not an absolute production number.
func electAndInstallFanout(s *Server, h *relayHandler, subs []chan struct{}) {
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
	for _, ch := range subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

// BenchmarkRouteInstall_Fanout measures how the routeMu critical-section hold
// time scales with peer announcement fan-out. Sub-runs at N = 0, 8, 64
// subscribers. Each subscriber channel is buffered to b.N+1 (never fills), so
// every send succeeds — modeling the common case where peer subscribers keep
// up. PROXY: see electAndInstallFanout — real AnnouncementWriters cannot be
// attached from this package, so N is a subscriber count stand-in via synthetic
// sends. Read the SLOPE across N (marginal hold cost per subscriber), not
// absolute numbers.
func BenchmarkRouteInstall_Fanout(b *testing.B) {
	for _, n := range []int{0, 8, 64} {
		b.Run(fmt.Sprintf("subscribers=%d", n), func(b *testing.B) {
			s := &Server{Config: &Config{}, TrackMux: moqt.NewTrackMux(0)}
			s.alternates = make(map[moqt.BroadcastPath]*alternate)
			subs := make([]chan struct{}, n)
			for i := range subs {
				subs[i] = make(chan struct{}, b.N+1) // never fills → send always succeeds
			}
			hs := make([]*relayHandler, b.N)
			for i := range hs {
				hs[i] = newBenchHandler(moqt.BroadcastPath(fmt.Sprintf("/fan/%d", i)))
			}

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				electAndInstallFanout(s, hs[i], subs)
			}
		})
	}
}
