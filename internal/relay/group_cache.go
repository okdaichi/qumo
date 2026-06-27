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
// It bounds growth and bounds the per-append work of the copy-on-write publish (see
// append): each append copies the current snapshot, so building an N-frame group is
// O(N^2) pointer copies. 256 keeps that trivial while leaving headroom over a
// typical ~120-frame (60fps x 2s) video group or a larger A/V group.
const MaxFramesPerGroup = 256

type groupCache struct {
	// frames holds the group's frames as an immutable snapshot published via
	// atomic.Pointer (RCU / copy-on-write). append builds a new slice and
	// CAS-publishes it; readers Load a snapshot and read it without any lock.
	// Because a published snapshot is never mutated in place, reads are
	// data-race-free under the Go memory model (the previous len-publishing scheme
	// raced on the slice header). Memory is reclaimed at group granularity by the
	// ring/pool, never by shrinking a live snapshot.
	frames   atomic.Pointer[[]*moqt.Frame]
	seq      moqt.GroupSequence
	complete atomic.Bool // True when all frames have been added
	refCount atomic.Int32
	evicted  atomic.Bool
	released atomic.Bool
}

// newGroupCache allocates a groupCache whose frames snapshot is an empty slice with
// the given capacity. atomic.Pointer cannot be set in a struct literal, so all
// construction goes through here or through groupRing.gcPool.New.
func newGroupCache(seq moqt.GroupSequence, frameCap int) *groupCache {
	gc := &groupCache{seq: seq}
	empty := make([]*moqt.Frame, 0, frameCap)
	gc.frames.Store(&empty)
	return gc
}

// snapshot returns the current immutable frames slice. Tests inspect contents
// through this; production reads go through next(), which Loads inline.
func (gc *groupCache) snapshot() []*moqt.Frame {
	return *gc.frames.Load()
}

// resetForReuse replaces the frames snapshot with a fresh empty slice. Called only
// on a cache that no reader can observe (during ring.reserve init and releaseCache),
// so the discarded snapshot has no live readers.
func (gc *groupCache) resetForReuse() {
	empty := make([]*moqt.Frame, 0, MaxFramesPerGroup)
	gc.frames.Store(&empty)
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

// append clones f into the cache using pool. It is thread-safe and safe to call
// concurrently: appends are serialized by a compare-and-swap retry loop on the
// snapshot pointer, so concurrent appends never lose a frame (at the cost of an
// O(N) copy per append). The published snapshot is immutable, so concurrent next()
// readers are data-race-free.
func (gc *groupCache) append(f *moqt.Frame, pool *FramePool) {
	// Clone outside the CAS loop: the clone is private until a CAS succeeds and is
	// reused across retries (a failed attempt discards only its throwaway slice).
	clone := pool.Get()
	_, _ = f.WriteTo(clone)

	for {
		oldPtr := gc.frames.Load()
		old := *oldPtr
		if len(old) >= MaxFramesPerGroup {
			pool.Put(clone)
			slog.Warn("group exceeded max frame limit", "seq", gc.seq, "max", MaxFramesPerGroup)
			return
		}
		// Copy-on-write: build a new immutable snapshot = old + clone, then publish.
		next := make([]*moqt.Frame, len(old)+1)
		copy(next, old)
		next[len(old)] = clone
		if gc.frames.CompareAndSwap(oldPtr, &next) {
			return
		}
		// Lost the CAS to another append; reload and retry (clone is still ours).
	}
}

// next returns the frame at the given index, or nil if out of range.
// Thread-free and lock-free: it Loads the current immutable snapshot (one atomic
// pointer read) and indexes into it. Published snapshots are never mutated, so this
// is data-race-free under the Go memory model.
func (gc *groupCache) next(index int) *moqt.Frame {
	s := gc.frames.Load()
	if s == nil || index < 0 || index >= len(*s) {
		return nil
	}
	return (*s)[index]
}

func newGroupRing(size int, pool *FramePool) *groupRing {
	ring := &groupRing{
		caches: make([]atomic.Pointer[groupCache], size),
		pool:   pool,
		size:   size,
	}
	ring.gcPool.New = func() any {
		return newGroupCache(0, MaxFramesPerGroup)
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
	// Reinitialize content state so the read contract does not depend on
	// releaseCache having cleaned up: publish a fresh empty snapshot.
	cache.resetForReuse()

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

	// No lock: releaseCache runs at most once (released CAS above) and only when
	// refCount == 0, so no reader is observing this cache. Return each frame in the
	// current snapshot to the pool, then drop the snapshot for reuse.
	snap := gc.frames.Load()
	if snap == nil {
		return
	}
	for _, f := range *snap {
		ring.pool.Put(f)
	}
	gc.resetForReuse()

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
