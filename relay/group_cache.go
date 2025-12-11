package relay

import (
	"sync"
	"sync/atomic"

	"github.com/okdaichi/gomoqt/moqt"
)

var GroupCacheCount = 8

type groupCache struct {
	mu     sync.Mutex // Protects frames slice for defensive programming
	seq    moqt.GroupSequence
	frames []*moqt.Frame
}

// Append appends a frame to the group cache.
// The frame is cloned and stored in the cache.
// Thread-safe: can be called concurrently (though typically called from single goroutine).
func (gc *groupCache) append(f *moqt.Frame) {
	gc.mu.Lock()
	defer gc.mu.Unlock()

	clone := DefaultFramePool.Get()

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

func newGroupRing() *groupRing {
	ring := &groupRing{
		caches: make([]atomic.Pointer[groupCache], GroupCacheCount),
		size:   GroupCacheCount, // Is this needed?
	}
	return ring
}

type groupRing struct {
	caches []atomic.Pointer[groupCache]
	size   int
	pos    atomic.Uint64
}

func (ring *groupRing) add(group *moqt.GroupReader) {
	cache := &groupCache{
		seq:    group.GroupSequence(),
		frames: make([]*moqt.Frame, 0, 1),
	}

	idx := int(ring.pos.Add(1) % uint64(ring.size))
	ring.caches[idx].Store(cache)

	frame := DefaultFramePool.Get()

	for frame := range group.Frames(frame) {
		cache.append(frame)
	}
}

func (ring *groupRing) get(seq moqt.GroupSequence) *groupCache {
	return ring.caches[uint64(seq)%uint64(ring.size)].Load()
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
