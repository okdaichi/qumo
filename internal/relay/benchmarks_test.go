package relay

import (
	"sync"
	"testing"

	"github.com/qumo-dev/gomoqt/moqt"
)

// ============================================================================
// Frame Pool Benchmarks
// ============================================================================

func BenchmarkFramePool_GetPut(b *testing.B) {
	pool := NewFramePool(DefaultNewFrameCapacity)

	b.ResetTimer()
	for range b.N {
		frame := pool.Get()
		frame.Write([]byte("test"))
		pool.Put(frame)
	}
}

func BenchmarkFramePool_GetPut_Parallel(b *testing.B) {
	pool := NewFramePool(DefaultNewFrameCapacity)

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			frame := pool.Get()
			frame.Write([]byte("test"))
			pool.Put(frame)
		}
	})
}

func BenchmarkFramePool_LargeFrame(b *testing.B) {
	pool := NewFramePool(10 * 1024 * 1024) // 10MB frames

	b.ResetTimer()
	for range b.N {
		frame := pool.Get()
		frame.Write(make([]byte, 1024*1024))
		pool.Put(frame)
	}
}

// ============================================================================
// Group Cache Benchmarks
// ============================================================================

func BenchmarkGroupCache_Next_HitRate(b *testing.B) {
	gc := &groupCache{
		seq:    1,
		frames: make([]*moqt.Frame, 0),
	}

	pool := NewFramePool(DefaultNewFrameCapacity)
	frame := moqt.NewFrame(DefaultNewFrameCapacity)
	frame.Write([]byte("test"))

	// Pre-populate with 100 frames
	for i := 0; i < 100; i++ {
		gc.append(frame, pool)
	}

	b.ResetTimer()
	for range b.N {
		_ = gc.next(50) // Access middle frame
	}
}

func BenchmarkGroupCache_ConcurrentAppendAndNext(b *testing.B) {
	const (
		writers   = 10
		readers   = 10
		framesCap = 1024 // bound the working set so the slice can't grow unboundedly
	)

	b.Run("10writers_10readers", func(b *testing.B) {
		pool := NewFramePool(DefaultNewFrameCapacity)
		gc := &groupCache{
			seq:    1,
			frames: make([]*moqt.Frame, 0, framesCap),
		}
		frame := moqt.NewFrame(DefaultNewFrameCapacity)
		frame.Write([]byte("test data"))

		b.ResetTimer()
		b.ReportAllocs()

		// Work is scaled to b.N and divided across goroutines, so the reported
		// ns/op reflects one operation (previously each goroutine ran b.N
		// iterations, inflating the result by writers+readers).
		var wg sync.WaitGroup
		wg.Add(writers + readers)

		for w := 0; w < writers; w++ {
			go func() {
				defer wg.Done()
				for i := 0; i < b.N/writers; i++ {
					// No trim: append self-limits at MaxFramesPerGroup, so frames
					// never shrinks — the invariant the lockless next() below relies
					// on. Trimming here set len(frames) to 0 while leaving the
					// atomic frameLen stale, so concurrent readers passed next()'s
					// bounds check and indexed a zero-length slice (panic).
					gc.append(frame, pool)
				}
			}()
		}

		for r := 0; r < readers; r++ {
			go func() {
				defer wg.Done()
				for i := 0; i < b.N/readers; i++ {
					_ = gc.next(i % 100)
				}
			}()
		}

		wg.Wait()
	})
}

// ============================================================================
// Group Ring Benchmarks
// ============================================================================

func BenchmarkGroupRing_Reserve(b *testing.B) {
	ring := newGroupRing(DefaultGroupCacheSize, DefaultFramePool)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cache := ring.reserve(moqt.GroupSequence(i))
		ring.decrRef(cache)
	}
}

// ============================================================================
// Group Ring Fill Benchmark
// ============================================================================

func BenchmarkGroupRing_Fill_VariableSize(b *testing.B) {
	tests := []struct {
		name       string
		frameCount int
		frameSize  int
	}{
		{"small_10frames_1KB", 10, 1024},
		{"medium_50frames_2KB", 50, 2048},
		{"large_100frames_10KB", 100, 10240},
	}

	for _, tt := range tests {
		b.Run(tt.name, func(b *testing.B) {
			pool := NewFramePool(DefaultNewFrameCapacity)
			ring := newGroupRing(DefaultGroupCacheSize, pool)

			frame := moqt.NewFrame(tt.frameSize)
			frame.Write(make([]byte, tt.frameSize))

			b.ResetTimer()
			b.ReportAllocs()

			for i := 0; i < b.N; i++ {
				cache := ring.reserve(moqt.GroupSequence(i))
				for j := 0; j < tt.frameCount; j++ {
					cache.append(frame, pool)
				}
				ring.decrRef(cache)
			}
		})
	}
}

// ============================================================================
// Broadcast Operation Benchmarks
// ============================================================================

func BenchmarkTrackDistributor_Broadcast(b *testing.B) {
	tests := []struct {
		name        string
		subscribers int
	}{
		{"1_subscriber", 1},
		{"10_subscribers", 10},
		{"100_subscribers", 100},
		{"1000_subscribers", 1000},
	}

	for _, tt := range tests {
		b.Run(tt.name, func(b *testing.B) {
			dist := &trackDistributor{}

			// Create subscriber channels
			for i := 0; i < tt.subscribers; i++ {
				ch := make(chan struct{}, 1)
				dist.subscribers = append(dist.subscribers, ch)
			}

			b.ResetTimer()
			b.ReportAllocs()

			for range b.N {
				dist.broadcast()
			}
		})
	}
}

// ============================================================================
// Subscribe/Unsubscribe Benchmarks
// ============================================================================

func BenchmarkTrackDistributor_SubscribeUnsubscribe(b *testing.B) {
	dist := &trackDistributor{}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		ch := dist.subscribe()
		dist.unsubscribe(ch)
	}
}

func BenchmarkTrackDistributor_SubscribeUnsubscribe_Parallel(b *testing.B) {
	dist := &trackDistributor{}

	b.ResetTimer()
	b.ReportAllocs()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			ch := dist.subscribe()
			dist.unsubscribe(ch)
		}
	})
}

// ============================================================================
// Memory Allocation Tracking Benchmarks
// ============================================================================

func BenchmarkMemAllocs_FramePool(b *testing.B) {
	pool := NewFramePool(DefaultNewFrameCapacity)

	b.ResetTimer()
	b.ReportAllocs()

	for range b.N {
		frame := pool.Get()
		pool.Put(frame)
	}
}

func BenchmarkMemAllocs_GroupCache_Append(b *testing.B) {
	const framesCap = 4096

	pool := NewFramePool(DefaultNewFrameCapacity)
	gc := &groupCache{
		seq:    1,
		frames: make([]*moqt.Frame, 0, framesCap),
	}

	frame := moqt.NewFrame(DefaultNewFrameCapacity)
	frame.Write([]byte("test"))

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		gc.append(frame, pool)
		// groupCache.append grows frames without bound; trim periodically to
		// bound memory without distorting the amortized allocs/op measurement.
		if i%framesCap == 0 {
			gc.mu.Lock()
			gc.frames = gc.frames[:0]
			gc.mu.Unlock()
		}
	}
}

func BenchmarkMemAllocs_GroupRing_Reserve(b *testing.B) {
	ring := newGroupRing(DefaultGroupCacheSize, DefaultFramePool)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		cache := ring.reserve(moqt.GroupSequence(i))
		ring.decrRef(cache)
	}
}

// ============================================================================
// Contention / Lock Pressure Benchmarks
// ============================================================================

func BenchmarkLockPressure_GroupCache(b *testing.B) {
	const framesCap = 512 // bound the working set so the slice can't grow unboundedly

	tests := []struct {
		name    string
		writers int
		readers int
	}{
		{"1w_1r", 1, 1},
		{"1w_10r", 1, 10},
		{"10w_10r", 10, 10},
		{"1w_100r", 1, 100},
	}

	for _, tt := range tests {
		b.Run(tt.name, func(b *testing.B) {
			pool := NewFramePool(DefaultNewFrameCapacity)
			gc := &groupCache{
				seq:    1,
				frames: make([]*moqt.Frame, 0, framesCap),
			}

			frame := moqt.NewFrame(DefaultNewFrameCapacity)
			frame.Write([]byte("test"))

			b.ResetTimer()
			b.ReportAllocs()

			var wg sync.WaitGroup

			// Writers: work scales with b.N (previously a fixed iteration
			// count made ns/op meaningless and ignored b.N).
			for w := 0; w < tt.writers; w++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for i := 0; i < b.N/tt.writers; i++ {
						// No trim: append self-limits at MaxFramesPerGroup, so frames
						// never shrinks - the invariant the lockless next() below relies
						// on. Trimming here set len(frames) to 0 while leaving frameLen
						// stale, so concurrent readers passed next()'s bounds check and
						// indexed a zero-length slice (panic).
						gc.append(frame, pool)
					}
				}()
			}

			// Readers: work scales with b.N, divided across readers.
			for r := 0; r < tt.readers; r++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for i := 0; i < b.N/tt.readers; i++ {
						_ = gc.next(i % 10)
					}
				}()
			}

			wg.Wait()
		})
	}
}

func BenchmarkLockPressure_TrackDistributor_Subscribe(b *testing.B) {
	tests := []struct {
		name  string
		count int
	}{
		{"10_goroutines", 10},
		{"100_goroutines", 100},
		{"1000_goroutines", 1000},
	}

	for _, tt := range tests {
		b.Run(tt.name, func(b *testing.B) {
			dist := &trackDistributor{}

			b.ResetTimer()
			b.ReportAllocs()

			var wg sync.WaitGroup
			for g := 0; g < tt.count; g++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for i := 0; i < b.N/tt.count; i++ {
						ch := dist.subscribe()
						dist.unsubscribe(ch)
					}
				}()
			}

			wg.Wait()
		})
	}
}

// ============================================================================
// Ring Contention Benchmarks
// ============================================================================

func BenchmarkGroupRing_ConcurrentReserveAndGet(b *testing.B) {
	tests := []struct {
		name    string
		writers int
		readers int
	}{
		{"1w_1r", 1, 1},
		{"1w_10r", 1, 10},
		{"10w_10r", 10, 10},
	}

	for _, tt := range tests {
		b.Run(tt.name, func(b *testing.B) {
			ring := newGroupRing(DefaultGroupCacheSize, DefaultFramePool)

			b.ResetTimer()
			b.ReportAllocs()

			var wg sync.WaitGroup

			// Writers
			for w := 0; w < tt.writers; w++ {
				wg.Add(1)
				go func(id int) {
					defer wg.Done()
					for i := 0; i < b.N/tt.writers; i++ {
						cache := ring.reserve(moqt.GroupSequence(i + id*1000000))
						ring.decrRef(cache)
					}
				}(w)
			}

			// Readers
			for r := 0; r < tt.readers; r++ {
				wg.Add(1)
				go func(id int) {
					defer wg.Done()
					for i := 0; i < b.N/tt.readers; i++ {
						cache := ring.get(moqt.GroupSequence(i % ring.size))
						if cache != nil {
							ring.decrRef(cache)
						}
					}
				}(r)
			}

			wg.Wait()
		})
	}
}
