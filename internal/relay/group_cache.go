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

// MaxFramesPerGroup is the maximum number of frames allowed in a single group
// cache. It bounds growth and sizes the per-cache backing array (see groupCache).
// 256 leaves headroom over a typical ~120-frame (60fps x 2s) video group or a
// larger A/V group.
const MaxFramesPerGroup = 256

type groupCache struct {
	// slots is a fixed, pre-allocated backing array of per-frame atomic pointers
	// (length >= MaxFramesPerGroup), reused across group generations via the
	// ring's gcPool — never reallocated on the append path. Together with count
	// it forms a lock-free append-only vector: append reserves a slot by
	// CAS-incrementing count (the published length), then Stores its clone into
	// that slot; next Loads a slot. This replaces the former
	// atomic.Pointer[[]*Frame] copy-on-write scheme, which rebuilt the whole
	// snapshot on every append — O(N^2) pointer copies and one slice allocation
	// per frame across an N-frame group. The new scheme is O(1) per append with
	// zero per-append allocation, and reads remain a single atomic load.
	//
	// Concurrency: appends are concurrency-safe (the CAS on count reserves a
	// unique slot per appender, so no frame is lost or overwritten) and
	// data-race-free (count and each slot are accessed only through atomics, and
	// distinct slots are distinct memory locations). In production, append is
	// single-writer per cache (one fill goroutine), so slots fill in order.
	slots []atomic.Pointer[moqt.Frame]
	// count is the number of reserved slots, i.e. the published length that
	// readers observe. A slot that has been reserved (count incremented) but not
	// yet Stored reads back nil; the egress loop already treats a nil frame as
	// "not ready yet" and waits for the next broadcast, so the brief reserve→Store
	// window is harmless. count is kept in [0, MaxFramesPerGroup] by the CAS in
	// append, so it never exceeds len(slots).
	count atomic.Int32

	seq      moqt.GroupSequence
	complete atomic.Bool // True when all frames have been added
	refCount atomic.Int32
	evicted  atomic.Bool
	released atomic.Bool
}

// newGroupCache allocates a groupCache whose slot backing array is exactly
// MaxFramesPerGroup long. That is the only correct size: append is hard-limited
// to MaxFramesPerGroup frames, so the array never needs to be larger (extra
// slots would be dead) and must not be smaller (append would index out of
// range). All construction goes through here or through groupRing.gcPool.New.
func newGroupCache(seq moqt.GroupSequence) *groupCache {
	return &groupCache{
		seq:   seq,
		slots: make([]atomic.Pointer[moqt.Frame], MaxFramesPerGroup),
	}
}

// snapshot returns a copy of the group's current frames. Tests inspect contents
// through this; production reads go through next(), which Loads a single slot
// inline. A reserved-but-not-yet-Stored slot appears as a nil entry.
func (gc *groupCache) snapshot() []*moqt.Frame {
	n := min(int(gc.count.Load()), len(gc.slots))
	out := make([]*moqt.Frame, n)
	for i := range out {
		out[i] = gc.slots[i].Load()
	}
	return out
}

// resetForReuse clears the slot array and count so the cache can be reused for a
// new group generation. Called only on a cache that no reader can observe (during
// ring.reserve init and releaseCache). It reuses the backing array — no
// allocation — unlike the previous scheme, which allocated a fresh slice.
func (gc *groupCache) resetForReuse() {
	n := min(int(gc.count.Load()), len(gc.slots))
	for i := range n {
		gc.slots[i].Store(nil)
	}
	gc.count.Store(0)
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
// concurrently: each appender reserves a unique slot via a compare-and-swap on
// count, so concurrent appends never lose or overwrite a frame — at O(1) cost
// per append (no snapshot copy). count and the reserved slot are accessed only
// through atomics, so concurrent next() readers are data-race-free.
func (gc *groupCache) append(f *moqt.Frame, pool *FramePool) {
	// Fast path: if the group is already full, bail before paying for the clone.
	// Re-checked under the CAS below; skipping the pool.Get/WriteTo on the common
	// at-limit path avoids a wasted frame copy.
	if int(gc.count.Load()) >= MaxFramesPerGroup {
		slog.Warn("group exceeded max frame limit", "seq", gc.seq, "max", MaxFramesPerGroup)
		return
	}

	// Clone before reserving; the clone is reused across CAS retries.
	clone := pool.Get()
	_, _ = f.WriteTo(clone)

	for {
		n := int(gc.count.Load())
		if n >= MaxFramesPerGroup {
			pool.Put(clone) // raced past the limit between the fast check and here
			slog.Warn("group exceeded max frame limit", "seq", gc.seq, "max", MaxFramesPerGroup)
			return
		}
		// Reserve slot n by publishing count = n+1. The CAS guarantees a unique
		// slot per appender and keeps count in [0, MaxFramesPerGroup], so slot n
		// is always in range. A concurrent reader may briefly see count = n+1
		// before the Store below; it reads slot n as nil (not-ready) and waits.
		if gc.count.CompareAndSwap(int32(n), int32(n+1)) {
			gc.slots[n].Store(clone)
			return
		}
		// Lost the CAS to another appender; retry (clone is still ours).
	}
}

// next returns the frame at the given index, or nil if the index is out of range
// or its slot has been reserved but not yet stored. Lock-free: one atomic load of
// count to bound the index, then one atomic load of the slot. Slots are only ever
// published through atomics, so this is data-race-free under the Go memory model.
func (gc *groupCache) next(index int) *moqt.Frame {
	if index < 0 || index >= int(gc.count.Load()) || index >= len(gc.slots) {
		return nil
	}
	return gc.slots[index].Load()
}

func newGroupRing(size int, pool *FramePool) *groupRing {
	ring := &groupRing{
		caches: make([]atomic.Pointer[groupCache], size),
		pool:   pool,
		size:   size,
	}
	ring.gcPool.New = func() any {
		return newGroupCache(0)
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
//
// onFrame is called per frame (not per group) on purpose: it drives both
// per-frame byte accounting AND the subscriber wakeup (broadcast). Egress waits
// for a wakeup at two points — between groups and, for a group whose frames
// trickle in over time, between frames within the group (deliverGroup). The
// per-frame wakeup is what delivers each trickled frame immediately; it is the
// sole delivery mechanism (the egress wait has no poll-timer fallback — see the
// egress wait comment and TestRelayChain_NotifyTimeoutNotLoadBearing), so
// collapsing it to a per-group wakeup would stall intra-group frames. The wakeup
// is cheap: a non-blocking send that mostly hits the buffered channel's default
// branch.
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
	// refCount == 0, so no reader is observing this cache. Return each stored
	// frame to the pool, then clear the slots for reuse.
	n := min(int(gc.count.Load()), len(gc.slots))
	for i := range n {
		if f := gc.slots[i].Load(); f != nil {
			ring.pool.Put(f)
		}
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
