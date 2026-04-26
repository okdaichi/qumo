package ingest

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// metricPublishersActive tracks the number of currently active ingest publishers
	// (RTMP/FLV streams).
	metricPublishersActive = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: "qumo",
		Subsystem: "ingest",
		Name:      "publishers_active",
		Help:      "Current number of active ingest publishers.",
	})

	// metricSubscribersActive tracks the number of currently active MoQT track
	// subscribers for the ingest subsystem.
	metricSubscribersActive = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: "qumo",
		Subsystem: "ingest",
		Name:      "subscribers_active",
		Help:      "Current number of active MoQT track subscribers in ingest.",
	})

	// metricSubscriberSkipsTotal counts how many times a subscriber was skipped
	// forward because it fell behind the ring buffer in ingest.
	metricSubscriberSkipsTotal = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: "qumo",
		Subsystem: "ingest",
		Name:      "subscriber_skips_total",
		Help:      "Total number of times subscribers were skipped forward due to falling behind in ingest.",
	})

	// metricBufferDepthGroups tracks the number of groups currently held in the
	// track's ring buffer in ingest, labelled by track name.
	metricBufferDepthGroups = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "qumo",
			Subsystem: "ingest",
			Name:      "buffer_depth_groups",
			Help:      "Number of groups currently held in the track's ring buffer in ingest.",
		},
		[]string{"track"},
	)

	// metricGroupDeliveryHistogram tracks the time it takes to deliver a full group to a subscriber in ingest.
	metricGroupDeliveryHistogram = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "qumo",
			Subsystem: "ingest",
			Name:      "group_delivery_seconds",
			Help:      "Time taken to deliver a complete group to a subscriber in ingest in seconds.",
			Buckets:   prometheus.DefBuckets,
		},
		[]string{"track"},
	)

	// metricSubscribeErrorsTotal counts how many times a MoQT subscription
	// request failed in ingest, labelled by error code.
	metricSubscribeErrorsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "qumo",
			Subsystem: "ingest",
			Name:      "subscribe_errors_total",
			Help:      "Total number of MoQT subscription errors in ingest.",
		},
		[]string{"code"},
	)
)
