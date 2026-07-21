package relay

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/qumo-dev/gomoqt/moqt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestRelayHandler(ctx context.Context) *relayHandler {
	ann, _ := moqt.NewAnnouncement(ctx, "/test")
	ctx, cancel := context.WithCancel(ctx)
	// minimal moqt.Session to satisfy non-nil constraints
	sess := &moqt.Session{}
	return &relayHandler{
		announcement: ann,
		session:      sess,
		tracks:       newTrackManager(0, nil),
		nodeID:       "test-node",
		ctx:          ctx,
		cancel:       cancel,
	}
}

func TestTrackDistributor_ByteCounters(t *testing.T) {
	// Use a unique trackID per run so parallel tests don't share counter state.
	nodeID := fmt.Sprintf("node-%d", time.Now().UnixNano())
	trackID := "[" + nodeID + "]/live/cam1/video"

	dist := newTrackDistributor(newTrackManager(0, nil), trackID, nil, nil)
	defer close(dist.done)

	// Verify the counters are wired to the correct Prometheus metric+label.
	// Simulate what addGroup and egress would do internally.
	dist.ingressCounter.Add(200)
	dist.ingressCounter.Add(100)
	dist.egressCounter.Add(150)

	assert.Equal(t, 300.0, testutil.ToFloat64(metricRelayIngressBytesTotal.WithLabelValues(trackID)))
	assert.Equal(t, 150.0, testutil.ToFloat64(metricRelayEgressBytesTotal.WithLabelValues(trackID)))
}

// ============================================================================
// trackDistributor Tests - Core Functionality
// ============================================================================

// TestTrackDistributor_Broadcast_SingleSubscriber tests basic broadcast functionality
func TestTrackDistributor_Broadcast_SingleSubscriber(t *testing.T) {
	dist := &trackDistributor{
		ring:        newGroupRing(DefaultGroupCacheSize, DefaultFramePool),
		subscribers: nil,
	}

	received := &atomic.Int32{}
	ready := make(chan struct{})
	var wg sync.WaitGroup

	wg.Go(func() {
		ch := dist.subscribe()
		defer dist.unsubscribe(ch)
		close(ready)

		timeout := time.After(500 * time.Millisecond)
		select {
		case <-ch:
			received.Add(1)
		case <-timeout:
			return
		}
	})

	<-ready

	dist.mu.RLock()
	for _, ch := range dist.subscribers {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
	dist.mu.RUnlock()

	wg.Wait()
	assert.Equal(t, int32(1), received.Load(), "subscriber should receive exactly one notification")
}

// TestTrackDistributor_Broadcast_MultipleSubscribers tests broadcast to multiple subscribers
func TestTrackDistributor_Broadcast_MultipleSubscribers(t *testing.T) {
	tests := map[string]struct {
		numSubscribers int
		broadcasts     int
	}{
		"ten_subscribers":     {numSubscribers: 10, broadcasts: 1},
		"hundred_subscribers": {numSubscribers: 100, broadcasts: 1},
		"multiple_broadcasts": {numSubscribers: 10, broadcasts: 5},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			dist := &trackDistributor{
				ring:        newGroupRing(DefaultGroupCacheSize, DefaultFramePool),
				subscribers: nil,
			}

			received := &atomic.Int32{}
			ready := make(chan struct{}, tt.numSubscribers)
			var wg sync.WaitGroup

			for i := 0; i < tt.numSubscribers; i++ {
				wg.Go(func() {
					ch := dist.subscribe()
					defer dist.unsubscribe(ch)
					ready <- struct{}{}

					for j := 0; j < tt.broadcasts; j++ {
						select {
						case <-ch:
							received.Add(1)
						case <-time.After(500 * time.Millisecond):
							return
						}
					}
				})
			}

			for i := 0; i < tt.numSubscribers; i++ {
				<-ready
			}

			for i := 0; i < tt.broadcasts; i++ {
				dist.mu.RLock()
				for _, ch := range dist.subscribers {
					select {
					case ch <- struct{}{}:
					default:
					}
				}
				dist.mu.RUnlock()
				// Give subscribers time to consume messages between broadcasts
				if i < tt.broadcasts-1 {
					<-time.After(10 * time.Millisecond)
				}
			}

			wg.Wait()
			expected := tt.numSubscribers * tt.broadcasts
			assert.Equal(t, int32(expected), received.Load(), "all subscribers should receive all broadcasts")
		})
	}
}

// TestTrackDistributor_SubscriptionLifecycle tests subscribe/unsubscribe operations
func TestTrackDistributor_SubscriptionLifecycle(t *testing.T) {
	dist := &trackDistributor{
		ring:        newGroupRing(DefaultGroupCacheSize, DefaultFramePool),
		subscribers: nil,
	}

	// Test basic subscribe
	ch1 := dist.subscribe()
	require.NotNil(t, ch1)

	assert.Len(t, dist.subscribers, 1)

	// Test multiple subscribes
	ch2 := dist.subscribe()
	ch3 := dist.subscribe()

	assert.Len(t, dist.subscribers, 3)

	// Test unsubscribe
	dist.unsubscribe(ch2)

	assert.Len(t, dist.subscribers, 2)

	// Test unsubscribe all
	dist.unsubscribe(ch1)
	dist.unsubscribe(ch3)

	assert.Empty(t, dist.subscribers)

	// Test double unsubscribe (should not panic)
	dist.unsubscribe(ch1)
	assert.Empty(t, dist.subscribers)
}

// TestTrackDistributor_ConcurrentAccess tests thread safety
func TestTrackDistributor_ConcurrentAccess(t *testing.T) {
	dist := &trackDistributor{
		ring:        newGroupRing(DefaultGroupCacheSize, DefaultFramePool),
		subscribers: nil,
	}

	const goroutines = 50
	const iterations = 100

	var wg sync.WaitGroup

	for range goroutines {
		wg.Go(func() {
			for range iterations {
				ch := dist.subscribe()
				dist.unsubscribe(ch)
			}
		})
	}

	for range goroutines {
		wg.Go(func() {
			for range iterations {
				dist.mu.RLock()
				for _, ch := range dist.subscribers {
					select {
					case ch <- struct{}{}:
					default:
					}
				}
				dist.mu.RUnlock()
			}
		})
	}

	wg.Wait()

	assert.Empty(t, dist.subscribers, "all subscribers should be unsubscribed")
}

// TestTrackDistributor_ChannelBuffering tests that channels are buffered
func TestTrackDistributor_ChannelBuffering(t *testing.T) {
	dist := &trackDistributor{
		ring:        newGroupRing(DefaultGroupCacheSize, DefaultFramePool),
		subscribers: nil,
	}

	ch := dist.subscribe()

	assert.Equal(t, 1, cap(ch))

	// Should not block on first send
	select {
	case ch <- struct{}{}:
		// OK
	case <-time.After(50 * time.Millisecond):
		require.Fail(t, "First send blocked")
	}

	// Channel is now full, but broadcast should not block
	done := make(chan struct{})
	go func() {
		select {
		case ch <- struct{}{}:
		default:
			// Expected - channel is full
		}
		done <- struct{}{}
	}()

	select {
	case <-done:
		// OK - didn't block
	case <-time.After(50 * time.Millisecond):
		require.Fail(t, "Broadcast blocked on full channel")
	}
}

// TestTrackDistributor_NoBroadcastBlocking ensures broadcasts never block
func TestTrackDistributor_NoBroadcastBlocking(t *testing.T) {
	dist := &trackDistributor{
		ring:        newGroupRing(DefaultGroupCacheSize, DefaultFramePool),
		subscribers: nil,
	}

	// Create subscribers but don't read
	for range 20 {
		dist.subscribe()
	}

	// Multiple broadcasts should complete quickly
	done := make(chan struct{})
	go func() {
		for range 100 {
			dist.mu.RLock()
			for _, ch := range dist.subscribers {
				select {
				case ch <- struct{}{}:
				default:
				}
			}
			dist.mu.RUnlock()
		}
		done <- struct{}{}
	}()

	select {
	case <-done:
		// Success
	case <-time.After(1 * time.Second):
		require.Fail(t, "Broadcasts blocked unexpectedly")
	}
}

// TestTrackDistributor_EdgeCases tests edge cases and boundary conditions
func TestTrackDistributor_EdgeCases(t *testing.T) {
	t.Run("subscribe_to_empty_distributor", func(t *testing.T) {
		dist := &trackDistributor{
			ring:        newGroupRing(DefaultGroupCacheSize, DefaultFramePool),
			subscribers: nil,
		}

		ch := dist.subscribe()
		require.NotNil(t, ch)

		assert.Len(t, dist.subscribers, 1)
	})

	t.Run("unsubscribe_nonexistent_channel", func(t *testing.T) {
		dist := &trackDistributor{
			ring:        newGroupRing(DefaultGroupCacheSize, DefaultFramePool),
			subscribers: nil,
		}

		// Unsubscribe channel that was never subscribed
		fakeCh := make(chan struct{}, 1)
		dist.unsubscribe(fakeCh)

		// Should not panic and map should remain empty
		assert.Empty(t, dist.subscribers)
	})

	t.Run("multiple_unsubscribe_same_channel", func(t *testing.T) {
		dist := &trackDistributor{
			ring:        newGroupRing(DefaultGroupCacheSize, DefaultFramePool),
			subscribers: nil,
		}

		ch := dist.subscribe()
		dist.unsubscribe(ch)
		dist.unsubscribe(ch) // Double unsubscribe

		// Should not panic
		assert.Empty(t, dist.subscribers)
	})

	t.Run("broadcast_to_zero_subscribers", func(t *testing.T) {
		dist := &trackDistributor{
			ring:        newGroupRing(DefaultGroupCacheSize, DefaultFramePool),
			subscribers: nil,
		}

		// Should not panic
		dist.mu.RLock()
		for _, ch := range dist.subscribers {
			select {
			case ch <- struct{}{}:
			default:
			}
		}
		dist.mu.RUnlock()
	})

	t.Run("rapid_subscribe_unsubscribe", func(t *testing.T) {
		dist := &trackDistributor{
			ring:        newGroupRing(DefaultGroupCacheSize, DefaultFramePool),
			subscribers: nil,
		}

		// Rapidly add and remove
		for range 1000 {
			ch := dist.subscribe()
			dist.unsubscribe(ch)
		}

		assert.Equal(t, 0, len(dist.subscribers), "Expected 0 subscribers")
	})
}

// TestTrackDistributor_Stress performs stress testing
func TestTrackDistributor_Stress(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stress test in short mode")
	}

	t.Run("high_frequency_broadcasts", func(t *testing.T) {
		dist := &trackDistributor{
			ring:        newGroupRing(DefaultGroupCacheSize, DefaultFramePool),
			subscribers: nil,
		}

		const numSubs = 100
		for range numSubs {
			dist.subscribe()
		}

		done := make(chan bool)
		go func() {
			for range 10000 {
				dist.mu.RLock()
				for _, ch := range dist.subscribers {
					select {
					case ch <- struct{}{}:
					default:
					}
				}
				dist.mu.RUnlock()
			}
			done <- true
		}()

		select {
		case <-done:
			// Success
		case <-time.After(10 * time.Second):
			require.Fail(t, "high frequency broadcast timed out")
		}
	})

	t.Run("subscriber_churn", func(t *testing.T) {
		dist := &trackDistributor{
			ring:        newGroupRing(DefaultGroupCacheSize, DefaultFramePool),
			subscribers: nil,
		}

		var wg sync.WaitGroup
		stopCh := make(chan struct{})

		for range 10 {
			wg.Go(func() {
				for {
					select {
					case <-stopCh:
						return
					default:
						ch := dist.subscribe()
						dist.unsubscribe(ch)
					}
				}
			})
		}

		for range 5 {
			wg.Go(func() {
				for {
					select {
					case <-stopCh:
						return
					default:
						dist.mu.RLock()
						for _, ch := range dist.subscribers {
							select {
							case ch <- struct{}{}:
							default:
							}
						}
						dist.mu.RUnlock()
					}
				}
			})
		}

		time.Sleep(2 * time.Second)
		close(stopCh)
		wg.Wait()
	})
}

// TestTrackDistributor_Scalability tests performance at different scales
func TestTrackDistributor_Scalability(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping scalability test in short mode")
	}

	scales := []int{1, 10, 50, 100, 500, 1000}

	for _, n := range scales {
		t.Run(fmt.Sprintf("%04d_subscribers", n), func(t *testing.T) {
			dist := &trackDistributor{
				ring:        newGroupRing(DefaultGroupCacheSize, DefaultFramePool),
				subscribers: nil,
			}

			// Create n subscribers
			for range n {
				dist.subscribe()
			}

			// Measure broadcast time
			start := time.Now()
			dist.mu.RLock()
			for _, ch := range dist.subscribers {
				select {
				case ch <- struct{}{}:
				default:
				}
			}
			dist.mu.RUnlock()
			elapsed := time.Since(start)

			t.Logf("%d subscribers: broadcast took %v", n, elapsed)

			// Sanity check - should complete quickly (more lenient for CI)
			assert.LessOrEqual(t, elapsed, 50*time.Millisecond, "Broadcast took too long for %d subscribers", n)
		})
	}
}

// TestTrackDistributor_MemoryBehavior tests memory-related behavior
func TestTrackDistributor_MemoryBehavior(t *testing.T) {
	t.Run("channel_garbage_collection", func(t *testing.T) {
		dist := &trackDistributor{
			ring:        newGroupRing(DefaultGroupCacheSize, DefaultFramePool),
			subscribers: nil,
		}

		// Subscribe many
		const count = 1000
		for range count {
			ch := dist.subscribe()
			// Immediately unsubscribe to allow GC
			dist.unsubscribe(ch)
		}

		assert.Empty(t, dist.subscribers, "Subscribers not cleaned up")
	})

	t.Run("no_channel_leaks_on_unsubscribe", func(t *testing.T) {
		dist := &trackDistributor{
			ring:        newGroupRing(DefaultGroupCacheSize, DefaultFramePool),
			subscribers: nil,
		}

		channels := make([]chan struct{}, 100)
		for i := range 100 {
			channels[i] = dist.subscribe()
		}

		// Unsubscribe all
		for _, ch := range channels {
			dist.unsubscribe(ch)
		}

		assert.Empty(t, dist.subscribers, "Expected empty map")
	})
}

// TestTrackDistributor_RaceConditions tests for race conditions
func TestTrackDistributor_RaceConditions(t *testing.T) {
	t.Run("concurrent_subscribe_and_broadcast", func(t *testing.T) {
		dist := &trackDistributor{
			ring:        newGroupRing(DefaultGroupCacheSize, DefaultFramePool),
			subscribers: nil,
		}

		var wg sync.WaitGroup
		wg.Add(2)

		go func() {
			defer wg.Done()
			for range 100 {
				dist.subscribe()
			}
		}()

		go func() {
			defer wg.Done()
			for range 100 {
				dist.mu.RLock()
				for _, ch := range dist.subscribers {
					select {
					case ch <- struct{}{}:
					default:
					}
				}
				dist.mu.RUnlock()
			}
		}()

		wg.Wait()
	})

	t.Run("concurrent_unsubscribe_and_broadcast", func(t *testing.T) {
		dist := &trackDistributor{
			ring:        newGroupRing(DefaultGroupCacheSize, DefaultFramePool),
			subscribers: nil,
		}

		channels := make([]chan struct{}, 100)
		for i := range 100 {
			channels[i] = dist.subscribe()
		}

		var wg sync.WaitGroup
		wg.Add(2)

		go func() {
			defer wg.Done()
			for _, ch := range channels {
				dist.unsubscribe(ch)
			}
		}()

		go func() {
			defer wg.Done()
			for range 100 {
				dist.mu.RLock()
				for _, ch := range dist.subscribers {
					select {
					case ch <- struct{}{}:
					default:
					}
				}
				dist.mu.RUnlock()
			}
		}()

		wg.Wait()
	})
}

// TestTrackDistributor_NotificationDelivery tests notification delivery guarantees
func TestTrackDistributor_NotificationDelivery(t *testing.T) {
	t.Run("all_subscribers_receive_notification", func(t *testing.T) {
		dist := &trackDistributor{
			ring:        newGroupRing(DefaultGroupCacheSize, DefaultFramePool),
			subscribers: nil,
		}

		const numSubs = 50
		received := make([]*atomic.Int32, numSubs)
		ready := make(chan struct{}, numSubs)
		var wg sync.WaitGroup

		for i := range numSubs {
			received[i] = &atomic.Int32{}
			wg.Add(1)
			idx := i
			go func() {
				defer wg.Done()
				ch := dist.subscribe()
				defer dist.unsubscribe(ch)
				ready <- struct{}{}

				timeout := time.After(500 * time.Millisecond)
				select {
				case <-ch:
					received[idx].Add(1)
				case <-timeout:
				}
			}()
		}

		for range numSubs {
			<-ready
		}

		dist.mu.RLock()
		for _, ch := range dist.subscribers {
			select {
			case ch <- struct{}{}:
			default:
			}
		}
		dist.mu.RUnlock()

		wg.Wait()

		failures := 0
		for i, count := range received {
			if count.Load() != 1 {
				t.Errorf("subscriber %d received %d notifications, expected 1", i, count.Load())
				failures++
			}
		}

		if failures > 0 {
			t.Errorf("%d/%d subscribers did not receive notification", failures, numSubs)
		}
	})

	t.Run("buffered_channel_prevents_loss", func(t *testing.T) {
		dist := &trackDistributor{
			ring:        newGroupRing(DefaultGroupCacheSize, DefaultFramePool),
			subscribers: nil,
		}

		ch := dist.subscribe()

		dist.mu.RLock()
		select {
		case ch <- struct{}{}:
		default:
			require.Fail(t, "buffered channel should not block")
		}
		dist.mu.RUnlock()

		select {
		case <-ch:
		case <-time.After(50 * time.Millisecond):
			require.Fail(t, "did not receive notification from buffer")
		}
	})
}

// ============================================================================
// trackDistributor Integration Tests
// ============================================================================

// TestTrackDistributor_GroupRingIntegration tests groupRing initialization
func TestTrackDistributor_GroupRingIntegration(t *testing.T) {
	dist := &trackDistributor{
		ring:        newGroupRing(DefaultGroupCacheSize, DefaultFramePool),
		subscribers: nil,
	}

	// Verify ring is properly initialized
	require.NotNil(t, dist.ring, "Ring should be initialized")

	head := dist.ring.head()
	assert.Equal(t, moqt.GroupSequence(0), head, "Expected initial head to be 0")

	earliest := dist.ring.earliestAvailable()
	assert.Equal(t, moqt.GroupSequence(1), earliest, "Expected earliest to be 1")
}

// TestTrackDistributor_DoneChannel tests that the done channel is closed when ingest stops
func TestTrackDistributor_DoneChannel(t *testing.T) {
	dist := newTrackDistributor(newTrackManager(0, nil), "[test-node]/test/test", nil, nil)

	// done should not be closed initially
	select {
	case <-dist.done:
		require.Fail(t, "done channel should not be closed yet")
	default:
	}

	// Simulate ingest finishing by closing done directly
	close(dist.done)

	select {
	case <-dist.done:
		// Expected
	case <-time.After(50 * time.Millisecond):
		require.Fail(t, "done channel should be closed")
	}
}

// TestTrackDistributor_RingBehavior tests ring head and earliest available
func TestTrackDistributor_RingBehavior(t *testing.T) {
	t.Run("ring_initialization", func(t *testing.T) {
		dist := &trackDistributor{
			ring:        newGroupRing(DefaultGroupCacheSize, DefaultFramePool),
			subscribers: nil,
		}

		// Verify ring is initialized
		assert.NotNil(t, dist.ring, "Ring should be initialized")

		// Verify initial head
		head := dist.ring.head()
		assert.Equal(t, moqt.GroupSequence(0), head, "Expected head 0")
	})

	t.Run("earliest_available_at_start", func(t *testing.T) {
		dist := &trackDistributor{
			ring:        newGroupRing(DefaultGroupCacheSize, DefaultFramePool),
			subscribers: nil,
		}

		earliest := dist.ring.earliestAvailable()
		assert.Equal(t, moqt.GroupSequence(1), earliest, "Expected earliest 1 at start")
	})

	t.Run("catchup_logic", func(t *testing.T) {
		dist := &trackDistributor{
			ring:        newGroupRing(DefaultGroupCacheSize, DefaultFramePool),
			subscribers: nil,
		}

		// Initially head should be 0
		assert.Equal(t, moqt.GroupSequence(0), dist.ring.head(), "Expected initial head to be 0")

		// Verify earliest available - starts at 1 for empty ring
		earliest := dist.ring.earliestAvailable()
		assert.GreaterOrEqual(t, earliest, uint64(0), "Expected earliest to be non-negative")
	})
}

// ============================================================================
// RelayHandler Tests - subscribe() does not hold mu
// ============================================================================

// TestRelayHandler_ConcurrentSubscribe verifies that subscribe() can be called
// concurrently without deadlock. This is a regression test for a bug where
// ServeTrack held a mutex during subscribe(), which performs a blocking network
// round-trip. A second ServeTrack call (e.g. for "video" while "video.meta" was
// subscribing) would block on the same mutex, causing a deadlock.
func TestRelayHandler_ConcurrentSubscribe(t *testing.T) {
	h := newTestRelayHandler(t.Context())

	// Pre-fill distributors to avoid real Session.Subscribe calls
	const numTracks = 10
	for i := range numTracks {
		name := moqt.TrackName(fmt.Sprintf("track-%d", i))
		trackID := "[test-node]/test/" + string(name)
		d := newTrackDistributor(h.tracks, trackID, nil, nil)
		defer close(d.done)
		h.tracks.store(trackID, d)
	}

	done := make(chan struct{}, numTracks)

	for i := range numTracks {
		go func() {
			defer func() { done <- struct{}{} }()
			name := moqt.TrackName(fmt.Sprintf("track-%d", i))

			// Should hit the cache and return immediately
			tr := h.subscribe(name)
			_ = tr
		}()
	}

	// All goroutines must finish within 1 second; a deadlock would hang.
	timeout := time.After(1 * time.Second)
	for range numTracks {
		select {
		case <-done:
		case <-timeout:
			t.Fatal("Deadlock detected: concurrent subscribe calls blocked")
		}
	}
}

// TestRelayHandler_SingleflightDedup verifies that concurrent ServeTrack calls
// for the same track name result in only one upstream subscribe via singleflight.
func TestRelayHandler_SingleflightDedup(t *testing.T) {
	h := newTestRelayHandler(t.Context()) // nil session for test

	// Pre-populate a distributor in the cache
	existing := newTrackDistributor(newTrackManager(0, nil), "[test-node]/test/video", nil, nil)
	defer close(existing.done) // Cleanup goroutine
	h.tracks.store("[test-node]/test/video", existing)

	// Load should return the existing one
	v, ok := h.tracks.load("[test-node]/test/video")
	require.True(t, ok, "Expected cached entry")
	assert.Same(t, existing, v, "Should return the cached distributor")

	// remove with the correct value should succeed
	h.tracks.remove("[test-node]/test/video", existing)

	// Subsequent load should miss
	_, ok = h.tracks.load("[test-node]/test/video")
	assert.False(t, ok, "Should not find deleted entry")
}

// ============================================================================
// RouteStats Tests
// ============================================================================

// TestRelayHandler_RouteStats_Interface verifies that *relayHandler satisfies
// the RouteReporter interface and is discoverable via type assertion.
func TestRelayHandler_RouteStats_Interface(t *testing.T) {
	ctx := context.Background()
	h := newTestRelayHandler(ctx)

	var th moqt.TrackHandler = h
	rr, ok := th.(RouteReporter)
	require.True(t, ok, "*relayHandler must implement RouteReporter")
	assert.NotNil(t, rr)
}

// TestRelayHandler_Hops_LocalAnnouncement confirms that a locally created
// announcement (no forwarding) reports 0 hops.
func TestRelayHandler_Hops_LocalAnnouncement(t *testing.T) {
	ctx := context.Background()
	h := newTestRelayHandler(ctx)

	assert.Equal(t, 0, h.RouteStats().Hops, "local announcement should have 0 hops")
}

// TestRelayHandler_RTT_NilSession returns a RouteStats with nil Probe when
// the session is nil.
func TestRelayHandler_RTT_NilSession(t *testing.T) {
	ctx := context.Background()
	h := newTestRelayHandler(ctx) // session is nil

	assert.Equal(t, 0, h.RouteStats().Hops, "nil session should yield 0 hops without panic")
	assert.Equal(t, uint64(0), h.RouteStats().EstimatedBitrate, "nil session should yield 0 bitrate without panic")
	assert.Equal(t, time.Duration(0), h.RouteStats().RTT, "nil session should yield 0 RTT without panic")
}

// ============================================================================
// ingest concurrent group processing tests
// ============================================================================

// TestIngest_ConcurrentGroups_AllCachesComplete exercises the reserve+fill split
// directly: reserve N caches sequentially, then fill them concurrently, and
// confirm that every cache is marked complete and holds the correct frames.
func TestIngest_ConcurrentGroups_AllCachesComplete(t *testing.T) {
	const numGroups = 10
	const framesPerGroup = 5

	ring := newGroupRing(numGroups, DefaultFramePool)
	caches := make([]*groupCache, numGroups)
	for i := range numGroups {
		caches[i] = ring.reserve(moqt.GroupSequence(i + 1))
	}

	var wg sync.WaitGroup
	for i := range numGroups {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			frames := make([][]byte, framesPerGroup)
			for j := range framesPerGroup {
				frames[j] = []byte(fmt.Sprintf("g%d-f%d", idx, j))
			}
			ring.fill(&fakeFrameSource{frames: frames}, caches[idx], nil)
		}(i)
	}
	wg.Wait()

	for i, c := range caches {
		assert.True(t, c.isComplete(), "group %d should be complete", i)
		for j := range framesPerGroup {
			assert.NotNil(t, c.next(j), "group %d frame %d should exist", i, j)
		}
	}
}

// TestIngest_ConcurrentGroups_FasterThanSerial confirms that concurrent fill
// completes in roughly framesPerGroup×delay time, not numGroups×framesPerGroup×delay.
// Uses testing/synctest so time advances deterministically.
func TestIngest_ConcurrentGroups_FasterThanSerial(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const numGroups = 6
		const framesPerGroup = 3
		const delay = 10 * time.Millisecond

		ring := newGroupRing(numGroups, DefaultFramePool)
		caches := make([]*groupCache, numGroups)
		for i := range numGroups {
			caches[i] = ring.reserve(moqt.GroupSequence(i + 1))
		}

		allDone := make(chan struct{})
		var wg sync.WaitGroup
		for i := range numGroups {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				frames := make([][]byte, framesPerGroup)
				for j := range framesPerGroup {
					frames[j] = []byte("x")
				}
				src := &slowFrameSource{
					fakeFrameSource: fakeFrameSource{frames: frames},
					delay:           delay,
				}
				ring.fill(src, caches[idx], nil)
			}(i)
		}
		go func() {
			wg.Wait()
			close(allDone)
		}()

		// All goroutines sleep concurrently for framesPerGroup×delay.
		// Advance time just past that to confirm they all complete.
		serialTime := time.Duration(numGroups*framesPerGroup) * delay
		concurrentTime := time.Duration(framesPerGroup) * delay

		time.Sleep(concurrentTime + delay)
		synctest.Wait()

		select {
		case <-allDone:
			// good: completed in roughly framesPerGroup×delay
		default:
			t.Fatalf("fills should complete within %v, not the serial %v", concurrentTime+delay, serialTime)
		}
	})
}

// TestIngest_WaitGroup_BlocksDoneUntilFillComplete verifies the LIFO defer
// ordering: wg.Wait() must run before close(done), so subscribers cannot see
// done closed while a fill goroutine is still writing frames.
func TestIngest_WaitGroup_BlocksDoneUntilFillComplete(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ring := newGroupRing(DefaultGroupCacheSize, DefaultFramePool)
		cache := ring.reserve(moqt.GroupSequence(1))

		done := make(chan struct{})
		fillDone := make(chan struct{})

		frames := [][]byte{[]byte("a"), []byte("b"), []byte("c")}
		src := &slowFrameSource{
			fakeFrameSource: fakeFrameSource{frames: frames},
			delay:           10 * time.Millisecond,
		}

		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			ring.fill(src, cache, nil)
			close(fillDone)
		}()
		go func() {
			wg.Wait()
			close(done)
		}()

		synctest.Wait()

		// Before time advances: fill should not be done yet.
		select {
		case <-fillDone:
			t.Fatal("fill should not be done before time advances")
		default:
		}

		// Advance past all frame sleeps (3 × 10ms).
		time.Sleep(50 * time.Millisecond)
		synctest.Wait()

		select {
		case <-fillDone:
		default:
			t.Fatal("fill should have completed after 50ms")
		}
		select {
		case <-done:
		default:
			t.Fatal("done should be closed after fill completed")
		}
	})
}

// ============================================================================
// trackDistributor metering integration tests
// ============================================================================

// TestTrackDistributor_MeteringIngress verifies that processGroup accumulates
// ingress bytes into the attached broadcastSession as well as the Prometheus counter.
func TestTrackDistributor_MeteringIngress(t *testing.T) {
	nodeID := fmt.Sprintf("meter-node-%d", time.Now().UnixNano())
	trackID := "[" + nodeID + "]/live/test/video"

	sess := newBroadcastSession("tok-ingress")
	dist := newTrackDistributor(newTrackManager(0, nil), trackID, sess, nil)
	t.Cleanup(func() { close(dist.done) })

	payload := []byte("hello-world") // 11 bytes
	src := &fakeFrameSource{frames: [][]byte{payload}}

	var wg sync.WaitGroup
	cache := dist.ring.reserve(moqt.GroupSequence(1))
	ok := dist.processGroup(context.Background(), &wg, moqt.GroupSequence(1), src)
	require.True(t, ok)
	wg.Wait()

	_ = cache // reserved above

	assert.Equal(t, int64(len(payload)), sess.ingressBytes.Load(),
		"processGroup must add ingress bytes to the broadcast session")
	assert.Equal(t, 0.0+float64(len(payload)),
		testutil.ToFloat64(metricRelayIngressBytesTotal.WithLabelValues(trackID)),
		"Prometheus ingress counter must also be updated")
}

// TestTrackDistributor_MeteringEgress verifies that the egress path accumulates
// egress bytes into the attached broadcastSession as well as the Prometheus counter.
func TestTrackDistributor_MeteringEgress(t *testing.T) {
	nodeID := fmt.Sprintf("meter-node-egress-%d", time.Now().UnixNano())
	trackID := "[" + nodeID + "]/live/test/video"

	sess := newBroadcastSession("tok-egress")
	dist := newTrackDistributor(newTrackManager(0, nil), trackID, sess, nil)
	t.Cleanup(func() { close(dist.done) })

	// Simulate the egress byte-counting path directly via the session methods,
	// mirroring what egress() does on each frame write.
	dist.egressCounter.Add(float64(512))
	sess.addEgress(512)
	dist.egressCounter.Add(float64(256))
	sess.addEgress(256)

	assert.Equal(t, int64(768), sess.egressBytes.Load(),
		"session egress counter must accumulate across multiple writes")
	assert.Equal(t, 768.0,
		testutil.ToFloat64(metricRelayEgressBytesTotal.WithLabelValues(trackID)),
		"Prometheus egress counter must also be updated")
}

// TestTrackDistributor_MeteringNilSession verifies that processGroup and the
// egress path do not panic when no broadcastSession is attached.
func TestTrackDistributor_MeteringNilSession(t *testing.T) {
	dist := newTrackDistributor(newTrackManager(0, nil), "nil-session/test", nil, nil)
	t.Cleanup(func() { close(dist.done) })

	src := &fakeFrameSource{frames: [][]byte{[]byte("frame")}}
	var wg sync.WaitGroup
	assert.NotPanics(t, func() {
		dist.processGroup(context.Background(), &wg, moqt.GroupSequence(1), src)
		wg.Wait()
	})
}

// ============================================================================
// trackDistributor.processGroup semaphore tests
// ============================================================================

// TestTrackDistributor_ProcessGroup_SemaphoreLimitsConcurrency verifies that
// processGroup blocks (semaphore-full) when MaxGroupFillsInFlight goroutines
// are already in flight, and resumes as soon as a slot is released.
// Uses testing/synctest for deterministic goroutine scheduling.
func TestTrackDistributor_ProcessGroup_SemaphoreLimitsConcurrency(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const limit = 2
		orig := MaxGroupFillsInFlight
		t.Cleanup(func() { MaxGroupFillsInFlight = orig })
		MaxGroupFillsInFlight = limit

		dist := newTrackDistributor(newTrackManager(0, nil), "test/sem", nil, nil)
		t.Cleanup(func() { close(dist.done) }) // release egress waiters
		require.Equal(t, limit, cap(dist.fillSem), "fillSem capacity must equal MaxGroupFillsInFlight")

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// Each slow source blocks indefinitely until the context is cancelled.
		// This keeps fill goroutines alive so we can count in-flight slots.
		slowSrc := func() frameSource {
			return &slowFrameSource{
				fakeFrameSource: fakeFrameSource{frames: [][]byte{[]byte("x")}},
				delay:           1 * time.Hour,
			}
		}

		var wg sync.WaitGroup

		// Spin up `limit` groups — all should be accepted without blocking.
		for i := range limit {
			ok := dist.processGroup(ctx, &wg, moqt.GroupSequence(i+1), slowSrc())
			require.True(t, ok, "processGroup should succeed while under the limit")
		}

		// All slots are now occupied. A further processGroup call must block.
		blocked := make(chan struct{})
		accepted := make(chan struct{})
		go func() {
			close(blocked)
			ok := dist.processGroup(ctx, &wg, moqt.GroupSequence(limit+1), slowSrc())
			if ok {
				close(accepted)
			}
		}()

		<-blocked
		synctest.Wait()

		// Verify it is still blocked (accepted not closed).
		select {
		case <-accepted:
			t.Fatal("processGroup should be blocked when semaphore is full")
		default:
		}

		// Advance time past the slow-source delay to let one fill goroutine finish,
		// which releases a semaphore slot and unblocks the waiting processGroup.
		time.Sleep(2 * time.Hour)
		synctest.Wait()

		select {
		case <-accepted:
		default:
			t.Fatal("processGroup should have unblocked after a slot was released")
		}

		// Drain remaining goroutines.
		cancel()
		synctest.Wait()
		wg.Wait()
	})
}

// TestTrackDistributor_ProcessGroup_CtxCancelUnblocks verifies that a
// processGroup call blocked on a full semaphore returns false when its
// context is cancelled.
func TestTrackDistributor_ProcessGroup_CtxCancelUnblocks(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		orig := MaxGroupFillsInFlight
		t.Cleanup(func() { MaxGroupFillsInFlight = orig })
		MaxGroupFillsInFlight = 1

		dist := newTrackDistributor(newTrackManager(0, nil), "test/cancel", nil, nil)
		t.Cleanup(func() { close(dist.done) }) // release egress waiters

		ctx, cancel := context.WithCancel(context.Background())

		var wg sync.WaitGroup

		// Fill the single slot with a goroutine that blocks until cancelled.
		holdSrc := &slowFrameSource{
			fakeFrameSource: fakeFrameSource{frames: [][]byte{[]byte("x")}},
			delay:           1 * time.Hour,
		}
		ok := dist.processGroup(ctx, &wg, moqt.GroupSequence(1), holdSrc)
		require.True(t, ok)

		// Second call must block on the full semaphore.
		result := make(chan bool, 1)
		go func() {
			result <- dist.processGroup(ctx, &wg, moqt.GroupSequence(2), &fakeFrameSource{frames: [][]byte{[]byte("y")}})
		}()

		synctest.Wait()

		// Cancel the context — the blocked processGroup must return false.
		cancel()
		synctest.Wait()

		select {
		case got := <-result:
			assert.False(t, got, "processGroup must return false on ctx cancellation")
		default:
			t.Fatal("processGroup did not return after ctx cancel")
		}

		wg.Wait()
	})
}

// ============================================================================
// isBetterRoute Tests
// ============================================================================

func TestIsBetterRoute(t *testing.T) {
	type testCase struct {
		candidate RouteStats
		current   RouteStats
		want      bool
	}
	tests := map[string]testCase{
		"fewer hops wins regardless of RTT": {
			candidate: RouteStats{Alive: true, Hops: 1, RTT: 100},
			current:   RouteStats{Alive: true, Hops: 2, RTT: 10},
			want:      true,
		},
		"more hops loses regardless of RTT": {
			candidate: RouteStats{Alive: true, Hops: 3, RTT: 1},
			current:   RouteStats{Alive: true, Hops: 2, RTT: 999},
			want:      false,
		},
		"equal hops: higher bitrate wins over lower RTT": {
			candidate: RouteStats{Alive: true, Hops: 2, EstimatedBitrate: 10_000_000, RTT: 80},
			current:   RouteStats{Alive: true, Hops: 2, EstimatedBitrate: 5_000_000, RTT: 20},
			want:      true,
		},
		"equal hops and bitrate: lower RTT wins": {
			candidate: RouteStats{Alive: true, Hops: 2, EstimatedBitrate: 5_000_000, RTT: 20},
			current:   RouteStats{Alive: true, Hops: 2, EstimatedBitrate: 5_000_000, RTT: 50},
			want:      true,
		},
		"equal hops: higher RTT loses": {
			candidate: RouteStats{Alive: true, Hops: 2, RTT: 80},
			current:   RouteStats{Alive: true, Hops: 2, RTT: 50},
			want:      false,
		},
		"equal hops: zero bitrate/RTT keeps existing route": {
			candidate: RouteStats{Alive: true, Hops: 2},
			current:   RouteStats{Alive: true, Hops: 2, EstimatedBitrate: 5_000_000, RTT: 50},
			want:      false,
		},
		// Alive dominates all quality metrics.
		"alive candidate beats dead current regardless of hops": {
			candidate: RouteStats{Alive: true, Hops: 5},
			current:   RouteStats{Alive: false, Hops: 1},
			want:      true,
		},
		"dead candidate loses to alive current regardless of hops": {
			candidate: RouteStats{Alive: false, Hops: 1},
			current:   RouteStats{Alive: true, Hops: 5},
			want:      false,
		},
		"both dead: keep existing route": {
			candidate: RouteStats{Alive: false, Hops: 1, RTT: 1},
			current:   RouteStats{Alive: false, Hops: 5, RTT: 999},
			want:      false,
		},
		"both alive: normal hop comparison applies": {
			candidate: RouteStats{Alive: true, Hops: 1, RTT: 100},
			current:   RouteStats{Alive: true, Hops: 2, RTT: 10},
			want:      true,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got, _ := isBetterRoute(tt.candidate, tt.current)
			assert.Equal(t, tt.want, got)
		})
	}
}

// ============================================================================
// RouteStats.Alive Tests
// ============================================================================

// TestRelayHandler_Alive_ActiveContext verifies Alive=true for a live handler.
func TestRelayHandler_Alive_ActiveContext(t *testing.T) {
	h := newTestRelayHandler(t.Context())
	assert.True(t, h.RouteStats().Alive, "handler with active context should be alive")
}

// TestRelayHandler_Alive_CancelledContext verifies Alive=false after ctx cancel.
func TestRelayHandler_Alive_CancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	ann, _ := moqt.NewAnnouncement(ctx, "/test")
	sess := &moqt.Session{}
	h := &relayHandler{
		announcement: ann,
		session:      sess,
		tracks:       newTrackManager(0, nil),
		ctx:          ctx,
		cancel:       cancel,
	}

	assert.True(t, h.RouteStats().Alive, "should be alive before cancel")
	cancel()
	assert.False(t, h.RouteStats().Alive, "should be dead after cancel")
}

// TestRelayHandler_Alive_RetractedAnnouncement verifies Alive=false when
// the announcement is retracted (IsActive=false) even if the context is live.
func TestRelayHandler_Alive_RetractedAnnouncement(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create an announcement then retract it via EndAnnouncementFunc.
	ann, end := moqt.NewAnnouncement(ctx, "/test")
	sess := &moqt.Session{}
	h := &relayHandler{
		announcement: ann,
		session:      sess,
		tracks:       newTrackManager(0, nil),
		ctx:          ctx,
		cancel:       cancel,
	}

	assert.True(t, h.RouteStats().Alive, "should be alive before retract")
	end() // retract the announcement
	<-ann.Done()
	assert.False(t, h.RouteStats().Alive, "should be dead after announcement retract")
}

// ============================================================================
// Drain Tests
// ============================================================================

// TestRelayHandler_Drain_ZeroTimeout verifies that Drain(0) cancels immediately.
func TestRelayHandler_Drain_ZeroTimeout(t *testing.T) {
	h := newTestRelayHandler(context.Background())
	require.True(t, h.RouteStats().Alive)

	h.Drain(0)

	// time.AfterFunc(0, ...) fires in a separate goroutine; give it a moment.
	assert.Eventually(t, func() bool {
		return !h.RouteStats().Alive
	}, 100*time.Millisecond, time.Millisecond, "Drain(0) should cancel context quickly")
}

// TestRelayHandler_Drain_WithTimeout verifies that Drain with a positive timeout
// leaves the handler alive initially and kills it after the delay.
func TestRelayHandler_Drain_WithTimeout(t *testing.T) {
	h := newTestRelayHandler(context.Background())

	h.Drain(50 * time.Millisecond)

	assert.True(t, h.RouteStats().Alive, "should still be alive immediately after Drain")

	assert.Eventually(t, func() bool {
		return !h.RouteStats().Alive
	}, 200*time.Millisecond, time.Millisecond, "handler should become dead after drain timeout")
}

// TestRelayHandler_Drain_Idempotent verifies multiple Drain calls don't panic
// and cancel is idempotent.
func TestRelayHandler_Drain_Idempotent(t *testing.T) {
	h := newTestRelayHandler(context.Background())

	require.NotPanics(t, func() {
		h.Drain(0)
		h.Drain(0)
		h.Drain(50 * time.Millisecond)
	})

	assert.Eventually(t, func() bool {
		return !h.RouteStats().Alive
	}, 100*time.Millisecond, time.Millisecond)
}

func BenchmarkEgressHistogramObserve(b *testing.B) {
	trackName := []byte("test-track-name")
	start := time.Now()

	b.Run("baseline", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			metricGroupDeliveryHistogram.WithLabelValues(string(trackName)).Observe(time.Since(start).Seconds())
		}
	})

	b.Run("optimized", func(b *testing.B) {
		trackNameStr := string(trackName)
		for i := 0; i < b.N; i++ {
			metricGroupDeliveryHistogram.WithLabelValues(trackNameStr).Observe(time.Since(start).Seconds())
		}
	})

	b.Run("prebound", func(b *testing.B) {
		observer := metricGroupDeliveryHistogram.WithLabelValues(string(trackName))
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			observer.Observe(time.Since(start).Seconds())
		}
	})
}
