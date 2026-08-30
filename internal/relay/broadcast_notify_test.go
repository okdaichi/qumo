package relay

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestBroadcastNotify_Init(t *testing.T) {
	var n broadcastNotify
	n.init()
	state := n.listen()
	assert.Equal(t, uint64(0), state.seq, "initial seq should be 0")
	assert.NotNil(t, state.ch, "initial channel should not be nil")
}

func TestBroadcastNotify_ZeroValueSafe(t *testing.T) {
	// Verify lazyInit makes zero-value safe
	var n broadcastNotify
	state := n.listen()
	assert.Equal(t, uint64(0), state.seq, "zero-value should return seq=0")
	assert.NotNil(t, state.ch, "zero-value should return valid channel")
}

func TestBroadcastNotify_NotifyAdvancesSeq(t *testing.T) {
	var n broadcastNotify
	n.init()

	state := n.listen()
	assert.Equal(t, uint64(0), state.seq)

	n.notify()

	state = n.listen()
	assert.Equal(t, uint64(1), state.seq, "notify should advance seq by 1")

	n.notify()
	state = n.listen()
	assert.Equal(t, uint64(2), state.seq, "second notify should advance seq to 2")
}

func TestBroadcastNotify_OldChannelClosed(t *testing.T) {
	var n broadcastNotify
	n.init()

	// Capture the initial channel
	before := n.listen()
	assert.Equal(t, uint64(0), before.seq)

	// Notify should close the old channel
	n.notify()

	// The old channel should be closed - select should return immediately
	select {
	case <-before.ch:
		// Expected: channel is closed
	default:
		t.Error("old channel should be closed after notify")
	}

	// The new channel should be open (not closed)
	after := n.listen()
	select {
	case <-after.ch:
		t.Error("new channel should not be closed after notify")
	default:
		// Expected: channel is open
	}
}

func TestBroadcastNotify_NotifyWakesWaiter(t *testing.T) {
	var n broadcastNotify
	n.init()

	done := make(chan struct{})
	go func() {
		state := n.listen()
		<-state.ch // wait for close
		close(done)
	}()

	// Give the goroutine time to reach the select
	time.Sleep(10 * time.Millisecond)

	n.notify()

	select {
	case <-done:
		// Waiter was woken up
	case <-time.After(time.Second):
		t.Fatal("waiter was not woken up within 1s")
	}
}

func TestBroadcastNotify_MultipleNotify(t *testing.T) {
	var n broadcastNotify
	n.init()

	const iterations = 1000
	for range iterations {
		n.notify()
	}

	state := n.listen()
	assert.Equal(t, uint64(iterations), state.seq)
}

func TestBroadcastNotify_ListenReturnsConsistentState(t *testing.T) {
	var n broadcastNotify
	n.init()

	n.notify()
	n.notify()

	// listen() should return both seq and ch from the same atomic snapshot
	state := n.listen()
	assert.Equal(t, uint64(2), state.seq, "seq should be 2 after two notifies")
	assert.NotNil(t, state.ch, "channel should not be nil")

	// Channel should be open (new channel from latest notify)
	select {
	case <-state.ch:
		t.Error("current channel should be open")
	default:
		// Expected
	}
}
