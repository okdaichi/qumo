package relay

import (
	"sync"

	"github.com/okdaichi/gomoqt/moqt"
)

var NewFrameCapacity = defaultNewFrameCapacity

const defaultNewFrameCapacity = 1500

var DefaultFramePool = NewFramePool(NewFrameCapacity)

type FramePool struct {
	pool sync.Pool
}

func NewFramePool(cap int) *FramePool {
	return &FramePool{
		pool: sync.Pool{
			New: func() any {
				return moqt.NewFrame(cap)
			},
		},
	}
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
