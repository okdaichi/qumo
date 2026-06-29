package rtsp

import (
	"bufio"
	"bytes"
	"io"
	"net/http"
	"net/url"
	"testing"
)

// sampleRTP is a 12-byte RTP header + 64-byte payload, representative of an
// interleaved video RTP frame on the ingest hot path.
var sampleRTP = func() []byte {
	b := make([]byte, 12+64)
	b[0] = 0x80 // V=2, no padding/extension
	b[1] = 0xE0 // marker, PT=96
	b[2] = 0x12
	b[3] = 0x34
	b[4] = 0x00
	b[5] = 0x01
	b[6] = 0x63
	b[7] = 0x2A
	b[8] = 0xAB
	b[9] = 0xCD
	b[10] = 0xEF
	b[11] = 0x01
	return b
}()

func BenchmarkUnmarshalRTP(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := UnmarshalRTP(sampleRTP); err != nil {
			b.Fatal(err)
		}
	}
}

const benchSDP = "v=0\r\n" +
	"o=- 0 0 IN IP4 127.0.0.1\r\n" +
	"s=stream\r\n" +
	"m=video 0 RTP/AVP 96\r\n" +
	"a=rtpmap:96 H264/90000\r\n" +
	"a=fmtp:96 packetization-mode=1; profile-level-id=42e01e\r\n" +
	"a=control:trackID=0\r\n" +
	"m=audio 0 RTP/AVP 97\r\n" +
	"a=rtpmap:97 mpeg4-generic/48000/2\r\n" +
	"a=control:trackID=1\r\n"

func BenchmarkParseSDP(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = ParseSDP(benchSDP)
	}
}

func BenchmarkRequest_Write(b *testing.B) {
	u, _ := url.Parse("rtsp://example.com/media.mp4")
	req := &Request{
		Method: MethodPlay,
		URL:    u,
		Proto:  "RTSP/1.0",
		Header: http.Header{
			"CSeq":      {"2"},
			"User-Agent": {"qumo-bench"},
			"Session":    {"12345678"},
		},
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := req.Write(io.Discard); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkReadRequest(b *testing.B) {
	u, _ := url.Parse("rtsp://example.com/media.mp4")
	req := &Request{
		Method: MethodPlay,
		URL:    u,
		Proto:  "RTSP/1.0",
		Header: http.Header{"CSeq": {"2"}},
	}
	// Serialize the request once, then re-parse the bytes each iteration.
	var serialized bytes.Buffer
	if err := req.Write(&serialized); err != nil {
		b.Fatal(err)
	}
	wire := serialized.Bytes()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		br := bufio.NewReader(bytes.NewReader(wire))
		if _, err := ReadRequest(br); err != nil {
			b.Fatal(err)
		}
	}
}
