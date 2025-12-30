package relay

import (
	"context"
	"sync"
	"time"

	"github.com/okdaichi/gomoqt/moqt"
)

// Optimized timeout for best CPU/latency tradeoff (based on benchmarks)
var NotifyTimeout = 1 * time.Millisecond

func Relay(ctx context.Context, sess *moqt.Session, mux *moqt.TrackMux) error {

	// TODO: measure accept time
	peer, err := sess.AcceptAnnounce("/")
	if err != nil {
		return err
	}

	for ann := range peer.Announcements(ctx) {

		handler := &RelayHandler{
			Announcement:   ann,
			Session:        sess,
			GroupCacheSize: DefaultGroupCacheSize,
			FramePool:      DefaultFramePool,
			relaying:       make(map[moqt.TrackName]*trackDistributor),
		}

		mux.Announce(ann, handler)
	}

	return nil
}

var _ moqt.TrackHandler = (*RelayHandler)(nil)

type RelayHandler struct {
	Announcement *moqt.Announcement
	Session      *moqt.Session

	GroupCacheSize int

	FramePool *FramePool

	mu       sync.RWMutex
	relaying map[moqt.TrackName]*trackDistributor
}

func (h *RelayHandler) ServeTrack(tw *moqt.TrackWriter) {
	h.mu.Lock()
	if h.relaying == nil {
		h.relaying = make(map[moqt.TrackName]*trackDistributor)
	}

	tr, ok := h.relaying[tw.TrackName]
	if !ok {
		// Start new track distributor
		tr = h.subscribe(tw.TrackName)
		if tr == nil {
			h.mu.Unlock()
			tw.CloseWithError(moqt.TrackNotFoundErrorCode)
			return
		}
	}
	h.mu.Unlock()

	tr.egress(tw)
}

func (h *RelayHandler) subscribe(name moqt.TrackName) *trackDistributor {
	if h.Session == nil {
		return nil
	}

	if h.Announcement == nil {
		return nil
	}
	if !h.Announcement.IsActive() {
		return nil
	}

	src, err := h.Session.Subscribe(h.Announcement.BroadcastPath(), name, nil)
	if err != nil {
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())

	d := &trackDistributor{
		ring:        newGroupRing(h.GroupCacheSize),
		subscribers: make(map[chan struct{}]struct{}),
		onClose: func() {
			// Cancel ingestion context
			cancel()

			// Remove from relaying map
			h.mu.Lock()
			delete(h.relaying, name)
			h.mu.Unlock()
		},
	}

	go d.ingest(ctx, src)

	return d
}

// func newTrackDistributor(src *moqt.TrackReader, cacheSize int, onClose func()) *trackDistributor {

// }

type trackDistributor struct {
	// src *moqt.TrackReader

	ring *groupRing

	// Broadcast channel pattern: each subscriber gets its own notification channel
	mu          sync.RWMutex
	subscribers map[chan struct{}]struct{}

	onClose func()
}

func (d *trackDistributor) egress(tw *moqt.TrackWriter) {
	// Get track writer context once and check if it's valid
	twCtx := tw.Context()
	var twDone <-chan struct{}
	if twCtx != nil {
		twDone = twCtx.Done()
	}

	// Subscribe to notifications
	notify := d.subscribe()
	defer d.unsubscribe(notify)

	last := d.ring.head()
	if last > 0 {
		last--
	}

	for {
		latest := d.ring.head()

		if last < latest {
			last++

			// Check if we've fallen too far behind
			earliest := d.ring.earliestAvailable()
			if last < earliest {
				// Subscriber fell behind - catchup

				// Skip to latest available
				last = latest - 1
				continue
			}

			cache := d.ring.get(last)
			if cache == nil {
				continue
			}

			gw, err := tw.OpenGroupAt(cache.seq)
			if err != nil {
				return
			}

			for _, frame := range cache.frames {
				if err := gw.WriteFrame(frame); err != nil {
					gw.Close()
					return
				}
			}

			gw.Close()
			continue
		}

		// Wait for new data with optimized timeout
		select {
		case <-notify:
			// New group available, retry immediately
		case <-time.After(NotifyTimeout):
			// Timeout fallback (1ms for optimal CPU/latency balance)
		case <-tw.Context().Done():
			// Relay shutdown
			return
		case <-twDone:
			// Client disconnected (only if twDone is not nil)
			return
		}
	}
}

func (d *trackDistributor) close() {
	// d.src.Close()
	d.onClose()
}

// subscribe registers a new subscriber and returns its notification channel
func (d *trackDistributor) subscribe() chan struct{} {
	d.mu.Lock()
	defer d.mu.Unlock()

	ch := make(chan struct{}, 1) // Buffered to prevent blocking
	d.subscribers[ch] = struct{}{}

	return ch
}

// unsubscribe removes a subscriber
func (d *trackDistributor) unsubscribe(ch chan struct{}) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.subscribers, ch)
}

func (d *trackDistributor) ingest(ctx context.Context, src *moqt.TrackReader) {
	defer d.close()

	for {

		gr, err := src.AcceptGroup(ctx)
		if err != nil {
			return
		}

		d.ring.add(gr)

		// Broadcast notification (RLock only, non-blocking, zero alloc)
		d.mu.RLock()
		notified := 0
		for ch := range d.subscribers {
			select {
			case ch <- struct{}{}:
				notified++
			default:
				// Channel full, subscriber will wake up on timeout
			}
		}
		d.mu.RUnlock()
	}
}
