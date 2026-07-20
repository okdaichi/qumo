package relay

import (
	"context"
	"testing"
	"testing/synctest"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/qumo-dev/gomoqt/moqt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newRecoveryHandler builds a relayHandler backed by a real Announcement for the
// given path. The returned func ends (retracts) the announcement. The handler's
// ctx is independent of the (zero) session so tests can control its liveness.
func newRecoveryHandler(t *testing.T, ctx context.Context, path string) (*relayHandler, func()) {
	t.Helper()
	ann, end := moqt.NewAnnouncement(ctx, moqt.BroadcastPath(path))
	hctx, hcancel := context.WithCancel(ctx)
	h := &relayHandler{
		announcement: ann,
		session:      &moqt.Session{},
		tracks:       newTrackManager(0, nil, nil),
		nodeID:       "test-node",
		ctx:          hctx,
		cancel:       hcancel,
	}
	return h, end
}

func newRecoveryServer() *Server {
	s := &Server{Config: &Config{}, TrackMux: moqt.NewTrackMux(0)}
	s.alternates = make(map[moqt.BroadcastPath]*alternate)
	return s
}

// retainedGauge reads the process-global retained-routes gauge. Tests assert
// deltas against a per-test baseline because the gauge has no labels and is
// shared across the whole test binary.
func retainedGauge() float64 {
	return testutil.ToFloat64(metricRelayRoutesRetained)
}

// TestRouteRecovery_PromotesRetainedAlternate is the core recovery scenario: a
// route that lost election is retained; when the incumbent's announcement ends,
// the retained route is promoted so the path is not stranded.
//
// Promotion is asynchronous (Announcement.end() runs AfterFuncs inline, and the
// recovery callback spawns promoteAlternate in a goroutine), so the test runs
// inside a synctest bubble and waits deterministically rather than polling.
func TestRouteRecovery_PromotesRetainedAlternate(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := newRecoveryServer()
		ctx := context.Background()
		path := "/live/recovery-promote"
		gaugeBefore := retainedGauge()
		promoBefore := testutil.ToFloat64(metricRelayRoutePromotions)

		incumbent, endIncumbent := newRecoveryHandler(t, ctx, path)
		s.installRoute(incumbent)
		ann, _ := s.TrackMux.TrackHandler(incumbent.announcement.BroadcastPath())
		require.NotNil(t, ann, "incumbent must be installed")

		alt, _ := newRecoveryHandler(t, ctx, path)
		s.retainRoute(alt)
		assert.Equal(t, gaugeBefore+1, retainedGauge(), "alternate retained")

		// Incumbent publication ends -> the recovery AfterFunc spawns
		// promoteAlternate; synctest.Wait waits for it to finish.
		endIncumbent()
		synctest.Wait()

		_, h := s.TrackMux.TrackHandler(alt.announcement.BroadcastPath())
		assert.Same(t, alt, h, "retained alternate promoted to active route")
		assert.Equal(t, gaugeBefore, retainedGauge(), "alternate left the retained set on promotion")
		assert.Equal(t, promoBefore+1, testutil.ToFloat64(metricRelayRoutePromotions),
			"promotion counter incremented")
	})
}

// TestRouteRecovery_RetainsBestAlternate verifies that retention keeps the
// BEST alternate per path (by isBetterRoute), not merely the latest rejected
// candidate — so promotion can never install a strictly worse route than one
// the relay had already accepted and then discarded.
func TestRouteRecovery_RetainsBestAlternate(t *testing.T) {
	t.Run("tie keeps the previous alternate", func(t *testing.T) {
		// Two handlers with identical RouteStats (same Hops/bitrate/RTT) —
		// isBetterRoute returns false on a tie, so the previous is kept.
		s := newRecoveryServer()
		ctx := context.Background()
		path := "/live/recovery-tie"

		h1, _ := newRecoveryHandler(t, ctx, path)
		h2, _ := newRecoveryHandler(t, ctx, path)

		s.retainRoute(h1)
		s.retainRoute(h2)

		s.routeMu.Lock()
		got := s.alternates[h1.announcement.BroadcastPath()]
		s.routeMu.Unlock()
		assert.Same(t, h1, got.handler, "on a tie the previous alternate is kept")
		assert.False(t, h2.ctx.Err() == nil, "the new (not-better) alternate was cancelled")
	})

	t.Run("a live alternate replaces a dead one", func(t *testing.T) {
		// isBetterRoute(live, dead) is true, so a live alternate displaces a
		// dead retained one.
		s := newRecoveryServer()
		ctx := context.Background()
		path := "/live/recovery-live-replaces-dead"

		dead, _ := newRecoveryHandler(t, ctx, path)
		s.retainRoute(dead)
		dead.cancel() // kill the retained alternate's ctx (announcement still active)

		live, _ := newRecoveryHandler(t, ctx, path)
		s.retainRoute(live)

		s.routeMu.Lock()
		got := s.alternates[live.announcement.BroadcastPath()]
		s.routeMu.Unlock()
		assert.Same(t, live, got.handler, "live alternate displaces the dead retained one")
	})
}

// TestRouteRecovery_DeadAlternateNotPromoted: an alternate whose ctx is dead is
// not installed when promotion fires; it is released instead.
func TestRouteRecovery_DeadAlternateNotPromoted(t *testing.T) {
	s := newRecoveryServer()
	ctx := context.Background()
	path := "/live/recovery-dead"
	gaugeBefore := retainedGauge()

	alt, _ := newRecoveryHandler(t, ctx, path)
	s.retainRoute(alt)
	require.Equal(t, gaugeBefore+1, retainedGauge(), "alternate retained")

	alt.cancel() // kill the alternate's ctx but keep its announcement active
	s.promoteAlternate(alt.announcement.BroadcastPath())

	ann, _ := s.TrackMux.TrackHandler(alt.announcement.BroadcastPath())
	assert.Nil(t, ann, "dead alternate must not be installed as the active route")
	assert.Equal(t, gaugeBefore, retainedGauge(), "dead alternate released from the retained set")

	s.routeMu.Lock()
	_, present := s.alternates[alt.announcement.BroadcastPath()]
	s.routeMu.Unlock()
	assert.False(t, present, "dead alternate removed from the alternates map")
}

// TestRouteRecovery_PromoteNoAlternate: promoting a path with no retained
// alternate is a no-op (no panic, no installation).
func TestRouteRecovery_PromoteNoAlternate(t *testing.T) {
	s := newRecoveryServer()
	path := "/live/recovery-empty"

	require.NotPanics(t, func() {
		s.promoteAlternate(moqt.BroadcastPath(path))
	})
}

// TestRouteRecovery_PromotionDoesNotClobberElectedRoute is the regression test
// for the clobber bug: displacing an incumbent (which ends its Announcement and
// spawns promoteAlternate) must NOT promote a retained alternate over the route
// that just won election and displaced it.
func TestRouteRecovery_PromotionDoesNotClobberElectedRoute(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := newRecoveryServer()
		ctx := context.Background()
		path := "/live/recovery-no-clobber"

		incumbent, _ := newRecoveryHandler(t, ctx, path)
		s.installRoute(incumbent)

		alt, _ := newRecoveryHandler(t, ctx, path)
		s.retainRoute(alt)

		// A better route wins election and displaces the incumbent. Displacement
		// ends the incumbent's Announcement -> the recovery AfterFunc spawns
		// promoteAlternate. synctest.Wait lets it run; the clobber guard sees
		// the winner is live and discards the alternate instead of promoting it.
		winner, _ := newRecoveryHandler(t, ctx, path)
		s.installRoute(winner)
		synctest.Wait()

		_, h := s.TrackMux.TrackHandler(winner.announcement.BroadcastPath())
		assert.Same(t, winner, h, "elected winner must remain the active route")

		s.routeMu.Lock()
		_, present := s.alternates[alt.announcement.BroadcastPath()]
		s.routeMu.Unlock()
		assert.False(t, present, "alternate discarded, not promoted over the elected route")
	})
}

// TestRouteRecovery_DiscardOnSelfEnd: when a retained alternate's own
// announcement ends, it is dropped from the retained set and cancelled.
//
// The discardAlternate AfterFunc runs inline from Announcement.end() (the
// alternate has a single callback), so the update is synchronous — no synctest.
func TestRouteRecovery_DiscardOnSelfEnd(t *testing.T) {
	s := newRecoveryServer()
	ctx := context.Background()
	path := "/live/recovery-discard"
	gaugeBefore := retainedGauge()

	alt, endAlt := newRecoveryHandler(t, ctx, path)
	s.retainRoute(alt)
	require.Equal(t, gaugeBefore+1, retainedGauge(), "alternate retained")

	endAlt() // fires discardAlternate inline

	s.routeMu.Lock()
	_, present := s.alternates[alt.announcement.BroadcastPath()]
	s.routeMu.Unlock()
	assert.False(t, present, "alternate removed from retained set on self-end")
	assert.Equal(t, gaugeBefore, retainedGauge(), "retained gauge decremented on discard")
}
