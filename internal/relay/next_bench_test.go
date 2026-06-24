package relay

import (
	"github.com/qumo-dev/gomoqt/moqt"
	"testing"
)

// BenchmarkNext_Serial compares serial next() calls with baseline vs lockless
func BenchmarkNext_Serial(b *testing.B) {
	gc := &groupCache{
		seq:    1,
		frames: make([]*moqt.Frame, 0, 100),
	}

	pool := NewFramePool(DefaultNewFrameCapacity)
	frame := moqt.NewFrame(DefaultNewFrameCapacity)
	frame.Write([]byte("test"))

	// Pre-populate with 100 frames
	for i := 0; i < 100; i++ {
		gc.append(frame, pool)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = gc.next(i % 100)
	}
}
