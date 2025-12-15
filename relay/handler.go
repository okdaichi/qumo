package relay

import (
	"context"
	"sync"
	"time"

	"github.com/okdaichi/gomoqt/moqt"
	"github.com/okdaichi/qumo/observability"
)

// Optimized timeout for best CPU/latency tradeoff (based on benchmarks)
var NotifyTimeout = 1 * time.Millisecond

func Relay(ctx context.Context, sess *moqt.Session, op func(handler *RelayHandler)) error {
	ctx, span := observability.Start(ctx, "relay.session")
	defer span.End()

	peer, err := sess.AcceptAnnounce("/")
	if err != nil {
		span.Error(err, "failed to accept announce")
		return err
	}

	for ann := range peer.Announcements(span.Context()) {
		_, annSpan := observability.Start(span.Context(), "relay.announcement",
			observability.Broadcast(ann.BroadcastPath().String()),
		)

		handler := &RelayHandler{
			Announcement: ann,
			Session:      sess,
			ctx:          annSpan.Context(),
		}

		op(handler)
		annSpan.End()
	}

	return nil
}

var _ moqt.TrackHandler = (*RelayHandler)(nil)

type RelayHandler struct {
	Announcement *moqt.Announcement
	Session      *moqt.Session

	mu       sync.RWMutex
	relaying map[moqt.TrackName]*trackDistributor

	ctx context.Context
}

func (h *RelayHandler) ServeTrack(tw *moqt.TrackWriter) {
	trackName := string(tw.TrackName)

	ctx, span := observability.Start(h.ctx, "relay.serve_track",
		observability.Track(trackName),
	)
	defer span.End()

	h.mu.Lock()
	if h.relaying == nil {
		h.relaying = make(map[moqt.TrackName]*trackDistributor)
	}

	tr, ok := h.relaying[tw.TrackName]
	if !ok {
		_, subSpan := observability.Start(ctx, "relay.subscribe",
			observability.Track(trackName),
		)

		tr = h.subscribe(tw.TrackName)
		if tr == nil {
			subSpan.Error(nil, "subscription failed")
			h.mu.Unlock()
			tw.CloseWithError(moqt.TrackNotFoundErrorCode)
			span.Error(nil, "track not found")
			return
		}

		tr.ctx = subSpan.Context()
		subSpan.End()
	}
	h.mu.Unlock()

	tr.serveTrack(ctx, tw)
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
	return newTrackDistributor(src, func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		delete(h.relaying, name)
	})
}

func newTrackDistributor(src *moqt.TrackReader, onClose func()) *trackDistributor {
	d := &trackDistributor{
		src:         src,
		ring:        newGroupRing(),
		subscribers: make(map[chan struct{}]struct{}),
		onClose:     onClose,
		rec:         observability.NewRecorder(string(src.TrackName)),
	}

	go d.relay(context.Background())

	return d
}

type trackDistributor struct {
	src *moqt.TrackReader

	ring *groupRing

	// Broadcast channel pattern: each subscriber gets its own notification channel
	mu          sync.RWMutex
	subscribers map[chan struct{}]struct{}

	onClose func()
	ctx     context.Context
	rec     *observability.TrackRecorder
}

func (d *trackDistributor) serveTrack(ctx context.Context, tw *moqt.TrackWriter) {
	trackName := string(tw.TrackName)

	ctx, span := observability.Start(ctx, "relay.distribute",
		observability.Track(trackName),
	)
	defer span.End()

	// Subscribe to notifications
	notify := d.subscribe()
	defer d.unsubscribe(notify)

	last := d.ring.head()
	if last > 0 {
		last--
	}

	for {
		select {
		case <-tw.Context().Done():
			return
		default:
		}

		latest := d.ring.head()

		if last < latest {
			last++

			// Check if we've fallen too far behind
			earliest := d.ring.earliestAvailable()
			if last < earliest {
				// Subscriber fell behind - catchup
				skipped := latest - last
				d.rec.Catchup(int(skipped))

				span.Event("subscriber.catchup",
					observability.GroupSequence(uint64(last)),
					observability.Frames(int(skipped)),
				)

				// Skip to latest available
				last = latest - 1
				continue
			}

			cache := d.ring.get(last)
			if cache == nil {
				d.rec.CacheMiss()
				continue
			}
			d.rec.CacheHit()

			// Write group with observability
			_, writeSpan := observability.StartWith(ctx, "relay.write_group",
				observability.Attrs(
					observability.GroupSequence(uint64(cache.seq)),
					observability.Frames(len(cache.frames)),
				),
				observability.Latency(d.rec.LatencyObs("write")),
			)

			gw, err := tw.OpenGroup(cache.seq)
			if err != nil {
				writeSpan.Error(err, "failed to open group")
				return
			}

			for _, frame := range cache.frames {
				if err := gw.WriteFrame(frame); err != nil {
					writeSpan.Error(err, "failed to write frame")
					gw.Close()
					return
				}
			}

			gw.Close()
			writeSpan.End()
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

func (d *trackDistributor) close() {
	d.src.Close()
	d.onClose()
}

// subscribe registers a new subscriber and returns its notification channel
func (d *trackDistributor) subscribe() chan struct{} {
	d.mu.Lock()
	defer d.mu.Unlock()

	ch := make(chan struct{}, 1) // Buffered to prevent blocking
	d.subscribers[ch] = struct{}{}

	d.rec.IncSubscribers()

	return ch
}

// unsubscribe removes a subscriber
func (d *trackDistributor) unsubscribe(ch chan struct{}) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.subscribers, ch)

	d.rec.DecSubscribers()
}

func (d *trackDistributor) relay(ctx context.Context) {
	ctx, span := observability.StartWith(ctx, "relay.loop",
		observability.OnStart(observability.IncTracks),
		observability.OnEnd(observability.DecTracks),
	)
	defer span.End()
	defer d.close()

	for {
		_, recvSpan := observability.StartWith(ctx, "relay.receive_group",
			observability.Latency(d.rec.LatencyObs("receive")),
		)

		gr, err := d.src.AcceptGroup(ctx)
		if err != nil {
			recvSpan.Error(err, "failed to receive group")
			return
		}

		d.ring.add(gr)
		recvSpan.End()

		// Metrics
		d.rec.GroupReceived()

		// Broadcast notification to all subscribers
		broadcastStart := time.Now()
		d.mu.RLock()
		subscriberCount := len(d.subscribers)
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

		// Record broadcast metrics
		d.rec.Broadcast(time.Since(broadcastStart), subscriberCount, notified)

		// Tracing event
		span.Event("group.relayed", observability.Subscribers(subscriberCount))
	}
}
