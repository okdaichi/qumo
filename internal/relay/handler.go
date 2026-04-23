package relay

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/qumo-dev/gomoqt/moqt"
	"golang.org/x/sync/singleflight"
)

// Optimized timeout for best CPU/latency tradeoff (based on benchmarks)
var NotifyTimeout = 1 * time.Millisecond

// DrainTimeout is the grace period given to a displaced relayHandler before
// its upstream context is cancelled. During this window existing subscribers
// can finish reading in-flight groups before the upstream subscription stops.
var DrainTimeout = 30 * time.Second

var errTrackNotFound = errors.New("track not found")

var _ moqt.TrackHandler = (*relayHandler)(nil)
var _ RouteReporter = (*relayHandler)(nil)
var _ Drainable = (*relayHandler)(nil)

// RouteStats holds routing quality metrics for a relayed broadcast path.
type RouteStats struct {
	// Alive reports whether the upstream session is still connected.
	// A false value means the handler is dead and must be replaced unconditionally.
	Alive bool
	// Hops is the number of relay hops the announcement has traversed.
	// Fewer hops generally implies lower latency.
	Hops int
	// Bitrate is the measured bitrate in bits per second. A value of 0 means unknown.
	Bitrate uint64
	// RTT is the smoothed round-trip time in milliseconds. A value of 0 means unknown.
	RTT uint64
}

// Drainable is implemented by handlers that support graceful drain-then-shutdown.
// When a handler is displaced by a better route, Drain is called so that
// in-flight streams can complete before the upstream subscription is torn down.
type Drainable interface {
	// Drain schedules cancellation of the handler's context after timeout.
	// It is idempotent: only the first call schedules a timer; subsequent calls
	// are no-ops. The handler's context is cancelled once when the timer fires.
	Drain(timeout time.Duration)
}

// RouteReporter is implemented by handlers that can report routing quality
// metrics for a relayed broadcast path. Use a type assertion on the
// TrackHandler returned by TrackMux.TrackHandler:
//
//	_, h := mux.TrackHandler(path)
//	if rr, ok := h.(relay.RouteReporter); ok {
//		stats := rr.RouteStats()
//	}
//
// Evaluation is intentionally performed only when a new route candidate
// arrives, not periodically, to preserve cache hit rates and playback
// continuity.
type RouteReporter interface {
	// RouteStats probes the upstream session once and returns combined
	// routing metrics. Called at most once per route comparison.
	RouteStats() RouteStats
}

type relayHandler struct {
	announcement *moqt.Announcement
	session      *moqt.Session

	tracks  *trackManager
	flights singleflight.Group

	ctx       context.Context
	cancel    context.CancelFunc
	drainOnce sync.Once
}

// isBetterRoute reports whether candidate is a strictly better route than
// current. A live route always beats a dead one. Among routes with the same
// liveness, fewer hops wins outright; equal hops are broken first by bitrate
// (higher available bandwidth is better for streaming), then by RTT (lower
// latency is better). When a metric cannot be determined (nil probe or 0
// value), the current route is preferred.
func isBetterRoute(candidate, current RouteStats) bool {
	// A live route always beats a dead one.
	if candidate.Alive != current.Alive {
		return candidate.Alive
	}
	// Both dead: no benefit in switching.
	if !candidate.Alive {
		return false
	}
	if candidate.Hops < current.Hops {
		return true
	}
	if candidate.Hops > current.Hops {
		return false
	}
	// Higher available bandwidth wins first.
	if candidate.Bitrate != current.Bitrate {
		return candidate.Bitrate > current.Bitrate
	}
	// Bandwidth equal or unknown: prefer lower RTT.
	if candidate.RTT == 0 || current.RTT == 0 {
		return false
	}
	return candidate.RTT < current.RTT
}

func newRelayHandler(ann *moqt.Announcement, sess *moqt.Session) *relayHandler {
	if sess == nil || ann == nil {
		return nil
	}

	ctx, cancel := context.WithCancel(sess.Context())
	h := &relayHandler{
		announcement: ann,
		session:      sess,
		tracks:       newTrackManager(),
		ctx:          ctx,
		cancel:       cancel,
	}
	return h
}

// Drain schedules cancellation of this handler's context after timeout.
// New subscribers will find no active handler; existing in-flight groups
// are allowed to finish within the grace window.
// It is idempotent: only the first call schedules a timer; subsequent calls
// are no-ops and do not create additional goroutines.
func (h *relayHandler) Drain(timeout time.Duration) {
	h.drainOnce.Do(func() {
		time.AfterFunc(timeout, h.cancel)
	})
}

// RouteStats probes the upstream session and returns combined routing metrics.
// The probe is performed once per call; results are not cached.
func (h *relayHandler) RouteStats() RouteStats {
	rs := RouteStats{
		Alive: h.ctx.Err() == nil && h.announcement.IsActive(),
		Hops:  len(h.announcement.HopIDs()),
	}
	if h.session != nil {
		if result, err := h.session.Probe(0); err == nil {
			rs.Bitrate = result.Bitrate
			rs.RTT = result.RTT
		}
	}
	return rs
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
		slog.Warn("relay: subscribe failed: session is nil", "track", name)
		return nil
	}

	announcement := h.announcement
	if announcement == nil {
		slog.Warn("relay: subscribe failed: announcement is nil", "track", name)
		return nil
	}
	if !announcement.IsActive() {
		slog.Warn("relay: subscribe failed: announcement inactive",
			"track", name,
			"broadcast_path", announcement.BroadcastPath())
		return nil
	}

	src, err := session.Subscribe(h.ctx, announcement.BroadcastPath(), name, nil)
	if err != nil {
		slog.Warn("relay: upstream subscribe failed",
			"broadcast_path", announcement.BroadcastPath(),
			"track", name,
			"error", err)
		return nil
	}

	d := newTrackDistributor(name, h.tracks)

	go d.ingest(h.ctx, src)

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

func (d *trackDistributor) ingest(ctx context.Context, src *moqt.TrackReader) {
	defer d.manager.remove(d.name, d)
	defer close(d.done)

	for {
		gr, err := src.AcceptGroup(ctx)
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
