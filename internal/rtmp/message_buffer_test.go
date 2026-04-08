package rtmp

import (
	"testing"
)

func TestMessageRingBufferPushPopWrapAround(t *testing.T) {
	rb := newMessageRingBuffer(2)
	m1 := &message{timestamp: 1}
	m2 := &message{timestamp: 2}
	m3 := &message{timestamp: 3}

	if !rb.Push(m1) || !rb.Push(m2) {
		t.Fatal("expected initial pushes to succeed")
	}
	if rb.Push(m3) {
		t.Fatal("expected push on full ring buffer to fail")
	}

	got, ok := rb.Pop()
	if !ok || got != m1 {
		t.Fatalf("first pop got=%v ok=%v want=%v", got, ok, m1)
	}
	if !rb.Push(m3) {
		t.Fatal("expected push after pop to succeed")
	}

	got, ok = rb.Pop()
	if !ok || got != m2 {
		t.Fatalf("second pop got=%v ok=%v want=%v", got, ok, m2)
	}
	got, ok = rb.Pop()
	if !ok || got != m3 {
		t.Fatalf("third pop got=%v ok=%v want=%v", got, ok, m3)
	}
	if _, ok := rb.Pop(); ok {
		t.Fatal("expected pop on empty ring buffer to fail")
	}
}

func TestMessagePoolResetsFields(t *testing.T) {
	m := acquireMessage()
	m.timestamp = 42
	m.messageStreamID = 7
	m.messageTypeID = 9
	if _, err := m.payload.WriteString("hello"); err != nil {
		t.Fatalf("payload write failed: %v", err)
	}

	releaseMessage(m)

	reused := acquireMessage()
	if reused.timestamp != 0 || reused.messageStreamID != 0 || reused.messageTypeID != 0 {
		t.Fatalf("pooled message was not reset: %+v", reused)
	}
	if reused.payload == nil {
		t.Fatal("pooled message payload is nil")
	}
	if reused.payload.Len() != 0 {
		t.Fatalf("pooled payload was not reset: len=%d", reused.payload.Len())
	}
}
