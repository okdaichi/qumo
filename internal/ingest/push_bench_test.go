package ingest

import (
	"testing"

	"github.com/qumo-dev/gomoqt/moqt"
)

// BenchmarkSession_PushVideo measures the real ingest per-frame cost through
// the public Session API (RegisterVideo + PushVideo), the path that option A
// changed. It is deliberately written against the stable API so the same file
// can run on base (buildMediaFrame path) and head (writeMediaFrame path) for a
// fair benchstat comparison. This file is intentionally untracked (not shipped).
func BenchmarkSession_PushVideo(b *testing.B) {
	mux := moqt.NewTrackMux(0)
	sess, err := NewSession(mux, "/live/bench")
	if err != nil {
		b.Fatalf("NewSession: %v", err)
	}
	defer sess.Close()
	if err := sess.RegisterVideo(&AVCConfig{
		NALULenSize: 4,
		SPS:         [][]byte{{0x67, 0x64, 0x00, 0x1F}},
		PPS:         [][]byte{{0x68, 0xEB}},
	}); err != nil {
		b.Fatalf("RegisterVideo: %v", err)
	}

	data := make([]byte, 1024) // ~1kB Annex-B frame
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Keyframe every 30 frames to open a new group (GOP) periodically.
		sess.PushVideo(int64(i)*33333, data, i%30 == 0)
	}
}
