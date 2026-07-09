package relay

import (
	"context"
	"testing"
	"time"

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
		tracks:       newTrackManager(0, nil),
		nodeID:       "test-node",
		ctx:          hctx,
		cancel:       hcancel,
	}
	return h, end
}

func newRecoveryServer() *Server {
	s := &Server{Config: &Config{}, TrackMux: moqt.NewTrackMux(0)}
	s.alternates = make(map[moqt.BroadcastPath]*altEntry)
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
func TestRouteRecovery_PromotesRetainedAlternate(t *testing.T) {
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

	// Incumbent publication ends -> promoteAlternates fires (asynchronously).
	endIncumbent()
	<-incumbent.announcement.Done()

	require.Eventually(t, func() bool {
		_, h := s.TrackMux.TrackHandler(alt.announcement.BroadcastPath())
		return h == alt
	}, time.Second, time.Millisecond, "retained alternate should be promoted to active route")

	assert.Equal(t, gaugeBefore, retainedGauge(), "alternate left the retained set on promotion")
	assert.Equal(t, promoBefore+1, testutil.ToFloat64(metricRelayRoutePromotions),
		"promotion counter incremented")
}

// TestRouteRecovery_RetainReplacesPrevious verifies only the latest rejected
// candidate is retained per path.
func TestRouteRecovery_RetainReplacesPrevious(t *testing.T) {
	s := newRecoveryServer()
	ctx := context.Background()
	path := "/live/recovery-replace"

	h1, _ := newRecoveryHandler(t, ctx, path)
	h2, _ := newRecoveryHandler(t, ctx, path)

	s.retainRoute(h1)
	s.retainRoute(h2)

	s.routeMu.Lock()
	got := s.alternates[h1.announcement.BroadcastPath()]
	s.routeMu.Unlock()
	assert.Same(t, h2, got.handler, "latest retained alternate wins the slot")
	assert.False(t, h1.ctx.Err() == nil, "previous alternate was cancelled")
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
	s.promoteAlternates(alt.announcement.BroadcastPath())

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
		s.promoteAlternates(moqt.BroadcastPath(path))
	})
}

// TestRouteRecovery_PromotionDoesNotClobberElectedRoute is the regression test
// for the clobber bug: displacing an incumbent (which ends its Announcement and
// fires promoteAlternates) must NOT promote a retained alternate over the route
// that just won election and displaced it.
func TestRouteRecovery_PromotionDoesNotClobberElectedRoute(t *testing.T) {
	s := newRecoveryServer()
	ctx := context.Background()
	path := "/live/recovery-no-clobber"

	incumbent, _ := newRecoveryHandler(t, ctx, path)
	s.installRoute(incumbent)

	alt, _ := newRecoveryHandler(t, ctx, path)
	s.retainRoute(alt)

	// A better route wins election and displaces the incumbent. Displacement
	// ends the incumbent's Announcement -> promoteAlternates fires async.
	winner, _ := newRecoveryHandler(t, ctx, path)
	s.installRoute(winner)

	// Displacement ends the incumbent's Announcement, which launches the
	// promoteAlternates goroutine asynchronously. Wait for it to settle: the
	// winner must remain the active route AND the alternate must be discarded
	// (promoteAlternates' clobber guard skips because the slot is live).
	require.Eventually(t, func() bool {
		_, h := s.TrackMux.TrackHandler(winner.announcement.BroadcastPath())
		if h != winner {
			return false
		}
		s.routeMu.Lock()
		_, present := s.alternates[alt.announcement.BroadcastPath()]
		s.routeMu.Unlock()
		return !present
	}, time.Second, time.Millisecond,
		"elected winner must remain active and the alternate must be discarded")
}

// TestRouteRecovery_DiscardOnSelfEnd: when a retained alternate's own
// announcement ends, it is dropped from the retained set and cancelled.
func TestRouteRecovery_DiscardOnSelfEnd(t *testing.T) {
	s := newRecoveryServer()
	ctx := context.Background()
	path := "/live/recovery-discard"
	gaugeBefore := retainedGauge()

	alt, endAlt := newRecoveryHandler(t, ctx, path)
	s.retainRoute(alt)
	require.Equal(t, gaugeBefore+1, retainedGauge(), "alternate retained")

	endAlt()
	<-alt.announcement.Done()

	require.Eventually(t, func() bool {
		s.routeMu.Lock()
		defer s.routeMu.Unlock()
		_, ok := s.alternates[alt.announcement.BroadcastPath()]
		return !ok
	}, time.Second, time.Millisecond, "alternate removed from retained set on self-end")

	assert.Equal(t, gaugeBefore, retainedGauge(), "retained gauge decremented on discard")
}
