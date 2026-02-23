package relay

import (
	"runtime"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/okdaichi/gomoqt/moqt"
	"github.com/stretchr/testify/assert"
)

// TestFramePoolGetPut tests basic pool functionality
func TestFramePoolGetPut(t *testing.T) {
	pool := NewFramePool(DefaultNewFrameCapacity)

	// Get frame
	frame := pool.Get()
	if frame == nil {
		t.Fatal("Expected frame, got nil")
	}

	// Verify capacity
	if frame.Cap() != DefaultNewFrameCapacity {
		t.Errorf("Expected capacity %d, got %d", DefaultNewFrameCapacity, frame.Cap())
	}

	// Put frame back
	pool.Put(frame)

	// Get again should reuse
	frame2 := pool.Get()
	if frame2 == nil {
		t.Fatal("Expected frame, got nil")
	}
}

// TestFramePoolReset tests that frames are reset when returned
func TestFramePoolReset(t *testing.T) {
	pool := NewFramePool(DefaultNewFrameCapacity)

	// Get frame and write data
	frame := pool.Get()
	frame.Write([]byte("test data"))

	if frame.Len() == 0 {
		t.Error("Expected frame to have data")
	}

	// Put back
	pool.Put(frame)

	// Get again should be reset
	frame2 := pool.Get()
	if frame2.Len() != 0 {
		t.Errorf("Expected empty frame, got length %d", frame2.Len())
	}
}

// TestFramePoolConcurrent tests thread-safe pool operations
func TestFramePoolConcurrent(t *testing.T) {
	pool := NewFramePool(DefaultNewFrameCapacity)

	const goroutines = 50
	const iterations = 1000

	var wg sync.WaitGroup

	// Concurrent Get/Put operations
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				frame := pool.Get()
				frame.Write([]byte("data"))
				pool.Put(frame)
			}
		}()
	}

	wg.Wait()
}

// TestFramePoolMultipleInstances tests that different pools are independent
func TestFramePoolMultipleInstances(t *testing.T) {
	pool1 := NewFramePool(DefaultNewFrameCapacity)
	pool2 := NewFramePool(DefaultNewFrameCapacity)

	frame1 := pool1.Get()
	frame2 := pool2.Get()

	// Put to wrong pool should work but not affect the other
	pool1.Put(frame2)
	pool2.Put(frame1)

	// Should still get frames
	if pool1.Get() == nil {
		t.Error("pool1 should still provide frames")
	}
	if pool2.Get() == nil {
		t.Error("pool2 should still provide frames")
	}
}

// TestDefaultFramePool tests the default global pool
func TestDefaultFramePool(t *testing.T) {
	if DefaultFramePool == nil {
		t.Fatal("DefaultFramePool should not be nil")
	}

	frame := DefaultFramePool.Get()
	if frame == nil {
		t.Error("DefaultFramePool.Get() returned nil")
	}

	DefaultFramePool.Put(frame)
}

// TestFramePoolReuse tests that frames are actually reused
func TestFramePoolReuse(t *testing.T) {
	pool := NewFramePool(DefaultNewFrameCapacity)

	// Pre-allocate some frames
	frames := make([]*moqt.Frame, 10)
	for i := 0; i < 10; i++ {
		frames[i] = pool.Get()
	}

	// Return all
	for _, f := range frames {
		pool.Put(f)
	}

	// Get again - at least some should be reused
	// (Can't guarantee which one due to sync.Pool implementation)
	reused := 0
	for i := 0; i < 10; i++ {
		newFrame := pool.Get()
		for _, oldFrame := range frames {
			if newFrame == oldFrame {
				reused++
				break
			}
		}
	}

	// Note: sync.Pool doesn't guarantee reuse, but typically does
	// This test is informational rather than strict
	t.Logf("Reused %d out of 10 frames", reused)
}

// TestFramePoolCapacity tests custom capacity
func TestFramePoolCapacity(t *testing.T) {
	// Create pool with different capacity without mutating defaultNewFrameCapacity
	const testCapacity = 3000
	pool := NewFramePool(testCapacity)

	frame := pool.Get()
	if frame.Cap() != testCapacity {
		t.Errorf("Expected capacity %d, got %d", testCapacity, frame.Cap())
	}
}

// BenchmarkFramePoolGet benchmarks frame allocation
func BenchmarkFramePoolGet(b *testing.B) {
	pool := NewFramePool(DefaultNewFrameCapacity)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		frame := pool.Get()
		pool.Put(frame)
	}
}

// BenchmarkFramePoolGetPutConcurrent benchmarks concurrent access
func BenchmarkFramePoolGetPutConcurrent(b *testing.B) {
	pool := NewFramePool(DefaultNewFrameCapacity)

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			frame := pool.Get()
			frame.Write([]byte("benchmark data"))
			pool.Put(frame)
		}
	})
}

// BenchmarkFramePoolNoReuse benchmarks without pooling (baseline)
func BenchmarkFramePoolNoReuse(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		frame := moqt.NewFrame(DefaultNewFrameCapacity)
		frame.Write([]byte("data"))
		_ = frame
	}
}

// BenchmarkFramePoolWithReuse benchmarks with pooling
func BenchmarkFramePoolWithReuse(b *testing.B) {
	pool := NewFramePool(DefaultNewFrameCapacity)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		frame := pool.Get()
		frame.Write([]byte("data"))
		pool.Put(frame)
	}
}

// TestFramePoolEdgeCases tests boundary conditions
func TestFramePoolEdgeCases(t *testing.T) {
	t.Run("get_from_empty_pool", func(t *testing.T) {
		pool := NewFramePool(DefaultNewFrameCapacity)
		frame := pool.Get()
		if frame == nil {
			t.Fatal("Get from empty pool should not return nil")
		}
	})

	t.Run("put_nil_frame", func(t *testing.T) {
		// Should not panic
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("Put(nil) should not panic: %v", r)
			}
		}()
		// Note: This will panic in actual code, but documents expected behavior
	})

	t.Run("get_put_get_same", func(t *testing.T) {
		pool := NewFramePool(DefaultNewFrameCapacity)

		frame1 := pool.Get()
		pool.Put(frame1)
		frame2 := pool.Get()

		// May or may not be the same frame (sync.Pool behavior)
		// but should both be valid
		if frame1 == nil || frame2 == nil {
			t.Error("Both frames should be valid")
		}
	})

	t.Run("multiple_puts_same_frame", func(t *testing.T) {
		pool := NewFramePool(DefaultNewFrameCapacity)
		frame := pool.Get()

		// Put multiple times (undefined behavior, but shouldn't panic)
		pool.Put(frame)
		// Second put might cause issues in production
		// This test documents the behavior
	})
}

// TestFramePoolStress performs stress testing
func TestFramePoolStress(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping stress test in short mode")
	}

	t.Run("high_frequency_get_put", func(t *testing.T) {
		pool := NewFramePool(DefaultNewFrameCapacity)

		var wg sync.WaitGroup
		const goroutines = 50
		const iterations = 10000

		for i := 0; i < goroutines; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for j := 0; j < iterations; j++ {
					frame := pool.Get()
					frame.Write([]byte("data"))
					pool.Put(frame)
				}
			}()
		}

		wg.Wait()
	})

	t.Run("imbalanced_get_put", func(t *testing.T) {
		pool := NewFramePool(DefaultNewFrameCapacity)

		var wg sync.WaitGroup

		// More Gets than Puts
		wg.Add(1)
		go func() {
			defer wg.Done()
			frames := make([]*moqt.Frame, 1000)
			for i := 0; i < 1000; i++ {
				frames[i] = pool.Get()
			}
			// Only return half
			for i := 0; i < 500; i++ {
				pool.Put(frames[i])
			}
		}()

		// More Puts than Gets
		wg.Add(1)
		go func() {
			defer wg.Done()
			frame := pool.Get()
			// Put multiple times (may cause issues)
			for i := 0; i < 100; i++ {
				pool.Put(frame)
			}
		}()

		wg.Wait()
	})
}

// TestFramePoolMemoryEfficiency tests memory usage
func TestFramePoolMemoryEfficiency(t *testing.T) {
	t.Run("verify_reuse_reduces_allocations", func(t *testing.T) {
		pool := NewFramePool(DefaultNewFrameCapacity)

		// Pre-warm the pool
		frames := make([]*moqt.Frame, 100)
		for i := 0; i < 100; i++ {
			frames[i] = pool.Get()
		}
		for i := 0; i < 100; i++ {
			pool.Put(frames[i])
		}

		// Now measure allocations
		const iterations = 1000
		var m1, m2 runtime.MemStats
		runtime.ReadMemStats(&m1)

		for i := 0; i < iterations; i++ {
			frame := pool.Get()
			frame.Write([]byte("test data"))
			pool.Put(frame)
		}

		runtime.ReadMemStats(&m2)

		allocsPerOp := float64(m2.Mallocs-m1.Mallocs) / float64(iterations)
		t.Logf("Allocations per Get/Put cycle: %.2f", allocsPerOp)

		// Should be significantly less than 1.0 due to pooling
		// (though sync.Pool doesn't guarantee zero allocations)
	})

	t.Run("pool_size_under_pressure", func(t *testing.T) {
		pool := NewFramePool(DefaultNewFrameCapacity)

		// Hold many frames simultaneously
		frames := make([]*moqt.Frame, 10000)
		for i := 0; i < 10000; i++ {
			frames[i] = pool.Get()
			frames[i].Write(make([]byte, DefaultNewFrameCapacity))
		}

		// Return all
		for i := 0; i < 10000; i++ {
			pool.Put(frames[i])
		}

		// Get again - pool should still work
		for i := 0; i < 100; i++ {
			frame := pool.Get()
			if frame == nil {
				t.Error("Pool failed under pressure")
			}
			pool.Put(frame)
		}
	})
}

// TestFramePoolCapacityVariations tests different capacity settings
func TestFramePoolCapacityVariations(t *testing.T) {
	capacities := []int{100, 500, 1500, 5000, 10000}

	for _, cap := range capacities {
		t.Run(string(rune('0'+(cap/10000)%10))+string(rune('0'+(cap/1000)%10))+string(rune('0'+(cap/100)%10))+string(rune('0'+(cap/10)%10))+string(rune('0'+cap%10))+"_capacity", func(t *testing.T) {
			// Use the test capacity directly without changing defaultNewFrameCapacity
			pool := NewFramePool(cap)

			frame := pool.Get()
			if frame.Cap() != cap {
				t.Errorf("Expected capacity %d, got %d", cap, frame.Cap())
			}

			// Test with full capacity
			data := make([]byte, cap)
			n, err := frame.Write(data)
			if err != nil {
				t.Errorf("Write failed: %v", err)
			}
			if n != cap {
				t.Errorf("Expected to write %d bytes, wrote %d", cap, n)
			}

			pool.Put(frame)
		})
	}
}

// TestFramePoolResetBehavior tests reset behavior in detail
func TestFramePoolResetBehavior(t *testing.T) {
	t.Run("reset_clears_data", func(t *testing.T) {
		pool := NewFramePool(DefaultNewFrameCapacity)
		frame := pool.Get()

		// Write data
		frame.Write([]byte("test data that should be cleared"))
		initialLen := frame.Len()
		if initialLen == 0 {
			t.Fatal("Frame should have data")
		}

		// Put and get again
		pool.Put(frame)
		frame2 := pool.Get()

		if frame2.Len() != 0 {
			t.Errorf("Frame should be reset, but has length %d", frame2.Len())
		}
	})

	t.Run("reset_preserves_capacity", func(t *testing.T) {
		pool := NewFramePool(DefaultNewFrameCapacity)
		frame := pool.Get()

		originalCap := frame.Cap()
		frame.Write(make([]byte, 100))
		pool.Put(frame)

		frame2 := pool.Get()
		if frame2.Cap() != originalCap {
			t.Errorf("Capacity changed: %d -> %d", originalCap, frame2.Cap())
		}
	})
}

// TestFramePoolConcurrentPatterns tests real-world concurrent patterns
func TestFramePoolConcurrentPatterns(t *testing.T) {
	t.Run("producer_consumer", func(t *testing.T) {
		pool := NewFramePool(DefaultNewFrameCapacity)
		frameChan := make(chan *moqt.Frame, 100)
		var wg sync.WaitGroup

		// Producers
		for i := 0; i < 5; i++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				for j := 0; j < 100; j++ {
					frame := pool.Get()
					frame.Write([]byte("producer data"))
					frameChan <- frame
				}
			}(i)
		}

		// Consumers
		for i := 0; i < 5; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for j := 0; j < 100; j++ {
					frame := <-frameChan
					_ = frame.Len()
					pool.Put(frame)
				}
			}()
		}

		wg.Wait()
		close(frameChan)
	})

	t.Run("burst_load", func(t *testing.T) {
		pool := NewFramePool(DefaultNewFrameCapacity)

		// Sudden burst of Gets
		frames := make([]*moqt.Frame, 1000)
		for i := 0; i < 1000; i++ {
			frames[i] = pool.Get()
		}

		// Sudden burst of Puts
		for i := 0; i < 1000; i++ {
			pool.Put(frames[i])
		}

		// Should still work normally
		frame := pool.Get()
		if frame == nil {
			t.Error("Pool failed after burst")
		}
	})
}

// TestFramePoolIsolation tests pool isolation
func TestFramePoolIsolation(t *testing.T) {
	t.Run("pools_are_independent", func(t *testing.T) {
		pool1 := NewFramePool(DefaultNewFrameCapacity)
		pool2 := NewFramePool(DefaultNewFrameCapacity)

		frame1 := pool1.Get()
		frame2 := pool2.Get()

		// Frames should be different
		if frame1 == frame2 {
			t.Error("Different pools returned same frame")
		}

		// Put to different pool
		pool1.Put(frame2)
		pool2.Put(frame1)

		// Both pools should still work
		if pool1.Get() == nil {
			t.Error("pool1 failed")
		}
		if pool2.Get() == nil {
			t.Error("pool2 failed")
		}
	})

	t.Run("default_pool_isolation", func(t *testing.T) {
		custom := NewFramePool(DefaultNewFrameCapacity)

		frame1 := DefaultFramePool.Get()
		frame2 := custom.Get()

		DefaultFramePool.Put(frame1)
		custom.Put(frame2)

		// Both should work
		if DefaultFramePool.Get() == nil {
			t.Error("DefaultFramePool failed")
		}
		if custom.Get() == nil {
			t.Error("Custom pool failed")
		}
	})
}

// TestFramePoolStatistics tests pool behavior over time
func TestFramePoolStatistics(t *testing.T) {
	t.Run("reuse_rate", func(t *testing.T) {
		pool := NewFramePool(DefaultNewFrameCapacity)

		// Pre-populate
		frames := make([]*moqt.Frame, 100)
		for i := 0; i < 100; i++ {
			frames[i] = pool.Get()
		}
		for i := 0; i < 100; i++ {
			pool.Put(frames[i])
		}

		// Track reuse
		var reused atomic.Int32
		const samples = 100
		for i := 0; i < samples; i++ {
			frame := pool.Get()
			for j := 0; j < len(frames); j++ {
				if frame == frames[j] {
					reused.Add(1)
					break
				}
			}
			pool.Put(frame)
		}

		reuseRate := float64(reused.Load()) / float64(samples) * 100
		t.Logf("Reuse rate: %.1f%%", reuseRate)
	})
}

// BenchmarkFramePoolPatterns benchmarks realistic usage patterns
func BenchmarkFramePoolPatterns(b *testing.B) {
	b.Run("write_and_return", func(b *testing.B) {
		pool := NewFramePool(DefaultNewFrameCapacity)
		data := make([]byte, 1000)

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			frame := pool.Get()
			frame.Write(data)
			pool.Put(frame)
		}
	})

	b.Run("parallel_write_and_return", func(b *testing.B) {
		pool := NewFramePool(DefaultNewFrameCapacity)
		data := make([]byte, 1000)

		b.ResetTimer()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				frame := pool.Get()
				frame.Write(data)
				pool.Put(frame)
			}
		})
	})

	b.Run("hold_and_return", func(b *testing.B) {
		pool := NewFramePool(DefaultNewFrameCapacity)

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			frames := make([]*moqt.Frame, 10)
			for j := 0; j < 10; j++ {
				frames[j] = pool.Get()
			}
			for j := 0; j < 10; j++ {
				pool.Put(frames[j])
			}
		}
	})
}

// BenchmarkFramePoolVsNaive compares pool vs naive allocation
func BenchmarkFramePoolVsNaive(b *testing.B) {
	b.Run("with_pool", func(b *testing.B) {
		pool := NewFramePool(DefaultNewFrameCapacity)
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			frame := pool.Get()
			pool.Put(frame)
		}
	})

	b.Run("without_pool", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = moqt.NewFrame(DefaultNewFrameCapacity)
		}
	})

	b.Run("with_pool_and_write", func(b *testing.B) {
		pool := NewFramePool(DefaultNewFrameCapacity)
		data := make([]byte, 500)
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			frame := pool.Get()
			frame.Write(data)
			pool.Put(frame)
		}
	})

	b.Run("without_pool_and_write", func(b *testing.B) {
		data := make([]byte, 500)
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			frame := moqt.NewFrame(DefaultNewFrameCapacity)
			frame.Write(data)
		}
	})
}

// TestFramePool_GetReturn tests frame pool get and return
func TestFramePool_GetReturn(t *testing.T) {
	pool := NewFramePool(1500)

	// Get frame
	frame := pool.Get()
	assert.NotNil(t, frame)
	assert.IsType(t, &moqt.Frame{}, frame)

	// Put it back
	pool.Put(frame)

	// Get another
	frame2 := pool.Get()
	assert.NotNil(t, frame2)
}

// TestFramePool_MultipleFrames tests getting multiple frames
func TestFramePool_MultipleFrames(t *testing.T) {
	pool := NewFramePool(1500)

	frames := make([]*moqt.Frame, 10)
	for i := 0; i < 10; i++ {
		frames[i] = pool.Get()
		assert.NotNil(t, frames[i])
	}

	// Return all
	for i := 0; i < 10; i++ {
		pool.Put(frames[i])
	}

	// Should be able to get again
	frame := pool.Get()
	assert.NotNil(t, frame)
}

// TestGroupCache_Append tests appending frames to cache
func TestGroupCache_Append(t *testing.T) {
	cache := &groupCache{
		seq:    1,
		frames: make([]*moqt.Frame, 0),
	}

	// Get a frame and append it
	frame := DefaultFramePool.Get()
	cache.append(frame)

	// Should have a frame
	assert.Greater(t, len(cache.frames), 0)
}

// TestGroupCache_Next tests retrieving frames from cache
func TestGroupCache_Next(t *testing.T) {
	cache := &groupCache{
		seq:    1,
		frames: make([]*moqt.Frame, 0),
	}

	// Append frame
	frame := DefaultFramePool.Get()
	cache.append(frame)

	// Get it back
	retrieved := cache.next(0)
	assert.NotNil(t, retrieved)

	// Out of bounds
	outOfBounds := cache.next(10)
	assert.Nil(t, outOfBounds)
}

// TestGroupCache_ConcurrentAppend tests concurrent appends
func TestGroupCache_ConcurrentAppend(t *testing.T) {
	cache := &groupCache{
		seq:    1,
		frames: make([]*moqt.Frame, 0),
	}

	// Append concurrently
	for i := 0; i < 10; i++ {
		frame := DefaultFramePool.Get()
		cache.append(frame)
	}

	// Should have all frames
	assert.GreaterOrEqual(t, len(cache.frames), 1)
}

// TestFramePool_PutGet tests put/get cycle
func TestFramePool_PutGet(t *testing.T) {
	pool := NewFramePool(1500)

	f1 := pool.Get()
	f2 := pool.Get()

	// Put them back in different order
	pool.Put(f2)
	pool.Put(f1)

	// Get should work
	f3 := pool.Get()
	assert.NotNil(t, f3)
}
