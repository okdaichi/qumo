package ingest

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"io"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/qumo-dev/gomoqt/moqt"
	"github.com/qumo-dev/qumo/internal/rtsp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

// --- Default-case integration test: fake RTSP server + pull client → Session ---

// buildRTPHeader constructs a minimal 12-byte RTP header.
func buildRTPHeader(pt uint8, marker bool, seq uint16, ts, ssrc uint32) []byte {
	h := make([]byte, 12)
	h[0] = 0x80 // V=2, no padding/ext/csrc
	h[1] = pt
	if marker {
		h[1] |= 0x80
	}
	binary.BigEndian.PutUint16(h[2:4], seq)
	binary.BigEndian.PutUint32(h[4:8], ts)
	binary.BigEndian.PutUint32(h[8:12], ssrc)
	return h
}

// startFakeRTSPServer starts a minimal RTSP server that responds to
// OPTIONS/DESCRIBE/SETUP/PLAY and then streams the supplied interleaved RTP
// frames before closing. It returns the listener address.
func startFakeRTSPServer(t *testing.T, sdpBody string, frames map[uint8][]byte) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		rc := rtsp.NewConn(conn)
		for {
			req, _, err := rc.ReadRequest()
			if err != nil {
				return
			}
			resp := rtsp.NewResponse(rtsp.StatusOK, req)
			if cseq := req.Header.Get("CSeq"); cseq != "" {
				resp.Header.Set("CSeq", cseq)
			}
			switch req.Method {
			case rtsp.MethodOptions:
				resp.Header.Set("Public", "DESCRIBE, SETUP, PLAY, TEARDOWN")
			case rtsp.MethodDescribe:
				body := []byte(sdpBody)
				resp.Header.Set("Content-Type", "application/sdp")
				resp.Header.Set("Content-Length", strconv.Itoa(len(body)))
				resp.Body = io.NopCloser(bytes.NewReader(body))
			case rtsp.MethodSetup:
				resp.Header.Set("Transport", req.Header.Get("Transport"))
				resp.Header.Set("Session", "fake-session")
			case rtsp.MethodPlay:
				_ = rc.WriteResponse(resp)
				for ch, payload := range frames {
					_ = rc.WriteInterleavedFrame(&rtsp.InterleavedFrame{
						Channel: ch, Payload: payload,
					})
				}
				return // done streaming — close connection
			}
			_ = rc.WriteResponse(resp)
		}
	}()

	return ln.Addr().String()
}

// TestPullStream_DefaultCase exercises the full pull pipeline: a fake RTSP
// server with H.264 video + AAC audio → the pull client (DESCRIBE/SETUP/PLAY)
// → interleaved RTP → depacketize → Session buffers receive frames.
func TestPullStream_DefaultCase(t *testing.T) {
	// SDP with H.264 video (sprop-parameter-sets) and AAC audio (mpeg4-generic).
	sps := []byte{0x67, 0x64, 0x00, 0x1F, 0xAC, 0xD9} // NAL type 7
	pps := []byte{0x68, 0xEB, 0xE3, 0xCB}             // NAL type 8
	sprop := base64.StdEncoding.EncodeToString(sps) + "," + base64.StdEncoding.EncodeToString(pps)
	sdpBody := strings.Join([]string{
		"v=0",
		"m=video 0 RTP/AVP 96",
		"a=rtpmap:96 H264/90000",
		"a=fmtp:96 packetization-mode=1; sprop-parameter-sets=" + sprop + "; profile-level-id=64001f",
		"a=control:trackID=0",
		"m=audio 0 RTP/AVP 97",
		"a=rtpmap:97 mpeg4-generic/48000/2",
		"a=fmtp:97 streamtype=5; profile-level-id=1; mode=AAC-hbr; sizelength=13; indexlength=3; indexdeltalength=3; config=1190",
		"a=control:trackID=1",
		"",
	}, "\r\n")

	// Video RTP: single NAL IDR (type 5), marker bit set (end of access unit).
	videoRTP := append(buildRTPHeader(96, true, 1, 0, 0x12345678),
		0x65, 0xAA, 0xBB, 0xCC, 0xDD, // IDR NAL (NRI=3, type=5)
	)

	// Audio RTP: mpeg4-generic payload wrapping one AAC AU.
	aacAU := []byte{0xDE, 0xAD, 0xBE, 0xEF}
	aacPayload := buildMpeg4Generic([][]byte{aacAU}, 13, 3)
	audioRTP := append(buildRTPHeader(97, true, 1, 0, 0x87654321), aacPayload...)

	addr := startFakeRTSPServer(t, sdpBody, map[uint8][]byte{
		0: videoRTP, // video channel
		2: audioRTP, // audio channel
	})

	// Create a Session (the pull client feeds it).
	trackMux := moqt.NewTrackMux(0)
	sess, err := NewSession(trackMux, "/live/camera")
	require.NoError(t, err)
	defer sess.Close()

	// Run pullStream with a timeout.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- pullStream(ctx, "rtsp://"+addr+"/test", sess) }()

	// Wait for both video and audio frames to arrive in the Session buffers.
	require.Eventually(t, func() bool {
		return sess.handler.video.buf.head() > 0 && sess.handler.audio.buf.head() > 0
	}, 3*time.Second, 50*time.Millisecond,
		"both video and audio frames should arrive in the Session")

	// Cancel context (pullStream will return on next ReadInterleaved error).
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("pullStream did not return after context cancel")
	}
}
