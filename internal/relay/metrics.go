package relay

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// metricSessionsActive tracks the number of currently active MoQT sessions
	// being served by Relay(). Replaces the active_connections field that was
	// previously tracked inside statusHandler.
	metricSessionsActive = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: "qumo",
		Subsystem: "relay",
		Name:      "sessions_active",
		Help:      "Current number of active MoQT relay sessions.",
	})

	// metricPeersConnected tracks the number of active outbound relay peer
	// connections managed by maintainPeer.
	metricPeersConnected = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: "qumo",
		Subsystem: "relay",
		Name:      "peers_connected",
		Help:      "Current number of outbound relay peer connections.",
	})

	// metricBroadcastsActive tracks the number of relay broadcast routes
	// currently registered in the TrackMux (including routes that are
	// draining after being replaced by a better route).
	metricBroadcastsActive = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: "qumo",
		Subsystem: "relay",
		Name:      "broadcasts_active",
		Help:      "Current number of active relay broadcast routes.",
	})

	// metricPeerRTTMilliseconds tracks the smoothed RTT (in milliseconds) to
	// each outbound relay peer, labelled by peer address.
	metricPeerRTTMilliseconds = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "qumo",
			Subsystem: "relay",
			Name:      "peer_rtt_ms",
			Help:      "Smoothed round-trip time to each outbound relay peer in milliseconds.",
		},
		[]string{"peer"},
	)

	// metricPeerDialAttempts counts outbound peer dial attempts, labelled by
	// peer address and result ("ok" or "error").
	metricPeerDialAttempts = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "qumo",
			Subsystem: "relay",
			Name:      "peer_dial_attempts_total",
			Help:      "Total number of outbound relay peer dial attempts.",
		},
		[]string{"peer", "result"},
	)

	// metricRouteReplacements counts how many times an existing broadcast route
	// was replaced by a strictly better candidate.
	metricRouteReplacements = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: "qumo",
		Subsystem: "relay",
		Name:      "route_replacements_total",
		Help:      "Total number of relay broadcast routes replaced by a better route.",
	})

	// metricRouteRejections counts route candidates that were rejected because
	// they were not better than the existing route. The reason label carries the
	// specific rejection cause from isBetterRoute.
	metricRouteRejections = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "qumo",
			Subsystem: "relay",
			Name:      "route_rejections_total",
			Help:      "Total number of relay route candidates rejected, by rejection reason.",
		},
		[]string{"reason"},
	)
)
