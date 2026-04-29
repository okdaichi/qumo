package relay

import (
	"iter"
	"time"

	"github.com/qumo-dev/gomoqt/moqt"
)

// fakeFrameSource is a test double that satisfies the frameSource interface.
// It yields a fixed set of pre-built payloads in order, simulating a fully
// received MoQT group without requiring a real network connection.
type fakeFrameSource struct {
	seq    moqt.GroupSequence
	frames [][]byte
}

func (f *fakeFrameSource) Frames(buf *moqt.Frame) iter.Seq[*moqt.Frame] {
	return func(yield func(*moqt.Frame) bool) {
		for _, data := range f.frames {
			buf.Reset()
			buf.Write(data)
			if !yield(buf) {
				return
			}
		}
	}
}

// slowFrameSource wraps fakeFrameSource and introduces a configurable per-frame
// delay. It is used in tests that verify concurrent throughput improvements.
type slowFrameSource struct {
	fakeFrameSource
	delay time.Duration
}

func (s *slowFrameSource) Frames(buf *moqt.Frame) iter.Seq[*moqt.Frame] {
	return func(yield func(*moqt.Frame) bool) {
		for _, data := range s.frames {
			time.Sleep(s.delay)
			buf.Reset()
			buf.Write(data)
			if !yield(buf) {
				return
			}
		}
	}
}
