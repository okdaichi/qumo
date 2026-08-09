package ingest

import (
	"testing"

	"github.com/qumo-dev/gomoqt/moqt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewSession_RejectsInvalidPath(t *testing.T) {
	tests := []struct {
		name string
		path moqt.BroadcastPath
	}{
		{"empty", ""},
		{"missing leading slash", "live/camera"},
		{"relative", "./live"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewSession(moqt.NewTrackMux(0), tt.path)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "invalid broadcast path")
		})
	}
}

func TestNewSession_AcceptsValidPath(t *testing.T) {
	sess, err := NewSession(moqt.NewTrackMux(0), "/live/camera")
	require.NoError(t, err)
	defer sess.Close()
}
