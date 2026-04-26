package relay

import (
	"context"
	"time"

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

// // pollRTT periodically samples the smoothed RTT for an outbound relay
// // session and updates the Prometheus gauge. It exits when the session ends.
// func pollRTT(sess *moqt.Session, addr string) {
// 	defer metricPeerRTTMilliseconds.DeleteLabelValues(addr)

// 	probe := func() {
// 		if result, err := sess.Probe(0); err == nil && result.RTT > 0 {
// 			metricPeerRTTMilliseconds.WithLabelValues(addr).Set(float64(result.RTT))
// 		}
// 	}
// 	probe() // immediate first sample

// 	ticker := time.NewTicker(30 * time.Second)
// 	defer ticker.Stop()
// 	for {
// 		select {
// 		case <-sess.Context().Done():
// 			return
// 		case <-ticker.C:
// 			probe()
// 		}
// 	}
// }
