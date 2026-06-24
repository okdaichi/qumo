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

// MaxFramesPerGroup is the maximum number of frames allowed in a single group cache.
// This limit prevents unbounded growth and enables lockless reads by guaranteeing
// that the frames slice never reallocates after construction.
// Typical video groups at 60fps for 2 seconds = ~120 frames.
// Audio-video groups may have more. Choose a value with comfortable headroom.
const MaxFramesPerGroup = 256

type groupCache struct {
	mu       sync.RWMutex // Protects frames slice; Lock for append only
	seq      moqt.GroupSequence
	frames   []*moqt.Frame
	frameLen atomic.Int32 // Number of frames in frames slice (for lockless reads)
	complete atomic.Bool  // True when all frames have been added
	refCount atomic.Int32
	evicted  atomic.Bool
	released atomic.Bool
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
	// Fast path: quick length check without full lock.
	// Racy in multi-writer mode, but we re-check under Lock below.
	gc.mu.RLock()
	atLimit := len(gc.frames) >= MaxFramesPerGroup
	gc.mu.RUnlock()

	if atLimit {
		// Slow path: log once and skip
		slog.Warn("group exceeded max frame limit", "seq", gc.seq, "max", MaxFramesPerGroup)
		return
	}

	// Clone outside the lock: the clone is private until appended, so the
	// memmove does not need to exclude concurrent readers.
	clone := pool.Get()
	_, _ = f.WriteTo(clone)

	gc.mu.Lock()
	// Re-check under lock in case another writer raced us (rare)
	if len(gc.frames) >= MaxFramesPerGroup {
		pool.Put(clone) // return unused clone to pool
		gc.mu.Unlock()
		return
	}
	gc.frames = append(gc.frames, clone)
	// Store length AFTER appending so readers see valid frames
	newLen := int32(len(gc.frames))
	gc.frameLen.Store(newLen)
	gc.mu.Unlock()
}

// next returns the frame at the given index.
// Thread-safe: can be called concurrently. Uses atomic length for lockless reads.
func (gc *groupCache) next(index int) *moqt.Frame {
	// Lockless read: load length atomically and check bounds
	frameLen := gc.frameLen.Load()
	if index < 0 || index >= int(frameLen) {
		return nil
	}
	// Slice base pointer is stable (F4: pre-allocated, MaxFramesPerGroup)
	// and we never shrink, so this access is safe without lock.
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
			frames: make([]*moqt.Frame, 0, MaxFramesPerGroup),
		}
	}
	return ring
}

type groupRing struct {
	mu     sync.RWMutex
	caches []atomic.Pointer[groupCache]
	pool   *FramePool
	size   int
	pos    atomic.Uint64
	gcPool sync.Pool
}

// reserve atomically allocates a ring slot for seq and returns the new cache.
// It must be called from the ingest goroutine (single writer) to preserve group ordering.
func (ring *groupRing) reserve(seq moqt.GroupSequence) *groupCache {
	// Prepare the new cache before acquiring the lock — it is private until Swap.
	cache := ring.gcPool.Get().(*groupCache)
	cache.seq = seq
	cache.complete.Store(false)
	cache.refCount.Store(1) // 1 reference for the in-flight filler
	cache.evicted.Store(false)
	cache.released.Store(false)
	// Reinitialize content state so the lockless read contract does not depend
	// on releaseCache having cleaned up. append indexes by len(frames) and next()
	// gates reads by frameLen, so both must start at zero together for a new group.
	cache.frames = cache.frames[:0]
	cache.frameLen.Store(0)

	idx := int(ring.pos.Add(1) % uint64(ring.size))

	ring.mu.Lock()
	old := ring.caches[idx].Swap(cache)
	if old != nil {
		ring.markEvictedLocked(old)
	}
	ring.mu.Unlock()
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
	defer ring.decrRef(cache) // filler completes and releases its reference

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
	ring.mu.RLock()
	defer ring.mu.RUnlock()

	idx := uint64(seq) % uint64(ring.size)
	cache := ring.caches[idx].Load()
	if cache != nil && cache.seq == seq && !cache.released.Load() {
		cache.incrRef()
		return cache
	}
	return nil
}

func (ring *groupRing) decrRef(gc *groupCache) {
	if gc.refCount.Add(-1) == 0 {
		if gc.evicted.Load() {
			ring.releaseCache(gc)
		}
	}
}

func (ring *groupRing) markEvictedLocked(gc *groupCache) {
	gc.evicted.Store(true)
	if gc.refCount.Load() == 0 {
		ring.releaseCache(gc)
	}
}

func (ring *groupRing) releaseCache(gc *groupCache) {
	if !gc.released.CompareAndSwap(false, true) {
		return // already released by another goroutine
	}

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
	gc.frameLen.Store(0) // Reset atomic length for reuse
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
