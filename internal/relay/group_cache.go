package relay

import (
	"iter"
	"log/slog"
	"sync"
	"sync/atomic"

	"github.com/qumo-dev/gomoqt/moqt"
)

// frameSource is satisfied by *moqt.GroupReader and can be implemented by
// test fakes to exercise fill without a real MoQT connection.
type frameSource interface {
	Frames(buf *moqt.Frame) iter.Seq[*moqt.Frame]
}

const DefaultGroupCacheSize = 8

type groupCache struct {
	mu       sync.Mutex // Protects frames slice for defensive programming
	seq      moqt.GroupSequence
	frames   []*moqt.Frame
	complete atomic.Bool // True when all frames have been added
	refCount atomic.Int32
	evicted  atomic.Bool
}

// isComplete returns true if the group has finished receiving all frames.
func (gc *groupCache) isComplete() bool {
	return gc.complete.Load()
}

// markComplete marks the group as complete.
func (gc *groupCache) markComplete() {
	gc.complete.Store(true)
}

func (gc *groupCache) incrRef() {
	gc.refCount.Add(1)
}

// Append appends a frame to the group cache using the provided pool.
// The frame is cloned and stored in the cache.
// Thread-safe: can be called concurrently (though typically called from single goroutine).
func (gc *groupCache) append(f *moqt.Frame, pool *FramePool) {
	gc.mu.Lock()
	defer gc.mu.Unlock()

	clone := pool.Get()

	// Clone the frame because the frame will be reused.
	// This operation never returns an error, so we can ignore it.
	_, _ = f.WriteTo(clone)

	gc.frames = append(gc.frames, clone)
}

// next returns the frame at the given index.
// Thread-safe: can be called concurrently.
func (gc *groupCache) next(index int) *moqt.Frame {
	gc.mu.Lock()
	defer gc.mu.Unlock()

	if index < 0 || index >= len(gc.frames) {
		return nil
	}
	return gc.frames[index]
}

func newGroupRing(size int, pool *FramePool) *groupRing {
	ring := &groupRing{
		caches: make([]atomic.Pointer[groupCache], size),
		pool:   pool,
		size:   size,
	}
	ring.gcPool.New = func() any {
		return &groupCache{
			frames: make([]*moqt.Frame, 0, 32),
		}
	}
	return ring
}

type groupRing struct {
	caches []atomic.Pointer[groupCache]
	pool   *FramePool
	size   int
	pos    atomic.Uint64
	gcPool sync.Pool
}

// reserve atomically allocates a ring slot for seq and returns the new cache.
// It must be called from the ingest goroutine (single writer) to preserve group ordering.
func (ring *groupRing) reserve(seq moqt.GroupSequence) *groupCache {
	cache := ring.gcPool.Get().(*groupCache)
	cache.seq = seq
	cache.complete.Store(false)
	cache.refCount.Store(0)
	cache.evicted.Store(false)

	idx := int(ring.pos.Add(1) % uint64(ring.size))
	old := ring.caches[idx].Swap(cache)
	if old != nil {
		ring.markEvicted(old)
	}
	return cache
}

// fill reads all frames from group into cache, calling onFrame after each frame
// and once more when the group is complete.
// onFrame receives the byte length of the frame just appended, or 0 on the
// completion call (after markComplete). It is safe to call fill concurrently
// for different groups.
func (ring *groupRing) fill(group frameSource, cache *groupCache, onFrame func(n int)) {
	buf := ring.pool.Get()
	defer ring.pool.Put(buf)
	frameCount := 0
	for frame := range group.Frames(buf) {
		frameCount++
		n := frame.Len()
		cache.append(frame, ring.pool)
		if onFrame != nil {
			onFrame(n)
		}
	}
	slog.Debug("group cached", "seq", cache.seq, "frames", frameCount)
	cache.markComplete()
	if onFrame != nil {
		onFrame(0) // signals group completion; no new bytes
	}
}

func (ring *groupRing) get(seq moqt.GroupSequence) *groupCache {
	idx := uint64(seq) % uint64(ring.size)
	cache := ring.caches[idx].Load()
	if cache != nil {
		cache.incrRef()
		// Double-check that it wasn't swapped out before we incremented it
		if ring.caches[idx].Load() != cache {
			ring.decrRef(cache)
			return nil
		}
	}
	return cache
}

func (ring *groupRing) decrRef(gc *groupCache) {
	if gc.refCount.Add(-1) == 0 {
		if gc.evicted.Load() {
			ring.releaseCache(gc)
		}
	}
}

func (ring *groupRing) markEvicted(gc *groupCache) {
	gc.evicted.Store(true)
	if gc.refCount.Load() == 0 {
		ring.releaseCache(gc)
	}
}

func (ring *groupRing) releaseCache(gc *groupCache) {
	gc.mu.Lock()
	if gc.frames == nil {
		gc.mu.Unlock()
		return
	}
	for _, f := range gc.frames {
		ring.pool.Put(f)
	}
	for i := range gc.frames {
		gc.frames[i] = nil
	}
	gc.frames = gc.frames[:0]
	gc.mu.Unlock()

	ring.gcPool.Put(gc)
}

func (ring *groupRing) head() moqt.GroupSequence {
	return moqt.GroupSequence(ring.pos.Load())
}

func (ring *groupRing) earliestAvailable() moqt.GroupSequence {
	head := ring.head()
	if head <= moqt.GroupSequence(ring.size) {
		return 1
	}
	return head - moqt.GroupSequence(ring.size) + 1
}
