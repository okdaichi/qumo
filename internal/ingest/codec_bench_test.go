package ingest

import (
	"testing"

	"github.com/qumo-dev/gomoqt/moqt"
)

// BenchmarkParseAVCConfig measures FLV AVCDecoderConfigurationRecord parsing,
// run once per RTMP connect (sequence header).
func BenchmarkParseAVCConfig(b *testing.B) {
	sps := []byte{0x67, 0x64, 0x00, 0x1F, 0xAC, 0xD9, 0x40, 0x50}
	pps := []byte{0x68, 0xEB, 0xE3, 0xCB}
	data := buildAVCSeqHeader(0x64, 0x00, 0x1F, sps, pps)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := ParseAVCConfig(data); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkAACDepacketizer_Depacketize measures mpeg4-generic RTP → AAC access
// unit depacketization on the RTSP audio hot path.
func BenchmarkAACDepacketizer_Depacketize(b *testing.B) {
	depack := newAACDepacketizer(fmtpAAC48k, 48000)
	au := make([]byte, 256) // ~256-byte AAC frame
	payload := buildMpeg4Generic([][]byte{au}, 13, 3)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := depack.depacketize(payload, 0); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkBuildMediaFrame measures the MediaFrame envelope construction that
// wraps every pushed video/audio frame.
func BenchmarkBuildMediaFrame(b *testing.B) {
	data := make([]byte, 1024)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = buildMediaFrame(1_000_000, data)
	}
}

// BenchmarkIngestFrameConstruction measures the per-frame allocation cost of
// Session.PushVideo/PushAudio: a pre-sized *moqt.Frame with the MediaFrame
// envelope streamed directly into it (no intermediate payload slice). The
// relay fan-out path, by contrast, reuses frames via relay.DefaultFramePool
// with refcount-based release.
func BenchmarkIngestFrameConstruction(b *testing.B) {
	data := make([]byte, 1024)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		f := moqt.NewFrame(mediaFrameSize(1_000_000, len(data)))
		writeMediaFrame(f, 1_000_000, data)
		_ = f
	}
}
