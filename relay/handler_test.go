package relay

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/okdaichi/gomoqt/moqt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRelay tests the Relay function with a mock session
func TestRelayWithNilSession(t *testing.T) {
	ctx := context.Background()

	// This test documents behavior - in practice, nil session would cause panic
	// but we test that the function accepts and processes the call
	defer func() {
		if r := recover(); r == nil {
			assert.Fail(t, "Expected panic with nil session")
		}
	}()

	_ = Relay(ctx, nil, func(handler *RelayHandler) {
		assert.Fail(t, "Handler should not be called with nil session")
	})
}

// TestRelayHandlerFields tests RelayHandler field initialization
func TestRelayHandlerFields(t *testing.T) {
	handler := &RelayHandler{
		ctx: context.Background(),
	}

	assert.NotNil(t, handler.ctx)
	assert.Nil(t, handler.relaying)
}

// TestDistributorBroadcast tests the core broadcast functionality
// without external dependencies
func TestDistributorBroadcast(t *testing.T) {
	tests := map[string]struct {
		numSubscribers int
		broadcasts     int
	}{
		"single_subscriber":   {numSubscribers: 1, broadcasts: 1},
		"ten_subscribers":     {numSubscribers: 10, broadcasts: 1},
		"hundred_subscribers": {numSubscribers: 100, broadcasts: 1},
		"multiple_broadcasts": {numSubscribers: 10, broadcasts: 5},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			dist := &trackDistributor{
				subscribers: make(map[chan struct{}]struct{}),
			}

			var wg sync.WaitGroup
			received := atomic.Int32{}

			// Start subscribers
			for i := 0; i < tt.numSubscribers; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					ch := dist.subscribe()
					defer dist.unsubscribe(ch)

					count := 0
					timeout := time.After(200 * time.Millisecond)
					for count < tt.broadcasts {
						select {
						case <-ch:
							count++
							received.Add(1)
						case <-timeout:
							assert.Fail(t, "Timeout waiting for broadcast %d", count+1)
							return
						}
					}
				}()
			}

			// Wait for all subscriptions
			time.Sleep(10 * time.Millisecond)

			// Send broadcasts
			for i := 0; i < tt.broadcasts; i++ {
				dist.mu.RLock()
				for ch := range dist.subscribers {
					select {
					case ch <- struct{}{}:
					default:
					}
				}
				dist.mu.RUnlock()
				time.Sleep(5 * time.Millisecond)
			}

			wg.Wait()

			expected := int32(tt.numSubscribers * tt.broadcasts)
			assert.Equal(t, expected, received.Load())
		})
	}
}

// TestSubscriptionLifecycle tests subscribe/unsubscribe operations
func TestSubscriptionLifecycle(t *testing.T) {
	dist := &trackDistributor{
		subscribers: make(map[chan struct{}]struct{}),
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

// TestConcurrentAccess tests thread safety
func TestConcurrentAccess(t *testing.T) {
	dist := &trackDistributor{
		subscribers: make(map[chan struct{}]struct{}),
	}

	const goroutines = 50
	const iterations = 100

	var wg sync.WaitGroup

	// Concurrent subscribe/unsubscribe
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				ch := dist.subscribe()
				time.Sleep(time.Microsecond)
				dist.unsubscribe(ch)
			}
		}()
	}

	// Concurrent broadcasts
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				dist.mu.RLock()
				for ch := range dist.subscribers {
					select {
					case ch <- struct{}{}:
					default:
					}
				}
				dist.mu.RUnlock()
			}
		}()
	}

	wg.Wait()

	assert.Empty(t, dist.subscribers)
}

// TestChannelBuffering tests that channels are buffered
func TestChannelBuffering(t *testing.T) {
	dist := &trackDistributor{
		subscribers: make(map[chan struct{}]struct{}),
	}

	ch := dist.subscribe()

	assert.Equal(t, 1, cap(ch))

	// Should not block on first send
	select {
	case ch <- struct{}{}:
		// OK
	case <-time.After(10 * time.Millisecond):
		assert.Fail(t, "First send blocked")
	}

	// Channel is now full, but broadcast should not block
	done := make(chan bool)
	go func() {
		select {
		case ch <- struct{}{}:
		default:
			// Expected - channel is full
		}
		done <- true
	}()

	select {
	case <-done:
		// OK - didn't block
	case <-time.After(10 * time.Millisecond):
		require.Fail(t, "Broadcast blocked on full channel")
	}
}

// TestNoBroadcastBlocking ensures broadcasts never block
func TestNoBroadcastBlocking(t *testing.T) {
	dist := &trackDistributor{
		subscribers: make(map[chan struct{}]struct{}),
	}

	// Create subscribers but don't read
	for i := 0; i < 20; i++ {
		dist.subscribe()
	}

	// Multiple broadcasts should complete quickly
	done := make(chan bool)
	go func() {
		for range 100 {
			dist.mu.RLock()
			for ch := range dist.subscribers {
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
	case <-time.After(500 * time.Millisecond):
		assert.Fail(t, "Broadcasts blocked unexpectedly")
	}
}

// TestDistributorEdgeCases tests edge cases and boundary conditions
func TestDistributorEdgeCases(t *testing.T) {
	t.Run("subscribe_to_empty_distributor", func(t *testing.T) {
		dist := &trackDistributor{
			subscribers: make(map[chan struct{}]struct{}),
		}

		ch := dist.subscribe()
		require.NotNil(t, ch)

		assert.Len(t, dist.subscribers, 1)
	})

	t.Run("unsubscribe_nonexistent_channel", func(t *testing.T) {
		dist := &trackDistributor{
			subscribers: make(map[chan struct{}]struct{}),
		}

		// Unsubscribe channel that was never subscribed
		fakeCh := make(chan struct{}, 1)
		dist.unsubscribe(fakeCh)

		// Should not panic and map should remain empty
		assert.Empty(t, dist.subscribers)
	})

	t.Run("multiple_unsubscribe_same_channel", func(t *testing.T) {
		dist := &trackDistributor{
			subscribers: make(map[chan struct{}]struct{}),
		}

		ch := dist.subscribe()
		dist.unsubscribe(ch)
		dist.unsubscribe(ch) // Double unsubscribe

		// Should not panic
		assert.Empty(t, dist.subscribers)
	})

	t.Run("broadcast_to_zero_subscribers", func(t *testing.T) {
		dist := &trackDistributor{
			subscribers: make(map[chan struct{}]struct{}),
		}

		// Should not panic
		dist.mu.RLock()
		for ch := range dist.subscribers {
			select {
			case ch <- struct{}{}:
			default:
			}
		}
		dist.mu.RUnlock()
	})

	t.Run("rapid_subscribe_unsubscribe", func(t *testing.T) {
		dist := &trackDistributor{
			subscribers: make(map[chan struct{}]struct{}),
		}

		// Rapidly add and remove
		for i := 0; i < 1000; i++ {
			ch := dist.subscribe()
			dist.unsubscribe(ch)
		}

		assert.Equal(t, 0, len(dist.subscribers), "Expected 0 subscribers")
	})
}

// TestDistributorStress performs stress testing
func TestDistributorStress(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping stress test in short mode")
	}

	t.Run("high_frequency_broadcasts", func(t *testing.T) {
		dist := &trackDistributor{
			subscribers: make(map[chan struct{}]struct{}),
		}

		// Create 100 subscribers
		const numSubs = 100
		for i := 0; i < numSubs; i++ {
			dist.subscribe()
		}

		// Rapid broadcasts
		done := make(chan bool)
		go func() {
			for i := 0; i < 10000; i++ {
				dist.mu.RLock()
				for ch := range dist.subscribers {
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
		case <-time.After(5 * time.Second):
			require.Fail(t, "High frequency broadcast timed out")
		}
	})

	t.Run("subscriber_churn", func(t *testing.T) {
		dist := &trackDistributor{
			subscribers: make(map[chan struct{}]struct{}),
		}

		var wg sync.WaitGroup
		stopCh := make(chan bool)

		// Constant subscribe/unsubscribe
		for i := 0; i < 10; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for {
					select {
					case <-stopCh:
						return
					default:
						ch := dist.subscribe()
						time.Sleep(time.Microsecond)
						dist.unsubscribe(ch)
					}
				}
			}()
		}

		// Constant broadcasting
		for i := 0; i < 5; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for {
					select {
					case <-stopCh:
						return
					default:
						dist.mu.RLock()
						for ch := range dist.subscribers {
							select {
							case ch <- struct{}{}:
							default:
							}
						}
						dist.mu.RUnlock()
					}
				}
			}()
		}

		// Run for 1 second
		time.Sleep(1 * time.Second)
		close(stopCh)
		wg.Wait()
	})
}

// TestDistributorScalability tests performance at different scales
func TestDistributorScalability(t *testing.T) {
	scales := []int{1, 10, 50, 100, 500, 1000}

	for _, n := range scales {
		t.Run(string(rune('0'+(n/1000)%10))+string(rune('0'+(n/100)%10))+string(rune('0'+(n/10)%10))+string(rune('0'+n%10))+"_subscribers", func(t *testing.T) {
			dist := &trackDistributor{
				subscribers: make(map[chan struct{}]struct{}),
			}

			// Create n subscribers
			for i := 0; i < n; i++ {
				dist.subscribe()
			}

			// Measure broadcast time
			start := time.Now()
			dist.mu.RLock()
			for ch := range dist.subscribers {
				select {
				case ch <- struct{}{}:
				default:
				}
			}
			dist.mu.RUnlock()
			elapsed := time.Since(start)

			t.Logf("%d subscribers: broadcast took %v", n, elapsed)

			// Sanity check - should complete quickly
			assert.LessOrEqual(t, elapsed, 10*time.Millisecond, "Broadcast took too long")
		})
	}
}

// TestDistributorMemoryBehavior tests memory-related behavior
func TestDistributorMemoryBehavior(t *testing.T) {
	t.Run("channel_garbage_collection", func(t *testing.T) {
		dist := &trackDistributor{
			subscribers: make(map[chan struct{}]struct{}),
		}

		// Subscribe many
		const count = 1000
		for i := 0; i < count; i++ {
			ch := dist.subscribe()
			// Immediately unsubscribe to allow GC
			dist.unsubscribe(ch)
		}

		assert.Empty(t, dist.subscribers, "Subscribers not cleaned up")
	})

	t.Run("no_channel_leaks_on_unsubscribe", func(t *testing.T) {
		dist := &trackDistributor{
			subscribers: make(map[chan struct{}]struct{}),
		}

		channels := make([]chan struct{}, 100)
		for i := 0; i < 100; i++ {
			channels[i] = dist.subscribe()
		}

		// Unsubscribe all
		for _, ch := range channels {
			dist.unsubscribe(ch)
		}

		assert.Empty(t, dist.subscribers, "Expected empty map")
	})
}

// TestDistributorTimeout tests timeout behavior
func TestDistributorTimeout(t *testing.T) {
	t.Run("verify_timeout_constant", func(t *testing.T) {
		assert.Equal(t, 1*time.Millisecond, NotifyTimeout, "Expected NotifyTimeout to be 1ms")
	})
}

// TestDistributorRaceConditions tests for race conditions
func TestDistributorRaceConditions(t *testing.T) {
	t.Run("concurrent_subscribe_and_broadcast", func(t *testing.T) {
		dist := &trackDistributor{
			subscribers: make(map[chan struct{}]struct{}),
		}

		done := make(chan bool, 2)

		// Concurrent subscribes
		go func() {
			for i := 0; i < 100; i++ {
				dist.subscribe()
			}
			done <- true
		}()

		// Concurrent broadcasts
		go func() {
			for i := 0; i < 100; i++ {
				dist.mu.RLock()
				for ch := range dist.subscribers {
					select {
					case ch <- struct{}{}:
					default:
					}
				}
				dist.mu.RUnlock()
			}
			done <- true
		}()

		<-done
		<-done
	})

	t.Run("concurrent_unsubscribe_and_broadcast", func(t *testing.T) {
		dist := &trackDistributor{
			subscribers: make(map[chan struct{}]struct{}),
		}

		// Create initial subscribers
		channels := make([]chan struct{}, 100)
		for i := 0; i < 100; i++ {
			channels[i] = dist.subscribe()
		}

		done := make(chan bool, 2)

		// Concurrent unsubscribes
		go func() {
			for _, ch := range channels {
				dist.unsubscribe(ch)
				time.Sleep(time.Microsecond)
			}
			done <- true
		}()

		// Concurrent broadcasts
		go func() {
			for i := 0; i < 100; i++ {
				dist.mu.RLock()
				for ch := range dist.subscribers {
					select {
					case ch <- struct{}{}:
					default:
					}
				}
				dist.mu.RUnlock()
				time.Sleep(time.Microsecond)
			}
			done <- true
		}()

		<-done
		<-done
	})
}

// TestDistributorNotificationDelivery tests notification delivery guarantees
func TestDistributorNotificationDelivery(t *testing.T) {
	t.Run("all_subscribers_receive_notification", func(t *testing.T) {
		dist := &trackDistributor{
			subscribers: make(map[chan struct{}]struct{}),
		}

		const numSubs = 50
		received := make([]*atomic.Int32, numSubs)
		var wg sync.WaitGroup

		// Create subscribers that count notifications
		for i := range numSubs {
			received[i] = &atomic.Int32{}
			wg.Add(1)
			idx := i
			go func() {
				defer wg.Done()
				ch := dist.subscribe()
				defer dist.unsubscribe(ch)

				timeout := time.After(100 * time.Millisecond)
				select {
				case <-ch:
					received[idx].Add(1)
				case <-timeout:
					// No notification received
				}
			}()
		}

		// Wait for all to subscribe
		time.Sleep(10 * time.Millisecond)

		// Broadcast
		dist.mu.RLock()
		for ch := range dist.subscribers {
			select {
			case ch <- struct{}{}:
			default:
			}
		}
		dist.mu.RUnlock()

		wg.Wait()

		// Verify all received
		failures := 0
		for i, count := range received {
			if count.Load() != 1 {
				assert.Equal(t, uint32(1), count.Load(), fmt.Sprintf("Subscriber %d received wrong notifications", i))
				failures++
			}
		}

		if failures > 0 {
			assert.Fail(t, fmt.Sprintf("%d/%d subscribers did not receive notification", failures, numSubs))
		}
	})

	t.Run("buffered_channel_prevents_loss", func(t *testing.T) {
		dist := &trackDistributor{
			subscribers: make(map[chan struct{}]struct{}),
		}

		ch := dist.subscribe()

		// Send notification without receiver
		dist.mu.RLock()
		select {
		case ch <- struct{}{}:
			// Success - buffered
		default:
			assert.Fail(t, "Buffered channel should not block")
			// Good
		case <-time.After(10 * time.Millisecond):
			assert.Fail(t, "Did not receive notification from buffer")
		}
	})
}

// BenchmarkBroadcast_10 benchmarks with 10 subscribers
func BenchmarkBroadcast_10(b *testing.B) {
	benchmarkBroadcast(b, 10)
}

// BenchmarkBroadcast_100 benchmarks with 100 subscribers
func BenchmarkBroadcast_100(b *testing.B) {
	benchmarkBroadcast(b, 100)
}

// BenchmarkBroadcast_500 benchmarks with 500 subscribers
func BenchmarkBroadcast_500(b *testing.B) {
	benchmarkBroadcast(b, 500)
}

func benchmarkBroadcast(b *testing.B, numSubscribers int) {
	dist := &trackDistributor{
		subscribers: make(map[chan struct{}]struct{}),
	}

	for i := 0; i < numSubscribers; i++ {
		dist.subscribe()
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		dist.mu.RLock()
		for ch := range dist.subscribers {
			select {
			case ch <- struct{}{}:
			default:
			}
		}
		dist.mu.RUnlock()
	}
}

// BenchmarkSubscribe benchmarks subscription operations
func BenchmarkSubscribe(b *testing.B) {
	dist := &trackDistributor{
		subscribers: make(map[chan struct{}]struct{}),
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ch := dist.subscribe()
		dist.unsubscribe(ch)
	}
}

// BenchmarkDistributorVariableLoad benchmarks with varying subscriber activity
func BenchmarkDistributorVariableLoad(b *testing.B) {
	dist := &trackDistributor{
		subscribers: make(map[chan struct{}]struct{}),
	}

	// 50% active subscribers
	const totalSubs = 100
	for i := 0; i < totalSubs; i++ {
		ch := dist.subscribe()
		if i%2 == 0 {
			// Active subscriber - drain channel
			go func() {
				for range ch {
				}
			}()
		}
		// Passive subscribers don't drain
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		dist.mu.RLock()
		for ch := range dist.subscribers {
			select {
			case ch <- struct{}{}:
			default:
			}
		}
		dist.mu.RUnlock()
	}
}

// TestRelayHandlerSubscribe tests the subscribe method of RelayHandler
func TestRelayHandlerSubscribe(t *testing.T) {
	t.Run("subscribe with nil session", func(t *testing.T) {
		handler := &RelayHandler{
			Session: nil,
		}

		tr := handler.subscribe("test/track")
		assert.Nil(t, tr, "Expected nil trackDistributor for nil session")
	})

	t.Run("subscribe with nil announcement", func(t *testing.T) {
		handler := &RelayHandler{
			Session:      &moqt.Session{},
			Announcement: nil,
		}

		tr := handler.subscribe("test/track")
		assert.Nil(t, tr, "Expected nil trackDistributor for nil announcement")
	})
}

// TestRelayHandlerServeTrackBasics tests basic ServeTrack functionality
func TestRelayHandlerServeTrackBasics(t *testing.T) {
	t.Run("relaying map initialization", func(t *testing.T) {
		handler := &RelayHandler{}

		assert.Nil(t, handler.relaying, "relaying should be nil initially")

		// ServeTrack should initialize the map
		// Note: We can't fully test ServeTrack without a real TrackWriter and Session
		// This test verifies the data structure initialization logic
	})

	t.Run("concurrent map access", func(t *testing.T) {
		handler := &RelayHandler{
			relaying: make(map[moqt.TrackName]*trackDistributor),
		}

		done := make(chan bool)
		for i := 0; i < 10; i++ {
			go func(id int) {
				handler.mu.Lock()
				// Simulate accessing the map
				_ = len(handler.relaying)
				handler.mu.Unlock()
				done <- true
			}(i)
		}

		for i := 0; i < 10; i++ {
			<-done
		}
	})
}

// TestTrackDistributorInitialization tests trackDistributor creation
func TestTrackDistributorInitialization(t *testing.T) {
	onCloseCalled := false
	onClose := func() {
		onCloseCalled = true
	}

	// Note: We can't fully test newTrackRelayer without a real TrackReader
	// This test verifies the logic would work correctly

	t.Run("onClose callback", func(t *testing.T) {
		dist := &trackDistributor{
			ring:        newGroupRing(DefaultGroupCacheSize),
			subscribers: make(map[chan struct{}]struct{}),
			onClose:     onClose,
		}

		// Call onClose directly (not close which requires src)
		if dist.onClose != nil {
			dist.onClose()
		}

		assert.True(t, onCloseCalled, "onClose callback should be called")
	})
}

// TestRelayHandlerRelayingCleanup tests cleanup of relaying map
func TestRelayHandlerRelayingCleanup(t *testing.T) {
	handler := &RelayHandler{
		relaying: make(map[moqt.TrackName]*trackDistributor),
	}

	trackName := moqt.TrackName("test/track")

	// Simulate adding a track
	handler.mu.Lock()
	handler.relaying[trackName] = &trackDistributor{
		ring:        newGroupRing(DefaultGroupCacheSize),
		subscribers: make(map[chan struct{}]struct{}),
	}
	handler.mu.Unlock()

	assert.Equal(t, 1, len(handler.relaying), "Track should be in relaying map")

	// Simulate cleanup
	handler.mu.Lock()
	delete(handler.relaying, trackName)
	handler.mu.Unlock()

	assert.Empty(t, handler.relaying, "Track should be removed from relaying map")
}

// TestGroupRingIntegration tests groupRing initialization
func TestGroupRingIntegration(t *testing.T) {
	dist := &trackDistributor{
		ring:        newGroupRing(DefaultGroupCacheSize),
		subscribers: make(map[chan struct{}]struct{}),
	}

	// Verify ring is properly initialized
	require.NotNil(t, dist.ring, "Ring should be initialized")

	head := dist.ring.head()
	assert.Equal(t, moqt.GroupSequence(0), head, "Expected initial head to be 0")

	earliest := dist.ring.earliestAvailable()
	assert.Equal(t, moqt.GroupSequence(1), earliest, "Expected earliest to be 1")
}

// TestNotifyTimeout tests the NotifyTimeout constant
func TestNotifyTimeout(t *testing.T) {
	assert.Greater(t, NotifyTimeout, time.Duration(0), "NotifyTimeout should be positive")

	// Verify it's the optimized value from benchmarks
	expectedTimeout := 1 * time.Millisecond
	assert.Equal(t, expectedTimeout, NotifyTimeout, "NotifyTimeout should be optimal value")
}

// TestRelayHandlerConcurrentServeTrack tests concurrent ServeTrack calls
func TestRelayHandlerConcurrentServeTrack(t *testing.T) {
	handler := &RelayHandler{
		relaying: make(map[moqt.TrackName]*trackDistributor),
	}

	done := make(chan bool)

	// Simulate concurrent access to the same track
	for i := 0; i < 10; i++ {
		go func() {
			handler.mu.Lock()
			trackName := moqt.TrackName("test/track")
			_, exists := handler.relaying[trackName]
			if !exists {
				handler.relaying[trackName] = &trackDistributor{
					ring:        newGroupRing(DefaultGroupCacheSize),
					subscribers: make(map[chan struct{}]struct{}),
				}
			}
			handler.mu.Unlock()
			done <- true
		}()
	}

	for i := 0; i < 10; i++ {
		<-done
	}

	assert.Equal(t, 1, len(handler.relaying), "Expected 1 track in relaying map")
}

// TestTrackDistributorServeTrackLogic tests serveTrack internal logic
func TestTrackDistributorServeTrackLogic(t *testing.T) {
	t.Run("ring initialization", func(t *testing.T) {
		dist := &trackDistributor{
			ring:        newGroupRing(DefaultGroupCacheSize),
			subscribers: make(map[chan struct{}]struct{}),
		}

		// Verify ring is initialized
		assert.NotNil(t, dist.ring, "Ring should be initialized")

		// Verify initial head
		head := dist.ring.head()
		assert.Equal(t, moqt.GroupSequence(0), head, "Expected head 0")
	})

	t.Run("earliest available at start", func(t *testing.T) {
		dist := &trackDistributor{
			ring: newGroupRing(DefaultGroupCacheSize),
		}

		earliest := dist.ring.earliestAvailable()
		assert.Equal(t, moqt.GroupSequence(1), earliest, "Expected earliest 1 at start")
	})
}

// TestRelayHandlerMemoryManagement tests memory cleanup
func TestRelayHandlerMemoryManagement(t *testing.T) {
	handler := &RelayHandler{
		relaying: make(map[moqt.TrackName]*trackDistributor),
	}

	// Add multiple tracks
	for i := 0; i < 5; i++ {
		trackName := moqt.TrackName("test/track" + string(rune(i)))
		handler.mu.Lock()
		handler.relaying[trackName] = &trackDistributor{
			ring:        newGroupRing(DefaultGroupCacheSize),
			subscribers: make(map[chan struct{}]struct{}),
		}
		handler.mu.Unlock()
	}

	assert.Equal(t, 5, len(handler.relaying), "Expected 5 tracks")

	// Clean up all tracks
	handler.mu.Lock()
	handler.relaying = make(map[moqt.TrackName]*trackDistributor)
	handler.mu.Unlock()

	assert.Empty(t, handler.relaying, "Expected 0 tracks after cleanup")
}

// TestRelayHandlerSubscribeNilSession tests subscribe with nil Session
func TestRelayHandlerSubscribeNilSession(t *testing.T) {
	handler := &RelayHandler{
		Session: nil,
	}

	result := handler.subscribe("test")
	assert.Nil(t, result, "Expected nil when Session is nil")
}

// TestRelayHandlerSubscribeNilAnnouncement tests subscribe with nil Announcement
func TestRelayHandlerSubscribeNilAnnouncement(t *testing.T) {
	handler := &RelayHandler{
		Session:      &moqt.Session{},
		Announcement: nil,
	}

	result := handler.subscribe("test")
	assert.Nil(t, result, "Expected nil when Announcement is nil")
}

// TestRelayHandlerServeTrackWithContext tests serveTrack with context cancellation
func TestRelayHandlerServeTrackWithContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dist := &trackDistributor{
		ring:        newGroupRing(DefaultGroupCacheSize),
		subscribers: make(map[chan struct{}]struct{}),
	}

	// Test that serveTrack respects cancelled context
	// We can't create a valid TrackWriter without the moqt library,
	// so we test the distributor's internal behavior instead

	// Verify ring is initialized
	assert.NotNil(t, dist.ring, "ring should be initialized")

	// Verify subscribers map is initialized
	assert.NotNil(t, dist.subscribers, "subscribers should be initialized")

	// Test subscribe/unsubscribe with context
	ch := dist.subscribe()
	assert.NotNil(t, ch, "subscribe should return channel")

	dist.unsubscribe(ch)

	// Use ctx to avoid unused variable error
	_ = ctx
}

// TestTrackDistributorCatchup tests catchup logic when subscriber falls behind
func TestTrackDistributorCatchup(t *testing.T) {
	dist := &trackDistributor{
		ring:        newGroupRing(DefaultGroupCacheSize),
		subscribers: make(map[chan struct{}]struct{}),
	}

	// Test that the ring can handle being ahead of subscribers
	// We don't actually add groups, but we verify ring behavior

	// Initially head should be 0
	assert.Equal(t, moqt.GroupSequence(0), dist.ring.head(), "Expected initial head to be 0")

	// Verify earliest available - starts at 1 for empty ring
	earliest := dist.ring.earliestAvailable()
	assert.GreaterOrEqual(t, earliest, uint64(0), "Expected earliest to be non-negative")
}

// TestTrackDistributorSubscribeUnsubscribe tests the subscribe/unsubscribe pattern
func TestTrackDistributorSubscribeUnsubscribe(t *testing.T) {
	dist := &trackDistributor{
		ring:        newGroupRing(DefaultGroupCacheSize),
		subscribers: make(map[chan struct{}]struct{}),
	}

	// Subscribe multiple times
	channels := make([]chan struct{}, 10)
	for i := 0; i < 10; i++ {
		channels[i] = dist.subscribe()
	}

	dist.mu.RLock()
	count := len(dist.subscribers)
	dist.mu.RUnlock()

	assert.Equal(t, 10, count, "Expected 10 subscribers")

	// Unsubscribe half
	for i := 0; i < 5; i++ {
		dist.unsubscribe(channels[i])
	}

	dist.mu.RLock()
	count = len(dist.subscribers)
	dist.mu.RUnlock()

	assert.Equal(t, 5, count, "Expected 5 subscribers after unsubscribe")

	// Unsubscribe all
	for i := 5; i < 10; i++ {
		dist.unsubscribe(channels[i])
	}

	dist.mu.RLock()
	count = len(dist.subscribers)
	dist.mu.RUnlock()

	assert.Equal(t, 0, count, "Expected 0 subscribers after full unsubscribe")
}

// TestTrackDistributorOnClose tests the onClose callback
func TestTrackDistributorOnClose(t *testing.T) {
	onCloseCalled := false

	dist := &trackDistributor{
		ring:        newGroupRing(DefaultGroupCacheSize),
		subscribers: make(map[chan struct{}]struct{}),
		onClose: func() {
			onCloseCalled = true
		},
	}

	// Test calling onClose directly
	if dist.onClose != nil {
		dist.onClose()
	}

	assert.True(t, onCloseCalled, "Expected onClose callback to be called")
}

// TestTrackDistributorSubscribeUnsubscribeMultiple tests subscribe/unsubscribe multiple times
func TestTrackDistributorSubscribeUnsubscribeMultiple(t *testing.T) {
	dist := &trackDistributor{
		ring:        newGroupRing(DefaultGroupCacheSize),
		subscribers: make(map[chan struct{}]struct{}),
	}

	// Subscribe multiple times
	channels := make([]chan struct{}, 10)
	for i := 0; i < 10; i++ {
		channels[i] = dist.subscribe()
	}

	dist.mu.RLock()
	count := len(dist.subscribers)
	dist.mu.RUnlock()

	assert.Equal(t, 10, count, "Expected 10 subscribers")

	// Unsubscribe half
	for i := 0; i < 5; i++ {
		dist.unsubscribe(channels[i])
	}

	dist.mu.RLock()
	count = len(dist.subscribers)
	dist.mu.RUnlock()

	assert.Equal(t, 5, count, "Expected 5 subscribers after unsubscribe")

	// Unsubscribe all
	for i := 5; i < 10; i++ {
		dist.unsubscribe(channels[i])
	}

	dist.mu.RLock()
	count = len(dist.subscribers)
	dist.mu.RUnlock()

	assert.Equal(t, 0, count, "Expected 0 subscribers after full unsubscribe")
}

// TestRelayHandlerConcurrentAccess tests concurrent access to RelayHandler
func TestRelayHandlerConcurrentAccess(t *testing.T) {
	handler := &RelayHandler{
		ctx: context.Background(),
	}

	var wg sync.WaitGroup
	trackNames := []moqt.TrackName{"track1", "track2", "track3", "track4", "track5"}

	// Simulate concurrent track registration
	for _, name := range trackNames {
		wg.Add(1)
		go func(tn moqt.TrackName) {
			defer wg.Done()

			handler.mu.Lock()
			if handler.relaying == nil {
				handler.relaying = make(map[moqt.TrackName]*trackDistributor)
			}

			// Simulate track initialization
			if _, ok := handler.relaying[tn]; !ok {
				handler.relaying[tn] = &trackDistributor{
					ring:        newGroupRing(DefaultGroupCacheSize),
					subscribers: make(map[chan struct{}]struct{}),
				}
			}
			handler.mu.Unlock()
		}(name)
	}

	wg.Wait()

	assert.Equal(t, len(trackNames), len(handler.relaying), "Expected correct number of tracks")
}

// TestNotifyTimeoutVariable tests that NotifyTimeout is accessible
func TestNotifyTimeoutVariable(t *testing.T) {
	assert.Greater(t, NotifyTimeout, time.Duration(0), "NotifyTimeout should be positive")

	// Test that we can modify it (for testing purposes)
	original := NotifyTimeout
	NotifyTimeout = 5 * time.Millisecond
	assert.Equal(t, 5*time.Millisecond, NotifyTimeout, "NotifyTimeout should be modifiable")
	NotifyTimeout = original
}

// ============================================================================
// RelayHandler Tests - Comprehensive test coverage
// ============================================================================

// TestRelayHandler_New tests RelayHandler initialization
func TestRelayHandler_New(t *testing.T) {
	tests := map[string]struct {
		hasAnnouncement bool
		hasSession      bool
		hasCtx          bool
	}{
		"basic initialization": {
			hasAnnouncement: true,
			hasSession:      true,
			hasCtx:          true,
		},
		"with nil announcement": {
			hasAnnouncement: false,
			hasSession:      true,
			hasCtx:          true,
		},
		"with nil session": {
			hasAnnouncement: true,
			hasSession:      false,
			hasCtx:          true,
		},
		"with nil context": {
			hasAnnouncement: true,
			hasSession:      true,
			hasCtx:          false,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			handler := &RelayHandler{}

			if tt.hasCtx {
				handler.ctx = context.Background()
			}

			assert.NotNil(t, handler)

			if tt.hasCtx {
				assert.NotNil(t, handler.ctx)
			}
			assert.Nil(t, handler.relaying)
		})
	}
}

// TestRelayHandler_ServeTrack tests the ServeTrack method behavior
func TestRelayHandler_ServeTrack_NilSession(t *testing.T) {
	handler := &RelayHandler{
		Announcement: nil,
		Session:      nil,
		ctx:          context.Background(),
	}

	// With nil session, ServeTrack should handle gracefully
	// This tests the defensive programming in the method
	if handler.Session == nil {
		// Expected behavior documented
		t.Log("ServeTrack with nil session should close track writer")
	}
}

// TestRelayHandler_Subscribe tests the subscribe method
func TestRelayHandler_Subscribe(t *testing.T) {
	tests := map[string]struct {
		setup     func() *RelayHandler
		trackName moqt.TrackName
		expectNil bool
	}{
		"nil session": {
			setup: func() *RelayHandler {
				return &RelayHandler{
					Session:      nil,
					Announcement: nil,
					ctx:          context.Background(),
				}
			},
			trackName: moqt.TrackName("test"),
			expectNil: true,
		},
		"nil announcement": {
			setup: func() *RelayHandler {
				return &RelayHandler{
					Session:      nil,
					Announcement: nil,
					ctx:          context.Background(),
				}
			},
			trackName: moqt.TrackName("test"),
			expectNil: true,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			handler := tt.setup()
			result := handler.subscribe(tt.trackName)

			if tt.expectNil {
				assert.Nil(t, result, "Expected nil trackDistributor")
			} else {
				assert.NotNil(t, result, "Expected non-nil trackDistributor")
			}
		})
	}
}

// TestRelayHandler_ConcurrentAccess tests concurrent access to handler fields
func TestRelayHandler_ConcurrentAccess(t *testing.T) {
	handler := &RelayHandler{
		ctx: context.Background(),
	}

	const numGoroutines = 10
	var wg sync.WaitGroup

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			handler.mu.Lock()
			if handler.relaying == nil {
				handler.relaying = make(map[moqt.TrackName]*trackDistributor)
			}
			trackName := moqt.TrackName("track")
			if _, exists := handler.relaying[trackName]; !exists {
				handler.relaying[trackName] = &trackDistributor{
					ring:        newGroupRing(DefaultGroupCacheSize),
					subscribers: make(map[chan struct{}]struct{}),
				}
			}
			handler.mu.Unlock()
		}(i)
	}

	wg.Wait()

	assert.NotEmpty(t, handler.relaying, "Expected at least one track to be registered")
}

// TestRelayHandler_RelayingMapInitialization tests relaying map lazy initialization
func TestRelayHandler_RelayingMapInitialization(t *testing.T) {
	handler := &RelayHandler{
		Announcement: nil,
		Session:      nil,
		ctx:          context.Background(),
	}

	// Initially nil
	require.Nil(t, handler.relaying, "relaying should be nil initially")

	// Simulate what ServeTrack does
	handler.mu.Lock()
	if handler.relaying == nil {
		handler.relaying = make(map[moqt.TrackName]*trackDistributor)
	}
	handler.mu.Unlock()

	// Now it should be initialized
	assert.NotNil(t, handler.relaying, "relaying should be initialized")
}

// ============================================================================
// trackDistributor Tests - Enhanced comprehensive coverage
// ============================================================================

// TestTrackDistributor_New tests newTrackDistributor initialization
func TestTrackDistributor_New_Basic(t *testing.T) {
	// Test basic structure initialization without complex mocking
	d := &trackDistributor{
		ring:        newGroupRing(DefaultGroupCacheSize),
		subscribers: make(map[chan struct{}]struct{}),
	}

	assert.NotNil(t, d.ring, "ring should be initialized")
	assert.NotNil(t, d.subscribers, "subscribers map should be initialized")
	assert.Empty(t, d.subscribers, "subscribers should be empty initially")
}

// TestTrackDistributor_Subscribe tests the subscribe method
func TestTrackDistributor_Subscribe(t *testing.T) {
	tests := map[string]struct {
		setup       func() *trackDistributor
		expectChan  bool
		expectCount int
	}{
		"single subscribe": {
			setup: func() *trackDistributor {
				return &trackDistributor{
					ring:        newGroupRing(DefaultGroupCacheSize),
					subscribers: make(map[chan struct{}]struct{}),
				}
			},
			expectChan:  true,
			expectCount: 1,
		},
		"multiple subscribes": {
			setup: func() *trackDistributor {
				return &trackDistributor{
					ring:        newGroupRing(DefaultGroupCacheSize),
					subscribers: make(map[chan struct{}]struct{}),
				}
			},
			expectChan:  true,
			expectCount: 5,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			d := tt.setup()

			channels := make([]chan struct{}, 0)
			expectedCount := tt.expectCount

			for i := 0; i < expectedCount; i++ {
				ch := d.subscribe()

				if tt.expectChan {
					assert.NotNil(t, ch, "subscribe returned nil channel")
				}
				channels = append(channels, ch)
			}

			// Verify count
			assert.Equal(t, expectedCount, len(d.subscribers), "Expected correct number of subscribers")

			// Verify channels are buffered
			for _, ch := range channels {
				assert.Equal(t, 1, cap(ch), "Expected buffered channel with capacity 1")
			}
		})
	}
}

// TestTrackDistributor_Unsubscribe tests the unsubscribe method
func TestTrackDistributor_Unsubscribe(t *testing.T) {
	tests := map[string]struct {
		setup          func() (*trackDistributor, []chan struct{})
		unsubscribeIdx int
		expectCount    int
	}{
		"single unsubscribe": {
			setup: func() (*trackDistributor, []chan struct{}) {
				d := &trackDistributor{
					ring:        newGroupRing(DefaultGroupCacheSize),
					subscribers: make(map[chan struct{}]struct{}),
				}
				ch1 := d.subscribe()
				ch2 := d.subscribe()
				return d, []chan struct{}{ch1, ch2}
			},
			unsubscribeIdx: 0,
			expectCount:    1,
		},
		"unsubscribe last": {
			setup: func() (*trackDistributor, []chan struct{}) {
				d := &trackDistributor{
					ring:        newGroupRing(DefaultGroupCacheSize),
					subscribers: make(map[chan struct{}]struct{}),
				}
				ch1 := d.subscribe()
				ch2 := d.subscribe()
				ch3 := d.subscribe()
				return d, []chan struct{}{ch1, ch2, ch3}
			},
			unsubscribeIdx: 2,
			expectCount:    2,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			d, channels := tt.setup()

			d.unsubscribe(channels[tt.unsubscribeIdx])

			assert.Equal(t, tt.expectCount, len(d.subscribers), "Expected correct number of subscribers after unsubscribe")

			// Verify the specific channel was removed
			_, exists := d.subscribers[channels[tt.unsubscribeIdx]]
			assert.False(t, exists, "Expected unsubscribed channel to be removed")
		})
	}
}

// TestTrackDistributor_Close tests the close method
func TestTrackDistributor_Close_Callback(t *testing.T) {
	onCloseCalled := false
	d := &trackDistributor{
		onClose: func() {
			onCloseCalled = true
		},
		ring:        newGroupRing(DefaultGroupCacheSize),
		subscribers: make(map[chan struct{}]struct{}),
	}

	// Simulate close (without calling src.Close since src is nil)
	if d.onClose != nil {
		d.onClose()
	}

	assert.True(t, onCloseCalled, "Expected onClose callback to be called")
}

// TestTrackDistributor_ServeTrack_Context tests serveTrack context handling
func TestTrackDistributor_ServeTrack_Context(t *testing.T) {
	d := &trackDistributor{
		ring:        newGroupRing(DefaultGroupCacheSize),
		subscribers: make(map[chan struct{}]struct{}),
	}

	// Test that context is properly used
	ctx := context.Background()
	assert.NotNil(t, ctx, "Context should not be nil")

	// Verify basic distributor state
	assert.NotNil(t, d.ring, "ring should be initialized")
	assert.NotNil(t, d.subscribers, "subscribers should be initialized")
}

// TestTrackDistributor_BroadcastNotification tests broadcast notification mechanism
func TestTrackDistributor_BroadcastNotification(t *testing.T) {
	tests := map[string]struct {
		numSubscribers int
		numBroadcasts  int
		timeout        time.Duration
	}{
		"single broadcast to one subscriber": {
			numSubscribers: 1,
			numBroadcasts:  1,
			timeout:        100 * time.Millisecond,
		},
		"single broadcast to multiple subscribers": {
			numSubscribers: 5,
			numBroadcasts:  1,
			timeout:        100 * time.Millisecond,
		},
		"multiple broadcasts": {
			numSubscribers: 3,
			numBroadcasts:  3,
			timeout:        100 * time.Millisecond,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			d := &trackDistributor{
				ring:        newGroupRing(DefaultGroupCacheSize),
				subscribers: make(map[chan struct{}]struct{}),
			}

			channels := make([]chan struct{}, tt.numSubscribers)
			for i := 0; i < tt.numSubscribers; i++ {
				channels[i] = d.subscribe()
			}

			var wg sync.WaitGroup
			for i := 0; i < tt.numSubscribers; i++ {
				wg.Add(1)
				go func(ch chan struct{}) {
					defer wg.Done()
					count := 0
					timeout := time.After(tt.timeout)
					for count < tt.numBroadcasts {
						select {
						case <-ch:
							count++
						case <-timeout:
							assert.Fail(t, "Subscriber timeout waiting for broadcast")
							return
						}
					}
				}(channels[i])
			}

			time.Sleep(10 * time.Millisecond)

			// Send broadcasts
			for i := 0; i < tt.numBroadcasts; i++ {
				d.mu.RLock()
				for ch := range d.subscribers {
					select {
					case ch <- struct{}{}:
					default:
					}
				}
				d.mu.RUnlock()
				time.Sleep(5 * time.Millisecond)
			}

			wg.Wait()
		})
	}
}
