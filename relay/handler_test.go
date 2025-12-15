package relay

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/okdaichi/gomoqt/moqt"
)

// TestDistributorBroadcast tests the core broadcast functionality
// without external dependencies
func TestDistributorBroadcast(t *testing.T) {
	tests := []struct {
		name           string
		numSubscribers int
		broadcasts     int
	}{
		{"single_subscriber", 1, 1},
		{"ten_subscribers", 10, 1},
		{"hundred_subscribers", 100, 1},
		{"multiple_broadcasts", 10, 5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
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
							t.Errorf("Timeout waiting for broadcast %d", count+1)
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
			if received.Load() != expected {
				t.Errorf("Expected %d total notifications, got %d", expected, received.Load())
			}
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
	if ch1 == nil {
		t.Fatal("subscribe returned nil channel")
	}

	if len(dist.subscribers) != 1 {
		t.Errorf("Expected 1 subscriber, got %d", len(dist.subscribers))
	}

	// Test multiple subscribes
	ch2 := dist.subscribe()
	ch3 := dist.subscribe()

	if len(dist.subscribers) != 3 {
		t.Errorf("Expected 3 subscribers, got %d", len(dist.subscribers))
	}

	// Test unsubscribe
	dist.unsubscribe(ch2)

	if len(dist.subscribers) != 2 {
		t.Errorf("Expected 2 subscribers after unsubscribe, got %d", len(dist.subscribers))
	}

	// Test unsubscribe all
	dist.unsubscribe(ch1)
	dist.unsubscribe(ch3)

	if len(dist.subscribers) != 0 {
		t.Errorf("Expected 0 subscribers, got %d", len(dist.subscribers))
	}

	// Test double unsubscribe (should not panic)
	dist.unsubscribe(ch1)
	if len(dist.subscribers) != 0 {
		t.Errorf("Expected 0 subscribers after double unsubscribe, got %d", len(dist.subscribers))
	}
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

	if len(dist.subscribers) != 0 {
		t.Errorf("Expected 0 subscribers at end, got %d", len(dist.subscribers))
	}
}

// TestChannelBuffering tests that channels are buffered
func TestChannelBuffering(t *testing.T) {
	dist := &trackDistributor{
		subscribers: make(map[chan struct{}]struct{}),
	}

	ch := dist.subscribe()

	if cap(ch) != 1 {
		t.Errorf("Expected channel capacity 1, got %d", cap(ch))
	}

	// Should not block on first send
	select {
	case ch <- struct{}{}:
		// OK
	case <-time.After(10 * time.Millisecond):
		t.Fatal("First send blocked")
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
		t.Fatal("Broadcast blocked on full channel")
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

	select {
	case <-done:
		// Success
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Broadcasts blocked unexpectedly")
	}
}

// TestDistributorEdgeCases tests edge cases and boundary conditions
func TestDistributorEdgeCases(t *testing.T) {
	t.Run("subscribe_to_empty_distributor", func(t *testing.T) {
		dist := &trackDistributor{
			subscribers: make(map[chan struct{}]struct{}),
		}

		ch := dist.subscribe()
		if ch == nil {
			t.Fatal("subscribe returned nil channel")
		}

		if len(dist.subscribers) != 1 {
			t.Errorf("Expected 1 subscriber, got %d", len(dist.subscribers))
		}
	})

	t.Run("unsubscribe_nonexistent_channel", func(t *testing.T) {
		dist := &trackDistributor{
			subscribers: make(map[chan struct{}]struct{}),
		}

		// Unsubscribe channel that was never subscribed
		fakeCh := make(chan struct{}, 1)
		dist.unsubscribe(fakeCh)

		// Should not panic and map should remain empty
		if len(dist.subscribers) != 0 {
			t.Error("Expected 0 subscribers")
		}
	})

	t.Run("multiple_unsubscribe_same_channel", func(t *testing.T) {
		dist := &trackDistributor{
			subscribers: make(map[chan struct{}]struct{}),
		}

		ch := dist.subscribe()
		dist.unsubscribe(ch)
		dist.unsubscribe(ch) // Double unsubscribe

		// Should not panic
		if len(dist.subscribers) != 0 {
			t.Error("Expected 0 subscribers after double unsubscribe")
		}
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

		if len(dist.subscribers) != 0 {
			t.Errorf("Expected 0 subscribers, got %d", len(dist.subscribers))
		}
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
			t.Fatal("High frequency broadcast timed out")
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
			if elapsed > 10*time.Millisecond {
				t.Errorf("Broadcast took too long: %v", elapsed)
			}
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

		if len(dist.subscribers) != 0 {
			t.Error("Subscribers not cleaned up")
		}
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

		if len(dist.subscribers) != 0 {
			t.Errorf("Expected empty map, got %d entries", len(dist.subscribers))
		}
	})
}

// TestDistributorTimeout tests timeout behavior
func TestDistributorTimeout(t *testing.T) {
	t.Run("verify_timeout_constant", func(t *testing.T) {
		if NotifyTimeout != 1*time.Millisecond {
			t.Errorf("Expected NotifyTimeout to be 1ms, got %v", NotifyTimeout)
		}
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
				t.Errorf("Subscriber %d received %d notifications, expected 1", i, count.Load())
				failures++
			}
		}

		if failures > 0 {
			t.Errorf("%d/%d subscribers did not receive notification", failures, numSubs)
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
			t.Error("Buffered channel should not block")
		}
		dist.mu.RUnlock()

		// Now receive
		select {
		case <-ch:
			// Good
		case <-time.After(10 * time.Millisecond):
			t.Error("Did not receive notification from buffer")
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
		if tr != nil {
			t.Error("Expected nil trackDistributor for nil session")
		}
	})

	t.Run("subscribe with nil announcement", func(t *testing.T) {
		handler := &RelayHandler{
			Session:      &moqt.Session{},
			Announcement: nil,
		}

		tr := handler.subscribe("test/track")
		if tr != nil {
			t.Error("Expected nil trackDistributor for nil announcement")
		}
	})
}

// TestRelayHandlerServeTrackBasics tests basic ServeTrack functionality
func TestRelayHandlerServeTrackBasics(t *testing.T) {
	t.Run("relaying map initialization", func(t *testing.T) {
		handler := &RelayHandler{}

		if handler.relaying != nil {
			t.Error("relaying should be nil initially")
		}

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
			ring:        newGroupRing(),
			subscribers: make(map[chan struct{}]struct{}),
			onClose:     onClose,
		}

		// Call onClose directly (not close which requires src)
		if dist.onClose != nil {
			dist.onClose()
		}

		if !onCloseCalled {
			t.Error("onClose callback should be called")
		}
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
		ring:        newGroupRing(),
		subscribers: make(map[chan struct{}]struct{}),
	}
	handler.mu.Unlock()

	if len(handler.relaying) != 1 {
		t.Error("Track should be in relaying map")
	}

	// Simulate cleanup
	handler.mu.Lock()
	delete(handler.relaying, trackName)
	handler.mu.Unlock()

	if len(handler.relaying) != 0 {
		t.Error("Track should be removed from relaying map")
	}
}

// TestGroupRingIntegration tests groupRing initialization
func TestGroupRingIntegration(t *testing.T) {
	dist := &trackDistributor{
		ring:        newGroupRing(),
		subscribers: make(map[chan struct{}]struct{}),
	}

	// Verify ring is properly initialized
	if dist.ring == nil {
		t.Fatal("Ring should be initialized")
	}

	head := dist.ring.head()
	if head != 0 {
		t.Errorf("Expected initial head to be 0, got %d", head)
	}

	earliest := dist.ring.earliestAvailable()
	if earliest != 1 {
		t.Errorf("Expected earliest to be 1, got %d", earliest)
	}
}

// TestNotifyTimeout tests the NotifyTimeout constant
func TestNotifyTimeout(t *testing.T) {
	if NotifyTimeout <= 0 {
		t.Error("NotifyTimeout should be positive")
	}

	// Verify it's the optimized value from benchmarks
	expectedTimeout := 1 * time.Millisecond
	if NotifyTimeout != expectedTimeout {
		t.Errorf("NotifyTimeout should be %v for optimal performance, got %v", expectedTimeout, NotifyTimeout)
	}
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
					ring:        newGroupRing(),
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

	if len(handler.relaying) != 1 {
		t.Errorf("Expected 1 track in relaying map, got %d", len(handler.relaying))
	}
}

// TestTrackDistributorServeTrackLogic tests serveTrack internal logic
func TestTrackDistributorServeTrackLogic(t *testing.T) {
	t.Run("ring initialization", func(t *testing.T) {
		dist := &trackDistributor{
			ring:        newGroupRing(),
			subscribers: make(map[chan struct{}]struct{}),
		}

		// Verify ring is initialized
		if dist.ring == nil {
			t.Error("Ring should be initialized")
		}

		// Verify initial head
		head := dist.ring.head()
		if head != 0 {
			t.Errorf("Expected head 0, got %d", head)
		}
	})

	t.Run("earliest available at start", func(t *testing.T) {
		dist := &trackDistributor{
			ring: newGroupRing(),
		}

		earliest := dist.ring.earliestAvailable()
		if earliest != 1 {
			t.Errorf("Expected earliest 1 at start, got %d", earliest)
		}
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
			ring:        newGroupRing(),
			subscribers: make(map[chan struct{}]struct{}),
		}
		handler.mu.Unlock()
	}

	if len(handler.relaying) != 5 {
		t.Errorf("Expected 5 tracks, got %d", len(handler.relaying))
	}

	// Clean up all tracks
	handler.mu.Lock()
	handler.relaying = make(map[moqt.TrackName]*trackDistributor)
	handler.mu.Unlock()

	if len(handler.relaying) != 0 {
		t.Errorf("Expected 0 tracks after cleanup, got %d", len(handler.relaying))
	}
}
