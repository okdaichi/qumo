package relay

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/okdaichi/gomoqt/moqt"
	"golang.org/x/sync/singleflight"
)

// Optimized timeout for best CPU/latency tradeoff (based on benchmarks)
var NotifyTimeout = 1 * time.Millisecond

var errTrackNotFound = errors.New("track not found")

var _ moqt.TrackHandler = (*relayHandler)(nil)

type relayHandler struct {
	announcement *moqt.Announcement
	session      *moqt.Session

	tracks  *trackManager
	flights singleflight.Group

	ctx context.Context
}

func newRelayHandler(ann *moqt.Announcement, sess *moqt.Session) *relayHandler {
	if sess == nil || ann == nil {
		return nil
	}

	h := &relayHandler{
		announcement: ann,
		session:      sess,
		tracks:       newTrackManager(),
		ctx:          sess.Context(),
	}
	return h
}

func (h *relayHandler) ServeTrack(tw *moqt.TrackWriter) {
	logger := slog.With(
		"broadcast_path", tw.BroadcastPath,
		"track_name", tw.TrackName,
	)

	// Fast path: reuse existing distributor
	if d, ok := h.tracks.load(tw.TrackName); ok {
		d.egress(tw)
		return
	}

	// Dedup: only one upstream subscribe per track name at a time
	ch := h.flights.DoChan(string(tw.TrackName), func() (any, error) {
		d := h.subscribe(tw.TrackName)
		if d == nil {
			return nil, errTrackNotFound
		}
		return d, nil
	})

	select {
	case result := <-ch:
		if result.Err != nil {
			tw.CloseWithError(moqt.SubscribeErrorCodeNotFound)
			logger.Warn("Track not found, closing track writer")
			return
		}
		logger.Debug("Relaying track")
		result.Val.(*trackDistributor).egress(tw)
	case <-tw.Context().Done():
		// Client unsubscribed before we could subscribe upstream - just return
		return
	}
}

func (h *relayHandler) subscribe(name moqt.TrackName) *trackDistributor {
	if d, ok := h.tracks.load(name); ok {
		return d
	}

	session := h.session
	if session == nil {
		return nil
	}

	announcement := h.announcement
	if announcement == nil {
		return nil
	}
	if !announcement.IsActive() {
		return nil
	}

	src, err := session.Subscribe(context.Background(), announcement.BroadcastPath(), name, nil)
	if err != nil {
		return nil
	}

	d := newTrackDistributor(name, h.tracks)

	go d.ingest(src)

	h.tracks.store(name, d)

	return d
}

// trackManager manages the set of active track distributors.
type trackManager struct {
	m sync.Map // moqt.TrackName → *trackDistributor
}

func newTrackManager() *trackManager {
	return &trackManager{}
}

func (tm *trackManager) load(name moqt.TrackName) (*trackDistributor, bool) {
	v, ok := tm.m.Load(name)
	if !ok {
		return nil, false
	}
	return v.(*trackDistributor), true
}

func (tm *trackManager) store(name moqt.TrackName, d *trackDistributor) {
	tm.m.Store(name, d)
}

func (tm *trackManager) remove(name moqt.TrackName, d *trackDistributor) {
	tm.m.CompareAndDelete(name, d)
}

type trackDistributor struct {
	name    moqt.TrackName
	ring    *groupRing
	manager *trackManager

	mu          sync.RWMutex
	subscribers map[chan struct{}]struct{}

	done chan struct{} // closed when ingest returns
}

func newTrackDistributor(name moqt.TrackName, manager *trackManager) *trackDistributor {
	return &trackDistributor{
		name:        name,
		ring:        newGroupRing(DefaultGroupCacheSize, DefaultFramePool),
		manager:     manager,
		subscribers: make(map[chan struct{}]struct{}),
		done:        make(chan struct{}),
	}
}

func (d *trackDistributor) egress(tw *moqt.TrackWriter) {
	// Get track writer context once and check if it's valid
	twCtx := tw.Context()

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
				slog.Warn("subscriber fell behind; skipping groups",
					"requested_group", last,
					"earliest_available", earliest,
					"latest_available", latest,
				)
				// Subscriber fell behind - catchup

				// Skip to latest available
				last = latest - 1
				continue
			}

			cache := d.ring.get(last)
			if cache == nil {
				last--
				continue
			}

			gw, err := tw.OpenGroupAt(cache.seq)
			if err != nil {
				return
			}

			slog.Debug("egress starting group",
				"track_name", tw.TrackName,
				"broadcast_path", tw.BroadcastPath,
				"group_sequence", cache.seq,
				"latest_available", latest,
				"earliest_available", earliest,
			)

			// Incrementally send frames as they become available
			frameIdx := 0
			for {
				frame := cache.next(frameIdx)
				if frame != nil {
					if frameIdx == 0 {
						slog.Debug("egress writing first frame of group",
							"track_name", tw.TrackName,
							"broadcast_path", tw.BroadcastPath,
							"group_sequence", cache.seq,
						)
					}
					if err := gw.WriteFrame(frame); err != nil {
						_ = gw.Close()
						return
					}
					frameIdx++
					continue
				}

				// No more frames available right now
				if cache.isComplete() {
					// Group is complete, move to next group
					break
				}

				// Wait for more frames
				select {
				case <-notify:
					// New frame may be available
				case <-time.After(NotifyTimeout):
					// Poll timeout
				case <-d.done:
					_ = gw.Close()
					return
				case <-twCtx.Done():
					_ = gw.Close()
					return
				}
			}

			_ = gw.Close()
			continue
		}

		// Wait for new data with optimized timeout
		select {
		case <-notify:
			// New group available, retry immediately
		case <-time.After(NotifyTimeout):
			// Timeout fallback (1ms for optimal CPU/latency balance)
		case <-d.done:
			// Distributor shut down (upstream ended)
			return
		case <-twCtx.Done():
			// Client disconnected or relay shutdown
			return
		}
	}
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

func (d *trackDistributor) ingest(src *moqt.TrackReader) {
	defer d.manager.remove(d.name, d)
	defer close(d.done)

	for {
		gr, err := src.AcceptGroup(context.Background())
		if err != nil {
			slog.Debug("ingest stopped", "error", err)
			return
		}

		d.ring.add(gr, d.broadcast)
	}
}

// broadcast notifies all subscribers that new data is available.
func (d *trackDistributor) broadcast() {
	d.mu.RLock()
	for ch := range d.subscribers {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
	d.mu.RUnlock()
}
