package ingest

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestResolveControlURL(t *testing.T) {
	const session = "rtsp://camera:554/stream"

	tests := map[string]struct {
		control string
		want    string
	}{
		"empty → session URL":    {"", session},
		"star → session URL":     {"*", session},
		"absolute rtsp URL":      {"rtsp://camera:554/stream/trackID=1", "rtsp://camera:554/stream/trackID=1"},
		"relative trackID":       {"trackID=0", "rtsp://camera:554/stream/trackID=0"},
		"relative path":          {"/stream/video", "rtsp://camera:554/stream/video"},
		"relative without slash": {"video", "rtsp://camera:554/stream/video"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			got := resolveControlURL(tc.control, session)
			assert.Equal(t, tc.want, got)
		})
	}
}
