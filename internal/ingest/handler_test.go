package ingest

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/qumo-dev/gomoqt/moqt"
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
// trackBuffer tests
// ---------------------------------------------------------------------------

func newTestTrackBuffer() *trackBuffer {
	ctx := context.Background()
	return newTrackBuffer(ctx, "test")
}

func TestTrackBuffer_OpenGroup(t *testing.T) {
	b := newTestTrackBuffer()

	assert.Equal(t, moqt.GroupSequence(0), b.head())

	// Create first group.
	g1 := b.openGroup()
	assert.Equal(t, moqt.GroupSequence(1), g1.seq)
	assert.Equal(t, moqt.GroupSequence(1), b.head())

	// Create second group.
	g2 := b.openGroup()
	assert.Equal(t, moqt.GroupSequence(2), g2.seq)
	assert.Equal(t, moqt.GroupSequence(2), b.head())
}

func TestTrackBuffer_Get(t *testing.T) {
	b := newTestTrackBuffer()

	g := b.openGroup()
	got := b.get(g.seq)
	assert.Equal(t, g, got)
}

func TestTrackBuffer_EarliestAvailable(t *testing.T) {
	b := newTestTrackBuffer()

	// No groups yet — earliest is still 1 (since head=0 < size=8).
	assert.Equal(t, moqt.GroupSequence(1), b.earliestAvailable())

	// Add groups up to ring size.
	for range defaultRingSize {
		b.openGroup()
	}
	// head = 8, size = 8, 8 <= 8 → earliest = 1
	assert.Equal(t, moqt.GroupSequence(1), b.earliestAvailable())

	// Add one more to overflow the ring.
	b.openGroup()
	// head = 9, 9 > 8 → earliest = 9 - 8 + 1 = 2
	assert.Equal(t, moqt.GroupSequence(2), b.earliestAvailable())
}

// ---------------------------------------------------------------------------
// videoTrack tests
// ---------------------------------------------------------------------------

func TestVideoTrack_Push(t *testing.T) {
	v := newVideoTrack(context.Background())

	// First push creates a new group (keyframe).
	f1 := moqt.NewFrame(4)
	f1.Write([]byte{0x17, 0x01, 0x00, 0x00})
	v.push(f1, true)

	assert.Equal(t, moqt.GroupSequence(1), v.buf.head())
	g := v.buf.get(1)
	require.NotNil(t, g)
	assert.Equal(t, f1, g.next(0))
	assert.False(t, g.isComplete())

	// Non-keyframe appends to current group.
	f2 := moqt.NewFrame(3)
	f2.Write([]byte{0x27, 0x01, 0x00})
	v.push(f2, false)

	assert.Equal(t, moqt.GroupSequence(1), v.buf.head()) // same group
	assert.Equal(t, f2, g.next(1))

	// New keyframe starts a new group and completes the old one.
	f3 := moqt.NewFrame(4)
	f3.Write([]byte{0x17, 0x01, 0x00, 0x00})
	v.push(f3, true)

	assert.Equal(t, moqt.GroupSequence(2), v.buf.head())
	assert.True(t, g.isComplete()) // old group completed
	g2 := v.buf.get(2)
	require.NotNil(t, g2)
	assert.Equal(t, f3, g2.next(0))
}

// ---------------------------------------------------------------------------
// singleTrack tests
// ---------------------------------------------------------------------------

func TestSingleTrack_Push(t *testing.T) {
	s := newSingleTrack(context.Background())

	f := moqt.NewFrame(3)
	f.Write([]byte{0xAF, 0x01, 0x00})
	s.push(f)

	assert.Equal(t, moqt.GroupSequence(1), s.buf.head())
	g := s.buf.get(1)
	require.NotNil(t, g)
	assert.Equal(t, f, g.next(0))
	assert.True(t, g.isComplete()) // single-track groups are immediately complete
}

func TestVideoTrack_Close(t *testing.T) {
	v := newVideoTrack(context.Background())

	// Push a video frame to create a group.
	f := moqt.NewFrame(3)
	f.Write([]byte{0x17, 0x01, 0x00})
	v.push(f, true)

	g := v.buf.get(1)
	require.NotNil(t, g)
	assert.False(t, g.isComplete())

	v.close()
	assert.True(t, g.isComplete())
}

func TestVideoTrack_Close_NoGroup(t *testing.T) {
	v := newVideoTrack(context.Background())
	// Should not panic when no current group exists.
	v.close()
}

func TestTrackBuffer_SubscribeUnsubscribe(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		b := newTestTrackBuffer()

		ch1 := b.subscribe()
		ch2 := b.subscribe()

		// Both subscribers receive notifications.
		b.notify()
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
		b.unsubscribe(ch1)

		b.notify()
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

func TestTrackBuffer_Notify(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		b := newTestTrackBuffer()

		ch := b.subscribe()
		defer b.unsubscribe(ch)

		b.notify()

		synctest.Wait()

		select {
		case <-ch:
			// Expected: notification received.
		default:
			t.Fatal("expected notification on subscriber channel")
		}
	})
}

func TestTrackBuffer_Notify_NonBlocking(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		b := newTestTrackBuffer()

		ch := b.subscribe()
		defer b.unsubscribe(ch)

		// Fill the channel buffer.
		b.notify()
		synctest.Wait()

		// Second notify should not block.
		b.notify()
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

func TestTrackBuffer_MultipleSubscribers(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		b := newTestTrackBuffer()

		ch1 := b.subscribe()
		ch2 := b.subscribe()
		defer b.unsubscribe(ch1)
		defer b.unsubscribe(ch2)

		b.notify()
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

func TestTrackBuffer_RingWrapAround(t *testing.T) {
	b := newTestTrackBuffer()

	// Fill beyond ring size.
	for i := range defaultRingSize + 3 {
		g := b.openGroup()
		f := moqt.NewFrame(1)
		f.Write([]byte{byte(i)})
		g.append(f)
		g.complete.Store(true)
	}

	// Oldest slots have been overwritten.
	head := b.head()
	assert.Equal(t, moqt.GroupSequence(defaultRingSize+3), head)

	// Can still access recent groups.
	for seq := b.earliestAvailable(); seq <= head; seq++ {
		g := b.get(seq)
		require.NotNil(t, g)
		assert.Equal(t, seq, g.seq)
	}
}

// ---------------------------------------------------------------------------
// ingestHandler tests
// ---------------------------------------------------------------------------

func TestNewIngestHandler(t *testing.T) {
	h, err := newIngestHandler(context.Background())
	require.NoError(t, err)

	require.NotNil(t, h.broadcast)
	require.NotNil(t, h.video)
	require.NotNil(t, h.audio)
}

func TestIngestHandler_Close(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		h, err := newIngestHandler(ctx)
		require.NoError(t, err)

		// Push a video frame so there's a current group.
		f := moqt.NewFrame(3)
		f.Write([]byte{0x17, 0x01, 0x00})
		h.video.push(f, true)

		g := h.video.buf.get(1)
		require.NotNil(t, g)
		assert.False(t, g.isComplete())

		h.close()

		// Video group completed.
		assert.True(t, g.isComplete())

		// Context cancelled by caller (Session) — simulate.
		cancel()
		select {
		case <-h.video.ctx.Done():
		default:
			t.Fatal("expected context to be cancelled")
		}
	})
}

func TestIngestHandler_Close_Idempotent(t *testing.T) {
	h, err := newIngestHandler(context.Background())
	require.NoError(t, err)

	// Multiple calls should not panic.
	h.close()
	h.close()
	h.close()
}

// ---------------------------------------------------------------------------
// Session tests
// ---------------------------------------------------------------------------

func TestNewSession_PushVideo_PushAudio_Close(t *testing.T) {
	mux := moqt.NewTrackMux(0)

	sess, err := NewSession(mux, "/live/test")
	require.NoError(t, err)

	// Push video keyframe.
	sess.PushVideo(0, []byte{0x17, 0x01, 0x00, 0x00}, true)
	assert.Equal(t, moqt.GroupSequence(1), sess.handler.video.buf.head())

	// Push video inter-frame (same group).
	sess.PushVideo(33000, []byte{0x27, 0x01, 0x00, 0x00}, false)
	assert.Equal(t, moqt.GroupSequence(1), sess.handler.video.buf.head())

	// Push audio.
	sess.PushAudio(0, []byte{0xAF, 0x01, 0x00})
	assert.Equal(t, moqt.GroupSequence(1), sess.handler.audio.buf.head())

	// Close should not panic.
	sess.Close()
}

func TestSession_Close_Idempotent(t *testing.T) {
	mux := moqt.NewTrackMux(0)
	sess, err := NewSession(mux, "/live/test2")
	require.NoError(t, err)

	sess.Close()
	sess.Close() // should not panic
}

func TestSession_PushMultipleKeyframes(t *testing.T) {
	mux := moqt.NewTrackMux(0)
	sess, err := NewSession(mux, "/live/gop-test")
	require.NoError(t, err)
	defer sess.Close()

	// First keyframe → group 1.
	sess.PushVideo(0, []byte{0x17, 0x01}, true)
	assert.Equal(t, moqt.GroupSequence(1), sess.handler.video.buf.head())

	// Inter-frames in group 1.
	sess.PushVideo(33000, []byte{0x27, 0x01}, false)
	sess.PushVideo(66000, []byte{0x27, 0x02}, false)

	// Second keyframe → group 2.
	sess.PushVideo(100000, []byte{0x17, 0x02}, true)
	assert.Equal(t, moqt.GroupSequence(2), sess.handler.video.buf.head())

	// Verify group 1 was completed.
	g := sess.handler.video.buf.get(1)
	require.NotNil(t, g)
	assert.True(t, g.isComplete())

	// Verify group 2 has the keyframe (MediaFrame-wrapped).
	g2 := sess.handler.video.buf.get(2)
	require.NotNil(t, g2)
	f := g2.next(0)
	require.NotNil(t, f)
	expected := buildMediaFrame(100000, []byte{0x17, 0x02})
	assert.Equal(t, expected, f.Body())
}

func TestSession_AudioIndependentGroups(t *testing.T) {
	mux := moqt.NewTrackMux(0)
	sess, err := NewSession(mux, "/live/audio-test")
	require.NoError(t, err)
	defer sess.Close()

	// Each audio push creates its own complete group.
	sess.PushAudio(0, []byte{0xAF, 0x01})
	sess.PushAudio(23000, []byte{0xAF, 0x02})
	sess.PushAudio(46000, []byte{0xAF, 0x03})

	assert.Equal(t, moqt.GroupSequence(3), sess.handler.audio.buf.head())

	// All groups should be complete.
	for seq := moqt.GroupSequence(1); seq <= 3; seq++ {
		g := sess.handler.audio.buf.get(seq)
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

// ---------------------------------------------------------------------------
// registerVideo / registerAudio tests
// ---------------------------------------------------------------------------

func TestRegisterVideo(t *testing.T) {
	h, err := newIngestHandler(context.Background())
	require.NoError(t, err)

	cfg := &AVCConfig{
		ProfileIDC:    0x64,
		ProfileCompat: 0x00,
		LevelIDC:      0x1F,
		Width:         1920,
		Height:        1080,
	}

	require.NoError(t, h.registerVideo(cfg))

	data, err := h.broadcast.CatalogBytes()
	require.NoError(t, err)

	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(data, &raw))

	var tracks []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw["tracks"], &tracks))
	require.Len(t, tracks, 1)

	var name string
	require.NoError(t, json.Unmarshal(tracks[0]["name"], &name))
	assert.Equal(t, "video", name)

	var codec string
	require.NoError(t, json.Unmarshal(tracks[0]["codec"], &codec))
	assert.Equal(t, "avc3.64001f", codec)
}

func TestRegisterAudio(t *testing.T) {
	h, err := newIngestHandler(context.Background())
	require.NoError(t, err)

	cfg := &AACConfig{
		ObjectType:    2,
		SampleRate:    48000,
		ChannelConfig: 2,
	}

	require.NoError(t, h.registerAudio(cfg))

	data, err := h.broadcast.CatalogBytes()
	require.NoError(t, err)

	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(data, &raw))

	var tracks []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw["tracks"], &tracks))
	require.Len(t, tracks, 1)

	var name string
	require.NoError(t, json.Unmarshal(tracks[0]["name"], &name))
	assert.Equal(t, "audio", name)

	var codec string
	require.NoError(t, json.Unmarshal(tracks[0]["codec"], &codec))
	assert.Equal(t, "mp4a.40.2", codec)
}

func TestRegisterVideoAndAudio(t *testing.T) {
	h, err := newIngestHandler(context.Background())
	require.NoError(t, err)

	video := &AVCConfig{
		ProfileIDC:    0x64,
		ProfileCompat: 0x00,
		LevelIDC:      0x1F,
		Width:         1920,
		Height:        1080,
	}
	audio := &AACConfig{
		ObjectType:    2,
		SampleRate:    48000,
		ChannelConfig: 2,
	}

	require.NoError(t, h.registerVideo(video))
	require.NoError(t, h.registerAudio(audio))

	data, err := h.broadcast.CatalogBytes()
	require.NoError(t, err)

	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(data, &raw))

	var tracks []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw["tracks"], &tracks))
	assert.Len(t, tracks, 2)
}
