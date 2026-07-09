package relay

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
)

// TestHandlePeerGoaway_Metric verifies the Dialer.OnGoaway callback records the
// event with the correct redirect label.
func TestHandlePeerGoaway_Metric(t *testing.T) {
	beforeAbsent := testutil.ToFloat64(metricPeerGoawayReceived.WithLabelValues("absent"))
	beforePresent := testutil.ToFloat64(metricPeerGoawayReceived.WithLabelValues("present"))

	handlePeerGoaway("")
	handlePeerGoaway("https://relay-eu.example.com/moq")
	handlePeerGoaway("https://relay-us.example.com/moq")

	assert.Equal(t, beforeAbsent+1, testutil.ToFloat64(metricPeerGoawayReceived.WithLabelValues("absent")))
	assert.Equal(t, beforePresent+2, testutil.ToFloat64(metricPeerGoawayReceived.WithLabelValues("present")))
}
