package relay

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/qumo-dev/gomoqt/moqt"
	"github.com/qumo-dev/gomoqt/transport"
	"github.com/stretchr/testify/assert"
)

type mockConnStatsProvider struct {
	stats transport.ConnectionStats
}

func (m *mockConnStatsProvider) ConnectionStats() transport.ConnectionStats {
	return m.stats
}

func TestPollConnStats(t *testing.T) {
	addr := "192.0.2.1:1234"
	ctx, cancel := context.WithCancel(context.Background())

	provider := &mockConnStatsProvider{
		stats: transport.ConnectionStats{
			SmoothedRTT: 42 * time.Millisecond,
			PacketsSent: 100,
			PacketsLost: 5,
		},
	}

	go pollConnStats(ctx, provider, addr)

	// Wait briefly for the immediate first sample to be collected.
	time.Sleep(50 * time.Millisecond)

	// Verify metrics were updated
	rtt := testutil.ToFloat64(metricConnSmoothedRTT.WithLabelValues(addr))
	assert.Equal(t, 42.0, rtt, "Expected SmoothedRTT metric to be 42")

	lossRate := testutil.ToFloat64(metricConnPacketLossRate.WithLabelValues(addr))
	assert.Equal(t, 0.05, lossRate, "Expected PacketLossRate metric to be 0.05")

	// Cancel the context and wait for cleanup
	cancel()
	time.Sleep(50 * time.Millisecond)

	// Verify metrics were deleted
	err := testutil.CollectAndCompare(metricConnSmoothedRTT, strings.NewReader(""))
	assert.NoError(t, err)

	err = testutil.CollectAndCompare(metricConnPacketLossRate, strings.NewReader(""))
	assert.NoError(t, err)
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
	ctx, cancel := context.WithCancel(context.Background())

	provider := &mockSessionStatsProvider{
		stats: moqt.SessionStats{
			RTT:              85 * time.Millisecond,
			EstimatedBitrate: 1500000,
		},
		ctx: ctx,
	}

	go pollSessionStats(provider, addr)

	// Wait briefly for the immediate first sample to be collected.
	time.Sleep(50 * time.Millisecond)

	// Verify metrics were updated
	rtt := testutil.ToFloat64(metricSessionRTTMilliseconds.WithLabelValues(addr))
	assert.Equal(t, 85.0, rtt, "Expected SessionRTTMilliseconds metric to be 85")

	bitrate := testutil.ToFloat64(metricSessionEstimatedBitrate.WithLabelValues(addr))
	assert.Equal(t, 1500000.0, bitrate, "Expected SessionEstimatedBitrate metric to be 1500000")

	// The histogram metricSessionRTTHistogram is harder to get an exact count without collecting the whole thing,
	// but we can at least ensure we don't panic and the gauges are right.

	// Cancel the context and wait for cleanup
	cancel()
	time.Sleep(50 * time.Millisecond)

	// Verify metrics were deleted
	err := testutil.CollectAndCompare(metricSessionRTTMilliseconds, strings.NewReader(""))
	assert.NoError(t, err)

	err = testutil.CollectAndCompare(metricSessionEstimatedBitrate, strings.NewReader(""))
	assert.NoError(t, err)
}
