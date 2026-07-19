package relay

import (
	"context"
	"testing"
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

func TestSampleConnStats(t *testing.T) {
	addr := "192.0.2.1:1234"
	provider := &mockConnStatsProvider{
		stats: transport.ConnectionStats{
			SmoothedRTT: 42 * time.Millisecond,
			PacketsSent: 100,
			PacketsLost: 5,
		},
	}
	t.Cleanup(func() {
		metricConnSmoothedRTT.DeleteLabelValues(addr)
		metricConnPacketLossRate.DeleteLabelValues(addr)
	})

	// sampleConnStats is a single synchronous snapshot — the sampler goroutine
	// calls it once per tick, so it needs no timers or bubble to exercise.
	sampleConnStats(provider, addr)

	assert.Equal(t, 42.0, testutil.ToFloat64(metricConnSmoothedRTT.WithLabelValues(addr)))
	assert.Equal(t, 0.05, testutil.ToFloat64(metricConnPacketLossRate.WithLabelValues(addr)))
}

// TestStatsSampler_ConnLifecycle exercises register → sample → deregister for a
// native-QUIC connection through the shared sampler, asserting the series is
// gone after removeConn (the cleanup that used to live in the poller's defer).
func TestStatsSampler_ConnLifecycle(t *testing.T) {
	addr := "192.0.2.9:1234"
	s := &statsSampler{}
	provider := &mockConnStatsProvider{
		stats: transport.ConnectionStats{SmoothedRTT: 7 * time.Millisecond, PacketsSent: 10, PacketsLost: 1},
	}

	s.addConn(addr, provider)
	s.sample()
	assert.Equal(t, 7.0, testutil.ToFloat64(metricConnSmoothedRTT.WithLabelValues(addr)))

	s.removeConn(addr)
	assert.False(t, metricHasLabelValue(t, metricConnSmoothedRTT, addr),
		"conn RTT series for %s should be deleted after removeConn", addr)
	assert.False(t, metricHasLabelValue(t, metricConnPacketLossRate, addr),
		"conn loss-rate series for %s should be deleted after removeConn", addr)
}

// TestStatsSampler_NilSafe verifies every sampler method (and the sweep) is a
// no-op on a nil receiver, so a Server or trackDistributor built without a
// sampler never panics.
func TestStatsSampler_NilSafe(t *testing.T) {
	var s *statsSampler
	assert.NotPanics(t, func() {
		s.addConn("a", nil)
		s.removeConn("a")
		s.addSession("a", nil)
		s.removeSession("a")
		s.addTrack("t", nil)
		s.removeTrack("t")
		s.sample()
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

func TestSampleSessionStats(t *testing.T) {
	addr := "192.0.2.2:4321"
	provider := &mockSessionStatsProvider{
		stats: moqt.SessionStats{
			RTT:              85 * time.Millisecond,
			EstimatedBitrate: 1500000,
		},
	}
	t.Cleanup(func() {
		metricSessionRTTMilliseconds.DeleteLabelValues(addr)
		metricSessionEstimatedBitrate.DeleteLabelValues(addr)
		metricSessionRTTHistogram.DeleteLabelValues(addr)
	})

	sampleSessionStats(provider, addr)

	assert.Equal(t, 85.0, testutil.ToFloat64(metricSessionRTTMilliseconds.WithLabelValues(addr)))
	assert.Equal(t, 1500000.0, testutil.ToFloat64(metricSessionEstimatedBitrate.WithLabelValues(addr)))
}

// TestStatsSampler_SessionLifecycle exercises register → sample → deregister for
// a session, asserting removeSession drops all three series — including the RTT
// histogram, which the old per-session poller leaked (it deleted only the two
// gauges).
func TestStatsSampler_SessionLifecycle(t *testing.T) {
	addr := "192.0.2.3:5555"
	s := &statsSampler{}
	provider := &mockSessionStatsProvider{
		stats: moqt.SessionStats{RTT: 12 * time.Millisecond, EstimatedBitrate: 900000},
	}

	s.addSession(addr, provider)
	s.sample()
	assert.Equal(t, 12.0, testutil.ToFloat64(metricSessionRTTMilliseconds.WithLabelValues(addr)))
	assert.Equal(t, 900000.0, testutil.ToFloat64(metricSessionEstimatedBitrate.WithLabelValues(addr)))

	s.removeSession(addr)
	assert.False(t, metricHasLabelValue(t, metricSessionRTTMilliseconds, addr),
		"session RTT series for %s should be deleted after removeSession", addr)
	assert.False(t, metricHasLabelValue(t, metricSessionEstimatedBitrate, addr),
		"session bitrate series for %s should be deleted after removeSession", addr)
	assert.False(t, metricHasLabelValue(t, metricSessionRTTHistogram, addr),
		"session RTT histogram series for %s should be deleted after removeSession", addr)
}
