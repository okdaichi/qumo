package relay

import (
	"sync"

	"github.com/okdaichi/gomoqt/moqt"
)

const DefaultNewFrameCapacity int = 1500

var DefaultFramePool = NewFramePool(DefaultNewFrameCapacity)

type FramePool struct {
	Capacity int
	pool     sync.Pool
}

func NewFramePool(cap int) *FramePool {
	fp := &FramePool{
		Capacity: cap,
	}

	fp.pool.New = func() any {
		return moqt.NewFrame(fp.Capacity)
	}

	return fp
}

func (fp *FramePool) Get() *moqt.Frame {
	frame := fp.pool.Get().(*moqt.Frame)
	frame.Reset()
	return frame
}

func (fp *FramePool) Put(f *moqt.Frame) {
	f.Reset()
	fp.pool.Put(f)
}
