package ingest

import (
	"sync"
	"testing"
	"testing/synctest"

	"github.com/okdaichi/gomoqt/moqt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// sourceGroup tests
// ---------------------------------------------------------------------------

func TestSourceGroup_Append_Next(t *testing.T) {
	g := &sourceGroup{
		seq:    1,
		frames: make([]*moqt.Frame, 0, 4),
	}

	f1 := moqt.NewFrame(5)
	f1.Write([]byte("hello"))
	f2 := moqt.NewFrame(5)
	f2.Write([]byte("world"))

	g.append(f1)
	g.append(f2)

	assert.Equal(t, f1, g.next(0))
	assert.Equal(t, f2, g.next(1))
	assert.Nil(t, g.next(2))
	assert.Nil(t, g.next(-1))
}

func TestSourceGroup_IsComplete(t *testing.T) {
	g := &sourceGroup{seq: 1, frames: make([]*moqt.Frame, 0, 4)}

	assert.False(t, g.isComplete())

	g.complete.Store(true)
	assert.True(t, g.isComplete())
}

// ---------------------------------------------------------------------------
// trackSource tests
// ---------------------------------------------------------------------------

func TestTrackSource_NewGroup(t *testing.T) {
	s := newTrackSource()

	assert.Equal(t, moqt.GroupSequence(0), s.head())

	// Create first group.
	g1 := s.newGroup()
	assert.Equal(t, moqt.GroupSequence(1), g1.seq)
	assert.Equal(t, moqt.GroupSequence(1), s.head())

	// Create second group.
	g2 := s.newGroup()
	assert.Equal(t, moqt.GroupSequence(2), g2.seq)
	assert.Equal(t, moqt.GroupSequence(2), s.head())
}

func TestTrackSource_Get(t *testing.T) {
	s := newTrackSource()

	g := s.newGroup()
	got := s.get(g.seq)
	assert.Equal(t, g, got)
}

func TestTrackSource_EarliestAvailable(t *testing.T) {
	s := newTrackSource()

	// No groups yet — earliest is still 1 (since head=0 < size=8).
	assert.Equal(t, moqt.GroupSequence(1), s.earliestAvailable())

	// Add groups up to ring size.
	for range defaultRingSize {
		s.newGroup()
	}
	// head = 8, size = 8, 8 <= 8 → earliest = 1
	assert.Equal(t, moqt.GroupSequence(1), s.earliestAvailable())

	// Add one more to overflow the ring.
	s.newGroup()
	// head = 9, 9 > 8 → earliest = 9 - 8 + 1 = 2
	assert.Equal(t, moqt.GroupSequence(2), s.earliestAvailable())
}

func TestTrackSource_PushVideo(t *testing.T) {
	s := newTrackSource()

	// First push creates a new group (keyframe).
	f1 := moqt.NewFrame(4)
	f1.Write([]byte{0x17, 0x01, 0x00, 0x00})
	s.pushVideo(f1, true)

	assert.Equal(t, moqt.GroupSequence(1), s.head())
	g := s.get(1)
	require.NotNil(t, g)
	assert.Equal(t, f1, g.next(0))
	assert.False(t, g.isComplete())

	// Non-keyframe appends to current group.
	f2 := moqt.NewFrame(3)
	f2.Write([]byte{0x27, 0x01, 0x00})
	s.pushVideo(f2, false)

	assert.Equal(t, moqt.GroupSequence(1), s.head()) // same group
	assert.Equal(t, f2, g.next(1))

	// New keyframe starts a new group and completes the old one.
	f3 := moqt.NewFrame(4)
	f3.Write([]byte{0x17, 0x01, 0x00, 0x00})
	s.pushVideo(f3, true)

	assert.Equal(t, moqt.GroupSequence(2), s.head())
	assert.True(t, g.isComplete()) // old group completed
	g2 := s.get(2)
	require.NotNil(t, g2)
	assert.Equal(t, f3, g2.next(0))
}

func TestTrackSource_PushAudio(t *testing.T) {
	s := newTrackSource()

	f := moqt.NewFrame(3)
	f.Write([]byte{0xAF, 0x01, 0x00})
	s.pushAudio(f)

	assert.Equal(t, moqt.GroupSequence(1), s.head())
	g := s.get(1)
	require.NotNil(t, g)
	assert.Equal(t, f, g.next(0))
	assert.True(t, g.isComplete()) // audio groups are immediately complete
}

func TestTrackSource_CloseCurrentGroup(t *testing.T) {
	s := newTrackSource()

	// Push a video frame to create a group.
	f := moqt.NewFrame(3)
	f.Write([]byte{0x17, 0x01, 0x00})
	s.pushVideo(f, true)

	g := s.get(1)
	require.NotNil(t, g)
	assert.False(t, g.isComplete())

	s.closeCurrentGroup()
	assert.True(t, g.isComplete())
}

func TestTrackSource_CloseCurrentGroup_NoGroup(t *testing.T) {
	s := newTrackSource()
	// Should not panic when no current group exists.
	s.closeCurrentGroup()
}

func TestTrackSource_SubscribeUnsubscribe(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := newTrackSource()

		ch1 := s.subscribe()
		ch2 := s.subscribe()

		// Both subscribers receive notifications.
		s.notify()
		synctest.Wait()

		select {
		case <-ch1:
		default:
			t.Fatal("subscriber 1 did not receive notification")
		}
		select {
		case <-ch2:
		default:
			t.Fatal("subscriber 2 did not receive notification")
		}

		// Unsubscribe ch1; only ch2 should receive further notifications.
		s.unsubscribe(ch1)

		s.notify()
		synctest.Wait()

		select {
		case <-ch1:
			t.Fatal("unsubscribed channel should not receive notification")
		default:
		}
		select {
		case <-ch2:
		default:
			t.Fatal("subscriber 2 did not receive notification after ch1 unsubscribed")
		}
	})
}

func TestTrackSource_Notify(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := newTrackSource()

		ch := s.subscribe()
		defer s.unsubscribe(ch)

		s.notify()

		synctest.Wait()

		select {
		case <-ch:
			// Expected: notification received.
		default:
			t.Fatal("expected notification on subscriber channel")
		}
	})
}

func TestTrackSource_Notify_NonBlocking(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := newTrackSource()

		ch := s.subscribe()
		defer s.unsubscribe(ch)

		// Fill the channel buffer.
		s.notify()
		synctest.Wait()

		// Second notify should not block.
		s.notify()
		synctest.Wait()

		// Drain one.
		<-ch

		// Channel should be empty now.
		select {
		case <-ch:
			t.Fatal("expected no more notifications")
		default:
		}
	})
}

func TestTrackSource_MultipleSubscribers(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := newTrackSource()

		ch1 := s.subscribe()
		ch2 := s.subscribe()
		defer s.unsubscribe(ch1)
		defer s.unsubscribe(ch2)

		s.notify()
		synctest.Wait()

		select {
		case <-ch1:
		default:
			t.Fatal("subscriber 1 did not receive notification")
		}

		select {
		case <-ch2:
		default:
			t.Fatal("subscriber 2 did not receive notification")
		}
	})
}

func TestTrackSource_RingWrapAround(t *testing.T) {
	s := newTrackSource()

	// Fill beyond ring size.
	for i := range defaultRingSize + 3 {
		f := moqt.NewFrame(1)
		f.Write([]byte{byte(i)})
		s.pushAudio(f)
	}

	// Oldest slots have been overwritten.
	head := s.head()
	assert.Equal(t, moqt.GroupSequence(defaultRingSize+3), head)

	// Can still access recent groups.
	for seq := s.earliestAvailable(); seq <= head; seq++ {
		g := s.get(seq)
		require.NotNil(t, g)
		assert.Equal(t, seq, g.seq)
	}
}

// ---------------------------------------------------------------------------
// ingestHandler tests
// ---------------------------------------------------------------------------

func TestNewIngestHandler(t *testing.T) {
	h := newIngestHandler()

	require.NotNil(t, h.video)
	require.NotNil(t, h.audio)
	require.NotNil(t, h.done)
}

func TestIngestHandler_Close(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newIngestHandler()

		// Push a video frame so there's a current group.
		f := moqt.NewFrame(3)
		f.Write([]byte{0x17, 0x01, 0x00})
		h.video.pushVideo(f, true)

		g := h.video.get(1)
		require.NotNil(t, g)
		assert.False(t, g.isComplete())

		h.close()

		// Video group completed.
		assert.True(t, g.isComplete())

		// Done channel closed.
		select {
		case <-h.done:
		default:
			t.Fatal("expected done channel to be closed")
		}
	})
}

func TestIngestHandler_Close_Idempotent(t *testing.T) {
	h := newIngestHandler()

	// Multiple calls should not panic.
	h.close()
	h.close()
	h.close()
}

// ---------------------------------------------------------------------------
// Session tests
// ---------------------------------------------------------------------------

func TestNewSession_PushVideo_PushAudio_Close(t *testing.T) {
	mux := moqt.NewTrackMux()

	sess := NewSession(mux, "/live/test")

	// Push video keyframe.
	sess.PushVideo(0, []byte{0x17, 0x01, 0x00, 0x00}, true)
	assert.Equal(t, moqt.GroupSequence(1), sess.handler.video.head())

	// Push video inter-frame (same group).
	sess.PushVideo(33000, []byte{0x27, 0x01, 0x00, 0x00}, false)
	assert.Equal(t, moqt.GroupSequence(1), sess.handler.video.head())

	// Push audio.
	sess.PushAudio(0, []byte{0xAF, 0x01, 0x00})
	assert.Equal(t, moqt.GroupSequence(1), sess.handler.audio.head())

	// Close should not panic.
	sess.Close()
}

func TestSession_Close_Idempotent(t *testing.T) {
	mux := moqt.NewTrackMux()
	sess := NewSession(mux, "/live/test2")

	sess.Close()
	sess.Close() // should not panic
}

func TestSession_PushMultipleKeyframes(t *testing.T) {
	mux := moqt.NewTrackMux()
	sess := NewSession(mux, "/live/gop-test")
	defer sess.Close()

	// First keyframe → group 1.
	sess.PushVideo(0, []byte{0x17, 0x01}, true)
	assert.Equal(t, moqt.GroupSequence(1), sess.handler.video.head())

	// Inter-frames in group 1.
	sess.PushVideo(33000, []byte{0x27, 0x01}, false)
	sess.PushVideo(66000, []byte{0x27, 0x02}, false)

	// Second keyframe → group 2.
	sess.PushVideo(100000, []byte{0x17, 0x02}, true)
	assert.Equal(t, moqt.GroupSequence(2), sess.handler.video.head())

	// Verify group 1 was completed.
	g := sess.handler.video.get(1)
	require.NotNil(t, g)
	assert.True(t, g.isComplete())

	// Verify group 2 has the keyframe (MediaFrame-wrapped).
	g2 := sess.handler.video.get(2)
	require.NotNil(t, g2)
	f := g2.next(0)
	require.NotNil(t, f)
	expected := buildMediaFrame(100000, []byte{0x17, 0x02})
	assert.Equal(t, expected, f.Body())
}

func TestSession_AudioIndependentGroups(t *testing.T) {
	mux := moqt.NewTrackMux()
	sess := NewSession(mux, "/live/audio-test")
	defer sess.Close()

	// Each audio push creates its own complete group.
	sess.PushAudio(0, []byte{0xAF, 0x01})
	sess.PushAudio(23000, []byte{0xAF, 0x02})
	sess.PushAudio(46000, []byte{0xAF, 0x03})

	assert.Equal(t, moqt.GroupSequence(3), sess.handler.audio.head())

	// All groups should be complete.
	for seq := moqt.GroupSequence(1); seq <= 3; seq++ {
		g := sess.handler.audio.get(seq)
		require.NotNil(t, g)
		assert.True(t, g.isComplete())
	}
}

// ---------------------------------------------------------------------------
// sourceGroup concurrency test
// ---------------------------------------------------------------------------

func TestSourceGroup_ConcurrentAppendNext(t *testing.T) {
	g := &sourceGroup{
		seq:    1,
		frames: make([]*moqt.Frame, 0, 100),
	}

	const n = 50
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := range n {
			f := moqt.NewFrame(1)
			f.Write([]byte{byte(i)})
			g.append(f)
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := range n {
			for {
				if f := g.next(i); f != nil {
					break
				}
			}
		}
	}()

	wg.Wait()

	// All frames should be present after completion.
	for i := range n {
		assert.NotNil(t, g.next(i))
	}
	assert.Nil(t, g.next(n))
}
