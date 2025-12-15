package relay

import (
	"context"
	"sync"
	"time"

	"github.com/okdaichi/gomoqt/moqt"
)

// Optimized timeout for best CPU/latency tradeoff (based on benchmarks)
var NotifyTimeout = 1 * time.Millisecond

func Relay(ctx context.Context, sess *moqt.Session, op func(handler *RelayHandler)) error {
	peer, err := sess.AcceptAnnounce("/")
	if err != nil {
		return err
	}

	for ann := range peer.Announcements(context.Background()) {
		handler := &RelayHandler{
			Announcement: ann,
			Session:      sess,
		}

		op(handler)
	}

	return nil
}

var _ moqt.TrackHandler = (*RelayHandler)(nil)

type RelayHandler struct {
	Announcement *moqt.Announcement
	Session      *moqt.Session

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
		tr = h.subscribe(tw.TrackName)
	}
	h.mu.Unlock()

	if tr == nil {
		tw.CloseWithError(moqt.TrackNotFoundErrorCode)
		return
	}

	tr.serveTrack(tw)
}

func (h *RelayHandler) subscribe(name moqt.TrackName) *trackDistributor {
	if h.Session == nil {
		return nil // TODO: return proper error(?)
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
	return newTrackRelayer(src, func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		delete(h.relaying, name)
	})
}

func newTrackRelayer(src *moqt.TrackReader, onClose func()) *trackDistributor {
	relayer := &trackDistributor{
		src:         src,
		ring:        newGroupRing(),
		subscribers: make(map[chan struct{}]struct{}),
		onClose:     onClose,
	}

	go relayer.relay(context.Background())

	return relayer
}

type trackDistributor struct {
	src *moqt.TrackReader

	ring *groupRing

	// Broadcast channel pattern: each subscriber gets its own notification channel
	mu          sync.RWMutex
	subscribers map[chan struct{}]struct{}

	onClose func()
}

func (r *trackDistributor) serveTrack(tw *moqt.TrackWriter) {
	// Subscribe to notifications
	notify := r.subscribe()
	defer r.unsubscribe(notify)

	last := r.ring.head()
	if last > 0 {
		last--
	}

	for {
		select {
		case <-tw.Context().Done():
			return
		default:
		}

		latest := r.ring.head()

		if last < latest {
			last++

			// Check if we've fallen too far behind
			earliest := r.ring.earliestAvailable()
			if last < earliest {
				// Skip to latest available
				last = latest - 1
				continue
			}

			cache := r.ring.get(last)
			if cache == nil {
				continue
			}

			gw, err := tw.OpenGroup(cache.seq)
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
			return
		}
	}
}

func (r *trackDistributor) close() {
	r.src.Close()
	r.onClose()
}

// subscribe registers a new subscriber and returns its notification channel
func (r *trackDistributor) subscribe() chan struct{} {
	r.mu.Lock()
	defer r.mu.Unlock()

	ch := make(chan struct{}, 1) // Buffered to prevent blocking
	r.subscribers[ch] = struct{}{}

	return ch
}

// unsubscribe removes a subscriber
func (r *trackDistributor) unsubscribe(ch chan struct{}) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.subscribers, ch)
}

func (r *trackDistributor) relay(ctx context.Context) {
	defer r.close()

	for {
		gr, err := r.src.AcceptGroup(ctx)
		if err != nil {
			return
		}

		r.ring.add(gr)

		// Broadcast notification to all subscribers
		r.mu.RLock()
		for ch := range r.subscribers {
			select {
			case ch <- struct{}{}:
				// Notification sent
			default:
				// Channel full, subscriber will wake up on timeout
			}
		}
		r.mu.RUnlock()
	}
}
