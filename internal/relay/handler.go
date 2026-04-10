package relay

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/okdaichi/gomoqt/moqt"
)

// Optimized timeout for best CPU/latency tradeoff (based on benchmarks)
var NotifyTimeout = 1 * time.Millisecond

var _ moqt.TrackHandler = (*RelayHandler)(nil)

type RelayHandler struct {
	Announcement *moqt.Announcement
	Session      *moqt.Session

	GroupCacheSize int

	FramePool *FramePool

	relaying sync.Map // moqt.TrackName → *trackDistributor
}

// newRelayHandler creates a RelayHandler with the given parameters. The
// Announcement may be nil; callers (e.g. server.relay) will set it later if
// necessary. A zero-valued GroupCacheSize or nil FramePool is acceptable (the
// handler will fall back to defaults).
func newRelayHandler(ann *moqt.Announcement, sess *moqt.Session, cacheSize int, pool *FramePool) *RelayHandler {
	return &RelayHandler{
		Announcement:   ann,
		Session:        sess,
		GroupCacheSize: cacheSize,
		FramePool:      pool,
	}
}

func (h *RelayHandler) ServeTrack(tw *moqt.TrackWriter) {
	logger := slog.With(
		"broadcast_path", tw.BroadcastPath,
		"track_name", tw.TrackName,
	)

	// Fast path: reuse existing distributor
	if v, ok := h.relaying.Load(tw.TrackName); ok {
		v.(*trackDistributor).egress(tw)
		return
	}

	// Slow path: subscribe upstream (no lock held)
	tr := h.subscribe(tw.TrackName)
	if tr == nil {
		tw.CloseWithError(moqt.TrackNotFoundErrorCode)
		logger.Warn("Track not found, closing track writer")
		return
	}

	// Atomic store: if another goroutine stored first, use theirs
	if actual, loaded := h.relaying.LoadOrStore(tw.TrackName, tr); loaded {
		tr.close()
		tr = actual.(*trackDistributor)
	}

	logger.Debug("Relaying track")

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
		ring:        newGroupRing(h.GroupCacheSize, h.FramePool),
		subscribers: make(map[chan struct{}]struct{}),
		onClose: func() {
			// Cancel ingestion context
			cancel()

			// Remove from relaying map
			h.relaying.Delete(name)
		},
	}

	go d.ingest(ctx, src)

	return d
}

type trackDistributor struct {
	ring *groupRing

	// Broadcast channel pattern: each subscriber gets its own notification channel
	mu          sync.RWMutex
	subscribers map[chan struct{}]struct{}

	onClose func()
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
						gw.Close()
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
				case <-twCtx.Done():
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
		case <-twCtx.Done():
			// Client disconnected or relay shutdown
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
			slog.Debug("ingest stopped", "error", err)
			return
		}

		// Pass notification callback to ring.add() for frame-level notifications
		d.ring.add(gr, func() {
			// Broadcast notification for each frame (RLock only, non-blocking)
			d.mu.RLock()
			for ch := range d.subscribers {
				select {
				case ch <- struct{}{}:
				default:
					// Channel full, subscriber will wake up on timeout
				}
			}
			d.mu.RUnlock()
		})
	}
}
