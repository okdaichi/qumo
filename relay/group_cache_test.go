package relay

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/okdaichi/gomoqt/moqt"
)

// TestGroupCacheAppend tests frame appending functionality
func TestGroupCacheAppend(t *testing.T) {
	gc := &groupCache{
		seq:    1,
		frames: make([]*moqt.Frame, 0),
	}

	// Create test frame
	frame := moqt.NewFrame(100)
	frame.Write([]byte("test data"))

	// Append frame
	gc.append(frame)

	if len(gc.frames) != 1 {
		t.Errorf("Expected 1 frame, got %d", len(gc.frames))
	}

	// Verify cloning (original and cached frame should be different objects)
	if gc.frames[0] == frame {
		t.Error("Frame was not cloned")
	}

	// Append multiple frames
	for i := 0; i < 5; i++ {
		f := moqt.NewFrame(100)
		f.Write([]byte("data"))
		gc.append(f)
	}

	if len(gc.frames) != 6 {
		t.Errorf("Expected 6 frames, got %d", len(gc.frames))
	}
}

// TestGroupCacheNext tests frame retrieval by index
func TestGroupCacheNext(t *testing.T) {
	gc := &groupCache{
		seq:    1,
		frames: make([]*moqt.Frame, 0),
	}

	// Add frames
	for i := 0; i < 3; i++ {
		frame := moqt.NewFrame(100)
		gc.append(frame)
	}

	// Test valid indices
	for i := 0; i < 3; i++ {
		frame := gc.next(i)
		if frame == nil {
			t.Errorf("Expected frame at index %d, got nil", i)
		}
	}

	// Test invalid indices
	if gc.next(-1) != nil {
		t.Error("Expected nil for negative index")
	}
	if gc.next(3) != nil {
		t.Error("Expected nil for out of bounds index")
	}
	if gc.next(100) != nil {
		t.Error("Expected nil for large index")
	}
}

// TestGroupRingAdd tests adding groups to ring buffer
func TestGroupRingAdd(t *testing.T) {
	ring := newGroupRing(DefaultGroupCacheSize)

	// Head should start at 0
	if ring.head() != 0 {
		t.Errorf("Expected initial head 0, got %d", ring.head())
	}

	// Note: We can't easily test add() without a real GroupReader
	// This would require mocking, which is tested in integration tests
}

// TestGroupRingHead tests head position tracking
func TestGroupRingHead(t *testing.T) {
	ring := newGroupRing(DefaultGroupCacheSize)

	// Initial head
	head := ring.head()
	if head != 0 {
		t.Errorf("Expected head 0, got %d", head)
	}

	// Simulate position changes
	ring.pos.Store(10)
	head = ring.head()
	if head != 10 {
		t.Errorf("Expected head 10, got %d", head)
	}
}

// TestGroupRingEarliestAvailable tests earliest available group calculation
func TestGroupRingEarliestAvailable(t *testing.T) {
	ring := newGroupRing(DefaultGroupCacheSize)
	size := moqt.GroupSequence(ring.size)

	tests := []struct {
		head     moqt.GroupSequence
		expected moqt.GroupSequence
	}{
		{0, 1},
		{1, 1},
		{5, 1},
		{size, 1},
		{size + 1, 2},
		{size + 5, 6},
		{100, 100 - size + 1},
	}

	for _, tt := range tests {
		ring.pos.Store(uint64(tt.head))
		earliest := ring.earliestAvailable()
		if earliest != tt.expected {
			t.Errorf("head=%d: expected earliest=%d, got %d", tt.head, tt.expected, earliest)
		}
	}
}

// TestGroupRingGet tests retrieving cached groups
func TestGroupRingGet(t *testing.T) {
	ring := newGroupRing(DefaultGroupCacheSize)

	// Store test cache
	testCache := &groupCache{
		seq:    5,
		frames: make([]*moqt.Frame, 0),
	}
	idx := uint64(5) % uint64(ring.size)
	ring.caches[idx].Store(testCache)

	// Retrieve by sequence
	retrieved := ring.get(5)
	if retrieved == nil {
		t.Fatal("Expected cache, got nil")
	}
	if retrieved.seq != 5 {
		t.Errorf("Expected seq 5, got %d", retrieved.seq)
	}
}

// TestGroupRingWrapAround tests ring buffer wrap-around behavior
func TestGroupRingWrapAround(t *testing.T) {
	ring := newGroupRing(DefaultGroupCacheSize)
	size := ring.size

	// Simulate adding more groups than ring size
	for i := 1; i <= size+5; i++ {
		cache := &groupCache{
			seq:    moqt.GroupSequence(i),
			frames: make([]*moqt.Frame, 0),
		}
		idx := i % size
		ring.caches[idx].Store(cache)
		ring.pos.Store(uint64(i))
	}

	// Verify wrap-around
	head := ring.head()
	if head != moqt.GroupSequence(size+5) {
		t.Errorf("Expected head %d, got %d", size+5, head)
	}

	// Old entries should be overwritten
	// Entry at index 0 should now contain seq=size or seq=size*2 depending on size
	retrieved := ring.get(moqt.GroupSequence(size))
	if retrieved != nil && retrieved.seq <= moqt.GroupSequence(size-1) {
		t.Error("Old entry was not overwritten")
	}
}

// TestGroupRingConcurrentAccess tests thread-safe access
func TestGroupRingConcurrentAccess(t *testing.T) {
	ring := newGroupRing(DefaultGroupCacheSize)

	// Concurrent writes
	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func(id int) {
			for j := 0; j < 100; j++ {
				cache := &groupCache{
					seq:    moqt.GroupSequence(id*100 + j),
					frames: make([]*moqt.Frame, 0),
				}
				idx := (id*100 + j) % ring.size
				ring.caches[idx].Store(cache)
				ring.pos.Add(1)
			}
			done <- true
		}(i)
	}

	// Concurrent reads
	for i := 0; i < 10; i++ {
		go func() {
			for j := 0; j < 100; j++ {
				_ = ring.head()
				_ = ring.earliestAvailable()
				_ = ring.get(moqt.GroupSequence(j))
			}
			done <- true
		}()
	}

	// Wait for completion
	for i := 0; i < 20; i++ {
		<-done
	}

	// Should complete without race conditions or panics
}

// BenchmarkGroupCacheAppend benchmarks frame appending
func BenchmarkGroupCacheAppend(b *testing.B) {
	gc := &groupCache{
		seq:    1,
		frames: make([]*moqt.Frame, 0, b.N),
	}
	frame := moqt.NewFrame(1500)
	frame.Write(make([]byte, 1000))

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		gc.append(frame)
	}
}

// BenchmarkGroupRingGet benchmarks cache retrieval
func BenchmarkGroupRingGet(b *testing.B) {
	ring := newGroupRing(DefaultGroupCacheSize)

	// Populate ring
	for i := 0; i < ring.size; i++ {
		cache := &groupCache{
			seq:    moqt.GroupSequence(i),
			frames: make([]*moqt.Frame, 0),
		}
		ring.caches[i].Store(cache)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = ring.get(moqt.GroupSequence(i % ring.size))
	}
}

// TestGroupRingEdgeCases tests boundary conditions
func TestGroupRingEdgeCases(t *testing.T) {
	t.Run("get_before_any_add", func(t *testing.T) {
		ring := newGroupRing(DefaultGroupCacheSize)
		cache := ring.get(1)
		// Should return nil or stale data, shouldn't panic
		_ = cache
	})

	t.Run("earliest_at_boundary", func(t *testing.T) {
		ring := newGroupRing(DefaultGroupCacheSize)

		// Exactly at ring size
		ring.pos.Store(uint64(ring.size))
		earliest := ring.earliestAvailable()
		if earliest != 1 {
			t.Errorf("At boundary, expected earliest=1, got %d", earliest)
		}

		// Just past ring size
		ring.pos.Store(uint64(ring.size + 1))
		earliest = ring.earliestAvailable()
		if earliest != 2 {
			t.Errorf("Past boundary, expected earliest=2, got %d", earliest)
		}
	})

	t.Run("head_overflow", func(t *testing.T) {
		ring := newGroupRing(DefaultGroupCacheSize)

		// Simulate large sequence numbers
		ring.pos.Store(uint64(^uint64(0) - 100)) // Near max uint64
		head := ring.head()
		if head == 0 {
			t.Error("Head should handle large numbers")
		}
	})

	t.Run("zero_size_protection", func(t *testing.T) {
		// Verify ring size is never zero
		ring := newGroupRing(DefaultGroupCacheSize)
		if ring.size == 0 {
			t.Fatal("Ring size should not be zero")
		}
	})
}

// TestGroupRingCapacity tests capacity handling
func TestGroupRingCapacity(t *testing.T) {
	t.Run("default_capacity", func(t *testing.T) {
		ring := newGroupRing(DefaultGroupCacheSize)
		if ring.size != DefaultGroupCacheSize {
			t.Errorf("Expected size %d, got %d", DefaultGroupCacheSize, ring.size)
		}
		if len(ring.caches) != DefaultGroupCacheSize {
			t.Errorf("Expected %d cache slots, got %d", DefaultGroupCacheSize, len(ring.caches))
		}
	})

	t.Run("custom_capacity", func(t *testing.T) {
		ring := newGroupRing(16)
		if ring.size != 16 {
			t.Errorf("Expected custom size 16, got %d", ring.size)
		}
	})
}

// TestGroupCacheBehavior tests detailed cache behavior
func TestGroupCacheBehavior(t *testing.T) {
	t.Run("append_empty_frame", func(t *testing.T) {
		gc := &groupCache{
			seq:    1,
			frames: make([]*moqt.Frame, 0),
		}

		emptyFrame := moqt.NewFrame(100)
		gc.append(emptyFrame)

		if len(gc.frames) != 1 {
			t.Error("Should append empty frame")
		}
	})

	t.Run("append_large_frame", func(t *testing.T) {
		gc := &groupCache{
			seq:    1,
			frames: make([]*moqt.Frame, 0),
		}

		largeFrame := moqt.NewFrame(10000)
		data := make([]byte, 10000)
		largeFrame.Write(data)
		gc.append(largeFrame)

		if len(gc.frames) != 1 {
			t.Error("Should handle large frames")
		}
	})

	t.Run("many_small_frames", func(t *testing.T) {
		gc := &groupCache{
			seq:    1,
			frames: make([]*moqt.Frame, 0),
		}

		const count = 1000
		for i := 0; i < count; i++ {
			frame := moqt.NewFrame(10)
			frame.Write([]byte{byte(i)})
			gc.append(frame)
		}

		if len(gc.frames) != count {
			t.Errorf("Expected %d frames, got %d", count, len(gc.frames))
		}
	})

	t.Run("frame_independence", func(t *testing.T) {
		gc := &groupCache{
			seq:    1,
			frames: make([]*moqt.Frame, 0),
		}

		original := moqt.NewFrame(100)
		original.Write([]byte("original"))
		gc.append(original)

		// Modify original
		original.Reset()
		original.Write([]byte("modified"))

		// Cached frame should be unchanged
		cached := gc.frames[0]
		if cached.Len() == 0 {
			t.Error("Cached frame was affected by original modification")
		}
	})
}

// TestGroupRingStress performs stress testing
func TestGroupRingStress(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping stress test in short mode")
	}

	t.Run("rapid_position_updates", func(t *testing.T) {
		ring := newGroupRing(DefaultGroupCacheSize)

		var wg sync.WaitGroup
		const goroutines = 10
		const iterations = 10000

		for i := 0; i < goroutines; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for j := 0; j < iterations; j++ {
					ring.pos.Add(1)
					_ = ring.head()
					_ = ring.earliestAvailable()
				}
			}()
		}

		wg.Wait()
	})

	t.Run("concurrent_cache_operations", func(t *testing.T) {
		ring := newGroupRing(DefaultGroupCacheSize)

		var wg sync.WaitGroup
		stopCh := make(chan bool)

		// Writers
		for i := 0; i < 5; i++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				seq := 0
				for {
					select {
					case <-stopCh:
						return
					default:
						cache := &groupCache{
							seq:    moqt.GroupSequence(id*10000 + seq),
							frames: make([]*moqt.Frame, 0),
						}
						idx := (id*10000 + seq) % ring.size
						ring.caches[idx].Store(cache)
						seq++
					}
				}
			}(i)
		}

		// Readers
		for i := 0; i < 10; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for {
					select {
					case <-stopCh:
						return
					default:
						for j := 0; j < 100; j++ {
							_ = ring.get(moqt.GroupSequence(j))
						}
					}
				}
			}()
		}

		time.Sleep(500 * time.Millisecond)
		close(stopCh)
		wg.Wait()
	})
}

// TestGroupCacheMemory tests memory behavior
func TestGroupCacheMemory(t *testing.T) {
	t.Run("frame_pool_integration", func(t *testing.T) {
		gc := &groupCache{
			seq:    1,
			frames: make([]*moqt.Frame, 0),
		}

		// Frames should come from pool
		frame := DefaultFramePool.Get()
		frame.Write([]byte("test"))
		gc.append(frame)

		// Return to pool
		DefaultFramePool.Put(frame)

		if len(gc.frames) != 1 {
			t.Error("Frame not cached")
		}
	})

	t.Run("large_cache_growth", func(t *testing.T) {
		gc := &groupCache{
			seq:    1,
			frames: make([]*moqt.Frame, 0, 1000),
		}

		// Add many frames
		for i := 0; i < 1000; i++ {
			frame := moqt.NewFrame(1500)
			gc.append(frame)
		}

		if len(gc.frames) != 1000 {
			t.Errorf("Expected 1000 frames, got %d", len(gc.frames))
		}
	})
}

// TestGroupRingSequenceNumbers tests sequence number handling
func TestGroupRingSequenceNumbers(t *testing.T) {
	t.Run("sequential_sequences", func(t *testing.T) {
		ring := newGroupRing(DefaultGroupCacheSize)

		for i := 1; i <= 20; i++ {
			cache := &groupCache{
				seq:    moqt.GroupSequence(i),
				frames: make([]*moqt.Frame, 0),
			}
			idx := i % ring.size
			ring.caches[idx].Store(cache)
			ring.pos.Store(uint64(i))
		}

		head := ring.head()
		if head != 20 {
			t.Errorf("Expected head 20, got %d", head)
		}
	})

	t.Run("non_sequential_sequences", func(t *testing.T) {
		ring := newGroupRing(DefaultGroupCacheSize)

		sequences := []int{1, 5, 3, 10, 7}
		for _, seq := range sequences {
			cache := &groupCache{
				seq:    moqt.GroupSequence(seq),
				frames: make([]*moqt.Frame, 0),
			}
			idx := seq % ring.size
			ring.caches[idx].Store(cache)
			ring.pos.Store(uint64(seq))
		}

		// Head should reflect last stored position
		head := ring.head()
		if head != 7 {
			t.Errorf("Expected head 7, got %d", head)
		}
	})
}

// TestGroupCacheConcurrency tests concurrent operations
func TestGroupCacheConcurrency(t *testing.T) {
	t.Run("concurrent_appends", func(t *testing.T) {
		gc := &groupCache{
			seq:    1,
			frames: make([]*moqt.Frame, 0),
		}

		var wg sync.WaitGroup
		const goroutines = 10
		const appendsPerGoroutine = 100

		for i := 0; i < goroutines; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for j := 0; j < appendsPerGoroutine; j++ {
					frame := moqt.NewFrame(100)
					frame.Write([]byte("data"))
					gc.append(frame)
				}
			}()
		}

		wg.Wait()

		expected := goroutines * appendsPerGoroutine
		if len(gc.frames) != expected {
			t.Errorf("Expected %d frames, got %d", expected, len(gc.frames))
		}
	})

	t.Run("concurrent_reads", func(t *testing.T) {
		gc := &groupCache{
			seq:    1,
			frames: make([]*moqt.Frame, 0),
		}

		// Pre-populate
		for i := 0; i < 100; i++ {
			frame := moqt.NewFrame(100)
			gc.append(frame)
		}

		var wg sync.WaitGroup
		var successCount atomic.Int32

		for i := 0; i < 50; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for j := 0; j < 100; j++ {
					if gc.next(j) != nil {
						successCount.Add(1)
					}
				}
			}()
		}

		wg.Wait()

		// Should successfully read all frames
		if successCount.Load() != 50*100 {
			t.Errorf("Not all reads succeeded: %d/5000", successCount.Load())
		}
	})
}

// BenchmarkGroupCacheOperations benchmarks various operations
func BenchmarkGroupCacheOperations(b *testing.B) {
	b.Run("sequential_append", func(b *testing.B) {
		gc := &groupCache{
			seq:    1,
			frames: make([]*moqt.Frame, 0, b.N),
		}
		frame := moqt.NewFrame(1500)
		frame.Write(make([]byte, 1000))

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			gc.append(frame)
		}
	})

	b.Run("random_access", func(b *testing.B) {
		gc := &groupCache{
			seq:    1,
			frames: make([]*moqt.Frame, 0, 100),
		}

		// Pre-populate
		for i := 0; i < 100; i++ {
			frame := moqt.NewFrame(1500)
			gc.append(frame)
		}

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = gc.next(i % 100)
		}
	})
}

// BenchmarkGroupRingOperations benchmarks ring operations
func BenchmarkGroupRingOperations(b *testing.B) {
	b.Run("head_read", func(b *testing.B) {
		ring := newGroupRing(DefaultGroupCacheSize)
		ring.pos.Store(12345)

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = ring.head()
		}
	})

	b.Run("earliest_calculation", func(b *testing.B) {
		ring := newGroupRing(DefaultGroupCacheSize)
		ring.pos.Store(100)

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = ring.earliestAvailable()
		}
	})

	b.Run("concurrent_get_head", func(b *testing.B) {
		ring := newGroupRing(DefaultGroupCacheSize)
		ring.pos.Store(100)

		// Populate ring
		for i := 0; i < ring.size; i++ {
			cache := &groupCache{
				seq:    moqt.GroupSequence(i),
				frames: make([]*moqt.Frame, 0),
			}
			ring.caches[i].Store(cache)
		}

		b.ResetTimer()
		b.RunParallel(func(pb *testing.PB) {
			i := 0
			for pb.Next() {
				_ = ring.get(moqt.GroupSequence(i % ring.size))
				_ = ring.head()
				i++
			}
		})
	})
}
