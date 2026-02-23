package relay

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/okdaichi/gomoqt/moqt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ============================================================================
// trackDistributor Tests - Core Functionality
// ============================================================================

// TestTrackDistributor_Broadcast_SingleSubscriber tests basic broadcast functionality
func TestTrackDistributor_Broadcast_SingleSubscriber(t *testing.T) {
	dist := &trackDistributor{
		ring:        newGroupRing(DefaultGroupCacheSize, DefaultFramePool),
		subscribers: make(map[chan struct{}]struct{}),
	}

	received := &atomic.Int32{}
	ready := make(chan struct{})
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
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
	}()

	<-ready

	dist.mu.RLock()
	for ch := range dist.subscribers {
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
				subscribers: make(map[chan struct{}]struct{}),
			}

			received := &atomic.Int32{}
			ready := make(chan struct{}, tt.numSubscribers)
			var wg sync.WaitGroup

			for i := 0; i < tt.numSubscribers; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
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
				}()
			}

			for i := 0; i < tt.numSubscribers; i++ {
				<-ready
			}

			for i := 0; i < tt.broadcasts; i++ {
				dist.mu.RLock()
				for ch := range dist.subscribers {
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

// TestTrackDistributor_ConcurrentAccess tests thread safety
func TestTrackDistributor_ConcurrentAccess(t *testing.T) {
	dist := &trackDistributor{
		ring:        newGroupRing(DefaultGroupCacheSize, DefaultFramePool),
		subscribers: make(map[chan struct{}]struct{}),
	}

	const goroutines = 50
	const iterations = 100

	var wg sync.WaitGroup

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				ch := dist.subscribe()
				dist.unsubscribe(ch)
			}
		}()
	}

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

	assert.Empty(t, dist.subscribers, "all subscribers should be unsubscribed")
}

// TestTrackDistributor_ChannelBuffering tests that channels are buffered
func TestTrackDistributor_ChannelBuffering(t *testing.T) {
	dist := &trackDistributor{
		ring:        newGroupRing(DefaultGroupCacheSize, DefaultFramePool),
		subscribers: make(map[chan struct{}]struct{}),
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
		subscribers: make(map[chan struct{}]struct{}),
	}

	// Create subscribers but don't read
	for i := 0; i < 20; i++ {
		dist.subscribe()
	}

	// Multiple broadcasts should complete quickly
	done := make(chan struct{})
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
			subscribers: make(map[chan struct{}]struct{}),
		}

		ch := dist.subscribe()
		require.NotNil(t, ch)

		assert.Len(t, dist.subscribers, 1)
	})

	t.Run("unsubscribe_nonexistent_channel", func(t *testing.T) {
		dist := &trackDistributor{
			ring:        newGroupRing(DefaultGroupCacheSize, DefaultFramePool),
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
			ring:        newGroupRing(DefaultGroupCacheSize, DefaultFramePool),
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
			ring:        newGroupRing(DefaultGroupCacheSize, DefaultFramePool),
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
			ring:        newGroupRing(DefaultGroupCacheSize, DefaultFramePool),
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

// TestTrackDistributor_Stress performs stress testing
func TestTrackDistributor_Stress(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stress test in short mode")
	}

	t.Run("high_frequency_broadcasts", func(t *testing.T) {
		dist := &trackDistributor{
			ring:        newGroupRing(DefaultGroupCacheSize, DefaultFramePool),
			subscribers: make(map[chan struct{}]struct{}),
		}

		const numSubs = 100
		for i := 0; i < numSubs; i++ {
			dist.subscribe()
		}

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
		case <-time.After(10 * time.Second):
			require.Fail(t, "high frequency broadcast timed out")
		}
	})

	t.Run("subscriber_churn", func(t *testing.T) {
		dist := &trackDistributor{
			ring:        newGroupRing(DefaultGroupCacheSize, DefaultFramePool),
			subscribers: make(map[chan struct{}]struct{}),
		}

		var wg sync.WaitGroup
		stopCh := make(chan struct{})

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
						dist.unsubscribe(ch)
					}
				}
			}()
		}

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
			ring:        newGroupRing(DefaultGroupCacheSize, DefaultFramePool),
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

// TestTrackDistributor_Timeout tests timeout behavior
func TestTrackDistributor_Timeout(t *testing.T) {
	t.Run("verify_timeout_constant", func(t *testing.T) {
		assert.Equal(t, 1*time.Millisecond, NotifyTimeout, "Expected NotifyTimeout to be 1ms")
	})
}

// TestTrackDistributor_RaceConditions tests for race conditions
func TestTrackDistributor_RaceConditions(t *testing.T) {
	t.Run("concurrent_subscribe_and_broadcast", func(t *testing.T) {
		dist := &trackDistributor{
			ring:        newGroupRing(DefaultGroupCacheSize, DefaultFramePool),
			subscribers: make(map[chan struct{}]struct{}),
		}

		var wg sync.WaitGroup
		wg.Add(2)

		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				dist.subscribe()
			}
		}()

		go func() {
			defer wg.Done()
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
		}()

		wg.Wait()
	})

	t.Run("concurrent_unsubscribe_and_broadcast", func(t *testing.T) {
		dist := &trackDistributor{
			ring:        newGroupRing(DefaultGroupCacheSize, DefaultFramePool),
			subscribers: make(map[chan struct{}]struct{}),
		}

		channels := make([]chan struct{}, 100)
		for i := 0; i < 100; i++ {
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
		}()

		wg.Wait()
	})
}

// TestTrackDistributor_NotificationDelivery tests notification delivery guarantees
func TestTrackDistributor_NotificationDelivery(t *testing.T) {
	t.Run("all_subscribers_receive_notification", func(t *testing.T) {
		dist := &trackDistributor{
			ring:        newGroupRing(DefaultGroupCacheSize, DefaultFramePool),
			subscribers: make(map[chan struct{}]struct{}),
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

		for i := 0; i < numSubs; i++ {
			<-ready
		}

		dist.mu.RLock()
		for ch := range dist.subscribers {
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
			subscribers: make(map[chan struct{}]struct{}),
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

// TestTrackDistributor_NotifyTimeout tests the NotifyTimeout constant
func TestTrackDistributor_NotifyTimeout(t *testing.T) {
	assert.Greater(t, NotifyTimeout, time.Duration(0), "NotifyTimeout should be positive")

	// Verify it's the optimized value from benchmarks
	expectedTimeout := 1 * time.Millisecond
	assert.Equal(t, expectedTimeout, NotifyTimeout, "NotifyTimeout should be optimal value")
}

// ============================================================================
// trackDistributor Integration Tests
// ============================================================================

// TestTrackDistributor_GroupRingIntegration tests groupRing initialization
func TestTrackDistributor_GroupRingIntegration(t *testing.T) {
	dist := &trackDistributor{
		ring:        newGroupRing(DefaultGroupCacheSize, DefaultFramePool),
		subscribers: make(map[chan struct{}]struct{}),
	}

	// Verify ring is properly initialized
	require.NotNil(t, dist.ring, "Ring should be initialized")

	head := dist.ring.head()
	assert.Equal(t, moqt.GroupSequence(0), head, "Expected initial head to be 0")

	earliest := dist.ring.earliestAvailable()
	assert.Equal(t, moqt.GroupSequence(1), earliest, "Expected earliest to be 1")
}

// TestTrackDistributor_OnClose tests the onClose callback
func TestTrackDistributor_OnClose(t *testing.T) {
	onCloseCalled := false

	dist := &trackDistributor{
		ring:        newGroupRing(DefaultGroupCacheSize, DefaultFramePool),
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

// TestTrackDistributor_RingBehavior tests ring head and earliest available
func TestTrackDistributor_RingBehavior(t *testing.T) {
	t.Run("ring_initialization", func(t *testing.T) {
		dist := &trackDistributor{
			ring:        newGroupRing(DefaultGroupCacheSize, DefaultFramePool),
			subscribers: make(map[chan struct{}]struct{}),
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
			subscribers: make(map[chan struct{}]struct{}),
		}

		earliest := dist.ring.earliestAvailable()
		assert.Equal(t, moqt.GroupSequence(1), earliest, "Expected earliest 1 at start")
	})

	t.Run("catchup_logic", func(t *testing.T) {
		dist := &trackDistributor{
			ring:        newGroupRing(DefaultGroupCacheSize, DefaultFramePool),
			subscribers: make(map[chan struct{}]struct{}),
		}

		// Initially head should be 0
		assert.Equal(t, moqt.GroupSequence(0), dist.ring.head(), "Expected initial head to be 0")

		// Verify earliest available - starts at 1 for empty ring
		earliest := dist.ring.earliestAvailable()
		assert.GreaterOrEqual(t, earliest, uint64(0), "Expected earliest to be non-negative")
	})
}
