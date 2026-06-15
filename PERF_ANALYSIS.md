# Relay Performance Analysis

## Baseline Measurements (5s benchmarks on Intel i7-10700K)

### Key Metrics Captured

| Operation | Time/op | Allocs/op | Issue |
|-----------|---------|-----------|-------|
| FramePool Get/Put | 12 ns | 0 | ✓ Optimal |
| GroupCache.Append | **959 ns** | **2 (1600B)** | ⚠️ Expensive |
| GroupCache.Next | 12 ns | 0 | ✓ Optimal |
| GroupRing.Reserve | 92 ns | 0 | ✓ Good |
| GroupRing.Fill (10 frames) | 744 ns | 0 | ✓ Good |
| Broadcast (1 sub) | 48 ns | 0 | ✓ Good |
| Broadcast (1000 subs) | **17.6 µs** | 0 | ⚠️ O(n) |
| Subscribe/Unsubscribe | 123 ns | 1 (112B) | ⚠️ Contention @ 10K goroutines |

### CPU Profile Results (GroupRing.Fill benchmark)

**Top CPU consumers:**
1. **runtime.memmove: 38.60%** ← Frame cloning via WriteTo
2. **Mutex.Unlock: 19.96%** ← Lock contention in groupCache.append
3. **groupCache.append: 80.37% cumulative** ← Main bottleneck
4. **FramePool.Get/Put: ~10% combined** ← Pool maintenance
5. **groupRing.reserve: 16.78%** ← Ring slot allocation

## Identified Bottlenecks

### 1. Frame Cloning Overhead (38.60% of CPU time)
**Root cause:** groupCache.append clones every frame via Frame.WriteTo
```go
_, _ = f.WriteTo(clone)  // expensive memmove
```

**Impact:** With typical 1KB frames @ 30 groups/sec × 10 frames = 300 KB/sec copied via memmove

**Cost:** 38.60% of total CPU time in benchmark

### 2. Lock Contention in groupCache.append (19.96% of CPU time)
**Root cause:** Mutex lock held during expensive memmove operation
```go
func (gc *groupCache) append(f *moqt.Frame, pool *FramePool) {
    gc.mu.Lock()
    defer gc.mu.Unlock()
    clone := pool.Get()
    _, _ = f.WriteTo(clone)  // <-- Lock held for entire memmove!
}
```

**Impact:** Multiple subscribers waiting to read from same cache

### 3. Broadcast Scaling (O(n) subscribers)
**Current:** 17.6 µs for 1000 subscribers vs 48 ns for 1 subscriber (365x slower)
```go
func (d *trackDistributor) broadcast() {
    d.mu.RLock()
    for ch := range d.subscribers {  // O(n) iteration
        select {
        case ch <- struct{}{}:
        default:
        }
    }
    d.mu.RUnlock()
}
```

**Impact:** Under high subscriber count, broadcast becomes expensive relative to egress path

### 4. Memory Allocations in groupCache.append
**Current:** 2 allocs / 1600B per frame cloned
- Alloc 1: Clone frame from pool
- Alloc 2: Frame slice append (occasional reallocation)

## Optimization Roadmap

### Phase 1: Quick Wins (Expected: 20-30% improvement)
- [ ] Pre-allocate groupCache.frames slices (~32 frames) to avoid append reallocations
- [ ] Reduce groupCache.frames slice header allocation pressure
- [ ] Benchmark pre-bound Prometheus histograms (already done, 3.7x improvement verified)

### Phase 2: Medium Effort (Expected: 30-50% improvement)
- [ ] Move lock release outside expensive memmove
- [ ] Batch notify subscribers instead of per-frame
- [ ] Reduce broadcast lock contention with RWMutex optimization

### Phase 3: Major Refactoring (Expected: 50%+ improvement)
- [ ] Reference-counted frame sharing instead of cloning (breaking change)
- [ ] Segment frames into cache buckets to parallelize appends
- [ ] Lazy cloning (only clone on egress, not ingest)

## Hypothesis for Next Optimization

**Statement:** Reducing lock hold time in groupCache.append will reduce mutex contention (19.96% of CPU) by 50-70%.

**Mechanism:** Currently lock held during memmove. Moving lock to only protect the slice append will allow concurrent readers to proceed.

**Prediction:** This will reduce cumulative append time from 80.37% to ~45-55% of total.

**Success criteria:** 
- Append operation latency ≤ 500 ns/op (currently 959 ns)
- Mutex.Unlock drops to <5% of CPU
- No regression in frame safety

**Refutation criteria:**
- Append latency doesn't improve by ≥30%
- Data corruption or race conditions detected
- Contention moves to other locks
