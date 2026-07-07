package relay

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"
	"github.com/qumo-dev/gomoqt/moqt"
	"github.com/qumo-dev/gomoqt/transport"
	"github.com/stretchr/testify/assert"
)

// metricHasLabelValue reports whether any sample collected from c carries the
// given label value. It observes the metric without mutating it, so it can
// assert that a specific series was deleted even when the same (process-global)
// metric vec carries unrelated samples left by sibling tests.
func metricHasLabelValue(t *testing.T, c prometheus.Collector, value string) bool {
	t.Helper()
	ch := make(chan prometheus.Metric, 64)
	go func() {
		c.Collect(ch)
		close(ch)
	}()
	for m := range ch {
		var pb dto.Metric
		if m.Write(&pb) != nil {
			continue
		}
		for _, l := range pb.GetLabel() {
			if l.GetValue() == value {
				return true
			}
		}
	}
	return false
}

type mockConnStatsProvider struct {
	stats transport.ConnectionStats
}

func (m *mockConnStatsProvider) ConnectionStats() transport.ConnectionStats {
	return m.stats
}

func TestPollConnStats(t *testing.T) {
	addr := "192.0.2.1:1234"
	provider := &mockConnStatsProvider{
		stats: transport.ConnectionStats{
			SmoothedRTT: 42 * time.Millisecond,
			PacketsSent: 100,
			PacketsLost: 5,
		},
	}

	// pollConnStats runs an immediate first sample then blocks on its ticker /
	// context, deleting its Prometheus series on ctx cancel. Both the first
	// sample and the deferred cleanup race a fixed wall-clock sleep, so run the
	// poller in a synctest bubble where its lifecycle is deterministic. Per the
	// project's go-test guidance (§8), timer/scheduling tests use synctest.
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		go pollConnStats(ctx, provider, addr)

		// The poller has run its immediate first sample and is now durably
		// blocked on the ticker/context select.
		synctest.Wait()

		assert.Equal(t, 42.0, testutil.ToFloat64(metricConnSmoothedRTT.WithLabelValues(addr)))
		assert.Equal(t, 0.05, testutil.ToFloat64(metricConnPacketLossRate.WithLabelValues(addr)))

		cancel()

		// The poller observes ctx.Done, runs its deferred DeleteLabelValues,
		// and exits — so its own addr series is gone.
		synctest.Wait()

		assert.False(t, metricHasLabelValue(t, metricConnSmoothedRTT, addr),
			"conn RTT series for %s should be deleted after cancel", addr)
		assert.False(t, metricHasLabelValue(t, metricConnPacketLossRate, addr),
			"conn loss-rate series for %s should be deleted after cancel", addr)
	})
}

type mockSessionStatsProvider struct {
	stats moqt.SessionStats
	ctx   context.Context
}

func (m *mockSessionStatsProvider) Stats() moqt.SessionStats {
	return m.stats
}

func (m *mockSessionStatsProvider) Context() context.Context {
	return m.ctx
}

func TestPollSessionStats(t *testing.T) {
	addr := "192.0.2.2:4321"

	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		provider := &mockSessionStatsProvider{
			stats: moqt.SessionStats{
				RTT:              85 * time.Millisecond,
				EstimatedBitrate: 1500000,
			},
			ctx: ctx,
		}

		go pollSessionStats(provider, addr)

		// Immediate first sample collected, then blocked on ticker/session ctx.
		synctest.Wait()

		assert.Equal(t, 85.0, testutil.ToFloat64(metricSessionRTTMilliseconds.WithLabelValues(addr)))
		assert.Equal(t, 1500000.0, testutil.ToFloat64(metricSessionEstimatedBitrate.WithLabelValues(addr)))

		cancel()

		// Deferred DeleteLabelValues has run for this addr.
		synctest.Wait()

		assert.False(t, metricHasLabelValue(t, metricSessionRTTMilliseconds, addr),
			"session RTT series for %s should be deleted after cancel", addr)
		assert.False(t, metricHasLabelValue(t, metricSessionEstimatedBitrate, addr),
			"session bitrate series for %s should be deleted after cancel", addr)
	})
}
