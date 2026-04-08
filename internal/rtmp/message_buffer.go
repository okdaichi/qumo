package rtmp

import (
	"bytes"
	"sync"
)

const DefaultMessageBufferSize = 128

// message represents a fully assembled RTMP message.
type message struct {
	messageHeader
	payload bytes.Buffer
}

func (m *message) unreadBytes() int {
	return int(m.messageHeader.messageLength) - m.payload.Len()
}

var messagePool = sync.Pool{
	New: func() any {
		return &message{payload: bytes.Buffer{}}
	},
}

func acquireMessage() *message {
	m := messagePool.Get().(*message)
	m.timestamp = 0
	m.messageStreamID = 0
	m.messageTypeID = 0
	m.payload.Reset()

	return m
}

func releaseMessage(m *message) {
	if m == nil {
		return
	}
	m.payload.Reset()
	m.timestamp = 0
	m.messageStreamID = 0
	m.messageTypeID = 0
	messagePool.Put(m)
}

// messageRingBuffer is a fixed-size circular queue of messages.
type messageRingBuffer struct {
	items []*message
	head  int
	size  int
}

func newMessageRingBuffer(capacity int) *messageRingBuffer {
	if capacity <= 0 {
		capacity = DefaultMessageBufferSize
	}
	return &messageRingBuffer{items: make([]*message, capacity)}
}

func (rb *messageRingBuffer) Len() int {
	if rb == nil {
		return 0
	}
	return rb.size
}

func (rb *messageRingBuffer) Cap() int {
	if rb == nil {
		return 0
	}
	return len(rb.items)
}

func (rb *messageRingBuffer) Full() bool {
	return rb != nil && rb.size == len(rb.items)
}

func (rb *messageRingBuffer) Push(m *message) bool {
	if rb == nil || len(rb.items) == 0 || rb.size == len(rb.items) {
		return false
	}
	idx := (rb.head + rb.size) % len(rb.items)
	rb.items[idx] = m
	rb.size++
	return true
}

func (rb *messageRingBuffer) Pop() (*message, bool) {
	if rb == nil || rb.size == 0 {
		return nil, false
	}
	m := rb.items[rb.head]
	rb.items[rb.head] = nil
	rb.head = (rb.head + 1) % len(rb.items)
	rb.size--
	return m, true
}

func (rb *messageRingBuffer) Reset() {
	if rb == nil {
		return
	}
	for i := 0; i < rb.size; i++ {
		idx := (rb.head + i) % len(rb.items)
		rb.items[idx] = nil
	}
	rb.head = 0
	rb.size = 0
}
