package relay

import (
	"context"
	"errors"
	"log/slog"
	"runtime"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/qumo-dev/gomoqt/moqt"
	"golang.org/x/sync/singleflight"
)

// Optimized timeout for best CPU/latency tradeoff (based on benchmarks)
var NotifyTimeout = 1 * time.Millisecond

// DrainTimeout is the grace period given to a displaced relayHandler before
// its upstream context is cancelled. During this window existing subscribers
// can finish reading in-flight groups before the upstream subscription stops.
var DrainTimeout = 30 * time.Second

// MaxGroupFillsInFlight is the maximum number of fill goroutines that a
// single trackDistributor may run simultaneously. It caps concurrent fill
// work and prevents unbounded goroutine growth under bursty or
// slow-consumer conditions. If the limit is reached, new fill goroutines wait
// for a slot after AcceptGroup returns; this does not apply backpressure at
// AcceptGroup itself.
// The default is max(32, 2×GOMAXPROCS); override before calling Relay.
// Must be >= 1; newTrackDistributor panics otherwise.
var MaxGroupFillsInFlight = max(32, 2*runtime.GOMAXPROCS(0))

var errTrackNotFound = errors.New("track not found")

// maxGroupFillsInFlightOrPanic returns MaxGroupFillsInFlight, panicking if it
// is less than 1 so that misconfiguration causes an immediate, clear failure
// rather than a silent deadlock.
func maxGroupFillsInFlightOrPanic() int {
	if MaxGroupFillsInFlight < 1 {
		panic("relay: MaxGroupFillsInFlight must be >= 1")
	}
	return MaxGroupFillsInFlight
}

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
	// EstimatedBitrate is the measured bitrate in bits per second. A value of 0 means unknown.
	EstimatedBitrate uint64
	// RTT is the smoothed round-trip time in milliseconds. A value of 0 means unknown.
	RTT time.Duration
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
	nodeID  string

	ctx       context.Context
	cancel    context.CancelFunc
	drainOnce sync.Once
}

// rejectionReason is the cause returned by isBetterRoute when a route
// candidate is not better than the existing route. Values map directly to
// the "reason" label on the qumo_relay_route_rejections_total metric.
type rejectionReason string

const (
	// rejectionDeadCandidate: candidate is not alive (session ended or announcement retracted).
	rejectionDeadCandidate rejectionReason = "dead_candidate"
	// rejectionInferiorHops: candidate has more hops than the current route.
	rejectionInferiorHops rejectionReason = "inferior_hops"
	// rejectionInferiorBitrate: candidate has lower measured bitrate.
	rejectionInferiorBitrate rejectionReason = "inferior_bitrate"
	// rejectionInferiorRTT: candidate has higher or equal RTT.
	rejectionInferiorRTT rejectionReason = "inferior_rtt"
	// rejectionEqualOrUnknown: RTT is unknown (0) for one or both routes, so
	// no improvement can be confirmed.
	rejectionEqualOrUnknown rejectionReason = "equal_or_unknown"
)

// isBetterRoute reports whether candidate is a strictly better route than
// current. A live route always beats a dead one. Among routes with the same
// liveness, fewer hops wins outright; equal hops are broken first by bitrate
// (higher available bandwidth is better for streaming), then by RTT (lower
// latency is better). When a metric cannot be determined (nil probe or 0
// value), the current route is preferred.
//
// The second return value is the rejection reason when the function returns
// false. It is empty when the function returns true.
func isBetterRoute(candidate, current RouteStats) (bool, rejectionReason) {
	// A live route always beats a dead one.
	if candidate.Alive != current.Alive {
		if candidate.Alive {
			return true, ""
		}
		return false, rejectionDeadCandidate
	}
	// Both dead: no benefit in switching.
	if !candidate.Alive {
		return false, rejectionDeadCandidate
	}
	if candidate.Hops < current.Hops {
		return true, ""
	}
	if candidate.Hops > current.Hops {
		return false, rejectionInferiorHops
	}
	// Higher available bandwidth wins first.
	if candidate.EstimatedBitrate != current.EstimatedBitrate {
		if candidate.EstimatedBitrate > current.EstimatedBitrate {
			return true, ""
		}
		return false, rejectionInferiorBitrate
	}
	// Bandwidth equal or unknown: prefer lower RTT.
	if candidate.RTT == 0 || current.RTT == 0 {
		return false, rejectionEqualOrUnknown
	}
	if candidate.RTT < current.RTT {
		return true, ""
	}
	return false, rejectionInferiorRTT
}

func newRelayHandler(ann *moqt.Announcement, sess *moqt.Session, nodeID string) *relayHandler {
	if sess == nil {
		panic("relay: session must not be nil")
	}
	if ann == nil {
		return nil
	}

	ctx, cancel := context.WithCancel(sess.Context())
	h := &relayHandler{
		announcement: ann,
		session:      sess,
		tracks:       newTrackManager(),
		nodeID:       nodeID,
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
	sessionStats := h.session.Stats()

	rs := RouteStats{
		Alive:            h.ctx.Err() == nil && h.announcement.IsActive(),
		Hops:             len(h.announcement.HopIDs()),
		EstimatedBitrate: sessionStats.EstimatedBitrate,
		RTT:              sessionStats.RTT,
	}

	return rs
}

func (h *relayHandler) ServeTrack(tw *moqt.TrackWriter) {
	logger := slog.With(
		"broadcast_path", tw.BroadcastPath,
		"track_name", tw.TrackName,
	)

	// Fast path: reuse existing distributor
	trackID := "[" + h.nodeID + "]" + string(tw.BroadcastPath) + "/" + string(tw.TrackName)
	if d, ok := h.tracks.load(trackID); ok {
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
			metricSubscribeErrorsTotal.WithLabelValues("not_found").Inc()
			logger.Warn("Track not found, closing track writer")
			return
		}
		result.Val.(*trackDistributor).egress(tw)
	case <-tw.Context().Done():
		// Client unsubscribed before we could subscribe upstream - just return
		return
	}
}

func (h *relayHandler) subscribe(name moqt.TrackName) *trackDistributor {
	announcement := h.announcement
	if announcement == nil {
		slog.Warn("relay: subscribe failed: announcement is nil", "track", name)
		return nil
	}

	trackID := "[" + h.nodeID + "]" + string(announcement.BroadcastPath()) + "/" + string(name)
	if d, ok := h.tracks.load(trackID); ok {
		return d
	}

	session := h.session
	if session == nil {
		slog.Warn("relay: subscribe failed: session is nil", "track", name)
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

	d := newTrackDistributor(h.tracks, trackID)

	go d.ingest(h.ctx, src)

	h.tracks.store(trackID, d)

	return d
}

// trackManager manages the set of active track distributors.
type trackManager struct {
	m sync.Map // trackID string → *trackDistributor
}

func newTrackManager() *trackManager {
	return &trackManager{}
}

func (tm *trackManager) load(trackID string) (*trackDistributor, bool) {
	v, ok := tm.m.Load(trackID)
	if !ok {
		return nil, false
	}
	return v.(*trackDistributor), true
}

func (tm *trackManager) store(trackID string, d *trackDistributor) {
	tm.m.Store(trackID, d)
}

func (tm *trackManager) remove(trackID string, d *trackDistributor) {
	tm.m.CompareAndDelete(trackID, d)
}

type trackDistributor struct {
	trackID string
	ring    *groupRing
	manager *trackManager

	// Pre-bound Prometheus counters to avoid per-frame label lookups in hot paths.
	ingressCounter prometheus.Counter
	egressCounter  prometheus.Counter

	// fillSem is a buffered-channel semaphore that limits the number of
	// concurrently running fill goroutines. Its capacity is set to
	// MaxGroupFillsInFlight at construction time.
	fillSem chan struct{}

	mu          sync.RWMutex
	subscribers map[chan struct{}]struct{}

	done chan struct{} // closed when ingest returns
}

func newTrackDistributor(manager *trackManager, trackID string) *trackDistributor {
	d := &trackDistributor{
		trackID:        trackID,
		ring:           newGroupRing(DefaultGroupCacheSize, DefaultFramePool),
		manager:        manager,
		ingressCounter: metricRelayIngressBytesTotal.WithLabelValues(trackID),
		egressCounter:  metricRelayEgressBytesTotal.WithLabelValues(trackID),
		fillSem:        make(chan struct{}, maxGroupFillsInFlightOrPanic()),
		subscribers:    make(map[chan struct{}]struct{}),

		done:           make(chan struct{}),
	}
	go d.pollCacheDepth()
	return d
}

func (d *trackDistributor) pollCacheDepth() {
	defer metricBufferDepthGroups.DeleteLabelValues(d.trackID)

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-d.done:
			return
		case <-ticker.C:
			head := d.ring.head()
			earliest := d.ring.earliestAvailable()
			depth := 0
			if head >= earliest {
				depth = int(head - earliest + 1)
			}
			metricBufferDepthGroups.WithLabelValues(d.trackID).Set(float64(depth))
		}
	}
}

func (d *trackDistributor) egress(tw *moqt.TrackWriter) {
	// Get track writer context once and check if it's valid
	twCtx := tw.Context()

	metricSubscribersActive.Inc()
	defer metricSubscribersActive.Dec()

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
				metricSubscriberSkipsTotal.Inc()

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
			start := time.Now()
			frameIdx := 0

			for {
				frame := cache.next(frameIdx)
				if frame != nil {
					if err := gw.WriteFrame(frame); err != nil {
						_ = gw.Close()
						return
					}
					d.egressCounter.Add(float64(frame.Len()))
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
			metricGroupDeliveryHistogram.WithLabelValues(string(tw.TrackName)).Observe(time.Since(start).Seconds())
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
	defer d.manager.remove(d.trackID, d)
	defer close(d.done)

	// wg tracks in-flight fill goroutines so we can wait for them before
	// closing d.done (which signals egress goroutines to stop).
	var wg sync.WaitGroup
	defer wg.Wait()

	for {
		gr, err := src.AcceptGroup(ctx)
		if err != nil {
			return
		}

		// Acquire a fill semaphore slot before reserving the ring slot.
		// This bounds in-flight goroutines to MaxGroupFillsInFlight and
		// prevents unbounded goroutine growth. The semaphore is acquired
		// after AcceptGroup returns, not before — it does not gate AcceptGroup.
		select {
		case d.fillSem <- struct{}{}:
		case <-ctx.Done():
			return
		}

		// Reserve a ring slot synchronously to preserve group ordering,
		// then fill frames concurrently so the next AcceptGroup is not blocked.
		cache := d.ring.reserve(gr.GroupSequence())
		metricGroupFillsInflight.Inc()
		wg.Go(func() {
			defer func() {
				<-d.fillSem
				metricGroupFillsInflight.Dec()
			}()
			d.ring.fill(gr, cache, d.broadcast)
		})
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
