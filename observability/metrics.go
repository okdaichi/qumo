package observability

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Metrics following Prometheus naming: namespace_subsystem_name_unit
var (
	activeTracks = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: "qumo",
		Subsystem: "relay",
		Name:      "tracks_active",
		Help:      "Number of active tracks",
	})

	activeSubscribers = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "qumo",
		Subsystem: "relay",
		Name:      "subscribers_active",
		Help:      "Number of active subscribers per track",
	}, []string{"track"})

	groupsReceived = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "qumo",
		Subsystem: "relay",
		Name:      "groups_received_total",
		Help:      "Total groups received",
	}, []string{"track"})

	groupLatency = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "qumo",
		Subsystem: "relay",
		Name:      "group_latency_seconds",
		Help:      "Group operation latency",
		Buckets:   prometheus.DefBuckets,
	}, []string{"track", "op"})

	framesRelayed = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "qumo",
		Subsystem: "relay",
		Name:      "frames_total",
		Help:      "Total frames relayed",
	}, []string{"track"})

	broadcastDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Namespace: "qumo",
		Subsystem: "relay",
		Name:      "broadcast_seconds",
		Help:      "Broadcast duration",
		Buckets:   []float64{.00001, .00005, .0001, .0005, .001, .005, .01},
	})

	broadcastRatio = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "qumo",
		Subsystem: "relay",
		Name:      "broadcast_ratio",
		Help:      "Ratio of subscribers notified",
		Buckets:   []float64{0, 0.5, 0.9, 0.99, 1.0},
	}, []string{"track"})

	subscriberLag = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "qumo",
		Subsystem: "relay",
		Name:      "subscriber_lag_groups",
		Help:      "Subscriber lag in groups",
		Buckets:   []float64{0, 1, 5, 10, 50, 100},
	}, []string{"track"})

	catchups = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "qumo",
		Subsystem: "relay",
		Name:      "catchups_total",
		Help:      "Total subscriber catchups",
	}, []string{"track"})

	cacheHits = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "qumo",
		Subsystem: "relay",
		Name:      "cache_hits_total",
		Help:      "Cache hits",
	}, []string{"track"})

	cacheMisses = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "qumo",
		Subsystem: "relay",
		Name:      "cache_misses_total",
		Help:      "Cache misses",
	}, []string{"track"})
)

// --- Global Metric Functions ---

// IncTracks increments active track count.
func IncTracks() {
	if MetricsEnabled() {
		activeTracks.Inc()
	}
}

// DecTracks decrements active track count.
func DecTracks() {
	if MetricsEnabled() {
		activeTracks.Dec()
	}
}

// --- Recorder ---

// TrackRecorder records metrics for a specific track.
// Create one per track using NewRecorder.
type TrackRecorder struct {
	track string
}

// NewRecorder creates a recorder for the given track.
func NewRecorder(track string) *TrackRecorder {
	return &TrackRecorder{track: track}
}

// IncSubscribers increments subscriber count.
func (r *TrackRecorder) IncSubscribers() {
	if MetricsEnabled() {
		activeSubscribers.WithLabelValues(r.track).Inc()
	}
}

// DecSubscribers decrements subscriber count.
func (r *TrackRecorder) DecSubscribers() {
	if MetricsEnabled() {
		activeSubscribers.WithLabelValues(r.track).Dec()
	}
}

// SetSubscribers sets subscriber count.
func (r *TrackRecorder) SetSubscribers(n int) {
	if MetricsEnabled() {
		activeSubscribers.WithLabelValues(r.track).Set(float64(n))
	}
}

// GroupReceived records a received group.
func (r *TrackRecorder) GroupReceived() {
	if MetricsEnabled() {
		groupsReceived.WithLabelValues(r.track).Inc()
	}
}

// CacheHit records a cache hit.
func (r *TrackRecorder) CacheHit() {
	if MetricsEnabled() {
		cacheHits.WithLabelValues(r.track).Inc()
	}
}

// CacheMiss records a cache miss.
func (r *TrackRecorder) CacheMiss() {
	if MetricsEnabled() {
		cacheMisses.WithLabelValues(r.track).Inc()
	}
}

// Catchup records a subscriber catchup with lag.
func (r *TrackRecorder) Catchup(lag int) {
	if MetricsEnabled() {
		catchups.WithLabelValues(r.track).Inc()
		subscriberLag.WithLabelValues(r.track).Observe(float64(lag))
	}
}

// Broadcast records a broadcast operation.
func (r *TrackRecorder) Broadcast(d time.Duration, total, notified int) {
	if !MetricsEnabled() {
		return
	}
	broadcastDuration.Observe(d.Seconds())
	r.SetSubscribers(total)
	if total > 0 {
		broadcastRatio.WithLabelValues(r.track).Observe(float64(notified) / float64(total))
	}
}

// LatencyObs returns an observer for latency metrics.
// op should be "receive" or "write".
func (r *TrackRecorder) LatencyObs(op string) prometheus.Observer {
	if !MetricsEnabled() {
		return nil
	}
	return groupLatency.WithLabelValues(r.track, op)
}
