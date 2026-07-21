package relay

import (
	"fmt"
	"sync"
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
func metricHasLabelValue(tb testing.TB, c prometheus.Collector, value string) bool {
	tb.Helper()
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

var _ connStatsProvider = (*fakeConnStatsProvider)(nil)

type fakeConnStatsProvider struct {
	stats transport.ConnectionStats
}

func (f *fakeConnStatsProvider) ConnectionStats() transport.ConnectionStats {
	return f.stats
}

func TestSampleConnStats(t *testing.T) {
	addr := "192.0.2.1:1234"
	provider := &fakeConnStatsProvider{
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

// TestStatsSampler_ConnLifecycle exercises register → sample → deregister → reap
// for a native-QUIC connection. removeConn only queues the deletion; the series
// is dropped by reapDeleted (which the run loop calls after each sweep).
func TestStatsSampler_ConnLifecycle(t *testing.T) {
	addr := "192.0.2.9:1234"
	s := &statsSampler{}
	provider := &fakeConnStatsProvider{
		stats: transport.ConnectionStats{SmoothedRTT: 7 * time.Millisecond, PacketsSent: 10, PacketsLost: 1},
	}

	s.addConn(addr, provider)
	s.sample()
	assert.Equal(t, 7.0, testutil.ToFloat64(metricConnSmoothedRTT.WithLabelValues(addr)))

	s.removeConn(addr)
	s.reapDeleted()
	assert.False(t, metricHasLabelValue(t, metricConnSmoothedRTT, addr),
		"conn RTT series for %s should be deleted after removeConn+reap", addr)
	assert.False(t, metricHasLabelValue(t, metricConnPacketLossRate, addr),
		"conn loss-rate series for %s should be deleted after removeConn+reap", addr)
}

// TestStatsSampler_ReapSkipsReRegistered verifies the reap is a no-op when the
// same addr was re-registered before the reap ran (ephemeral-port reuse): the
// new owner's series must survive.
func TestStatsSampler_ReapSkipsReRegistered(t *testing.T) {
	addr := "192.0.2.10:1234"
	s := &statsSampler{}
	t.Cleanup(func() {
		metricConnSmoothedRTT.DeleteLabelValues(addr)
		metricConnPacketLossRate.DeleteLabelValues(addr)
	})
	provider := &fakeConnStatsProvider{
		stats: transport.ConnectionStats{SmoothedRTT: 5 * time.Millisecond, PacketsSent: 10, PacketsLost: 1},
	}

	// Old conn departs (queues a reap), then a new conn reuses the same addr and
	// is sampled — all before the reap runs.
	s.removeConn(addr)
	s.addConn(addr, provider)
	s.sample()
	s.reapDeleted()

	assert.True(t, metricHasLabelValue(t, metricConnSmoothedRTT, addr),
		"re-registered conn series for %s must survive the stale reap", addr)
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
		s.reapDeleted()
	})
}

var _ sessionStatsProvider = (*fakeSessionStatsProvider)(nil)

type fakeSessionStatsProvider struct {
	stats moqt.SessionStats
}

func (f *fakeSessionStatsProvider) Stats() moqt.SessionStats {
	return f.stats
}

func TestSampleSessionStats(t *testing.T) {
	addr := "192.0.2.2:4321"
	provider := &fakeSessionStatsProvider{
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
	provider := &fakeSessionStatsProvider{
		stats: moqt.SessionStats{RTT: 12 * time.Millisecond, EstimatedBitrate: 900000},
	}

	s.addSession(addr, provider)
	s.sample()
	assert.Equal(t, 12.0, testutil.ToFloat64(metricSessionRTTMilliseconds.WithLabelValues(addr)))
	assert.Equal(t, 900000.0, testutil.ToFloat64(metricSessionEstimatedBitrate.WithLabelValues(addr)))

	s.removeSession(addr)
	s.reapDeleted()
	assert.False(t, metricHasLabelValue(t, metricSessionRTTMilliseconds, addr),
		"session RTT series for %s should be deleted after removeSession+reap", addr)
	assert.False(t, metricHasLabelValue(t, metricSessionEstimatedBitrate, addr),
		"session bitrate series for %s should be deleted after removeSession+reap", addr)
	assert.False(t, metricHasLabelValue(t, metricSessionRTTHistogram, addr),
		"session RTT histogram series for %s should be deleted after removeSession+reap", addr)
}

// TestStatsSampler_ConcurrentSampleAndDeregister is the regression guard for the
// resurrection race: with sampling on one goroutine and deregistration on
// others, a series deleted inline would be re-created by a concurrent Set and
// leak. Because deletion is deferred onto the sampler goroutine (reapDeleted),
// after a final sweep+reap every departed series must be gone. Run under -race.
func TestStatsSampler_ConcurrentSampleAndDeregister(t *testing.T) {
	s := &statsSampler{}
	const n = 200
	addrs := make([]string, n)
	for i := range addrs {
		addr := fmt.Sprintf("198.51.100.1:%d", 10000+i)
		addrs[i] = addr
		s.addSession(addr, &fakeSessionStatsProvider{stats: moqt.SessionStats{RTT: 10 * time.Millisecond}})
	}
	t.Cleanup(func() {
		for _, addr := range addrs {
			metricSessionRTTMilliseconds.DeleteLabelValues(addr)
			metricSessionEstimatedBitrate.DeleteLabelValues(addr)
			metricSessionRTTHistogram.DeleteLabelValues(addr)
		}
	})

	var wg sync.WaitGroup
	// One goroutine sweeps repeatedly (the sampler's role); others deregister.
	wg.Go(func() {
		for range 50 {
			s.sample()
			s.reapDeleted()
		}
	})
	for _, addr := range addrs {
		wg.Go(func() {
			s.removeSession(addr)
		})
	}
	wg.Wait()

	// All entities departed; a final sweep+reap must clear every series.
	s.sample()
	s.reapDeleted()
	for _, addr := range addrs {
		assert.False(t, metricHasLabelValue(t, metricSessionRTTMilliseconds, addr),
			"session RTT series for %s must not survive deregistration", addr)
	}
}
