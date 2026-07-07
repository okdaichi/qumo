package relay

import (
	"context"
	"time"

	"github.com/qumo-dev/gomoqt/moqt"
	"github.com/qumo-dev/gomoqt/transport"
)

// connStatsProvider is satisfied by native QUIC connections (quic.Connection)
// but not by WebTransport sessions. It mirrors the unexported
// probeStatsProvider interface in gomoqt/moqt/session.go.
type connStatsProvider interface {
	ConnectionStats() transport.ConnectionStats
}

// pollConnStats polls QUIC connection-level statistics for an inbound
// native-QUIC connection and updates Prometheus gauges until ctx is cancelled.
func pollConnStats(ctx context.Context, provider connStatsProvider, addr string) {
	defer func() {
		metricConnSmoothedRTT.DeleteLabelValues(addr)
		metricConnPacketLossRate.DeleteLabelValues(addr)
	}()

	poll := func() {
		stats := provider.ConnectionStats()
		if stats.SmoothedRTT > 0 {
			metricConnSmoothedRTT.WithLabelValues(addr).Set(float64(stats.SmoothedRTT.Milliseconds()))
		}
		if stats.PacketsSent > 0 {
			lossRate := float64(stats.PacketsLost) / float64(stats.PacketsSent)
			metricConnPacketLossRate.WithLabelValues(addr).Set(lossRate)
		}
	}
	poll() // immediate first sample

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			poll()
		}
	}
}

// sessionStatsProvider is satisfied by *moqt.Session.
type sessionStatsProvider interface {
	Stats() moqt.SessionStats
	Context() context.Context
}

// pollSessionStats periodically samples MoQT-level session statistics (RTT and
// estimated bitrate) and updates Prometheus gauges. It exits when the session
// context is cancelled.
func pollSessionStats(sess sessionStatsProvider, addr string) {
	defer func() {
		metricSessionRTTMilliseconds.DeleteLabelValues(addr)
		metricSessionEstimatedBitrate.DeleteLabelValues(addr)
	}()

	poll := func() {
		stats := sess.Stats()
		if stats.RTT > 0 {
			metricSessionRTTMilliseconds.WithLabelValues(addr).Set(float64(stats.RTT.Milliseconds()))
			metricSessionRTTHistogram.WithLabelValues(addr).Observe(stats.RTT.Seconds())
		}
		if stats.EstimatedBitrate > 0 {
			metricSessionEstimatedBitrate.WithLabelValues(addr).Set(float64(stats.EstimatedBitrate))
		}
	}
	poll() // immediate first sample

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-sess.Context().Done():
			return
		case <-ticker.C:
			poll()
		}
	}
}
