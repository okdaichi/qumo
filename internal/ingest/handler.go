package ingest

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/okdaichi/gomoqt/moqt"
)

const (
	trackVideo   moqt.TrackName = "video"
	trackAudio   moqt.TrackName = "audio"
	trackCatalog moqt.TrackName = "catalog"

	defaultRingSize = 8

	notifyTimeout = 1 * time.Millisecond
)

var _ moqt.TrackHandler = (*ingestHandler)(nil)

// ingestHandler bridges a single publish stream to MoQT subscribers.
// It implements [moqt.TrackHandler]; the TrackMux calls ServeTrack once per
// subscribing client.
type ingestHandler struct {
	video   *trackSource
	audio   *trackSource
	catalog *trackSource
	done    chan struct{} // closed when the publisher disconnects
	once    sync.Once
}

func newIngestHandler() *ingestHandler {
	return &ingestHandler{
		video:   newTrackSource(),
		audio:   newTrackSource(),
		catalog: newTrackSource(),
		done:    make(chan struct{}),
	}
}

// ServeTrack is called by TrackMux for each subscribing MoQT client. It
// blocks until the subscriber disconnects or the publisher ends.
func (h *ingestHandler) ServeTrack(tw *moqt.TrackWriter) {
	var src *trackSource
	switch tw.TrackName {
	case trackVideo:
		src = h.video
	case trackAudio:
		src = h.audio
	case trackCatalog:
		src = h.catalog
	default:
		tw.CloseWithError(moqt.SubscribeErrorCodeNotFound)
		return
	}
	src.serve(tw, h.done)
}

// close signals all subscribers that the publisher has disconnected.
func (h *ingestHandler) close() {
	h.once.Do(func() {
		h.video.closeCurrentGroup()
		close(h.done)
	})
}

// trackSource manages a ring buffer of MoQT groups for a single media track
// (video or audio) and fans out the data to multiple concurrent subscribers.
type trackSource struct {
	// Ring buffer of groups.
	ring []atomic.Pointer[sourceGroup]
	size int
	pos  atomic.Uint64 // monotonically increasing; first group = 1

	// Current open group (video only; audio completes each group immediately).
	currentMu sync.Mutex
	current   *sourceGroup

	// Subscriber notification.
	subMu       sync.RWMutex
	subscribers map[chan struct{}]struct{}
}

func newTrackSource() *trackSource {
	return &trackSource{
		ring:        make([]atomic.Pointer[sourceGroup], defaultRingSize),
		size:        defaultRingSize,
		subscribers: make(map[chan struct{}]struct{}),
	}
}

func (s *trackSource) pushVideo(f *moqt.Frame, isKeyframe bool) {
	s.currentMu.Lock()
	if s.current == nil || isKeyframe {
		if s.current != nil {
			s.current.complete.Store(true)
		}
		s.current = s.newGroup()
	}
	s.current.append(f)
	s.currentMu.Unlock()

	s.notify()
}

func (s *trackSource) pushAudio(f *moqt.Frame) {
	g := s.newGroup()
	g.append(f)
	g.complete.Store(true)

	s.notify()
}

// pushCatalog publishes a catalog frame as a single-frame group. Each
// call replaces the previous catalog (new group sequence).
func (s *trackSource) pushCatalog(f *moqt.Frame) {
	g := s.newGroup()
	g.append(f)
	g.complete.Store(true)

	s.notify()
}

// closeCurrentGroup marks the current video group as complete. Called when
// the RTMP publisher disconnects.
func (s *trackSource) closeCurrentGroup() {
	s.currentMu.Lock()
	if s.current != nil {
		s.current.complete.Store(true)
	}
	s.currentMu.Unlock()

	s.notify()
}

func (s *trackSource) newGroup() *sourceGroup {
	p := s.pos.Add(1)
	g := &sourceGroup{
		seq:    moqt.GroupSequence(p),
		frames: make([]*moqt.Frame, 0, 4),
	}
	s.ring[p%uint64(s.size)].Store(g)
	return g
}

// --- subscriber notification ---

func (s *trackSource) notify() {
	s.subMu.RLock()
	for ch := range s.subscribers {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
	s.subMu.RUnlock()
}

func (s *trackSource) subscribe() chan struct{} {
	s.subMu.Lock()
	defer s.subMu.Unlock()
	ch := make(chan struct{}, 1)
	s.subscribers[ch] = struct{}{}
	return ch
}

func (s *trackSource) unsubscribe(ch chan struct{}) {
	s.subMu.Lock()
	defer s.subMu.Unlock()
	delete(s.subscribers, ch)
}

// --- ring buffer access ---

func (s *trackSource) head() moqt.GroupSequence {
	return moqt.GroupSequence(s.pos.Load())
}

func (s *trackSource) earliestAvailable() moqt.GroupSequence {
	h := s.head()
	if h <= moqt.GroupSequence(s.size) {
		return 1
	}
	return h - moqt.GroupSequence(s.size) + 1
}

func (s *trackSource) get(seq moqt.GroupSequence) *sourceGroup {
	return s.ring[uint64(seq)%uint64(s.size)].Load()
}

// --- subscriber egress ---

// serve writes groups and frames to a single MoQT TrackWriter. It blocks
// until the subscriber disconnects or the publisher (done channel) exits.
func (s *trackSource) serve(tw *moqt.TrackWriter, done <-chan struct{}) {
	twCtx := tw.Context()
	notify := s.subscribe()
	defer s.unsubscribe(notify)

	last := s.head()
	if last > 0 {
		last--
	}

	for {
		latest := s.head()

		if last < latest {
			last++

			earliest := s.earliestAvailable()
			if last < earliest {
				// Subscriber fell behind; skip to latest.
				last = latest - 1
				continue
			}

			g := s.get(last)
			if g == nil {
				last--
				continue
			}

			gw, err := tw.OpenGroupAt(g.seq)
			if err != nil {
				return
			}

			frameIdx := 0
			for {
				f := g.next(frameIdx)
				if f != nil {
					if err := gw.WriteFrame(f); err != nil {
						gw.Close()
						return
					}
					frameIdx++
					continue
				}

				if g.isComplete() {
					break
				}

				// Wait for more frames within this group.
				select {
				case <-notify:
				case <-time.After(notifyTimeout):
				case <-done:
					gw.Close()
					return
				case <-twCtx.Done():
					gw.Close()
					return
				}
			}

			gw.Close()
			continue
		}

		// No new groups yet; wait for data.
		select {
		case <-notify:
		case <-time.After(notifyTimeout):
		case <-done:
			return
		case <-twCtx.Done():
			return
		}
	}
}

// --- sourceGroup ---

// sourceGroup holds the frames of a single MoQT group.
type sourceGroup struct {
	seq      moqt.GroupSequence
	mu       sync.Mutex
	frames   []*moqt.Frame
	complete atomic.Bool
}

func (g *sourceGroup) append(f *moqt.Frame) {
	g.mu.Lock()
	g.frames = append(g.frames, f)
	g.mu.Unlock()
}

func (g *sourceGroup) next(index int) *moqt.Frame {
	g.mu.Lock()
	defer g.mu.Unlock()
	if index < 0 || index >= len(g.frames) {
		return nil
	}
	return g.frames[index]
}

func (g *sourceGroup) isComplete() bool {
	return g.complete.Load()
}
