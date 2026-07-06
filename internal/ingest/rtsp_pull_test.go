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

func TestRedactURL(t *testing.T) {
	assert.Equal(t, "rtsp://camera:554/stream", redactURL("rtsp://admin:secret@camera:554/stream"))
	assert.Equal(t, "rtsp://camera:554/stream", redactURL("rtsp://camera:554/stream"))
	assert.Equal(t, "garbage", redactURL("garbage")) // unparseable → returned as-is
}

func TestSameOrigin(t *testing.T) {
	const session = "rtsp://camera:554/stream"
	assert.True(t, sameOrigin("rtsp://camera:554/stream/trackID=0", session)) // same origin
	assert.True(t, sameOrigin("rtsp://camera:554/other", session))            // same host:port
	assert.False(t, sameOrigin("rtsp://evil.com/stream", session))            // different host (SSRF)
	assert.False(t, sameOrigin("http://camera:554/stream", session))          // different scheme
	assert.False(t, sameOrigin("rtsp://camera:555/stream", session))          // different port
}
