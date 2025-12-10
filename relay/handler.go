package relay

import (
	"context"
	"sync"
	"time"

	"github.com/okdaichi/gomoqt/moqt"
)

var SleepDuration = 100 * time.Microsecond

func Serve(w moqt.SetupResponseWriter, r *moqt.SetupRequest) {
	sess, err := moqt.Accept(w, r, nil)
	if err != nil {
		return
	}

	upstream, err := sess.AcceptAnnounce("/")
	if err != nil {
		return
	}

	for ann := range upstream.Announcements(context.Background()) {
		handler := &RelayHandler{
			Announcement: ann,
			Session:      sess,
		}

		moqt.Announce(ann, handler)
	}
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
		src:     src,
		ring:    newGroupRing(),
		notify:  make(chan struct{}, 1),
		onClose: onClose,
	}

	go relayer.relay(context.Background())

	return relayer
}

type trackDistributor struct {
	src *moqt.TrackReader

	ring *groupRing

	notify chan struct{}

	onClose func()
}

func (r *trackDistributor) serveTrack(tw *moqt.TrackWriter) {
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

		// Wait for new data
		select {
		case <-r.notify:
			// New group available, retry immediately
		case <-time.After(SleepDuration):
			// Timeout fallback
		case <-tw.Context().Done():
			return
		}
	}
}

func (r *trackDistributor) close() {
	r.src.Close()
	r.onClose()
}

func (r *trackDistributor) relay(ctx context.Context) {
	defer r.close()

	for {
		gr, err := r.src.AcceptGroup(ctx)
		if err != nil {
			return
		}

		r.ring.add(gr)

		// Notify waiting subscribers (non-blocking)
		select {
		case r.notify <- struct{}{}:
		default:
		}
	}
}
